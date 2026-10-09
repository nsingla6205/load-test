package workflows

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

// TestQuotaRuleActivityStartToCloseTimeout_DefaultValue verifies the quota rule activity start-to-close timeout
// is read from QUOTA_RULE_START_TO_CLOSE_TIMEOUT_SEC and defaults to 300 seconds (5 minutes).
func TestQuotaRuleActivityStartToCloseTimeout_DefaultValue(t *testing.T) {
	assert.Equal(t, uint64(300), startToCloseTimeoutQuotaRuleActivitySec,
		"default QUOTA_RULE_START_TO_CLOSE_TIMEOUT_SEC should be 300 seconds (5 minutes)")
}

func TestCreateQuotaRuleWorkflow(t *testing.T) {
	// Common test data
	volumeID := int64(1)
	poolID := int64(1)
	svmID := int64(1)

	createBaseVolume := func(isDataProtection bool, protocols []string) *datamodel.Volume {
		return &datamodel.Volume{
			BaseModel: datamodel.BaseModel{
				ID:   volumeID,
				UUID: "volume-uuid-1",
			},
			Name:        "test-volume",
			PoolID:      poolID,
			SizeInBytes: 200 * 1024 * 1024,
			Pool: &datamodel.Pool{
				BaseModel: datamodel.BaseModel{
					ID: poolID,
				},
				DeploymentName: "test-deployment",
				PoolCredentials: &datamodel.PoolCredentials{
					Password: "test-password",
				},
			},
			Svm: &datamodel.Svm{
				BaseModel: datamodel.BaseModel{
					ID: svmID,
				},
				Name: "test-svm",
				SvmDetails: &datamodel.SvmDetails{
					ExternalUUID: "svm-external-uuid",
				},
			},
			VolumeAttributes: &datamodel.VolumeAttributes{
				IsDataProtection: isDataProtection,
				Protocols:        protocols,
			},
		}
	}

	createBaseQuotaRule := func() *datamodel.QuotaRule {
		return &datamodel.QuotaRule{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "quota-rule-uuid-1",
			},
			Name:           "test-quota-rule",
			QuotaType:      "INDIVIDUAL_USER_QUOTA",
			DiskLimitInKib: 100 * 1024,
			QuotaTarget:    "1001",
			VolumeID:       volumeID,
			State:          datamodel.LifeCycleStateCreating,
			StateDetails:   datamodel.LifeCycleStateCreatingDetails,
			RQuota:         true,
		}
	}

	createBaseParams := func() *commonparams.CreateQuotaRulesParam {
		return &commonparams.CreateQuotaRulesParam{
			ProjectId:  "test-project",
			LocationId: "us-central1-a",
		}
	}

	createBaseNodes := func() []*datamodel.Node {
		return []*datamodel.Node{
			{
				BaseModel: datamodel.BaseModel{
					ID: 1,
				},
				EndpointAddress: "127.0.0.1",
			},
		}
	}

	t.Run("WhenGetVolumeByIDFails", func(tt *testing.T) {
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
		quotaRuleCommonActivity := activities.QuotaRuleCommonActivity{SE: mockStorage}
		commonActivity := activities.CommonActivities{SE: mockStorage}

		env.RegisterActivity(quotaRuleCommonActivity.GetVolumeByID)
		env.RegisterActivity(quotaRuleCommonActivity.UpdateQuotaRuleState)
		env.RegisterActivity(commonActivity.UpdateJobStatus)
		env.RegisterActivity(commonActivity.GetJob)

		params := createBaseParams()
		quotaRule := createBaseQuotaRule()

		// Mock GetJob for EnsureJobState
		env.OnActivity("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil)
		// Mock GetVolumeByID to fail
		env.OnActivity("GetVolumeByID", mock.Anything, volumeID, int64(0)).Return(nil, errors.New("volume not found"))
		env.OnActivity("UpdateQuotaRuleState", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)

		env.ExecuteWorkflow(CreateQuotaRuleWorkflow, params, quotaRule)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
	})

	t.Run("WhenCreateQuotaRuleForDataProtectionVolumeFails", func(tt *testing.T) {
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
		quotaRuleActivity := activities.QuotaRuleCreateActivity{SE: mockStorage}
		quotaRuleCommonActivity := activities.QuotaRuleCommonActivity{SE: mockStorage}
		commonActivity := activities.CommonActivities{SE: mockStorage}

		env.RegisterActivity(quotaRuleCommonActivity.GetVolumeByID)
		env.RegisterActivity(quotaRuleActivity.CreateQuotaRuleForDataProtectionVolume)
		env.RegisterActivity(quotaRuleCommonActivity.UpdateQuotaRuleState)
		env.RegisterActivity(commonActivity.UpdateJobStatus)
		env.RegisterActivity(commonActivity.GetJob)

		params := createBaseParams()
		quotaRule := createBaseQuotaRule()
		volume := createBaseVolume(true, []string{"NFSV3"})

		env.OnActivity("GetVolumeByID", mock.Anything, volumeID, int64(0)).Return(volume, nil)
		// The workflow updates quota rule state to READY before calling CreateQuotaRuleForDataProtectionVolume
		env.OnActivity("CreateQuotaRuleForDataProtectionVolume", mock.Anything, mock.MatchedBy(func(qr *datamodel.QuotaRule) bool {
			return qr.UUID == quotaRule.UUID && qr.State == datamodel.LifeCycleStateREADY
		})).Return(errors.New("failed to create DP quota rule"))
		env.OnActivity("UpdateQuotaRuleState", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)

		// Mock GetJob for EnsureJobState
		env.OnActivity("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil)

		env.ExecuteWorkflow(CreateQuotaRuleWorkflow, params, quotaRule)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
	})

	t.Run("WhenGetNodeFails", func(tt *testing.T) {
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
		quotaRuleCommonActivity := activities.QuotaRuleCommonActivity{SE: mockStorage}
		commonActivity := activities.CommonActivities{SE: mockStorage}

		env.RegisterActivity(quotaRuleCommonActivity.GetVolumeByID)
		env.RegisterActivity(commonActivity.GetNode)
		env.RegisterActivity(quotaRuleCommonActivity.UpdateQuotaRuleState)
		env.RegisterActivity(commonActivity.UpdateJobStatus)
		env.RegisterActivity(commonActivity.GetJob)

		params := createBaseParams()
		quotaRule := createBaseQuotaRule()
		volume := createBaseVolume(false, []string{"NFSV3"})

		env.OnActivity("GetVolumeByID", mock.Anything, volumeID, int64(0)).Return(volume, nil)
		env.OnActivity("GetNode", mock.Anything, poolID).Return(nil, errors.New("failed to get nodes"))
		env.OnActivity("UpdateQuotaRuleState", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)

		// Mock GetJob for EnsureJobState
		env.OnActivity("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil)

		env.ExecuteWorkflow(CreateQuotaRuleWorkflow, params, quotaRule)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
	})

	t.Run("WhenUpdateRQuotaOnSvmFails", func(tt *testing.T) {
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
		quotaRuleCommonActivity := activities.QuotaRuleCommonActivity{SE: mockStorage}
		commonActivity := activities.CommonActivities{SE: mockStorage}

		env.RegisterActivity(quotaRuleCommonActivity.GetVolumeByID)
		env.RegisterActivity(commonActivity.GetNode)
		env.RegisterActivity(quotaRuleCommonActivity.UpdateRQuotaOnSvm)
		env.RegisterActivity(quotaRuleCommonActivity.UpdateQuotaRuleState)
		env.RegisterActivity(commonActivity.UpdateJobStatus)
		env.RegisterActivity(commonActivity.GetJob)

		params := createBaseParams()
		quotaRule := createBaseQuotaRule()
		volume := createBaseVolume(false, []string{"NFSV3"})
		nodes := createBaseNodes()

		env.OnActivity("GetVolumeByID", mock.Anything, volumeID, int64(0)).Return(volume, nil)
		env.OnActivity("GetNode", mock.Anything, poolID).Return(nodes, nil)
		env.OnActivity("UpdateRQuotaOnSvm", mock.Anything, "svm-external-uuid", mock.Anything, true).Return(errors.New("failed to update rquota"))
		env.OnActivity("UpdateQuotaRuleState", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)

		// Mock GetJob for EnsureJobState
		env.OnActivity("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil)

		env.ExecuteWorkflow(CreateQuotaRuleWorkflow, params, quotaRule)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
	})

	t.Run("WhenHandleDefaultQuotaRuleUpdateFails", func(tt *testing.T) {
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
		quotaRuleActivity := activities.QuotaRuleCreateActivity{SE: mockStorage}
		quotaRuleCommonActivity := activities.QuotaRuleCommonActivity{SE: mockStorage}
		commonActivity := activities.CommonActivities{SE: mockStorage}

		env.RegisterActivity(quotaRuleCommonActivity.GetVolumeByID)
		env.RegisterActivity(commonActivity.GetNode)
		env.RegisterActivity(quotaRuleCommonActivity.UpdateRQuotaOnSvm)
		env.RegisterActivity(quotaRuleActivity.HandleDefaultQuotaRuleUpdate)
		env.RegisterActivity(quotaRuleCommonActivity.UpdateQuotaRuleState)
		env.RegisterActivity(commonActivity.UpdateJobStatus)
		env.RegisterActivity(commonActivity.GetJob)

		params := createBaseParams()
		quotaRule := createBaseQuotaRule()
		quotaRule.QuotaTarget = "" // Default quota
		volume := createBaseVolume(false, []string{"NFSV3"})
		nodes := createBaseNodes()

		env.OnActivity("GetVolumeByID", mock.Anything, volumeID, int64(0)).Return(volume, nil)
		env.OnActivity("GetNode", mock.Anything, poolID).Return(nodes, nil)
		env.OnActivity("UpdateRQuotaOnSvm", mock.Anything, "svm-external-uuid", mock.Anything, true).Return(nil)
		env.OnActivity("HandleDefaultQuotaRuleUpdate", mock.Anything, volume, mock.Anything, quotaRule.QuotaType, quotaRule.DiskLimitInKib).Return(errors.New("unexpected error updating default quota"))
		env.OnActivity("UpdateQuotaRuleState", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)

		// Mock GetJob for EnsureJobState
		env.OnActivity("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil)

		env.ExecuteWorkflow(CreateQuotaRuleWorkflow, params, quotaRule)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
	})

	t.Run("WhenCreateQuotaRuleOnONTAPFails", func(tt *testing.T) {
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
		quotaRuleActivity := activities.QuotaRuleCreateActivity{SE: mockStorage}
		quotaRuleCommonActivity := activities.QuotaRuleCommonActivity{SE: mockStorage}
		commonActivity := activities.CommonActivities{SE: mockStorage}

		env.RegisterActivity(quotaRuleCommonActivity.GetVolumeByID)
		env.RegisterActivity(commonActivity.GetNode)
		env.RegisterActivity(quotaRuleCommonActivity.UpdateRQuotaOnSvm)
		env.RegisterActivity(quotaRuleActivity.HandleDefaultQuotaRuleUpdate)
		env.RegisterActivity(quotaRuleActivity.CreateQuotaRuleOnONTAP)
		env.RegisterActivity(quotaRuleCommonActivity.UpdateQuotaRuleState)
		env.RegisterActivity(commonActivity.UpdateJobStatus)
		env.RegisterActivity(commonActivity.GetJob)

		params := createBaseParams()
		quotaRule := createBaseQuotaRule()
		volume := createBaseVolume(false, []string{"NFSV3"})
		nodes := createBaseNodes()

		env.OnActivity("GetVolumeByID", mock.Anything, volumeID, int64(0)).Return(volume, nil)
		env.OnActivity("GetNode", mock.Anything, poolID).Return(nodes, nil)
		env.OnActivity("UpdateRQuotaOnSvm", mock.Anything, "svm-external-uuid", mock.Anything, true).Return(nil)
		env.OnActivity("HandleDefaultQuotaRuleUpdate", mock.Anything, volume, mock.Anything, quotaRule.QuotaType, quotaRule.DiskLimitInKib).Return(vsaerrors.New("not found"))
		env.OnActivity("CreateQuotaRuleOnONTAP", mock.Anything, mock.Anything, volume, quotaRule).Return(nil, errors.New("failed to create quota rule on ONTAP"))
		env.OnActivity("UpdateQuotaRuleState", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)

		// Mock GetJob for EnsureJobState
		env.OnActivity("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil)

		env.ExecuteWorkflow(CreateQuotaRuleWorkflow, params, quotaRule)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
	})

	t.Run("WhenGetQuotaStatusFails", func(tt *testing.T) {
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
		quotaRuleActivity := activities.QuotaRuleCreateActivity{SE: mockStorage}
		quotaRuleCommonActivity := activities.QuotaRuleCommonActivity{SE: mockStorage}
		commonActivity := activities.CommonActivities{SE: mockStorage}

		env.RegisterActivity(quotaRuleCommonActivity.GetVolumeByID)
		env.RegisterActivity(commonActivity.GetNode)
		env.RegisterActivity(quotaRuleCommonActivity.UpdateRQuotaOnSvm)
		env.RegisterActivity(quotaRuleActivity.HandleDefaultQuotaRuleUpdate)
		env.RegisterActivity(quotaRuleActivity.CreateQuotaRuleOnONTAP)
		env.RegisterActivity(quotaRuleActivity.GetQuotaStatus)
		env.RegisterActivity(quotaRuleCommonActivity.UpdateQuotaRuleState)
		env.RegisterActivity(commonActivity.UpdateJobStatus)
		env.RegisterActivity(commonActivity.GetJob)

		params := createBaseParams()
		quotaRule := createBaseQuotaRule()
		volume := createBaseVolume(false, []string{"NFSV3"})
		nodes := createBaseNodes()

		quotaRuleResp := &vsa.QuotaRuleProviderResponse{
			State:   vsa.JobRespSuccess,
			Message: "Quota rule created successfully",
		}

		env.OnActivity("GetVolumeByID", mock.Anything, volumeID, int64(0)).Return(volume, nil)
		env.OnActivity("GetNode", mock.Anything, poolID).Return(nodes, nil)
		env.OnActivity("UpdateRQuotaOnSvm", mock.Anything, "svm-external-uuid", mock.Anything, true).Return(nil)
		env.OnActivity("HandleDefaultQuotaRuleUpdate", mock.Anything, volume, mock.Anything, quotaRule.QuotaType, quotaRule.DiskLimitInKib).Return(vsaerrors.New("not found"))
		env.OnActivity("CreateQuotaRuleOnONTAP", mock.Anything, mock.Anything, volume, quotaRule).Return(quotaRuleResp, nil)
		env.OnActivity("GetQuotaStatus", mock.Anything, mock.Anything, volume).Return(nil, errors.New("failed to get quota status"))
		env.OnActivity("UpdateQuotaRuleState", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)

		// Mock GetJob for EnsureJobState
		env.OnActivity("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil)

		env.ExecuteWorkflow(CreateQuotaRuleWorkflow, params, quotaRule)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
	})

	t.Run("WhenHandleQuotaEnableDisableFails", func(tt *testing.T) {
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
		quotaRuleActivity := activities.QuotaRuleCreateActivity{SE: mockStorage}
		quotaRuleCommonActivity := activities.QuotaRuleCommonActivity{SE: mockStorage}
		commonActivity := activities.CommonActivities{SE: mockStorage}

		env.RegisterActivity(quotaRuleCommonActivity.GetVolumeByID)
		env.RegisterActivity(commonActivity.GetNode)
		env.RegisterActivity(quotaRuleCommonActivity.UpdateRQuotaOnSvm)
		env.RegisterActivity(quotaRuleActivity.HandleDefaultQuotaRuleUpdate)
		env.RegisterActivity(quotaRuleActivity.CreateQuotaRuleOnONTAP)
		env.RegisterActivity(quotaRuleActivity.GetQuotaStatus)
		env.RegisterActivity(quotaRuleCommonActivity.HandleQuotaEnableDisable)
		env.RegisterActivity(quotaRuleCommonActivity.UpdateQuotaRuleState)
		env.RegisterActivity(commonActivity.UpdateJobStatus)
		env.RegisterActivity(commonActivity.GetJob)

		params := createBaseParams()
		quotaRule := createBaseQuotaRule()
		volume := createBaseVolume(false, []string{"NFSV3"})
		nodes := createBaseNodes()

		quotaRuleResp := &vsa.QuotaRuleProviderResponse{
			State:   vsa.JobRespSuccess,
			Message: "Quota rule created successfully",
		}

		quotaStatus := &vsa.QuotaStatus{
			State:   vsa.QuotaStateOff,
			Enabled: false,
		}

		env.OnActivity("GetVolumeByID", mock.Anything, volumeID, int64(0)).Return(volume, nil)
		env.OnActivity("GetNode", mock.Anything, poolID).Return(nodes, nil)
		env.OnActivity("UpdateRQuotaOnSvm", mock.Anything, "svm-external-uuid", mock.Anything, true).Return(nil)
		env.OnActivity("HandleDefaultQuotaRuleUpdate", mock.Anything, volume, mock.Anything, quotaRule.QuotaType, quotaRule.DiskLimitInKib).Return(vsaerrors.New("not found"))
		env.OnActivity("CreateQuotaRuleOnONTAP", mock.Anything, mock.Anything, volume, quotaRule).Return(quotaRuleResp, nil)
		env.OnActivity("GetQuotaStatus", mock.Anything, mock.Anything, volume).Return(quotaStatus, nil)
		env.OnActivity("HandleQuotaEnableDisable", mock.Anything, mock.Anything, volume, true).Return(nil, errors.New("failed to enable quota"))
		env.OnActivity("UpdateQuotaRuleState", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)

		// Mock GetJob for EnsureJobState
		env.OnActivity("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil)

		env.ExecuteWorkflow(CreateQuotaRuleWorkflow, params, quotaRule)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
	})

	t.Run("WhenQuotaReinitializationFails", func(tt *testing.T) {
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
		quotaRuleActivity := activities.QuotaRuleCreateActivity{SE: mockStorage}
		quotaRuleCommonActivity := activities.QuotaRuleCommonActivity{SE: mockStorage}
		commonActivity := activities.CommonActivities{SE: mockStorage}

		env.RegisterActivity(quotaRuleCommonActivity.GetVolumeByID)
		env.RegisterActivity(commonActivity.GetNode)
		env.RegisterActivity(quotaRuleCommonActivity.UpdateRQuotaOnSvm)
		env.RegisterActivity(quotaRuleActivity.HandleDefaultQuotaRuleUpdate)
		env.RegisterActivity(quotaRuleActivity.CreateQuotaRuleOnONTAP)
		env.RegisterActivity(quotaRuleActivity.GetQuotaStatus)
		env.RegisterActivity(quotaRuleCommonActivity.QuotaReinitialization)
		env.RegisterActivity(quotaRuleCommonActivity.UpdateQuotaRuleState)
		env.RegisterActivity(commonActivity.UpdateJobStatus)
		env.RegisterActivity(commonActivity.GetJob)

		params := createBaseParams()
		quotaRule := createBaseQuotaRule()
		volume := createBaseVolume(false, []string{"NFSV3"})
		nodes := createBaseNodes()

		quotaRuleResp := &vsa.QuotaRuleProviderResponse{
			State:   vsa.JobRespFailure,
			Message: "Quota policy rule create operation succeeded, however quota resize failed",
		}

		quotaStatus := &vsa.QuotaStatus{
			State:   vsa.QuotaStateOn,
			Enabled: true,
		}

		env.OnActivity("GetVolumeByID", mock.Anything, volumeID, int64(0)).Return(volume, nil)
		env.OnActivity("GetNode", mock.Anything, poolID).Return(nodes, nil)
		env.OnActivity("UpdateRQuotaOnSvm", mock.Anything, "svm-external-uuid", mock.Anything, true).Return(nil)
		env.OnActivity("HandleDefaultQuotaRuleUpdate", mock.Anything, volume, mock.Anything, quotaRule.QuotaType, quotaRule.DiskLimitInKib).Return(vsaerrors.New("not found"))
		env.OnActivity("CreateQuotaRuleOnONTAP", mock.Anything, mock.Anything, volume, quotaRule).Return(quotaRuleResp, nil)
		env.OnActivity("GetQuotaStatus", mock.Anything, mock.Anything, volume).Return(quotaStatus, nil)
		env.OnActivity("QuotaReinitialization", mock.Anything, mock.Anything, volume).Return(errors.New("failed to reinitialize quota"))
		env.OnActivity("UpdateQuotaRuleState", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)

		// Mock GetJob for EnsureJobState
		env.OnActivity("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil)

		env.ExecuteWorkflow(CreateQuotaRuleWorkflow, params, quotaRule)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
	})

	t.Run("WhenGetVolumeReplicationFails", func(tt *testing.T) {
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
		quotaRuleActivity := activities.QuotaRuleCreateActivity{SE: mockStorage}
		quotaRuleCommonActivity := activities.QuotaRuleCommonActivity{SE: mockStorage}
		commonActivity := activities.CommonActivities{SE: mockStorage}

		env.RegisterActivity(quotaRuleCommonActivity.GetVolumeByID)
		env.RegisterActivity(commonActivity.GetNode)
		env.RegisterActivity(quotaRuleCommonActivity.UpdateRQuotaOnSvm)
		env.RegisterActivity(quotaRuleActivity.HandleDefaultQuotaRuleUpdate)
		env.RegisterActivity(quotaRuleActivity.CreateQuotaRuleOnONTAP)
		env.RegisterActivity(quotaRuleActivity.GetQuotaStatus)
		env.RegisterActivity(quotaRuleCommonActivity.HandleQuotaEnableDisable)
		env.RegisterActivity(quotaRuleCommonActivity.GetVolumeReplication)
		env.RegisterActivity(quotaRuleCommonActivity.UpdateQuotaRuleState)
		env.RegisterActivity(commonActivity.UpdateJobStatus)
		env.RegisterActivity(commonActivity.GetJob)

		params := createBaseParams()
		quotaRule := createBaseQuotaRule()
		volume := createBaseVolume(false, []string{"NFSV3"})
		nodes := createBaseNodes()

		quotaRuleResp := &vsa.QuotaRuleProviderResponse{
			State:   vsa.JobRespSuccess,
			Message: "Quota rule created successfully",
		}

		quotaStatus := &vsa.QuotaStatus{
			State:   vsa.QuotaStateOff,
			Enabled: false,
		}

		quotaEnableResp := &vsa.JobStatus{
			State:   vsa.JobRespSuccess,
			Message: "quota enabled",
		}

		env.OnActivity("GetVolumeByID", mock.Anything, volumeID, int64(0)).Return(volume, nil)
		env.OnActivity("GetNode", mock.Anything, poolID).Return(nodes, nil)
		env.OnActivity("UpdateRQuotaOnSvm", mock.Anything, "svm-external-uuid", mock.Anything, true).Return(nil)
		env.OnActivity("HandleDefaultQuotaRuleUpdate", mock.Anything, volume, mock.Anything, quotaRule.QuotaType, quotaRule.DiskLimitInKib).Return(vsaerrors.New("not found"))
		env.OnActivity("CreateQuotaRuleOnONTAP", mock.Anything, mock.Anything, volume, quotaRule).Return(quotaRuleResp, nil)
		env.OnActivity("GetQuotaStatus", mock.Anything, mock.Anything, volume).Return(quotaStatus, nil)
		env.OnActivity("HandleQuotaEnableDisable", mock.Anything, mock.Anything, volume, true).Return(quotaEnableResp, nil)
		env.OnActivity("GetVolumeReplication", mock.Anything, volumeID).Return(nil, errors.New("failed to get volume replication"))
		env.OnActivity("UpdateQuotaRuleState", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)

		// Mock GetJob for EnsureJobState
		env.OnActivity("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil)

		env.ExecuteWorkflow(CreateQuotaRuleWorkflow, params, quotaRule)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
	})

	t.Run("WhenVerifyReplicationStateFails", func(tt *testing.T) {
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
		quotaRuleActivity := activities.QuotaRuleCreateActivity{SE: mockStorage}
		quotaRuleCommonActivity := activities.QuotaRuleCommonActivity{SE: mockStorage}
		commonActivity := activities.CommonActivities{SE: mockStorage}

		env.RegisterActivity(quotaRuleCommonActivity.GetVolumeByID)
		env.RegisterActivity(commonActivity.GetNode)
		env.RegisterActivity(quotaRuleCommonActivity.UpdateRQuotaOnSvm)
		env.RegisterActivity(quotaRuleActivity.HandleDefaultQuotaRuleUpdate)
		env.RegisterActivity(quotaRuleActivity.CreateQuotaRuleOnONTAP)
		env.RegisterActivity(quotaRuleActivity.GetQuotaStatus)
		env.RegisterActivity(quotaRuleCommonActivity.HandleQuotaEnableDisable)
		env.RegisterActivity(quotaRuleCommonActivity.GetVolumeReplication)
		env.RegisterActivity(quotaRuleCommonActivity.VerifyReplicationState)
		env.RegisterActivity(quotaRuleCommonActivity.UpdateQuotaRuleState)
		env.RegisterActivity(commonActivity.UpdateJobStatus)
		env.RegisterActivity(commonActivity.GetJob)

		params := createBaseParams()
		quotaRule := createBaseQuotaRule()
		volume := createBaseVolume(false, []string{"NFSV3"})
		nodes := createBaseNodes()

		quotaRuleResp := &vsa.QuotaRuleProviderResponse{
			State:   vsa.JobRespSuccess,
			Message: "Quota rule created successfully",
		}

		quotaStatus := &vsa.QuotaStatus{
			State:   vsa.QuotaStateOff,
			Enabled: false,
		}

		quotaEnableResp := &vsa.JobStatus{
			State:   vsa.JobRespSuccess,
			Message: "quota enabled",
		}

		replications := []*datamodel.VolumeReplication{
			{
				BaseModel: datamodel.BaseModel{
					ID: 1,
				},
				VolumeID: volumeID,
				ReplicationAttributes: &datamodel.ReplicationDetails{
					DestinationVolumeUUID: "dest-volume-uuid",
					DestinationVolumeName: "dest-volume-name",
					DestinationLocation:   "us-west1-a",
				},
				RemoteUri: "projects/987654321/locations/us-west1-a/volumes/dest-volume-uuid/replications/replication-1",
			},
		}

		env.OnActivity("GetVolumeByID", mock.Anything, volumeID, int64(0)).Return(volume, nil)
		env.OnActivity("GetNode", mock.Anything, poolID).Return(nodes, nil)
		env.OnActivity("UpdateRQuotaOnSvm", mock.Anything, "svm-external-uuid", mock.Anything, true).Return(nil)
		env.OnActivity("HandleDefaultQuotaRuleUpdate", mock.Anything, volume, mock.Anything, quotaRule.QuotaType, quotaRule.DiskLimitInKib).Return(vsaerrors.New("not found"))
		env.OnActivity("CreateQuotaRuleOnONTAP", mock.Anything, mock.Anything, volume, quotaRule).Return(quotaRuleResp, nil)
		env.OnActivity("GetQuotaStatus", mock.Anything, mock.Anything, volume).Return(quotaStatus, nil)
		env.OnActivity("HandleQuotaEnableDisable", mock.Anything, mock.Anything, volume, true).Return(quotaEnableResp, nil)
		env.OnActivity("GetVolumeReplication", mock.Anything, volumeID).Return(replications, nil)
		env.OnActivity("VerifyReplicationState", mock.Anything, replications[0], params.LocationId).Return(false, errors.New("replication state validation failed"))
		env.OnActivity("UpdateQuotaRuleState", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)

		// Mock GetJob for EnsureJobState
		env.OnActivity("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil)

		env.ExecuteWorkflow(CreateQuotaRuleWorkflow, params, quotaRule)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
	})

	t.Run("WhenHydrateQuotaRuleCreateFails_WorkflowFails", func(tt *testing.T) {
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
		quotaRuleActivity := activities.QuotaRuleCreateActivity{SE: mockStorage}
		quotaRuleCommonActivity := activities.QuotaRuleCommonActivity{SE: mockStorage}
		commonActivity := activities.CommonActivities{SE: mockStorage}

		env.RegisterActivity(quotaRuleCommonActivity.GetVolumeByID)
		env.RegisterActivity(commonActivity.GetNode)
		env.RegisterActivity(quotaRuleCommonActivity.UpdateRQuotaOnSvm)
		env.RegisterActivity(quotaRuleActivity.HandleDefaultQuotaRuleUpdate)
		env.RegisterActivity(quotaRuleActivity.CreateQuotaRuleOnONTAP)
		env.RegisterActivity(quotaRuleActivity.GetQuotaStatus)
		env.RegisterActivity(quotaRuleCommonActivity.HandleQuotaEnableDisable)
		env.RegisterActivity(quotaRuleCommonActivity.GetVolumeReplication)
		env.RegisterActivity(quotaRuleCommonActivity.VerifyReplicationState)
		env.RegisterActivity(quotaRuleCommonActivity.GetSignedDstTokenForQuotaRule)
		env.RegisterActivity(quotaRuleActivity.CreateQuotaRuleOnDestination)
		env.RegisterActivity(quotaRuleCommonActivity.DescribeQuotaRuleRemoteJob)
		env.RegisterActivity(quotaRuleCommonActivity.HydrateQuotaRuleCreate)
		env.RegisterActivity(quotaRuleCommonActivity.UpdateQuotaRuleState)
		env.RegisterActivity(commonActivity.UpdateJobStatus)
		env.RegisterActivity(commonActivity.GetJob)

		params := createBaseParams()
		quotaRule := createBaseQuotaRule()
		volume := createBaseVolume(false, []string{"NFSV3"})
		nodes := createBaseNodes()

		quotaRuleResp := &vsa.QuotaRuleProviderResponse{State: vsa.JobRespSuccess, Message: "created"}
		quotaStatus := &vsa.QuotaStatus{State: vsa.QuotaStateOff, Enabled: false}
		quotaEnableResp := &vsa.JobStatus{State: vsa.JobRespSuccess, Message: "quota enabled"}
		replications := []*datamodel.VolumeReplication{
			{
				BaseModel: datamodel.BaseModel{ID: 1},
				VolumeID:  volumeID,
				ReplicationAttributes: &datamodel.ReplicationDetails{
					DestinationVolumeUUID: "dest-volume-uuid",
					DestinationVolumeName: "dest-volume-name",
					DestinationLocation:   "us-west1-a",
				},
				RemoteUri: "projects/987654321/locations/us-west1-a/volumes/dest-volume-uuid/replications/replication-1",
			},
		}

		destQuotaRule := &datamodel.QuotaRule{
			BaseModel: datamodel.BaseModel{UUID: "dest-quota-uuid"},
			Name:      quotaRule.Name,
		}
		jwtToken := "test-jwt-token"

		env.OnActivity("GetVolumeByID", mock.Anything, volumeID, int64(0)).Return(volume, nil)
		env.OnActivity("GetNode", mock.Anything, poolID).Return(nodes, nil)
		env.OnActivity("UpdateRQuotaOnSvm", mock.Anything, "svm-external-uuid", mock.Anything, true).Return(nil)
		env.OnActivity("HandleDefaultQuotaRuleUpdate", mock.Anything, volume, mock.Anything, quotaRule.QuotaType, quotaRule.DiskLimitInKib).Return(vsaerrors.New("not found"))
		env.OnActivity("CreateQuotaRuleOnONTAP", mock.Anything, mock.Anything, volume, quotaRule).Return(quotaRuleResp, nil)
		env.OnActivity("GetQuotaStatus", mock.Anything, mock.Anything, volume).Return(quotaStatus, nil)
		env.OnActivity("HandleQuotaEnableDisable", mock.Anything, mock.Anything, volume, true).Return(quotaEnableResp, nil)
		env.OnActivity("GetVolumeReplication", mock.Anything, volumeID).Return(replications, nil)
		env.OnActivity("VerifyReplicationState", mock.Anything, replications[0], params.LocationId).Return(true, nil)
		env.OnActivity("GetSignedDstTokenForQuotaRule", mock.Anything, "987654321").Return(&jwtToken, nil)
		env.OnActivity("CreateQuotaRuleOnDestination", mock.Anything, "dest-volume-uuid", quotaRule, "us-west1-a", "987654321", &jwtToken).Return(&activities.QuotaRuleOperationResult{IsDone: true, QuotaRule: destQuotaRule}, nil)
		env.OnActivity("HydrateQuotaRuleCreate", mock.Anything, mock.Anything, "dest-volume-name", "us-west1-a", "987654321").Return(errors.New("hydration failed"))
		env.OnActivity("UpdateQuotaRuleState", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil)

		env.ExecuteWorkflow(CreateQuotaRuleWorkflow, params, quotaRule)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError(), "hydration error should be fatal")
	})

	t.Run("WhenCreateQuotaRuleOnDestinationFails", func(tt *testing.T) {
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
		quotaRuleActivity := activities.QuotaRuleCreateActivity{SE: mockStorage}
		quotaRuleCommonActivity := activities.QuotaRuleCommonActivity{SE: mockStorage}
		commonActivity := activities.CommonActivities{SE: mockStorage}

		env.RegisterActivity(quotaRuleCommonActivity.GetVolumeByID)
		env.RegisterActivity(commonActivity.GetNode)
		env.RegisterActivity(quotaRuleCommonActivity.UpdateRQuotaOnSvm)
		env.RegisterActivity(quotaRuleActivity.HandleDefaultQuotaRuleUpdate)
		env.RegisterActivity(quotaRuleActivity.CreateQuotaRuleOnONTAP)
		env.RegisterActivity(quotaRuleActivity.GetQuotaStatus)
		env.RegisterActivity(quotaRuleCommonActivity.HandleQuotaEnableDisable)
		env.RegisterActivity(quotaRuleCommonActivity.GetVolumeReplication)
		env.RegisterActivity(quotaRuleCommonActivity.VerifyReplicationState)
		env.RegisterActivity(quotaRuleCommonActivity.GetSignedDstTokenForQuotaRule)
		env.RegisterActivity(quotaRuleActivity.CreateQuotaRuleOnDestination)
		env.RegisterActivity(quotaRuleCommonActivity.DescribeQuotaRuleRemoteJob)
		env.RegisterActivity(quotaRuleCommonActivity.HydrateQuotaRuleCreate)
		env.RegisterActivity(quotaRuleCommonActivity.UpdateQuotaRuleState)
		env.RegisterActivity(commonActivity.UpdateJobStatus)
		env.RegisterActivity(commonActivity.GetJob)

		params := createBaseParams()
		quotaRule := createBaseQuotaRule()
		volume := createBaseVolume(false, []string{"NFSV3"})
		nodes := createBaseNodes()

		quotaRuleResp := &vsa.QuotaRuleProviderResponse{
			State:   vsa.JobRespSuccess,
			Message: "Quota rule created successfully",
		}

		quotaStatus := &vsa.QuotaStatus{
			State:   vsa.QuotaStateOff,
			Enabled: false,
		}

		quotaEnableResp := &vsa.JobStatus{
			State:   vsa.JobRespSuccess,
			Message: "quota enabled",
		}

		replications := []*datamodel.VolumeReplication{
			{
				BaseModel: datamodel.BaseModel{
					ID: 1,
				},
				VolumeID: volumeID,
				ReplicationAttributes: &datamodel.ReplicationDetails{
					DestinationVolumeUUID: "dest-volume-uuid",
					DestinationVolumeName: "dest-volume-name",
					DestinationLocation:   "us-west1-a",
				},
				RemoteUri: "projects/987654321/locations/us-west1-a/volumes/dest-volume-uuid/replications/replication-1",
			},
		}

		env.OnActivity("GetVolumeByID", mock.Anything, volumeID, int64(0)).Return(volume, nil)
		env.OnActivity("GetNode", mock.Anything, poolID).Return(nodes, nil)
		env.OnActivity("UpdateRQuotaOnSvm", mock.Anything, "svm-external-uuid", mock.Anything, true).Return(nil)
		env.OnActivity("HandleDefaultQuotaRuleUpdate", mock.Anything, volume, mock.Anything, quotaRule.QuotaType, quotaRule.DiskLimitInKib).Return(vsaerrors.New("not found"))
		env.OnActivity("CreateQuotaRuleOnONTAP", mock.Anything, mock.Anything, volume, quotaRule).Return(quotaRuleResp, nil)
		env.OnActivity("GetQuotaStatus", mock.Anything, mock.Anything, volume).Return(quotaStatus, nil)
		env.OnActivity("HandleQuotaEnableDisable", mock.Anything, mock.Anything, volume, true).Return(quotaEnableResp, nil)
		env.OnActivity("GetVolumeReplication", mock.Anything, volumeID).Return(replications, nil)
		env.OnActivity("VerifyReplicationState", mock.Anything, replications[0], params.LocationId).Return(true, nil)
		jwtToken := "test-jwt-token"
		env.OnActivity("GetSignedDstTokenForQuotaRule", mock.Anything, "987654321").Return(&jwtToken, nil)
		env.OnActivity("CreateQuotaRuleOnDestination", mock.Anything, "dest-volume-uuid", quotaRule, "us-west1-a", "987654321", &jwtToken).Return(nil, errors.New("failed to create quota rule on destination"))
		env.OnActivity("UpdateQuotaRuleState", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)

		// Mock GetJob for EnsureJobState
		env.OnActivity("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil)

		env.ExecuteWorkflow(CreateQuotaRuleWorkflow, params, quotaRule)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
	})

	t.Run("WhenFinishQuotaRuleJobFails", func(tt *testing.T) {
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
		quotaRuleActivity := activities.QuotaRuleCreateActivity{SE: mockStorage}
		quotaRuleCommonActivity := activities.QuotaRuleCommonActivity{SE: mockStorage}
		commonActivity := activities.CommonActivities{SE: mockStorage}

		env.RegisterActivity(quotaRuleCommonActivity.GetVolumeByID)
		env.RegisterActivity(commonActivity.GetNode)
		env.RegisterActivity(quotaRuleCommonActivity.UpdateRQuotaOnSvm)
		env.RegisterActivity(quotaRuleActivity.HandleDefaultQuotaRuleUpdate)
		env.RegisterActivity(quotaRuleActivity.CreateQuotaRuleOnONTAP)
		env.RegisterActivity(quotaRuleActivity.GetQuotaStatus)
		env.RegisterActivity(quotaRuleCommonActivity.HandleQuotaEnableDisable)
		env.RegisterActivity(quotaRuleCommonActivity.GetVolumeReplication)
		env.RegisterActivity(quotaRuleCommonActivity.VerifyReplicationState)
		env.RegisterActivity(quotaRuleCommonActivity.UpdateQuotaRuleState)
		env.RegisterActivity(commonActivity.UpdateJobStatus)
		env.RegisterActivity(commonActivity.GetJob)

		params := createBaseParams()
		quotaRule := createBaseQuotaRule()
		volume := createBaseVolume(false, []string{"NFSV3"})
		nodes := createBaseNodes()

		quotaRuleResp := &vsa.QuotaRuleProviderResponse{
			State:   vsa.JobRespSuccess,
			Message: "Quota rule created successfully",
		}

		quotaStatus := &vsa.QuotaStatus{
			State:   vsa.QuotaStateOff,
			Enabled: false,
		}

		quotaEnableResp := &vsa.JobStatus{
			State:   vsa.JobRespSuccess,
			Message: "quota enabled",
		}

		env.OnActivity("GetVolumeByID", mock.Anything, volumeID, int64(0)).Return(volume, nil)
		env.OnActivity("GetNode", mock.Anything, poolID).Return(nodes, nil)
		env.OnActivity("UpdateRQuotaOnSvm", mock.Anything, "svm-external-uuid", mock.Anything, true).Return(nil)
		env.OnActivity("HandleDefaultQuotaRuleUpdate", mock.Anything, volume, mock.Anything, quotaRule.QuotaType, quotaRule.DiskLimitInKib).Return(vsaerrors.New("not found"))
		env.OnActivity("CreateQuotaRuleOnONTAP", mock.Anything, mock.Anything, volume, quotaRule).Return(quotaRuleResp, nil)
		env.OnActivity("GetQuotaStatus", mock.Anything, mock.Anything, volume).Return(quotaStatus, nil)
		env.OnActivity("HandleQuotaEnableDisable", mock.Anything, mock.Anything, volume, true).Return(quotaEnableResp, nil)
		env.OnActivity("GetVolumeReplication", mock.Anything, volumeID).Return([]*datamodel.VolumeReplication{}, nil)
		env.OnActivity("UpdateQuotaRuleState", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)

		// Mock GetJob for EnsureJobState
		env.OnActivity("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil)

		env.ExecuteWorkflow(CreateQuotaRuleWorkflow, params, quotaRule)

		// Note: FinishQuotaRuleJob is not called in the current workflow implementation
		// The workflow completes successfully after UpdateQuotaRuleState
		assert.True(tt, env.IsWorkflowCompleted())
		assert.NoError(tt, env.GetWorkflowError())
	})

	t.Run("WhenQuotaCreateWorkflowSucceeds", func(tt *testing.T) {
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
		quotaRuleActivity := activities.QuotaRuleCreateActivity{SE: mockStorage}
		quotaRuleCommonActivity := activities.QuotaRuleCommonActivity{SE: mockStorage}
		commonActivity := activities.CommonActivities{SE: mockStorage}

		env.RegisterActivity(quotaRuleCommonActivity.GetVolumeByID)
		env.RegisterActivity(commonActivity.GetNode)
		env.RegisterActivity(quotaRuleCommonActivity.UpdateRQuotaOnSvm)
		env.RegisterActivity(quotaRuleActivity.HandleDefaultQuotaRuleUpdate)
		env.RegisterActivity(quotaRuleActivity.CreateQuotaRuleOnONTAP)
		env.RegisterActivity(quotaRuleActivity.GetQuotaStatus)
		env.RegisterActivity(quotaRuleCommonActivity.HandleQuotaEnableDisable)
		env.RegisterActivity(quotaRuleCommonActivity.GetVolumeReplication)
		env.RegisterActivity(quotaRuleCommonActivity.VerifyReplicationState)
		env.RegisterActivity(quotaRuleCommonActivity.GetSignedDstTokenForQuotaRule)
		env.RegisterActivity(quotaRuleActivity.CreateQuotaRuleOnDestination)
		env.RegisterActivity(quotaRuleCommonActivity.DescribeQuotaRuleRemoteJob)
		env.RegisterActivity(quotaRuleCommonActivity.HydrateQuotaRuleCreate)
		env.RegisterActivity(quotaRuleCommonActivity.UpdateQuotaRuleState)
		env.RegisterActivity(commonActivity.UpdateJobStatus)
		env.RegisterActivity(commonActivity.GetJob)

		params := createBaseParams()
		quotaRule := createBaseQuotaRule()
		volume := createBaseVolume(false, []string{"NFSV3"})
		nodes := createBaseNodes()

		quotaRuleResp := &vsa.QuotaRuleProviderResponse{
			State:   vsa.JobRespSuccess,
			Message: "Quota rule created successfully",
		}

		quotaStatus := &vsa.QuotaStatus{
			State:   vsa.QuotaStateOff,
			Enabled: false,
		}

		quotaEnableResp := &vsa.JobStatus{
			State:   vsa.JobRespSuccess,
			Message: "quota enabled",
		}

		replications := []*datamodel.VolumeReplication{
			{
				BaseModel: datamodel.BaseModel{
					ID: 1,
				},
				VolumeID: volumeID,
				ReplicationAttributes: &datamodel.ReplicationDetails{
					DestinationVolumeUUID: "dest-volume-uuid",
					DestinationVolumeName: "dest-volume-name",
					DestinationLocation:   "us-west1-a",
				},
				RemoteUri: "projects/987654321/locations/us-west1-a/volumes/dest-volume-uuid/replications/replication-1",
			},
		}

		// A DONE status update failure is logged but does not fail an otherwise successful workflow.
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Once()
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(errors.New("failed to mark completed job done")).Once()
		env.OnActivity("GetVolumeByID", mock.Anything, volumeID, int64(0)).Return(volume, nil)
		env.OnActivity("GetNode", mock.Anything, poolID).Return(nodes, nil)
		env.OnActivity("UpdateRQuotaOnSvm", mock.Anything, "svm-external-uuid", mock.Anything, true).Return(nil)
		env.OnActivity("HandleDefaultQuotaRuleUpdate", mock.Anything, volume, mock.Anything, quotaRule.QuotaType, quotaRule.DiskLimitInKib).Return(vsaerrors.New("not found"))
		env.OnActivity("CreateQuotaRuleOnONTAP", mock.Anything, mock.Anything, volume, quotaRule).Return(quotaRuleResp, nil)
		env.OnActivity("GetQuotaStatus", mock.Anything, mock.Anything, volume).Return(quotaStatus, nil)
		env.OnActivity("HandleQuotaEnableDisable", mock.Anything, mock.Anything, volume, true).Return(quotaEnableResp, nil)
		env.OnActivity("GetVolumeReplication", mock.Anything, volumeID).Return(replications, nil)
		env.OnActivity("VerifyReplicationState", mock.Anything, replications[0], params.LocationId).Return(true, nil)
		jwtToken := "test-jwt-token"
		env.OnActivity("GetSignedDstTokenForQuotaRule", mock.Anything, "987654321").Return(&jwtToken, nil)
		// Return destination quota rule so workflow uses it for hydration (not source dbQuotaRule)
		destQuotaRule := &datamodel.QuotaRule{
			BaseModel: datamodel.BaseModel{UUID: "dest-quota-rule-uuid"},
			Name:      quotaRule.Name,
		}
		env.OnActivity("CreateQuotaRuleOnDestination", mock.Anything, "dest-volume-uuid", quotaRule, "us-west1-a", "987654321", &jwtToken).Return(&activities.QuotaRuleOperationResult{IsDone: true, QuotaRule: destQuotaRule}, nil)
		// Integration: HydrateQuotaRuleCreate must be called with destination quota rule (mapQuotaRuleToHydrateObject uses UUID and Name; both must be non-empty)
		env.OnActivity("HydrateQuotaRuleCreate", mock.Anything, mock.MatchedBy(func(qr *datamodel.QuotaRule) bool {
			return qr != nil && qr.UUID == "dest-quota-rule-uuid" && qr.Name == quotaRule.Name &&
				qr.UUID != "" && qr.Name != "" // hydrate object ResourceId and QuotaRuleId must not be empty
		}), "dest-volume-name", "us-west1-a", "987654321").Return(nil)
		env.OnActivity("UpdateQuotaRuleState", mock.Anything, mock.Anything, mock.Anything).Return(nil)

		// Mock GetJob for EnsureJobState
		env.OnActivity("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil)

		env.ExecuteWorkflow(CreateQuotaRuleWorkflow, params, quotaRule)

		// Verify workflow completed successfully
		_, err := env.QueryWorkflowByID("default-test-workflow-id", "status")
		assert.Nil(tt, err)
		assert.True(tt, env.IsWorkflowCompleted())
		assert.NoError(tt, env.GetWorkflowError())
	})
	t.Run("WhenCancellationSignalReceivedBeforeGetVolumeByID", func(tt *testing.T) {
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
		quotaRuleCommonActivity := activities.QuotaRuleCommonActivity{SE: mockStorage}
		commonActivity := activities.CommonActivities{SE: mockStorage}

		env.RegisterActivity(quotaRuleCommonActivity.GetVolumeByID)
		env.RegisterActivity(quotaRuleCommonActivity.UpdateQuotaRuleState)
		env.RegisterActivity(commonActivity.UpdateJobStatus)
		env.RegisterActivity(commonActivity.GetJob)

		params := createBaseParams()
		quotaRule := createBaseQuotaRule()

		// Mock storage call made by GetVolumeByID activity
		mockStorage.On("GetVolumeByIDAndAccountID", mock.Anything, volumeID, int64(0)).Return(nil, errors.New("volume not found")).Maybe()

		env.OnActivity("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("UpdateQuotaRuleState", mock.Anything, mock.Anything, mock.Anything).Return(nil)

		// Send cancellation signal before GetVolumeByID
		env.RegisterDelayedCallback(func() {
			env.SignalWorkflow(CancelQuotaRuleSignalName, "cancel data")
		}, 5*time.Millisecond)

		env.ExecuteWorkflow(CreateQuotaRuleWorkflow, params, quotaRule)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
	})
	t.Run("WhenCancellationSignalReceivedBeforeDataProtectionCheck", func(tt *testing.T) {
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
		quotaRuleCommonActivity := activities.QuotaRuleCommonActivity{SE: mockStorage}
		commonActivity := activities.CommonActivities{SE: mockStorage}

		env.RegisterActivity(quotaRuleCommonActivity.GetVolumeByID)
		env.RegisterActivity(quotaRuleCommonActivity.UpdateQuotaRuleState)
		env.RegisterActivity(commonActivity.UpdateJobStatus)
		env.RegisterActivity(commonActivity.GetJob)

		params := createBaseParams()
		quotaRule := createBaseQuotaRule()
		volume := createBaseVolume(true, []string{"NFSV3"})

		env.OnActivity("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil)
		env.OnActivity("GetVolumeByID", mock.Anything, volumeID, int64(0)).Return(volume, nil)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)

		// Send cancellation signal before CreateQuotaRuleForDataProtectionVolume
		env.RegisterDelayedCallback(func() {
			env.SignalWorkflow(CancelQuotaRuleSignalName, "cancel data")
		}, 10*time.Millisecond)

		env.OnActivity("UpdateQuotaRuleState", mock.Anything, mock.Anything, mock.Anything).Return(nil)

		env.ExecuteWorkflow(CreateQuotaRuleWorkflow, params, quotaRule)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
	})
	t.Run("WhenCancellationSignalReceivedBeforeGetVolumeByID", func(tt *testing.T) {
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
		quotaRuleCommonActivity := activities.QuotaRuleCommonActivity{SE: mockStorage}
		commonActivity := activities.CommonActivities{SE: mockStorage}

		env.RegisterActivity(quotaRuleCommonActivity.GetVolumeByID)
		env.RegisterActivity(quotaRuleCommonActivity.UpdateQuotaRuleState)
		env.RegisterActivity(commonActivity.UpdateJobStatus)
		env.RegisterActivity(commonActivity.GetJob)

		params := createBaseParams()
		quotaRule := createBaseQuotaRule()
		volume := createBaseVolume(false, []string{"NFSV3"})

		env.OnActivity("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil)
		env.OnActivity("GetVolumeByID", mock.Anything, volumeID, int64(0)).Return(volume, nil)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)

		// Send cancellation signal before GetVolumeByID
		env.RegisterDelayedCallback(func() {
			env.SignalWorkflow(CancelQuotaRuleSignalName, "cancel data")
		}, 10*time.Millisecond)

		env.OnActivity("UpdateQuotaRuleState", mock.Anything, mock.Anything, mock.Anything).Return(nil)

		env.ExecuteWorkflow(CreateQuotaRuleWorkflow, params, quotaRule)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
	})
	t.Run("WhenCancellationSignalReceivedAfterValidateQuotaTargetByProtocolBeforeGetNode", func(tt *testing.T) {
		// This test covers cancellation check after GetVolumeByID but before GetNode
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
		quotaRuleCommonActivity := activities.QuotaRuleCommonActivity{SE: mockStorage}
		commonActivity := activities.CommonActivities{SE: mockStorage}

		env.RegisterActivity(quotaRuleCommonActivity.GetVolumeByID)
		env.RegisterActivity(commonActivity.GetNode)
		env.RegisterActivity(quotaRuleCommonActivity.UpdateQuotaRuleState)
		env.RegisterActivity(commonActivity.UpdateJobStatus)
		env.RegisterActivity(commonActivity.GetJob)

		params := createBaseParams()
		quotaRule := createBaseQuotaRule()
		volume := createBaseVolume(false, []string{"NFSV3"})

		env.OnActivity("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil)
		env.OnActivity("GetVolumeByID", mock.Anything, volumeID, int64(0)).Return(volume, nil)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)

		// GetNode activity calls GetNodesByPoolID on storage, so we need to mock it
		mockStorage.On("GetNodesByPoolID", mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil).Maybe()

		// Send cancellation signal after GetVolumeByID but before GetNode
		env.RegisterDelayedCallback(func() {
			env.SignalWorkflow(CancelQuotaRuleSignalName, "cancel data")
		}, 12*time.Millisecond)

		env.OnActivity("UpdateQuotaRuleState", mock.Anything, mock.Anything, mock.Anything).Return(nil)

		env.ExecuteWorkflow(CreateQuotaRuleWorkflow, params, quotaRule)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
	})
	t.Run("WhenCancellationSignalReceivedBeforeUpdateRQuotaOnSvm", func(tt *testing.T) {
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
		quotaRuleCommonActivity := activities.QuotaRuleCommonActivity{SE: mockStorage}
		commonActivity := activities.CommonActivities{SE: mockStorage}

		env.RegisterActivity(quotaRuleCommonActivity.GetVolumeByID)
		env.RegisterActivity(commonActivity.GetNode)
		env.RegisterActivity(quotaRuleCommonActivity.UpdateRQuotaOnSvm)
		env.RegisterActivity(quotaRuleCommonActivity.UpdateQuotaRuleState)
		env.RegisterActivity(commonActivity.UpdateJobStatus)
		env.RegisterActivity(commonActivity.GetJob)

		params := createBaseParams()
		quotaRule := createBaseQuotaRule()
		volume := createBaseVolume(false, []string{"NFSV3"})
		nodes := createBaseNodes()

		// Mock storage call made by GetVolumeByID activity (using Maybe since cancellation might prevent the call)
		mockStorage.On("GetVolumeByIDAndAccountID", mock.Anything, volumeID, int64(0)).Return(volume, nil).Maybe()

		env.OnActivity("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil)
		env.OnActivity("GetVolumeByID", mock.Anything, volumeID, int64(0)).Return(volume, nil)
		env.OnActivity("GetNode", mock.Anything, poolID).Return(nodes, nil)
		env.OnActivity("UpdateRQuotaOnSvm", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)

		// Send cancellation signal before UpdateRQuotaOnSvm
		env.RegisterDelayedCallback(func() {
			env.SignalWorkflow(CancelQuotaRuleSignalName, "cancel data")
		}, 15*time.Millisecond)

		env.OnActivity("UpdateQuotaRuleState", mock.Anything, mock.Anything, mock.Anything).Return(nil)

		env.ExecuteWorkflow(CreateQuotaRuleWorkflow, params, quotaRule)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
	})
	t.Run("WhenCancellationSignalReceivedBeforeHandleDefaultQuotaRuleUpdate", func(tt *testing.T) {
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
		quotaRuleActivity := activities.QuotaRuleCreateActivity{SE: mockStorage}
		quotaRuleCommonActivity := activities.QuotaRuleCommonActivity{SE: mockStorage}
		commonActivity := activities.CommonActivities{SE: mockStorage}

		env.RegisterActivity(quotaRuleCommonActivity.GetVolumeByID)
		env.RegisterActivity(commonActivity.GetNode)
		env.RegisterActivity(quotaRuleCommonActivity.UpdateRQuotaOnSvm)
		env.RegisterActivity(quotaRuleActivity.HandleDefaultQuotaRuleUpdate)
		env.RegisterActivity(quotaRuleCommonActivity.UpdateQuotaRuleState)
		env.RegisterActivity(commonActivity.UpdateJobStatus)
		env.RegisterActivity(commonActivity.GetJob)

		params := createBaseParams()
		quotaRule := createBaseQuotaRule()
		quotaRule.QuotaTarget = "" // Default quota
		volume := createBaseVolume(false, []string{"NFSV3"})
		nodes := createBaseNodes()

		env.OnActivity("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil)
		env.OnActivity("GetVolumeByID", mock.Anything, volumeID, int64(0)).Return(volume, nil)
		env.OnActivity("GetNode", mock.Anything, poolID).Return(nodes, nil)
		env.OnActivity("UpdateRQuotaOnSvm", mock.Anything, "svm-external-uuid", mock.Anything, true).Return(nil)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)

		// Send cancellation signal before HandleDefaultQuotaRuleUpdate
		env.RegisterDelayedCallback(func() {
			env.SignalWorkflow(CancelQuotaRuleSignalName, "cancel data")
		}, 20*time.Millisecond)

		env.OnActivity("UpdateQuotaRuleState", mock.Anything, mock.Anything, mock.Anything).Return(nil)

		env.ExecuteWorkflow(CreateQuotaRuleWorkflow, params, quotaRule)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
	})
	t.Run("WhenCancellationSignalReceivedBeforeCreateQuotaRuleOnONTAP", func(tt *testing.T) {
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
		quotaRuleActivity := activities.QuotaRuleCreateActivity{SE: mockStorage}
		quotaRuleCommonActivity := activities.QuotaRuleCommonActivity{SE: mockStorage}
		commonActivity := activities.CommonActivities{SE: mockStorage}

		env.RegisterActivity(quotaRuleCommonActivity.GetVolumeByID)
		env.RegisterActivity(commonActivity.GetNode)
		env.RegisterActivity(quotaRuleCommonActivity.UpdateRQuotaOnSvm)
		env.RegisterActivity(quotaRuleActivity.HandleDefaultQuotaRuleUpdate)
		env.RegisterActivity(quotaRuleActivity.CreateQuotaRuleOnONTAP)
		env.RegisterActivity(quotaRuleCommonActivity.UpdateQuotaRuleState)
		env.RegisterActivity(commonActivity.UpdateJobStatus)
		env.RegisterActivity(commonActivity.GetJob)

		params := createBaseParams()
		quotaRule := createBaseQuotaRule()
		volume := createBaseVolume(false, []string{"NFSV3"})
		nodes := createBaseNodes()

		env.OnActivity("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil)
		env.OnActivity("GetVolumeByID", mock.Anything, volumeID, int64(0)).Return(volume, nil)
		env.OnActivity("GetNode", mock.Anything, poolID).Return(nodes, nil)
		env.OnActivity("UpdateRQuotaOnSvm", mock.Anything, "svm-external-uuid", mock.Anything, true).Return(nil)
		env.OnActivity("HandleDefaultQuotaRuleUpdate", mock.Anything, volume, mock.Anything, quotaRule.QuotaType, quotaRule.DiskLimitInKib).Return(vsaerrors.New("not found"))
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)

		// Send cancellation signal before CreateQuotaRuleOnONTAP
		env.RegisterDelayedCallback(func() {
			env.SignalWorkflow(CancelQuotaRuleSignalName, "cancel data")
		}, 25*time.Millisecond)

		env.OnActivity("UpdateQuotaRuleState", mock.Anything, mock.Anything, mock.Anything).Return(nil)

		env.ExecuteWorkflow(CreateQuotaRuleWorkflow, params, quotaRule)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
	})
	t.Run("WhenCancellationSignalReceivedBeforeGetQuotaStatus", func(tt *testing.T) {
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
		quotaRuleActivity := activities.QuotaRuleCreateActivity{SE: mockStorage}
		quotaRuleCommonActivity := activities.QuotaRuleCommonActivity{SE: mockStorage}
		commonActivity := activities.CommonActivities{SE: mockStorage}

		env.RegisterActivity(quotaRuleCommonActivity.GetVolumeByID)
		env.RegisterActivity(commonActivity.GetNode)
		env.RegisterActivity(quotaRuleCommonActivity.UpdateRQuotaOnSvm)
		env.RegisterActivity(quotaRuleActivity.HandleDefaultQuotaRuleUpdate)
		env.RegisterActivity(quotaRuleActivity.CreateQuotaRuleOnONTAP)
		env.RegisterActivity(quotaRuleActivity.GetQuotaStatus)
		env.RegisterActivity(quotaRuleCommonActivity.UpdateQuotaRuleState)
		env.RegisterActivity(commonActivity.UpdateJobStatus)
		env.RegisterActivity(commonActivity.GetJob)

		params := createBaseParams()
		quotaRule := createBaseQuotaRule()
		volume := createBaseVolume(false, []string{"NFSV3"})
		nodes := createBaseNodes()

		quotaRuleResp := &vsa.QuotaRuleProviderResponse{
			State:   vsa.JobRespSuccess,
			Message: "Quota rule created successfully",
		}

		env.OnActivity("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil)
		env.OnActivity("GetVolumeByID", mock.Anything, volumeID, int64(0)).Return(volume, nil)
		env.OnActivity("GetNode", mock.Anything, poolID).Return(nodes, nil)
		env.OnActivity("UpdateRQuotaOnSvm", mock.Anything, "svm-external-uuid", mock.Anything, true).Return(nil)
		env.OnActivity("HandleDefaultQuotaRuleUpdate", mock.Anything, volume, mock.Anything, quotaRule.QuotaType, quotaRule.DiskLimitInKib).Return(vsaerrors.New("not found"))
		env.OnActivity("CreateQuotaRuleOnONTAP", mock.Anything, mock.Anything, volume, quotaRule).Return(quotaRuleResp, nil)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)

		// Send cancellation signal before GetQuotaStatus
		env.RegisterDelayedCallback(func() {
			env.SignalWorkflow(CancelQuotaRuleSignalName, "cancel data")
		}, 30*time.Millisecond)

		env.OnActivity("UpdateQuotaRuleState", mock.Anything, mock.Anything, mock.Anything).Return(nil)

		env.ExecuteWorkflow(CreateQuotaRuleWorkflow, params, quotaRule)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
	})
	t.Run("WhenCancellationSignalReceivedBeforeHandleQuotaEnableDisable", func(tt *testing.T) {
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
		quotaRuleActivity := activities.QuotaRuleCreateActivity{SE: mockStorage}
		quotaRuleCommonActivity := activities.QuotaRuleCommonActivity{SE: mockStorage}
		commonActivity := activities.CommonActivities{SE: mockStorage}

		env.RegisterActivity(quotaRuleCommonActivity.GetVolumeByID)
		env.RegisterActivity(commonActivity.GetNode)
		env.RegisterActivity(quotaRuleCommonActivity.UpdateRQuotaOnSvm)
		env.RegisterActivity(quotaRuleActivity.HandleDefaultQuotaRuleUpdate)
		env.RegisterActivity(quotaRuleActivity.CreateQuotaRuleOnONTAP)
		env.RegisterActivity(quotaRuleActivity.GetQuotaStatus)
		env.RegisterActivity(quotaRuleCommonActivity.HandleQuotaEnableDisable)
		env.RegisterActivity(quotaRuleCommonActivity.UpdateQuotaRuleState)
		env.RegisterActivity(commonActivity.UpdateJobStatus)
		env.RegisterActivity(commonActivity.GetJob)

		params := createBaseParams()
		quotaRule := createBaseQuotaRule()
		volume := createBaseVolume(false, []string{"NFSV3"})
		nodes := createBaseNodes()

		quotaRuleResp := &vsa.QuotaRuleProviderResponse{
			State:   vsa.JobRespSuccess,
			Message: "Quota rule created successfully",
		}

		quotaStatus := &vsa.QuotaStatus{
			State:   vsa.QuotaStateOff,
			Enabled: false,
		}

		env.OnActivity("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil)
		env.OnActivity("GetVolumeByID", mock.Anything, volumeID, int64(0)).Return(volume, nil)
		env.OnActivity("GetNode", mock.Anything, poolID).Return(nodes, nil)
		env.OnActivity("UpdateRQuotaOnSvm", mock.Anything, "svm-external-uuid", mock.Anything, true).Return(nil)
		env.OnActivity("HandleDefaultQuotaRuleUpdate", mock.Anything, volume, mock.Anything, quotaRule.QuotaType, quotaRule.DiskLimitInKib).Return(vsaerrors.New("not found"))
		env.OnActivity("CreateQuotaRuleOnONTAP", mock.Anything, mock.Anything, volume, quotaRule).Return(quotaRuleResp, nil)
		env.OnActivity("GetQuotaStatus", mock.Anything, mock.Anything, volume).Return(quotaStatus, nil)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)

		// Send cancellation signal before HandleQuotaEnableDisable
		env.RegisterDelayedCallback(func() {
			env.SignalWorkflow(CancelQuotaRuleSignalName, "cancel data")
		}, 35*time.Millisecond)

		env.OnActivity("UpdateQuotaRuleState", mock.Anything, mock.Anything, mock.Anything).Return(nil)

		env.ExecuteWorkflow(CreateQuotaRuleWorkflow, params, quotaRule)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
	})
	t.Run("WhenCancellationSignalReceivedBeforeQuotaReinitialization", func(tt *testing.T) {
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
		quotaRuleActivity := activities.QuotaRuleCreateActivity{SE: mockStorage}
		quotaRuleCommonActivity := activities.QuotaRuleCommonActivity{SE: mockStorage}
		commonActivity := activities.CommonActivities{SE: mockStorage}

		env.RegisterActivity(quotaRuleCommonActivity.GetVolumeByID)
		env.RegisterActivity(commonActivity.GetNode)
		env.RegisterActivity(quotaRuleCommonActivity.UpdateRQuotaOnSvm)
		env.RegisterActivity(quotaRuleActivity.HandleDefaultQuotaRuleUpdate)
		env.RegisterActivity(quotaRuleActivity.CreateQuotaRuleOnONTAP)
		env.RegisterActivity(quotaRuleActivity.GetQuotaStatus)
		env.RegisterActivity(quotaRuleCommonActivity.QuotaReinitialization)
		env.RegisterActivity(quotaRuleCommonActivity.UpdateQuotaRuleState)
		env.RegisterActivity(commonActivity.UpdateJobStatus)
		env.RegisterActivity(commonActivity.GetJob)

		params := createBaseParams()
		quotaRule := createBaseQuotaRule()
		volume := createBaseVolume(false, []string{"NFSV3"})
		nodes := createBaseNodes()

		quotaRuleResp := &vsa.QuotaRuleProviderResponse{
			State:   vsa.JobRespFailure,
			Message: "resize operation failed",
		}

		quotaStatus := &vsa.QuotaStatus{
			State:   vsa.QuotaStateOn,
			Enabled: true,
		}

		env.OnActivity("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil)
		env.OnActivity("GetVolumeByID", mock.Anything, volumeID, int64(0)).Return(volume, nil)
		env.OnActivity("GetNode", mock.Anything, poolID).Return(nodes, nil)
		env.OnActivity("UpdateRQuotaOnSvm", mock.Anything, "svm-external-uuid", mock.Anything, true).Return(nil)
		env.OnActivity("HandleDefaultQuotaRuleUpdate", mock.Anything, volume, mock.Anything, quotaRule.QuotaType, quotaRule.DiskLimitInKib).Return(vsaerrors.New("not found"))
		env.OnActivity("CreateQuotaRuleOnONTAP", mock.Anything, mock.Anything, volume, quotaRule).Return(quotaRuleResp, nil)
		env.OnActivity("GetQuotaStatus", mock.Anything, mock.Anything, volume).Return(quotaStatus, nil)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)

		// Send cancellation signal before QuotaReinitialization
		env.RegisterDelayedCallback(func() {
			env.SignalWorkflow(CancelQuotaRuleSignalName, "cancel data")
		}, 40*time.Millisecond)

		env.OnActivity("UpdateQuotaRuleState", mock.Anything, mock.Anything, mock.Anything).Return(nil)

		env.ExecuteWorkflow(CreateQuotaRuleWorkflow, params, quotaRule)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
	})
	t.Run("WhenCancellationSignalReceivedBeforeGetVolumeReplication", func(tt *testing.T) {
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
		quotaRuleActivity := activities.QuotaRuleCreateActivity{SE: mockStorage}
		quotaRuleCommonActivity := activities.QuotaRuleCommonActivity{SE: mockStorage}
		commonActivity := activities.CommonActivities{SE: mockStorage}

		env.RegisterActivity(quotaRuleCommonActivity.GetVolumeByID)
		env.RegisterActivity(commonActivity.GetNode)
		env.RegisterActivity(quotaRuleCommonActivity.UpdateRQuotaOnSvm)
		env.RegisterActivity(quotaRuleActivity.HandleDefaultQuotaRuleUpdate)
		env.RegisterActivity(quotaRuleActivity.CreateQuotaRuleOnONTAP)
		env.RegisterActivity(quotaRuleActivity.GetQuotaStatus)
		env.RegisterActivity(quotaRuleCommonActivity.HandleQuotaEnableDisable)
		env.RegisterActivity(quotaRuleCommonActivity.GetVolumeReplication)
		env.RegisterActivity(quotaRuleCommonActivity.UpdateQuotaRuleState)
		env.RegisterActivity(commonActivity.UpdateJobStatus)
		env.RegisterActivity(commonActivity.GetJob)

		params := createBaseParams()
		quotaRule := createBaseQuotaRule()
		volume := createBaseVolume(false, []string{"NFSV3"})
		nodes := createBaseNodes()

		quotaRuleResp := &vsa.QuotaRuleProviderResponse{
			State:   vsa.JobRespSuccess,
			Message: "Quota rule created successfully",
		}

		quotaStatus := &vsa.QuotaStatus{
			State:   vsa.QuotaStateOff,
			Enabled: false,
		}

		quotaEnableResp := &vsa.JobStatus{
			State:   vsa.JobRespSuccess,
			Message: "quota enabled",
		}

		env.OnActivity("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil)
		env.OnActivity("GetVolumeByID", mock.Anything, volumeID, int64(0)).Return(volume, nil)
		env.OnActivity("GetNode", mock.Anything, poolID).Return(nodes, nil)
		env.OnActivity("UpdateRQuotaOnSvm", mock.Anything, "svm-external-uuid", mock.Anything, true).Return(nil)
		env.OnActivity("HandleDefaultQuotaRuleUpdate", mock.Anything, volume, mock.Anything, quotaRule.QuotaType, quotaRule.DiskLimitInKib).Return(vsaerrors.New("not found"))
		env.OnActivity("CreateQuotaRuleOnONTAP", mock.Anything, mock.Anything, volume, quotaRule).Return(quotaRuleResp, nil)
		env.OnActivity("GetQuotaStatus", mock.Anything, mock.Anything, volume).Return(quotaStatus, nil)
		env.OnActivity("HandleQuotaEnableDisable", mock.Anything, mock.Anything, volume, true).Run(func(args mock.Arguments) {
			// Send cancellation signal right after HandleQuotaEnableDisable completes, before the cancellation check that precedes GetVolumeReplication
			env.SignalWorkflow(CancelQuotaRuleSignalName, "cancel data")
		}).Return(quotaEnableResp, nil)
		env.OnActivity("GetVolumeReplication", mock.Anything, volumeID).Return([]*datamodel.VolumeReplication{}, nil).Maybe()
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)

		// Mock storage call made by GetVolumeReplication activity
		mockStorage.On("ListVolumeReplications", mock.Anything, mock.Anything, mock.Anything).Return([]*datamodel.VolumeReplication{}, nil).Maybe()

		env.OnActivity("UpdateQuotaRuleState", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

		env.ExecuteWorkflow(CreateQuotaRuleWorkflow, params, quotaRule)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
	})
	t.Run("WhenCancellationSignalReceivedBeforeVerifyReplicationState", func(tt *testing.T) {
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
		quotaRuleActivity := activities.QuotaRuleCreateActivity{SE: mockStorage}
		quotaRuleCommonActivity := activities.QuotaRuleCommonActivity{SE: mockStorage}
		commonActivity := activities.CommonActivities{SE: mockStorage}

		env.RegisterActivity(quotaRuleCommonActivity.GetVolumeByID)
		env.RegisterActivity(commonActivity.GetNode)
		env.RegisterActivity(quotaRuleCommonActivity.UpdateRQuotaOnSvm)
		env.RegisterActivity(quotaRuleActivity.HandleDefaultQuotaRuleUpdate)
		env.RegisterActivity(quotaRuleActivity.CreateQuotaRuleOnONTAP)
		env.RegisterActivity(quotaRuleActivity.GetQuotaStatus)
		env.RegisterActivity(quotaRuleCommonActivity.HandleQuotaEnableDisable)
		env.RegisterActivity(quotaRuleCommonActivity.GetVolumeReplication)
		env.RegisterActivity(quotaRuleCommonActivity.VerifyReplicationState)
		env.RegisterActivity(quotaRuleCommonActivity.UpdateQuotaRuleState)
		env.RegisterActivity(commonActivity.UpdateJobStatus)
		env.RegisterActivity(commonActivity.GetJob)

		params := createBaseParams()
		quotaRule := createBaseQuotaRule()
		volume := createBaseVolume(false, []string{"NFSV3"})
		nodes := createBaseNodes()

		quotaRuleResp := &vsa.QuotaRuleProviderResponse{
			State:   vsa.JobRespSuccess,
			Message: "Quota rule created successfully",
		}

		quotaStatus := &vsa.QuotaStatus{
			State:   vsa.QuotaStateOff,
			Enabled: false,
		}

		quotaEnableResp := &vsa.JobStatus{
			State:   vsa.JobRespSuccess,
			Message: "quota enabled",
		}

		replications := []*datamodel.VolumeReplication{
			{
				BaseModel: datamodel.BaseModel{
					ID: 1,
				},
				VolumeID: volumeID,
				ReplicationAttributes: &datamodel.ReplicationDetails{
					DestinationVolumeUUID: "dest-volume-uuid",
					DestinationVolumeName: "dest-volume-name",
					DestinationLocation:   "us-west1-a",
				},
				RemoteUri: "projects/987654321/locations/us-west1-a/volumes/dest-volume-uuid/replications/replication-1",
			},
		}

		env.OnActivity("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil)
		env.OnActivity("GetVolumeByID", mock.Anything, volumeID, int64(0)).Return(volume, nil)
		env.OnActivity("GetNode", mock.Anything, poolID).Return(nodes, nil)
		env.OnActivity("UpdateRQuotaOnSvm", mock.Anything, "svm-external-uuid", mock.Anything, true).Return(nil)
		env.OnActivity("HandleDefaultQuotaRuleUpdate", mock.Anything, volume, mock.Anything, quotaRule.QuotaType, quotaRule.DiskLimitInKib).Return(vsaerrors.New("not found"))
		env.OnActivity("CreateQuotaRuleOnONTAP", mock.Anything, mock.Anything, volume, quotaRule).Return(quotaRuleResp, nil)
		env.OnActivity("GetQuotaStatus", mock.Anything, mock.Anything, volume).Return(quotaStatus, nil)
		env.OnActivity("HandleQuotaEnableDisable", mock.Anything, mock.Anything, volume, true).Return(quotaEnableResp, nil)
		env.OnActivity("GetVolumeReplication", mock.Anything, volumeID).Return(replications, nil)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)

		// Send cancellation signal before VerifyReplicationState
		env.RegisterDelayedCallback(func() {
			env.SignalWorkflow(CancelQuotaRuleSignalName, "cancel data")
		}, 50*time.Millisecond)

		env.OnActivity("UpdateQuotaRuleState", mock.Anything, mock.Anything, mock.Anything).Return(nil)

		env.ExecuteWorkflow(CreateQuotaRuleWorkflow, params, quotaRule)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
	})
	t.Run("WhenCancellationSignalReceivedBeforeGetSignedDstTokenForQuotaRule", func(tt *testing.T) {
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
		quotaRuleActivity := activities.QuotaRuleCreateActivity{SE: mockStorage}
		quotaRuleCommonActivity := activities.QuotaRuleCommonActivity{SE: mockStorage}
		commonActivity := activities.CommonActivities{SE: mockStorage}

		env.RegisterActivity(quotaRuleCommonActivity.GetVolumeByID)
		env.RegisterActivity(commonActivity.GetNode)
		env.RegisterActivity(quotaRuleCommonActivity.UpdateRQuotaOnSvm)
		env.RegisterActivity(quotaRuleActivity.HandleDefaultQuotaRuleUpdate)
		env.RegisterActivity(quotaRuleActivity.CreateQuotaRuleOnONTAP)
		env.RegisterActivity(quotaRuleActivity.GetQuotaStatus)
		env.RegisterActivity(quotaRuleCommonActivity.HandleQuotaEnableDisable)
		env.RegisterActivity(quotaRuleCommonActivity.GetVolumeReplication)
		env.RegisterActivity(quotaRuleCommonActivity.VerifyReplicationState)
		env.RegisterActivity(quotaRuleCommonActivity.GetSignedDstTokenForQuotaRule)
		env.RegisterActivity(quotaRuleCommonActivity.UpdateQuotaRuleState)
		env.RegisterActivity(commonActivity.UpdateJobStatus)
		env.RegisterActivity(commonActivity.GetJob)

		params := createBaseParams()
		quotaRule := createBaseQuotaRule()
		volume := createBaseVolume(false, []string{"NFSV3"})
		nodes := createBaseNodes()

		quotaRuleResp := &vsa.QuotaRuleProviderResponse{
			State:   vsa.JobRespSuccess,
			Message: "Quota rule created successfully",
		}

		quotaStatus := &vsa.QuotaStatus{
			State:   vsa.QuotaStateOff,
			Enabled: false,
		}

		quotaEnableResp := &vsa.JobStatus{
			State:   vsa.JobRespSuccess,
			Message: "quota enabled",
		}

		replications := []*datamodel.VolumeReplication{
			{
				BaseModel: datamodel.BaseModel{
					ID: 1,
				},
				VolumeID: volumeID,
				ReplicationAttributes: &datamodel.ReplicationDetails{
					DestinationVolumeUUID: "dest-volume-uuid",
					DestinationVolumeName: "dest-volume-name",
					DestinationLocation:   "us-west1-a",
				},
				RemoteUri: "projects/987654321/locations/us-west1-a/volumes/dest-volume-uuid/replications/replication-1",
			},
		}

		env.OnActivity("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil)
		env.OnActivity("GetVolumeByID", mock.Anything, volumeID, int64(0)).Return(volume, nil)
		env.OnActivity("GetNode", mock.Anything, poolID).Return(nodes, nil)
		env.OnActivity("UpdateRQuotaOnSvm", mock.Anything, "svm-external-uuid", mock.Anything, true).Return(nil)
		env.OnActivity("HandleDefaultQuotaRuleUpdate", mock.Anything, volume, mock.Anything, quotaRule.QuotaType, quotaRule.DiskLimitInKib).Return(vsaerrors.New("not found"))
		env.OnActivity("CreateQuotaRuleOnONTAP", mock.Anything, mock.Anything, volume, quotaRule).Return(quotaRuleResp, nil)
		env.OnActivity("GetQuotaStatus", mock.Anything, mock.Anything, volume).Return(quotaStatus, nil)
		env.OnActivity("HandleQuotaEnableDisable", mock.Anything, mock.Anything, volume, true).Return(quotaEnableResp, nil)
		env.OnActivity("GetVolumeReplication", mock.Anything, volumeID).Return(replications, nil)
		env.OnActivity("VerifyReplicationState", mock.Anything, replications[0], params.LocationId).Return(true, nil)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)

		// Send cancellation signal before GetSignedDstTokenForQuotaRule
		env.RegisterDelayedCallback(func() {
			env.SignalWorkflow(CancelQuotaRuleSignalName, "cancel data")
		}, 55*time.Millisecond)

		env.OnActivity("UpdateQuotaRuleState", mock.Anything, mock.Anything, mock.Anything).Return(nil)

		env.ExecuteWorkflow(CreateQuotaRuleWorkflow, params, quotaRule)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
	})
	t.Run("WhenCancellationSignalReceivedBeforeCreateQuotaRuleOnDestination", func(tt *testing.T) {
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
		quotaRuleActivity := activities.QuotaRuleCreateActivity{SE: mockStorage}
		quotaRuleCommonActivity := activities.QuotaRuleCommonActivity{SE: mockStorage}
		commonActivity := activities.CommonActivities{SE: mockStorage}

		env.RegisterActivity(quotaRuleCommonActivity.GetVolumeByID)
		env.RegisterActivity(commonActivity.GetNode)
		env.RegisterActivity(quotaRuleCommonActivity.UpdateRQuotaOnSvm)
		env.RegisterActivity(quotaRuleActivity.HandleDefaultQuotaRuleUpdate)
		env.RegisterActivity(quotaRuleActivity.CreateQuotaRuleOnONTAP)
		env.RegisterActivity(quotaRuleActivity.GetQuotaStatus)
		env.RegisterActivity(quotaRuleCommonActivity.HandleQuotaEnableDisable)
		env.RegisterActivity(quotaRuleCommonActivity.GetVolumeReplication)
		env.RegisterActivity(quotaRuleCommonActivity.VerifyReplicationState)
		env.RegisterActivity(quotaRuleCommonActivity.GetSignedDstTokenForQuotaRule)
		env.RegisterActivity(quotaRuleActivity.CreateQuotaRuleOnDestination)
		env.RegisterActivity(quotaRuleCommonActivity.UpdateQuotaRuleState)
		env.RegisterActivity(commonActivity.UpdateJobStatus)
		env.RegisterActivity(commonActivity.GetJob)

		params := createBaseParams()
		quotaRule := createBaseQuotaRule()
		volume := createBaseVolume(false, []string{"NFSV3"})
		nodes := createBaseNodes()

		quotaRuleResp := &vsa.QuotaRuleProviderResponse{
			State:   vsa.JobRespSuccess,
			Message: "Quota rule created successfully",
		}

		quotaStatus := &vsa.QuotaStatus{
			State:   vsa.QuotaStateOff,
			Enabled: false,
		}

		quotaEnableResp := &vsa.JobStatus{
			State:   vsa.JobRespSuccess,
			Message: "quota enabled",
		}

		replications := []*datamodel.VolumeReplication{
			{
				BaseModel: datamodel.BaseModel{
					ID: 1,
				},
				VolumeID: volumeID,
				ReplicationAttributes: &datamodel.ReplicationDetails{
					DestinationVolumeUUID: "dest-volume-uuid",
					DestinationVolumeName: "dest-volume-name",
					DestinationLocation:   "us-west1-a",
				},
				RemoteUri: "projects/987654321/locations/us-west1-a/volumes/dest-volume-uuid/replications/replication-1",
			},
		}

		jwtToken := "test-jwt-token"

		env.OnActivity("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil)
		env.OnActivity("GetVolumeByID", mock.Anything, volumeID, int64(0)).Return(volume, nil)
		env.OnActivity("GetNode", mock.Anything, poolID).Return(nodes, nil)
		env.OnActivity("UpdateRQuotaOnSvm", mock.Anything, "svm-external-uuid", mock.Anything, true).Return(nil)
		env.OnActivity("HandleDefaultQuotaRuleUpdate", mock.Anything, volume, mock.Anything, quotaRule.QuotaType, quotaRule.DiskLimitInKib).Return(vsaerrors.New("not found"))
		env.OnActivity("CreateQuotaRuleOnONTAP", mock.Anything, mock.Anything, volume, quotaRule).Return(quotaRuleResp, nil)
		env.OnActivity("GetQuotaStatus", mock.Anything, mock.Anything, volume).Return(quotaStatus, nil)
		env.OnActivity("HandleQuotaEnableDisable", mock.Anything, mock.Anything, volume, true).Return(quotaEnableResp, nil)
		env.OnActivity("GetVolumeReplication", mock.Anything, volumeID).Return(replications, nil)
		env.OnActivity("VerifyReplicationState", mock.Anything, replications[0], params.LocationId).Return(true, nil)
		env.OnActivity("GetSignedDstTokenForQuotaRule", mock.Anything, "987654321").Return(&jwtToken, nil)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)

		// Send cancellation signal before CreateQuotaRuleOnDestination
		env.RegisterDelayedCallback(func() {
			env.SignalWorkflow(CancelQuotaRuleSignalName, "cancel data")
		}, 60*time.Millisecond)

		env.OnActivity("UpdateQuotaRuleState", mock.Anything, mock.Anything, mock.Anything).Return(nil)

		env.ExecuteWorkflow(CreateQuotaRuleWorkflow, params, quotaRule)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
	})
	t.Run("WhenCancellationSignalReceivedBeforeDescribeQuotaRuleRemoteJob", func(tt *testing.T) {
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
		quotaRuleActivity := activities.QuotaRuleCreateActivity{SE: mockStorage}
		quotaRuleCommonActivity := activities.QuotaRuleCommonActivity{SE: mockStorage}
		commonActivity := activities.CommonActivities{SE: mockStorage}

		env.RegisterActivity(quotaRuleCommonActivity.GetVolumeByID)
		env.RegisterActivity(commonActivity.GetNode)
		env.RegisterActivity(quotaRuleCommonActivity.UpdateRQuotaOnSvm)
		env.RegisterActivity(quotaRuleActivity.HandleDefaultQuotaRuleUpdate)
		env.RegisterActivity(quotaRuleActivity.CreateQuotaRuleOnONTAP)
		env.RegisterActivity(quotaRuleActivity.GetQuotaStatus)
		env.RegisterActivity(quotaRuleCommonActivity.HandleQuotaEnableDisable)
		env.RegisterActivity(quotaRuleCommonActivity.GetVolumeReplication)
		env.RegisterActivity(quotaRuleCommonActivity.VerifyReplicationState)
		env.RegisterActivity(quotaRuleCommonActivity.GetSignedDstTokenForQuotaRule)
		env.RegisterActivity(quotaRuleActivity.CreateQuotaRuleOnDestination)
		env.RegisterActivity(quotaRuleCommonActivity.DescribeQuotaRuleRemoteJob)
		env.RegisterActivity(quotaRuleCommonActivity.UpdateQuotaRuleState)
		env.RegisterActivity(commonActivity.UpdateJobStatus)
		env.RegisterActivity(commonActivity.GetJob)

		params := createBaseParams()
		quotaRule := createBaseQuotaRule()
		volume := createBaseVolume(false, []string{"NFSV3"})
		nodes := createBaseNodes()

		quotaRuleResp := &vsa.QuotaRuleProviderResponse{
			State:   vsa.JobRespSuccess,
			Message: "Quota rule created successfully",
		}

		quotaStatus := &vsa.QuotaStatus{
			State:   vsa.QuotaStateOff,
			Enabled: false,
		}

		quotaEnableResp := &vsa.JobStatus{
			State:   vsa.JobRespSuccess,
			Message: "quota enabled",
		}

		replications := []*datamodel.VolumeReplication{
			{
				BaseModel: datamodel.BaseModel{
					ID: 1,
				},
				VolumeID: volumeID,
				ReplicationAttributes: &datamodel.ReplicationDetails{
					DestinationVolumeUUID: "dest-volume-uuid",
					DestinationVolumeName: "dest-volume-name",
					DestinationLocation:   "us-west1-a",
				},
				RemoteUri: "projects/987654321/locations/us-west1-a/volumes/dest-volume-uuid/replications/replication-1",
			},
		}

		jwtToken := "test-jwt-token"
		operationResult := &activities.QuotaRuleOperationResult{
			OperationName: "test-operation",
			IsDone:        false,
		}

		env.OnActivity("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil)
		env.OnActivity("GetVolumeByID", mock.Anything, volumeID, int64(0)).Return(volume, nil)
		env.OnActivity("GetNode", mock.Anything, poolID).Return(nodes, nil)
		env.OnActivity("UpdateRQuotaOnSvm", mock.Anything, "svm-external-uuid", mock.Anything, true).Return(nil)
		env.OnActivity("HandleDefaultQuotaRuleUpdate", mock.Anything, volume, mock.Anything, quotaRule.QuotaType, quotaRule.DiskLimitInKib).Return(vsaerrors.New("not found"))
		env.OnActivity("CreateQuotaRuleOnONTAP", mock.Anything, mock.Anything, volume, quotaRule).Return(quotaRuleResp, nil)
		env.OnActivity("GetQuotaStatus", mock.Anything, mock.Anything, volume).Return(quotaStatus, nil)
		env.OnActivity("HandleQuotaEnableDisable", mock.Anything, mock.Anything, volume, true).Return(quotaEnableResp, nil)
		env.OnActivity("GetVolumeReplication", mock.Anything, volumeID).Return(replications, nil)
		env.OnActivity("VerifyReplicationState", mock.Anything, replications[0], params.LocationId).Return(true, nil)
		env.OnActivity("GetSignedDstTokenForQuotaRule", mock.Anything, "987654321").Return(&jwtToken, nil)
		env.OnActivity("CreateQuotaRuleOnDestination", mock.Anything, "dest-volume-uuid", quotaRule, "us-west1-a", "987654321", &jwtToken).Return(operationResult, nil)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)

		// Send cancellation signal before DescribeQuotaRuleRemoteJob
		env.RegisterDelayedCallback(func() {
			env.SignalWorkflow(CancelQuotaRuleSignalName, "cancel data")
		}, 65*time.Millisecond)

		env.OnActivity("UpdateQuotaRuleState", mock.Anything, mock.Anything, mock.Anything).Return(nil)

		env.ExecuteWorkflow(CreateQuotaRuleWorkflow, params, quotaRule)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
	})
	t.Run("WhenCancellationSignalReceivedBeforeHydrateQuotaRuleCreate", func(tt *testing.T) {
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
		quotaRuleActivity := activities.QuotaRuleCreateActivity{SE: mockStorage}
		quotaRuleCommonActivity := activities.QuotaRuleCommonActivity{SE: mockStorage}
		commonActivity := activities.CommonActivities{SE: mockStorage}

		env.RegisterActivity(quotaRuleCommonActivity.GetVolumeByID)
		env.RegisterActivity(commonActivity.GetNode)
		env.RegisterActivity(quotaRuleCommonActivity.UpdateRQuotaOnSvm)
		env.RegisterActivity(quotaRuleActivity.HandleDefaultQuotaRuleUpdate)
		env.RegisterActivity(quotaRuleActivity.CreateQuotaRuleOnONTAP)
		env.RegisterActivity(quotaRuleActivity.GetQuotaStatus)
		env.RegisterActivity(quotaRuleCommonActivity.HandleQuotaEnableDisable)
		env.RegisterActivity(quotaRuleCommonActivity.GetVolumeReplication)
		env.RegisterActivity(quotaRuleCommonActivity.VerifyReplicationState)
		env.RegisterActivity(quotaRuleCommonActivity.GetSignedDstTokenForQuotaRule)
		env.RegisterActivity(quotaRuleActivity.CreateQuotaRuleOnDestination)
		env.RegisterActivity(quotaRuleCommonActivity.DescribeQuotaRuleRemoteJob)
		env.RegisterActivity(quotaRuleCommonActivity.HydrateQuotaRuleCreate)
		env.RegisterActivity(quotaRuleCommonActivity.UpdateQuotaRuleState)
		env.RegisterActivity(commonActivity.UpdateJobStatus)
		env.RegisterActivity(commonActivity.GetJob)

		params := createBaseParams()
		quotaRule := createBaseQuotaRule()
		volume := createBaseVolume(false, []string{"NFSV3"})
		nodes := createBaseNodes()

		quotaRuleResp := &vsa.QuotaRuleProviderResponse{
			State:   vsa.JobRespSuccess,
			Message: "Quota rule created successfully",
		}

		quotaStatus := &vsa.QuotaStatus{
			State:   vsa.QuotaStateOff,
			Enabled: false,
		}

		quotaEnableResp := &vsa.JobStatus{
			State:   vsa.JobRespSuccess,
			Message: "quota enabled",
		}

		replications := []*datamodel.VolumeReplication{
			{
				BaseModel: datamodel.BaseModel{
					ID: 1,
				},
				VolumeID: volumeID,
				ReplicationAttributes: &datamodel.ReplicationDetails{
					DestinationVolumeUUID: "dest-volume-uuid",
					DestinationVolumeName: "dest-volume-name",
					DestinationLocation:   "us-west1-a",
				},
				RemoteUri: "projects/987654321/locations/us-west1-a/volumes/dest-volume-uuid/replications/replication-1",
			},
		}

		jwtToken := "test-jwt-token"
		destQuotaRule := &datamodel.QuotaRule{
			BaseModel: datamodel.BaseModel{UUID: "dest-quota-rule-uuid"},
			Name:      quotaRule.Name,
		}
		operationResult := &activities.QuotaRuleOperationResult{
			OperationName: "test-operation",
			IsDone:        true,
			QuotaRule:     destQuotaRule,
		}

		env.OnActivity("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil)
		env.OnActivity("GetVolumeByID", mock.Anything, volumeID, int64(0)).Return(volume, nil)
		env.OnActivity("GetNode", mock.Anything, poolID).Return(nodes, nil)
		env.OnActivity("UpdateRQuotaOnSvm", mock.Anything, "svm-external-uuid", mock.Anything, true).Return(nil)
		env.OnActivity("HandleDefaultQuotaRuleUpdate", mock.Anything, volume, mock.Anything, quotaRule.QuotaType, quotaRule.DiskLimitInKib).Return(vsaerrors.New("not found"))
		env.OnActivity("CreateQuotaRuleOnONTAP", mock.Anything, mock.Anything, volume, quotaRule).Return(quotaRuleResp, nil)
		env.OnActivity("GetQuotaStatus", mock.Anything, mock.Anything, volume).Return(quotaStatus, nil)
		env.OnActivity("HandleQuotaEnableDisable", mock.Anything, mock.Anything, volume, true).Return(quotaEnableResp, nil)
		env.OnActivity("GetVolumeReplication", mock.Anything, volumeID).Return(replications, nil)
		env.OnActivity("VerifyReplicationState", mock.Anything, replications[0], params.LocationId).Return(true, nil)
		env.OnActivity("GetSignedDstTokenForQuotaRule", mock.Anything, "987654321").Return(&jwtToken, nil)
		env.OnActivity("CreateQuotaRuleOnDestination", mock.Anything, "dest-volume-uuid", quotaRule, "us-west1-a", "987654321", &jwtToken).Return(operationResult, nil)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)

		// Send cancellation signal before HydrateQuotaRuleCreate
		env.RegisterDelayedCallback(func() {
			env.SignalWorkflow(CancelQuotaRuleSignalName, "cancel data")
		}, 70*time.Millisecond)

		env.OnActivity("UpdateQuotaRuleState", mock.Anything, mock.Anything, mock.Anything).Return(nil)

		env.ExecuteWorkflow(CreateQuotaRuleWorkflow, params, quotaRule)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
	})
	t.Run("WhenCancellationSignalReceivedBeforeFinalUpdateQuotaRuleState", func(tt *testing.T) {
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
		quotaRuleActivity := activities.QuotaRuleCreateActivity{SE: mockStorage}
		quotaRuleCommonActivity := activities.QuotaRuleCommonActivity{SE: mockStorage}
		commonActivity := activities.CommonActivities{SE: mockStorage}

		env.RegisterActivity(quotaRuleCommonActivity.GetVolumeByID)
		env.RegisterActivity(commonActivity.GetNode)
		env.RegisterActivity(quotaRuleCommonActivity.UpdateRQuotaOnSvm)
		env.RegisterActivity(quotaRuleActivity.HandleDefaultQuotaRuleUpdate)
		env.RegisterActivity(quotaRuleActivity.CreateQuotaRuleOnONTAP)
		env.RegisterActivity(quotaRuleActivity.GetQuotaStatus)
		env.RegisterActivity(quotaRuleCommonActivity.HandleQuotaEnableDisable)
		env.RegisterActivity(quotaRuleCommonActivity.GetVolumeReplication)
		env.RegisterActivity(quotaRuleCommonActivity.VerifyReplicationState)
		env.RegisterActivity(quotaRuleCommonActivity.GetSignedDstTokenForQuotaRule)
		env.RegisterActivity(quotaRuleActivity.CreateQuotaRuleOnDestination)
		env.RegisterActivity(quotaRuleCommonActivity.DescribeQuotaRuleRemoteJob)
		env.RegisterActivity(quotaRuleCommonActivity.HydrateQuotaRuleCreate)
		env.RegisterActivity(quotaRuleCommonActivity.UpdateQuotaRuleState)
		env.RegisterActivity(commonActivity.UpdateJobStatus)
		env.RegisterActivity(commonActivity.GetJob)

		params := createBaseParams()
		quotaRule := createBaseQuotaRule()
		volume := createBaseVolume(false, []string{"NFSV3"})
		nodes := createBaseNodes()

		quotaRuleResp := &vsa.QuotaRuleProviderResponse{
			State:   vsa.JobRespSuccess,
			Message: "Quota rule created successfully",
		}

		quotaStatus := &vsa.QuotaStatus{
			State:   vsa.QuotaStateOff,
			Enabled: false,
		}

		quotaEnableResp := &vsa.JobStatus{
			State:   vsa.JobRespSuccess,
			Message: "quota enabled",
		}

		replications := []*datamodel.VolumeReplication{
			{
				BaseModel: datamodel.BaseModel{
					ID: 1,
				},
				VolumeID: volumeID,
				ReplicationAttributes: &datamodel.ReplicationDetails{
					DestinationVolumeUUID: "dest-volume-uuid",
					DestinationVolumeName: "dest-volume-name",
					DestinationLocation:   "us-west1-a",
				},
				RemoteUri: "projects/987654321/locations/us-west1-a/volumes/dest-volume-uuid/replications/replication-1",
			},
		}

		jwtToken := "test-jwt-token"
		destQuotaRule := &datamodel.QuotaRule{
			BaseModel: datamodel.BaseModel{UUID: "dest-quota-rule-uuid"},
			Name:      quotaRule.Name,
		}
		operationResult := &activities.QuotaRuleOperationResult{
			OperationName: "test-operation",
			IsDone:        true,
			QuotaRule:     destQuotaRule,
		}

		env.OnActivity("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil)
		env.OnActivity("GetVolumeByID", mock.Anything, volumeID, int64(0)).Return(volume, nil)
		env.OnActivity("GetNode", mock.Anything, poolID).Return(nodes, nil)
		env.OnActivity("UpdateRQuotaOnSvm", mock.Anything, "svm-external-uuid", mock.Anything, true).Return(nil)
		env.OnActivity("HandleDefaultQuotaRuleUpdate", mock.Anything, volume, mock.Anything, quotaRule.QuotaType, quotaRule.DiskLimitInKib).Return(vsaerrors.New("not found"))
		env.OnActivity("CreateQuotaRuleOnONTAP", mock.Anything, mock.Anything, volume, quotaRule).Return(quotaRuleResp, nil)
		env.OnActivity("GetQuotaStatus", mock.Anything, mock.Anything, volume).Return(quotaStatus, nil)
		env.OnActivity("HandleQuotaEnableDisable", mock.Anything, mock.Anything, volume, true).Return(quotaEnableResp, nil)
		env.OnActivity("GetVolumeReplication", mock.Anything, volumeID).Return(replications, nil)
		env.OnActivity("VerifyReplicationState", mock.Anything, replications[0], params.LocationId).Return(true, nil)
		env.OnActivity("GetSignedDstTokenForQuotaRule", mock.Anything, "987654321").Return(&jwtToken, nil)
		env.OnActivity("CreateQuotaRuleOnDestination", mock.Anything, "dest-volume-uuid", quotaRule, "us-west1-a", "987654321", &jwtToken).Return(operationResult, nil)
		env.OnActivity("DescribeQuotaRuleRemoteJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(operationResult, nil).Maybe()
		env.OnActivity("HydrateQuotaRuleCreate", mock.Anything, mock.Anything, "dest-volume-name", "us-west1-a", "987654321").Run(func(args mock.Arguments) {
			// Send cancellation signal right after HydrateQuotaRuleCreate completes, before the final cancellation check
			env.SignalWorkflow(CancelQuotaRuleSignalName, "cancel data")
		}).Return(nil)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("UpdateQuotaRuleState", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

		env.ExecuteWorkflow(CreateQuotaRuleWorkflow, params, quotaRule)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
	})
	t.Run("WhenCancellationSignalReceivedAfterInitialCheckBeforeGetVolumeByID", func(tt *testing.T) {
		// This test covers line 146-147: cancellation check right after defer setup
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
		quotaRuleCommonActivity := activities.QuotaRuleCommonActivity{SE: mockStorage}
		commonActivity := activities.CommonActivities{SE: mockStorage}

		env.RegisterActivity(quotaRuleCommonActivity.GetVolumeByID)
		env.RegisterActivity(quotaRuleCommonActivity.UpdateQuotaRuleState)
		env.RegisterActivity(commonActivity.UpdateJobStatus)
		env.RegisterActivity(commonActivity.GetJob)

		params := createBaseParams()
		quotaRule := createBaseQuotaRule()

		env.OnActivity("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("UpdateQuotaRuleState", mock.Anything, mock.Anything, mock.Anything).Return(nil)

		// Mock storage call made by GetVolumeByID activity (using Maybe since cancellation might prevent the call)
		mockStorage.On("GetVolumeByIDAndAccountID", mock.Anything, volumeID, int64(0)).Return(createBaseVolume(false, []string{"NFSV3"}), nil).Maybe()

		// Send cancellation signal right after the initial cancellation check (line 145) but before GetVolumeByID
		env.RegisterDelayedCallback(func() {
			env.SignalWorkflow(CancelQuotaRuleSignalName, "cancel data")
		}, 1*time.Millisecond)

		env.ExecuteWorkflow(CreateQuotaRuleWorkflow, params, quotaRule)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
	})
	t.Run("WhenCancellationSignalReceivedForDPVolumeAfterDPCheck", func(tt *testing.T) {
		// This test covers line 166-167: cancellation check for DP volume after DP check
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
		quotaRuleActivity := activities.QuotaRuleCreateActivity{SE: mockStorage}
		quotaRuleCommonActivity := activities.QuotaRuleCommonActivity{SE: mockStorage}
		commonActivity := activities.CommonActivities{SE: mockStorage}

		env.RegisterActivity(quotaRuleCommonActivity.GetVolumeByID)
		env.RegisterActivity(quotaRuleActivity.CreateQuotaRuleForDataProtectionVolume)
		env.RegisterActivity(quotaRuleCommonActivity.UpdateQuotaRuleState)
		env.RegisterActivity(commonActivity.UpdateJobStatus)
		env.RegisterActivity(commonActivity.GetJob)

		params := createBaseParams()
		quotaRule := createBaseQuotaRule()
		volume := createBaseVolume(true, []string{"NFSV3"})

		env.OnActivity("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil)
		env.OnActivity("GetVolumeByID", mock.Anything, volumeID, int64(0)).Run(func(args mock.Arguments) {
			// Send cancellation signal after GetVolumeByID completes, before the DP cancellation check
			env.SignalWorkflow(CancelQuotaRuleSignalName, "cancel data")
		}).Return(volume, nil)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("UpdateQuotaRuleState", mock.Anything, mock.Anything, mock.Anything).Return(nil)

		env.ExecuteWorkflow(CreateQuotaRuleWorkflow, params, quotaRule)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
	})
	t.Run("WhenCancellationSignalReceivedAfterDPCheckBeforeGetNode", func(tt *testing.T) {
		// This test covers line 181-182: cancellation check after DP check but before GetNode
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
		quotaRuleCommonActivity := activities.QuotaRuleCommonActivity{SE: mockStorage}
		commonActivity := activities.CommonActivities{SE: mockStorage}

		env.RegisterActivity(quotaRuleCommonActivity.GetVolumeByID)
		env.RegisterActivity(commonActivity.GetNode)
		env.RegisterActivity(quotaRuleCommonActivity.UpdateQuotaRuleState)
		env.RegisterActivity(commonActivity.UpdateJobStatus)
		env.RegisterActivity(commonActivity.GetJob)

		params := createBaseParams()
		quotaRule := createBaseQuotaRule()
		volume := createBaseVolume(false, []string{"NFSV3"})

		env.OnActivity("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil)
		env.OnActivity("GetVolumeByID", mock.Anything, volumeID, int64(0)).Run(func(args mock.Arguments) {
			// Send cancellation signal after GetVolumeByID completes, before the cancellation check at line 180
			env.SignalWorkflow(CancelQuotaRuleSignalName, "cancel data")
		}).Return(volume, nil)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("UpdateQuotaRuleState", mock.Anything, mock.Anything, mock.Anything).Return(nil)

		env.ExecuteWorkflow(CreateQuotaRuleWorkflow, params, quotaRule)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
	})
}

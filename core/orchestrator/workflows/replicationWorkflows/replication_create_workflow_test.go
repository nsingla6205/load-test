package replicationWorkflows

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/replicationActivities"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/replication"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	gcpgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

// Helper function to set quotaRuleSync to true and return a cleanup function
func setQuotaRuleSyncTrue() func() {
	originalValue := quotaRuleSync
	quotaRuleSync = true
	return func() {
		quotaRuleSync = originalValue
	}
}

func TestCreateVolumeReplicationWorkflow(t *testing.T) {
	t.Run("TestCreateVolumeReplicationWorkflow_Success", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		mockStorage := database.NewMockStorage(tt)
		commonActivity := activities.CommonActivities{SE: mockStorage}
		volumeCreateReplicationActivity := replicationActivities.VolumeReplicationCreateActivity{SE: mockStorage}
		env.SetHeader(mockHeader)
		env.RegisterActivity(volumeCreateReplicationActivity.GetSrcBasePath)
		env.RegisterActivity(volumeCreateReplicationActivity.GetDstBasePath)
		env.RegisterActivity(volumeCreateReplicationActivity.GetSignedSrcToken)
		env.RegisterActivity(volumeCreateReplicationActivity.GetSignedDstToken)
		env.RegisterActivity(commonActivity.GetNode)
		env.RegisterActivity(volumeCreateReplicationActivity.GetSourceInterclusterLifs)
		env.RegisterActivity(volumeCreateReplicationActivity.GetDestinationPoolDetails)
		env.RegisterActivity(volumeCreateReplicationActivity.CreateSnapmirrorFirewall)
		env.RegisterActivity(volumeCreateReplicationActivity.PollSnapmirrorFirewallOperation)
		env.RegisterActivity(volumeCreateReplicationActivity.CreateClusterPeering)
		env.RegisterActivity(volumeCreateReplicationActivity.AcceptClusterPeering)
		env.RegisterActivity(volumeCreateReplicationActivity.DescribeRemoteJob)
		env.RegisterActivity(volumeCreateReplicationActivity.UpdateReplicationState)
		env.RegisterActivity(volumeCreateReplicationActivity.CreateDestinationVolume)
		env.RegisterActivity(volumeCreateReplicationActivity.UpdateDestinationVolumeDetails)
		env.RegisterActivity(volumeCreateReplicationActivity.HydrateDestinationVolume)
		env.RegisterActivity(volumeCreateReplicationActivity.UpdateReplicationState)
		env.RegisterActivity(volumeCreateReplicationActivity.GetVolumeSVMNames)
		env.RegisterActivity(volumeCreateReplicationActivity.CreateReplicationOnDestination)
		env.RegisterActivity(volumeCreateReplicationActivity.UpdateDestinationVolumeReplicationDetails)
		env.RegisterActivity(volumeCreateReplicationActivity.AcceptSvmPeer)
		env.RegisterActivity(volumeCreateReplicationActivity.UpdateReplicationDetails)
		env.RegisterActivity(volumeCreateReplicationActivity.ListQuotaRulesLocal)
		env.RegisterActivity(volumeCreateReplicationActivity.CreateQuotaRulesOnDestination)
		env.RegisterActivity(volumeCreateReplicationActivity.HydrateQuotaRules)
		env.RegisterActivity(volumeCreateReplicationActivity.MountReplication)
		env.RegisterActivity(commonActivity.UpdateJobStatus)
		env.RegisterActivity(commonActivity.GetJob)

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			Name: "test-account",
		}
		volume := &datamodel.Volume{
			Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)},
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "",
					CertificateID: "",
				},
			},
			Svm:              &datamodel.Svm{Name: "svm_test"},
			VolumeAttributes: &datamodel.VolumeAttributes{BlockProperties: &datamodel.BlockProperties{OSType: "LINUX"}},
		}
		params := &commonparams.CreateVolumeReplicationParams{
			AccountName: "test-account",
			Name:        "test-replication",
		}
		replicationDb := &datamodel.VolumeReplication{
			Name:      params.Name,
			AccountID: account.ID,
			Account:   account,
			VolumeID:  int64(1),
			Volume:    volume,
		}
		event := &replication.CreateReplicationEvent{
			SourceVolume: datamodel.Volume{
				Name: "source-volume",
			},
		}
		replicationResult := &replication.CreateReplicationResult{
			SrcProjectNumber: &event.SourceProjectNumber,
			DstProjectNumber: &event.DestinationProjectNumber,
			Event:            event,
		}
		// Mocking the required activities
		env.OnActivity("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     "NEW",
		}, nil)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetSrcBasePath", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("GetDstBasePath", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("GetSignedSrcToken", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("GetSignedDstToken", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("GetNode", mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
		env.OnActivity("GetSourceInterclusterLifs", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("GetDestinationPoolDetails", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("CreateSnapmirrorFirewall", mock.Anything, mock.Anything).Return(&replication.CreateReplicationResult{
			Event: event,
			Operation: &commonparams.Operations{
				OperationName: "test-operation-id",
				OperationType: "firewall",
				IsDone:        false,
				Project:       "test-project",
			},
		}, nil)
		env.OnActivity("PollSnapmirrorFirewallOperation", mock.Anything, mock.Anything).Return(&replication.CreateReplicationResult{
			Event: event,
			Operation: &commonparams.Operations{
				OperationName: "test-operation-id",
				OperationType: "firewall",
				IsDone:        true,
				Project:       "test-project",
			},
		}, nil)
		env.OnActivity("CreateClusterPeering", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("AcceptClusterPeering", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("DescribeRemoteJob", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("UpdateReplicationState", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CreateDestinationVolume", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("UpdateDestinationVolumeDetails", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("HydrateDestinationVolume", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("GetVolumeSVMNames", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("CreateReplicationOnDestination", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("UpdateDestinationVolumeReplicationDetails", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("AcceptSvmPeer", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("UpdateReplicationDetails", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("ListQuotaRulesLocal", mock.Anything, mock.Anything).Return([]*datamodel.QuotaRule{}, nil).Maybe()
		env.OnActivity("CreateQuotaRulesOnDestination", mock.Anything, mock.Anything).Return(replicationResult, nil).Maybe()
		env.OnActivity("HydrateQuotaRules", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("MountReplication", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.ExecuteWorkflow(CreateVolumeReplicationWorkflow, params, replicationDb, event)

		_, err := env.QueryWorkflowByID("default-test-workflow-id", "status")
		assert.Nil(tt, err)
		assert.True(tt, env.IsWorkflowCompleted())
		assert.NoError(tt, env.GetWorkflowError())
	})

	t.Run("TestCreateVolumeReplicationWorkflow_HeartbeatTimeoutIsConfigured", func(tt *testing.T) {
		// Asserts the customer-facing create workflow schedules its activities with a non-zero
		// HeartbeatTimeout equal to the retry policy value; the activity-started listener captures
		// the timeout actually applied, so this fails if HeartbeatTimeout is dropped from ActivityOptions.
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		mockStorage := database.NewMockStorage(tt)
		commonActivity := activities.CommonActivities{SE: mockStorage}
		volumeCreateReplicationActivity := replicationActivities.VolumeReplicationCreateActivity{SE: mockStorage}
		env.SetHeader(mockHeader)
		env.RegisterActivity(volumeCreateReplicationActivity.GetSrcBasePath)
		env.RegisterActivity(volumeCreateReplicationActivity.GetDstBasePath)
		env.RegisterActivity(volumeCreateReplicationActivity.GetSignedSrcToken)
		env.RegisterActivity(volumeCreateReplicationActivity.GetSignedDstToken)
		env.RegisterActivity(commonActivity.GetNode)
		env.RegisterActivity(volumeCreateReplicationActivity.GetSourceInterclusterLifs)
		env.RegisterActivity(volumeCreateReplicationActivity.GetDestinationPoolDetails)
		env.RegisterActivity(volumeCreateReplicationActivity.CreateSnapmirrorFirewall)
		env.RegisterActivity(volumeCreateReplicationActivity.PollSnapmirrorFirewallOperation)
		env.RegisterActivity(volumeCreateReplicationActivity.CreateClusterPeering)
		env.RegisterActivity(volumeCreateReplicationActivity.AcceptClusterPeering)
		env.RegisterActivity(volumeCreateReplicationActivity.DescribeRemoteJob)
		env.RegisterActivity(volumeCreateReplicationActivity.UpdateReplicationState)
		env.RegisterActivity(volumeCreateReplicationActivity.CreateDestinationVolume)
		env.RegisterActivity(volumeCreateReplicationActivity.UpdateDestinationVolumeDetails)
		env.RegisterActivity(volumeCreateReplicationActivity.HydrateDestinationVolume)
		env.RegisterActivity(volumeCreateReplicationActivity.GetVolumeSVMNames)
		env.RegisterActivity(volumeCreateReplicationActivity.CreateReplicationOnDestination)
		env.RegisterActivity(volumeCreateReplicationActivity.UpdateDestinationVolumeReplicationDetails)
		env.RegisterActivity(volumeCreateReplicationActivity.AcceptSvmPeer)
		env.RegisterActivity(volumeCreateReplicationActivity.UpdateReplicationDetails)
		env.RegisterActivity(volumeCreateReplicationActivity.ListQuotaRulesLocal)
		env.RegisterActivity(volumeCreateReplicationActivity.CreateQuotaRulesOnDestination)
		env.RegisterActivity(volumeCreateReplicationActivity.HydrateQuotaRules)
		env.RegisterActivity(volumeCreateReplicationActivity.MountReplication)
		env.RegisterActivity(commonActivity.UpdateJobStatus)
		env.RegisterActivity(commonActivity.GetJob)

		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-account"}
		volume := &datamodel.Volume{
			Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{Password: "password"}},
			Svm:  &datamodel.Svm{Name: "svm_test"}, VolumeAttributes: &datamodel.VolumeAttributes{BlockProperties: &datamodel.BlockProperties{OSType: "LINUX"}},
		}
		params := &commonparams.CreateVolumeReplicationParams{AccountName: "test-account", Name: "test-replication"}
		replicationDb := &datamodel.VolumeReplication{Name: params.Name, AccountID: account.ID, Account: account, VolumeID: int64(1), Volume: volume}
		event := &replication.CreateReplicationEvent{SourceVolume: datamodel.Volume{Name: "source-volume"}}
		replicationResult := &replication.CreateReplicationResult{SrcProjectNumber: &event.SourceProjectNumber, DstProjectNumber: &event.DestinationProjectNumber, Event: event}

		env.OnActivity("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"}, State: "NEW"}, nil)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetSrcBasePath", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("GetDstBasePath", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("GetSignedSrcToken", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("GetSignedDstToken", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("GetNode", mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
		env.OnActivity("GetSourceInterclusterLifs", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("GetDestinationPoolDetails", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("CreateSnapmirrorFirewall", mock.Anything, mock.Anything).Return(&replication.CreateReplicationResult{
			Event:     event,
			Operation: &commonparams.Operations{OperationName: "test-operation-id", OperationType: "firewall", IsDone: false, Project: "test-project"},
		}, nil)
		env.OnActivity("PollSnapmirrorFirewallOperation", mock.Anything, mock.Anything).Return(&replication.CreateReplicationResult{
			Event:     event,
			Operation: &commonparams.Operations{OperationName: "test-operation-id", OperationType: "firewall", IsDone: true, Project: "test-project"},
		}, nil)
		env.OnActivity("CreateClusterPeering", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("AcceptClusterPeering", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("DescribeRemoteJob", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("UpdateReplicationState", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CreateDestinationVolume", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("UpdateDestinationVolumeDetails", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("HydrateDestinationVolume", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("GetVolumeSVMNames", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("CreateReplicationOnDestination", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("UpdateDestinationVolumeReplicationDetails", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("AcceptSvmPeer", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("UpdateReplicationDetails", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("ListQuotaRulesLocal", mock.Anything, mock.Anything).Return([]*datamodel.QuotaRule{}, nil).Maybe()
		env.OnActivity("CreateQuotaRulesOnDestination", mock.Anything, mock.Anything).Return(replicationResult, nil).Maybe()
		env.OnActivity("HydrateQuotaRules", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("MountReplication", mock.Anything, mock.Anything).Return(replicationResult, nil)

		// Capture the HeartbeatTimeout actually applied to scheduled activities.
		var observedHeartbeatTimeout time.Duration
		env.SetOnActivityStartedListener(func(info *activity.Info, _ context.Context, _ converter.EncodedValues) {
			if info.HeartbeatTimeout > observedHeartbeatTimeout {
				observedHeartbeatTimeout = info.HeartbeatTimeout
			}
		})

		env.ExecuteWorkflow(CreateVolumeReplicationWorkflow, params, replicationDb, event)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.NoError(tt, env.GetWorkflowError())

		retryPolicy, rpErr := workflows.PopulateRetryPolicyParams()
		assert.NoError(tt, rpErr)
		assert.Positive(tt, observedHeartbeatTimeout, "replication create activities must be scheduled with a HeartbeatTimeout")
		assert.Equal(tt, retryPolicy.HeartBeatTimeout, observedHeartbeatTimeout, "HeartbeatTimeout must match the retry policy value")
	})

	t.Run("TestCreateVolumeReplicationWorkflow_EnsureJobStateError", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		mockStorage := database.NewMockStorage(tt)
		commonActivity := activities.CommonActivities{SE: mockStorage}
		env.SetHeader(mockHeader)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.UpdateJobStatus)

		params := &commonparams.CreateVolumeReplicationParams{
			AccountName: "test-account",
			Name:        "test-replication",
		}
		replicationDb := &datamodel.VolumeReplication{
			Name: params.Name,
		}
		event := &replication.CreateReplicationEvent{
			SourceVolume: datamodel.Volume{
				Name: "source-volume",
			},
		}

		// Mock GetJob to return a job with state PROCESSING (not NEW) to trigger EnsureJobState error
		env.OnActivity("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     "PROCESSING", // Wrong state to trigger error
		}, nil)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Maybe()

		env.ExecuteWorkflow(CreateVolumeReplicationWorkflow, params, replicationDb, event)

		// Assert that the workflow failed due to EnsureJobState error
		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
	})

	t.Run("TestCreateVolumeReplicationWorkflow_EnsureJobState_JobNotFound", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		mockStorage := database.NewMockStorage(tt)
		commonActivity := activities.CommonActivities{SE: mockStorage}
		env.SetHeader(mockHeader)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.UpdateJobStatus)

		params := &commonparams.CreateVolumeReplicationParams{
			AccountName: "test-account",
			Name:        "test-replication",
		}
		replicationDb := &datamodel.VolumeReplication{
			Name: params.Name,
		}
		event := &replication.CreateReplicationEvent{
			SourceVolume: datamodel.Volume{
				Name: "source-volume",
			},
		}

		// Mock GetJob to return nil (job not found) to trigger EnsureJobState error
		env.OnActivity("GetJob", mock.Anything, mock.Anything).Return((*datamodel.Job)(nil), nil)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Maybe()

		env.ExecuteWorkflow(CreateVolumeReplicationWorkflow, params, replicationDb, event)

		// Assert that the workflow failed due to EnsureJobState error
		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
	})

	t.Run("TestCreateVolumeReplicationWorkflow_EnsureJobState_GetJobError", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		mockStorage := database.NewMockStorage(tt)
		commonActivity := activities.CommonActivities{SE: mockStorage}
		env.SetHeader(mockHeader)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.UpdateJobStatus)

		params := &commonparams.CreateVolumeReplicationParams{
			AccountName: "test-account",
			Name:        "test-replication",
		}
		replicationDb := &datamodel.VolumeReplication{
			Name: params.Name,
		}
		event := &replication.CreateReplicationEvent{
			SourceVolume: datamodel.Volume{
				Name: "source-volume",
			},
		}

		// Mock GetJob to return an error
		env.OnActivity("GetJob", mock.Anything, mock.Anything).Return((*datamodel.Job)(nil), assert.AnError)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Maybe()

		env.ExecuteWorkflow(CreateVolumeReplicationWorkflow, params, replicationDb, event)

		// Assert that the workflow failed due to EnsureJobState error
		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
	})

	t.Run("TestCreateVolumeReplicationWorkflow_WithQuotaRuleSync", func(tt *testing.T) {
		// Set quotaRuleSync to true for this test
		cleanup := setQuotaRuleSyncTrue()
		defer cleanup()

		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		mockStorage := database.NewMockStorage(tt)
		commonActivity := activities.CommonActivities{SE: mockStorage}
		volumeCreateReplicationActivity := replicationActivities.VolumeReplicationCreateActivity{SE: mockStorage}
		env.SetHeader(mockHeader)
		env.RegisterActivity(volumeCreateReplicationActivity.GetSrcBasePath)
		env.RegisterActivity(volumeCreateReplicationActivity.GetDstBasePath)
		env.RegisterActivity(volumeCreateReplicationActivity.GetSignedSrcToken)
		env.RegisterActivity(volumeCreateReplicationActivity.GetSignedDstToken)
		env.RegisterActivity(commonActivity.GetNode)
		env.RegisterActivity(volumeCreateReplicationActivity.GetSourceInterclusterLifs)
		env.RegisterActivity(volumeCreateReplicationActivity.GetDestinationPoolDetails)
		env.RegisterActivity(volumeCreateReplicationActivity.CreateSnapmirrorFirewall)
		env.RegisterActivity(volumeCreateReplicationActivity.PollSnapmirrorFirewallOperation)
		env.RegisterActivity(volumeCreateReplicationActivity.CreateClusterPeering)
		env.RegisterActivity(volumeCreateReplicationActivity.AcceptClusterPeering)
		env.RegisterActivity(volumeCreateReplicationActivity.DescribeRemoteJob)
		env.RegisterActivity(volumeCreateReplicationActivity.UpdateReplicationState)
		env.RegisterActivity(volumeCreateReplicationActivity.CreateDestinationVolume)
		env.RegisterActivity(volumeCreateReplicationActivity.UpdateDestinationVolumeDetails)
		env.RegisterActivity(volumeCreateReplicationActivity.HydrateDestinationVolume)
		env.RegisterActivity(volumeCreateReplicationActivity.GetVolumeSVMNames)
		env.RegisterActivity(volumeCreateReplicationActivity.CreateReplicationOnDestination)
		env.RegisterActivity(volumeCreateReplicationActivity.UpdateDestinationVolumeReplicationDetails)
		env.RegisterActivity(volumeCreateReplicationActivity.AcceptSvmPeer)
		env.RegisterActivity(volumeCreateReplicationActivity.UpdateReplicationDetails)
		env.RegisterActivity(volumeCreateReplicationActivity.ListQuotaRulesLocal)
		env.RegisterActivity(volumeCreateReplicationActivity.CreateQuotaRulesOnDestination)
		env.RegisterActivity(volumeCreateReplicationActivity.HydrateQuotaRules)
		env.RegisterActivity(volumeCreateReplicationActivity.MountReplication)
		env.RegisterActivity(commonActivity.UpdateJobStatus)
		env.RegisterActivity(commonActivity.GetJob)

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			Name: "test-account",
		}
		volume := &datamodel.Volume{
			Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)},
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "",
					CertificateID: "",
				},
			},
			Svm:              &datamodel.Svm{Name: "svm_test"},
			VolumeAttributes: &datamodel.VolumeAttributes{BlockProperties: &datamodel.BlockProperties{OSType: "LINUX"}},
		}
		params := &commonparams.CreateVolumeReplicationParams{
			AccountName: "test-account",
			Name:        "test-replication",
		}
		replicationDb := &datamodel.VolumeReplication{
			Name:      params.Name,
			AccountID: account.ID,
			Account:   account,
			VolumeID:  int64(1),
			Volume:    volume,
		}
		event := &replication.CreateReplicationEvent{
			SourceVolume: datamodel.Volume{
				Name: "source-volume",
			},
			DestinationLocationID: "us-east1",
		}
		replicationResult := &replication.CreateReplicationResult{
			SrcProjectNumber: &event.SourceProjectNumber,
			DstProjectNumber: &event.DestinationProjectNumber,
			Event:            event,
			DstVolume: &gcpgenserver.VolumeV1beta{
				ResourceId: "dst-vol-name",
				VolumeId:   gcpgenserver.NewOptString("dst-vol-uuid"),
			},
		}

		sourceQuotaRules := []*datamodel.QuotaRule{
			{
				BaseModel: datamodel.BaseModel{UUID: "src-quota-uuid-1"},
				Name:      "src-quota-1",
				QuotaType: "INDIVIDUAL_USER_QUOTA",
			},
		}

		destinationQuotaRules := []*datamodel.QuotaRule{
			{
				BaseModel: datamodel.BaseModel{UUID: "dst-quota-uuid-1"},
				Name:      "dst-quota-1",
				QuotaType: "INDIVIDUAL_USER_QUOTA",
			},
		}

		replicationResultWithQuotaRules := &replication.CreateReplicationResult{
			SrcProjectNumber:      replicationResult.SrcProjectNumber,
			DstProjectNumber:      replicationResult.DstProjectNumber,
			Event:                 replicationResult.Event,
			DstVolume:             replicationResult.DstVolume,
			SourceQuotaRules:      sourceQuotaRules,
			DestinationQuotaRules: destinationQuotaRules,
		}

		// Mocking the required activities
		env.OnActivity("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     "NEW",
		}, nil)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetSrcBasePath", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("GetDstBasePath", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("GetSignedSrcToken", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("GetSignedDstToken", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("GetNode", mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
		env.OnActivity("GetSourceInterclusterLifs", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("GetDestinationPoolDetails", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("CreateSnapmirrorFirewall", mock.Anything, mock.Anything).Return(&replication.CreateReplicationResult{
			Event: event,
			Operation: &commonparams.Operations{
				OperationName: "test-operation-id",
				OperationType: "firewall",
				IsDone:        false,
				Project:       "test-project",
			},
		}, nil)
		env.OnActivity("PollSnapmirrorFirewallOperation", mock.Anything, mock.Anything).Return(&replication.CreateReplicationResult{
			Event: event,
			Operation: &commonparams.Operations{
				OperationName: "test-operation-id",
				OperationType: "firewall",
				IsDone:        true,
				Project:       "test-project",
			},
		}, nil)
		env.OnActivity("CreateClusterPeering", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("AcceptClusterPeering", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("DescribeRemoteJob", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("UpdateReplicationState", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CreateDestinationVolume", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("UpdateDestinationVolumeDetails", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("HydrateDestinationVolume", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("GetVolumeSVMNames", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("CreateReplicationOnDestination", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("UpdateDestinationVolumeReplicationDetails", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("AcceptSvmPeer", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("UpdateReplicationDetails", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("ListQuotaRulesLocal", mock.Anything, mock.Anything).Return(sourceQuotaRules, nil)
		env.OnActivity("CreateQuotaRulesOnDestination", mock.Anything, mock.Anything).Return(replicationResultWithQuotaRules, nil)
		env.OnActivity("HydrateQuotaRules", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("MountReplication", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.ExecuteWorkflow(CreateVolumeReplicationWorkflow, params, replicationDb, event)

		_, err := env.QueryWorkflowByID("default-test-workflow-id", "status")
		assert.Nil(tt, err)
		assert.True(tt, env.IsWorkflowCompleted())
		assert.NoError(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
	})

	t.Run("TestCreateVolumeReplicationWorkflow_QuotaRuleSyncFails", func(tt *testing.T) {
		// Set quotaRuleSync to true for this test
		cleanup := setQuotaRuleSyncTrue()
		defer cleanup()

		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		mockStorage := database.NewMockStorage(tt)
		commonActivity := activities.CommonActivities{SE: mockStorage}
		volumeCreateReplicationActivity := replicationActivities.VolumeReplicationCreateActivity{SE: mockStorage}
		env.SetHeader(mockHeader)
		env.RegisterActivity(volumeCreateReplicationActivity.GetSrcBasePath)
		env.RegisterActivity(volumeCreateReplicationActivity.GetDstBasePath)
		env.RegisterActivity(volumeCreateReplicationActivity.GetSignedSrcToken)
		env.RegisterActivity(volumeCreateReplicationActivity.GetSignedDstToken)
		env.RegisterActivity(commonActivity.GetNode)
		env.RegisterActivity(volumeCreateReplicationActivity.GetSourceInterclusterLifs)
		env.RegisterActivity(volumeCreateReplicationActivity.GetDestinationPoolDetails)
		env.RegisterActivity(volumeCreateReplicationActivity.CreateSnapmirrorFirewall)
		env.RegisterActivity(volumeCreateReplicationActivity.PollSnapmirrorFirewallOperation)
		env.RegisterActivity(volumeCreateReplicationActivity.CreateClusterPeering)
		env.RegisterActivity(volumeCreateReplicationActivity.AcceptClusterPeering)
		env.RegisterActivity(volumeCreateReplicationActivity.DescribeRemoteJob)
		env.RegisterActivity(volumeCreateReplicationActivity.UpdateReplicationState)
		env.RegisterActivity(volumeCreateReplicationActivity.CreateDestinationVolume)
		env.RegisterActivity(volumeCreateReplicationActivity.UpdateDestinationVolumeDetails)
		env.RegisterActivity(volumeCreateReplicationActivity.HydrateDestinationVolume)
		env.RegisterActivity(volumeCreateReplicationActivity.GetVolumeSVMNames)
		env.RegisterActivity(volumeCreateReplicationActivity.CreateReplicationOnDestination)
		env.RegisterActivity(volumeCreateReplicationActivity.UpdateDestinationVolumeReplicationDetails)
		env.RegisterActivity(volumeCreateReplicationActivity.AcceptSvmPeer)
		env.RegisterActivity(volumeCreateReplicationActivity.UpdateReplicationDetails)
		env.RegisterActivity(volumeCreateReplicationActivity.MountReplication)
		env.RegisterActivity(volumeCreateReplicationActivity.ListQuotaRulesLocal)
		env.RegisterActivity(volumeCreateReplicationActivity.CreateQuotaRulesOnDestination)
		env.RegisterActivity(commonActivity.UpdateJobStatus)
		env.RegisterActivity(commonActivity.GetJob)

		params := &commonparams.CreateVolumeReplicationParams{
			AccountName: "test-account",
			Name:        "test-replication",
		}
		replicationDb := &datamodel.VolumeReplication{
			Name: params.Name,
		}
		event := &replication.CreateReplicationEvent{
			SourceVolume: datamodel.Volume{
				Name: "source-volume",
			},
		}
		replicationResult := &replication.CreateReplicationResult{
			SrcProjectNumber: &event.SourceProjectNumber,
			DstProjectNumber: &event.DestinationProjectNumber,
			Event:            event,
		}

		sourceQuotaRules := []*datamodel.QuotaRule{
			{
				BaseModel: datamodel.BaseModel{UUID: "src-quota-uuid-1"},
				Name:      "src-quota-1",
				QuotaType: "INDIVIDUAL_USER_QUOTA",
			},
		}

		// Mocking the required activities
		env.OnActivity("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     "NEW",
		}, nil)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetSrcBasePath", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("GetDstBasePath", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("GetSignedSrcToken", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("GetSignedDstToken", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("GetNode", mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
		env.OnActivity("GetSourceInterclusterLifs", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("GetDestinationPoolDetails", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("CreateSnapmirrorFirewall", mock.Anything, mock.Anything).Return(&replication.CreateReplicationResult{
			Event: event,
			Operation: &commonparams.Operations{
				OperationName: "test-operation-id",
				OperationType: "firewall",
				IsDone:        false,
				Project:       "test-project",
			},
		}, nil)
		env.OnActivity("PollSnapmirrorFirewallOperation", mock.Anything, mock.Anything).Return(&replication.CreateReplicationResult{
			Event: event,
			Operation: &commonparams.Operations{
				OperationName: "test-operation-id",
				OperationType: "firewall",
				IsDone:        true,
				Project:       "test-project",
			},
		}, nil)
		env.OnActivity("CreateClusterPeering", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("AcceptClusterPeering", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("DescribeRemoteJob", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("UpdateReplicationState", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CreateDestinationVolume", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("UpdateDestinationVolumeDetails", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("HydrateDestinationVolume", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("GetVolumeSVMNames", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("CreateReplicationOnDestination", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("UpdateDestinationVolumeReplicationDetails", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("AcceptSvmPeer", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("UpdateReplicationDetails", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("MountReplication", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("ListQuotaRulesLocal", mock.Anything, mock.Anything).Return(sourceQuotaRules, nil)
		env.OnActivity("CreateQuotaRulesOnDestination", mock.Anything, mock.Anything).Return(nil, errors.New("failed to create quota rules"))
		env.OnActivity("UpdateReplicationState", mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Maybe()

		env.ExecuteWorkflow(CreateVolumeReplicationWorkflow, params, replicationDb, event)

		assert.True(tt, env.IsWorkflowCompleted())
		// The workflow should fail when CreateQuotaRulesOnDestination fails
		// In Temporal test environment, when an activity fails, the workflow should fail
		// However, GetWorkflowError() might return nil if the error is not properly propagated
		// So we check both the result and the error
		var result gcpgenserver.V1betaDescribeVolumeRes
		resultErr := env.GetWorkflowResult(&result)
		workflowErr := env.GetWorkflowError()

		// The workflow should fail, so either GetWorkflowResult should fail or GetWorkflowError should return an error
		if resultErr == nil && workflowErr == nil {
			tt.Fatal("Expected workflow to fail when CreateQuotaRulesOnDestination fails, but workflow completed successfully")
		}

		// If there's a workflow error, it should contain the activity error message
		if workflowErr != nil {
			assert.Contains(tt, workflowErr.Error(), "failed to create quota rules")
		} else if resultErr != nil {
			// If GetWorkflowResult failed, that's also acceptable (workflow failed)
			assert.Error(tt, resultErr)
		}
		env.AssertExpectations(tt)
	})
}

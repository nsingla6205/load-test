package replicationWorkflows

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/replicationActivities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func TestDeleteInternalVolumeReplicationWorkflow(t *testing.T) {
	t.Run("TestDeleteInternalVolumeReplicationWorkflow", func(tt *testing.T) {
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
		internalVolumeCreateReplicationActivity := replicationActivities.InternalVolumeReplicationDeleteActivity{SE: mockStorage}
		env.SetHeader(mockHeader)
		env.RegisterActivity(commonActivity.GetNode)
		env.RegisterActivity(internalVolumeCreateReplicationActivity.GetVolumeReplicationFromOntap)
		env.RegisterActivity(internalVolumeCreateReplicationActivity.DeleteVolumeReplication)
		env.RegisterActivity(commonActivity.UpdateJobStatus)

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			Name: "test-account",
		}
		volume := &datamodel.Volume{
			Pool:             &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{SecretID: "test-secret", Password: "test-secret", CertificateID: "test-cert"}},
			Svm:              &datamodel.Svm{Name: "svm_test"},
			VolumeAttributes: &datamodel.VolumeAttributes{BlockProperties: &datamodel.BlockProperties{OSType: "LINUX"}},
		}

		params := &models.VolumeReplication{
			Account: &models.Account{
				BaseModel: models.BaseModel{
					ID: 1,
				},
				Name: "test-account",
			},
			Name: "test-replication",
			ReplicationAttributes: &models.ReplicationDetails{
				DestinationVolumeUUID: "test-volume-uuid",
			},
			Volume: &models.Volume{
				BaseModel: models.BaseModel{
					ID: 1,
				},
			},
		}
		replicationDb := &datamodel.VolumeReplication{
			Name: params.Name,
			ReplicationAttributes: &datamodel.ReplicationDetails{
				DestinationVolumeUUID: params.ReplicationAttributes.DestinationVolumeUUID,
			},
			AccountID: account.ID,
			Account:   account,
			VolumeID:  params.VolumeID,
			Volume:    volume,
		}

		mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetNode", mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
		env.OnActivity("GetVolumeReplicationFromOntap", mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
		env.OnActivity("DeleteVolumeReplication", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeReplication{}, nil)
		mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.ExecuteWorkflow(DeleteInternalVolumeReplicationWorkflow, replicationDb, false, false)

		_, err := env.QueryWorkflowByID("default-test-workflow-id", "status")
		assert.Nil(tt, err)
		assert.True(tt, env.IsWorkflowCompleted())
		assert.NoError(tt, env.GetWorkflowError())
	})
}

func TestDeleteInternalVolumeReplicationWorkflowFailure(t *testing.T) {
	t.Run("TestDeleteInternalVolumeReplicationWorkflow", func(tt *testing.T) {
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
		internalVolumeCreateReplicationActivity := replicationActivities.InternalVolumeReplicationDeleteActivity{SE: mockStorage}
		env.SetHeader(mockHeader)
		env.RegisterActivity(commonActivity.GetNode)
		env.RegisterActivity(internalVolumeCreateReplicationActivity.DeleteVolumeReplication)
		env.RegisterActivity(commonActivity.UpdateJobStatus)

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			Name: "test-account",
		}
		volume := &datamodel.Volume{
			Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			}},
			Svm:              &datamodel.Svm{Name: "svm_test"},
			VolumeAttributes: &datamodel.VolumeAttributes{BlockProperties: &datamodel.BlockProperties{OSType: "LINUX"}},
		}

		params := &models.VolumeReplication{
			Account: &models.Account{
				BaseModel: models.BaseModel{
					ID: 1,
				},
				Name: "test-account",
			},
			Name: "test-replication",
			ReplicationAttributes: &models.ReplicationDetails{
				DestinationVolumeUUID: "test-volume-uuid",
			},
			Volume: &models.Volume{
				BaseModel: models.BaseModel{
					ID: 1,
				},
			},
		}
		replicationDb := &datamodel.VolumeReplication{
			Name: params.Name,
			ReplicationAttributes: &datamodel.ReplicationDetails{
				DestinationVolumeUUID: params.ReplicationAttributes.DestinationVolumeUUID,
			},
			AccountID: account.ID,
			Account:   account,
			VolumeID:  params.VolumeID,
			Volume:    volume,
		}

		mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(assert.AnError)

		env.ExecuteWorkflow(DeleteInternalVolumeReplicationWorkflow, replicationDb, false, false)
		assert.NotNil(tt, env.GetWorkflowError())
	})
}

func TestDeleteInternalVolumeReplicationWhenCleanupAfterReverse(t *testing.T) {
	t.Run("TestDeleteInternalVolumeReplicationWhenCleanupAfterReverse", func(tt *testing.T) {
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
		internalVolumeCreateReplicationActivity := replicationActivities.InternalVolumeReplicationDeleteActivity{SE: mockStorage}
		env.SetHeader(mockHeader)
		env.RegisterActivity(commonActivity.GetNode)
		env.RegisterActivity(internalVolumeCreateReplicationActivity.CleanupReplicationAfterReverse)
		env.RegisterActivity(commonActivity.UpdateJobStatus)

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			Name: "test-account",
		}
		volume := &datamodel.Volume{
			Pool:             &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{SecretID: "test-secret", Password: "test-secret", CertificateID: "test-cert"}},
			Svm:              &datamodel.Svm{Name: "svm_test"},
			VolumeAttributes: &datamodel.VolumeAttributes{BlockProperties: &datamodel.BlockProperties{OSType: "LINUX"}},
		}

		params := &models.VolumeReplication{
			Account: &models.Account{
				BaseModel: models.BaseModel{
					ID: 1,
				},
				Name: "test-account",
			},
			Name: "test-replication",
			ReplicationAttributes: &models.ReplicationDetails{
				DestinationVolumeUUID: "test-volume-uuid",
			},
			Volume: &models.Volume{
				BaseModel: models.BaseModel{
					ID: 1,
				},
			},
		}
		replicationDb := &datamodel.VolumeReplication{
			Name: params.Name,
			ReplicationAttributes: &datamodel.ReplicationDetails{
				DestinationVolumeUUID: params.ReplicationAttributes.DestinationVolumeUUID,
			},
			AccountID: account.ID,
			Account:   account,
			VolumeID:  params.VolumeID,
			Volume:    volume,
		}

		mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetNode", mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
		env.OnActivity("CleanupReplicationAfterReverse", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeReplication{}, nil)
		mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.ExecuteWorkflow(DeleteInternalVolumeReplicationWorkflow, replicationDb, true, false)

		_, err := env.QueryWorkflowByID("default-test-workflow-id", "status")
		assert.Nil(tt, err)
		assert.True(tt, env.IsWorkflowCompleted())
		assert.NoError(tt, env.GetWorkflowError())
	})
}

func setupOntapStateWorkflowEnv(t *testing.T) (*testsuite.TestWorkflowEnvironment, *datamodel.VolumeReplication, *database.MockStorage) {
	t.Helper()
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
	mockHeader := &commonpb.Header{
		Fields: map[string]*commonpb.Payload{
			"logParam": encodedValue,
		},
	}
	mockStorage := database.NewMockStorage(t)
	commonActivity := activities.CommonActivities{SE: mockStorage}
	deleteActivity := replicationActivities.InternalVolumeReplicationDeleteActivity{SE: mockStorage}
	stopActivity := replicationActivities.InternalStopVolumeReplicationActivity{SE: mockStorage}
	env.SetHeader(mockHeader)
	env.RegisterActivity(commonActivity.GetNode)
	env.RegisterActivity(deleteActivity.GetVolumeReplicationFromOntap)
	env.RegisterActivity(deleteActivity.DeleteVolumeReplication)
	env.RegisterActivity(stopActivity.BreakVolumeReplication)
	env.RegisterActivity(commonActivity.UpdateJobStatus)

	account := &datamodel.Account{
		BaseModel: datamodel.BaseModel{ID: 1},
		Name:      "test-account",
	}
	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{
			BaseModel:       datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{SecretID: "test-secret", Password: "test-secret", CertificateID: "test-cert"},
			DeploymentName:  "test-deployment",
		},
		Svm:              &datamodel.Svm{Name: "svm_test"},
		VolumeAttributes: &datamodel.VolumeAttributes{BlockProperties: &datamodel.BlockProperties{OSType: "LINUX"}},
	}
	replicationDb := &datamodel.VolumeReplication{
		Name: "test-replication",
		ReplicationAttributes: &datamodel.ReplicationDetails{
			DestinationVolumeUUID: "test-volume-uuid",
			ExternalUUID:          "snapmirror-uuid",
		},
		AccountID: account.ID,
		Account:   account,
		Volume:    volume,
	}
	return env, replicationDb, mockStorage
}

func TestDeleteInternalVolumeReplicationWorkflow_OntapSnapmirroredBreaksBeforeDelete(t *testing.T) {
	env, replicationDb, mockStorage := setupOntapStateWorkflowEnv(t)

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("GetNode", mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	env.OnActivity("GetVolumeReplicationFromOntap", mock.Anything, mock.Anything, mock.Anything).
		Return(&vsa.VolumeReplication{MirrorState: vsa.SnapmirrorStateMirrored}, nil).Once()
	env.OnActivity("BreakVolumeReplication", mock.Anything, mock.Anything, mock.Anything, true).
		Return(&vsa.VolumeReplication{}, nil).Once()
	env.OnActivity("DeleteVolumeReplication", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&vsa.VolumeReplication{}, nil).Once()

	env.ExecuteWorkflow(DeleteInternalVolumeReplicationWorkflow, replicationDb, false, true)

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

func TestDeleteInternalVolumeReplicationWorkflow_OntapSnapmirroredBreakFails(t *testing.T) {
	env, replicationDb, mockStorage := setupOntapStateWorkflowEnv(t)
	deleteActivity := replicationActivities.InternalVolumeReplicationDeleteActivity{SE: mockStorage}
	env.RegisterActivity(deleteActivity.UpdateReplicationStateInDBForDelete)

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, string(datamodel.JobsStatePROCESSING), mock.Anything, mock.Anything).Return(nil)
	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, string(datamodel.JobsStateERROR), mock.Anything, mock.Anything).Return(nil).Once()
	env.OnActivity("GetNode", mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	env.OnActivity("GetVolumeReplicationFromOntap", mock.Anything, mock.Anything, mock.Anything).
		Return(&vsa.VolumeReplication{MirrorState: vsa.SnapmirrorStateMirrored}, nil).Once()
	env.OnActivity("BreakVolumeReplication", mock.Anything, mock.Anything, mock.Anything, true).
		Return(nil, temporal.NewNonRetryableApplicationError("break failed", "BreakFailed", errors.New("break failed"))).Once()
	env.OnActivity("UpdateReplicationStateInDBForDelete", mock.Anything, mock.Anything).Return(nil).Once()

	env.ExecuteWorkflow(DeleteInternalVolumeReplicationWorkflow, replicationDb, false, true)

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
	env.AssertActivityNotCalled(t, "DeleteVolumeReplication", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	mockStorage.AssertCalled(t, "UpdateJob", mock.Anything, mock.Anything, string(datamodel.JobsStateERROR), mock.Anything, mock.Anything)
}

func TestDeleteInternalVolumeReplicationWorkflow_OntapBrokenOffSkipsBreak(t *testing.T) {
	env, replicationDb, mockStorage := setupOntapStateWorkflowEnv(t)

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("GetNode", mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	env.OnActivity("GetVolumeReplicationFromOntap", mock.Anything, mock.Anything, mock.Anything).
		Return(&vsa.VolumeReplication{MirrorState: vsa.SnapmirrorStateBroken}, nil).Once()
	env.OnActivity("DeleteVolumeReplication", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&vsa.VolumeReplication{}, nil).Once()

	env.ExecuteWorkflow(DeleteInternalVolumeReplicationWorkflow, replicationDb, false, false)

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

func TestDeleteInternalVolumeReplicationWorkflow_OntapGetFails(t *testing.T) {
	env, replicationDb, mockStorage := setupOntapStateWorkflowEnv(t)
	deleteActivity := replicationActivities.InternalVolumeReplicationDeleteActivity{SE: mockStorage}
	env.RegisterActivity(deleteActivity.UpdateReplicationStateInDBForDelete)

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, string(datamodel.JobsStatePROCESSING), mock.Anything, mock.Anything).Return(nil)
	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, string(datamodel.JobsStateERROR), mock.Anything, mock.Anything).Return(nil).Once()
	env.OnActivity("GetNode", mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	env.OnActivity("GetVolumeReplicationFromOntap", mock.Anything, mock.Anything, mock.Anything).
		Return(nil, temporal.NewNonRetryableApplicationError("get failed", "GetFailed", errors.New("get failed"))).Once()
	env.OnActivity("UpdateReplicationStateInDBForDelete", mock.Anything, mock.Anything).Return(nil).Once()

	env.ExecuteWorkflow(DeleteInternalVolumeReplicationWorkflow, replicationDb, false, false)

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
	mockStorage.AssertCalled(t, "UpdateJob", mock.Anything, mock.Anything, string(datamodel.JobsStateERROR), mock.Anything, mock.Anything)
}

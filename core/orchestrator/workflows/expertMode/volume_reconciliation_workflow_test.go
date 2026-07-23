package expertMode

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	expertmodeactivities "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/expert_mode_activities"
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

func TestVolumeCreateReconciliationWorkflow(t *testing.T) {
	t.Run("Success_VolumeFoundAndUpdated", func(tt *testing.T) {
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
		expertModeActivity := expertmodeactivities.ExpertModeVolumeActivity{SE: mockStorage}
		env.SetHeader(mockHeader)
		env.RegisterActivity(commonActivity.ResolveOntapNodeForPool)
		env.RegisterActivity(commonActivity.UpdateJobStatus)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(expertModeActivity.FetchOntapVolumeByName)
		env.RegisterActivity(expertModeActivity.UpdateExpertModeVolumeInDB)

		volume := &datamodel.ExpertModeVolumes{
			BaseModel:   datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:        "test-volume",
			SizeInBytes: 1099511627776,
			Style:       "flexvol",
			State:       datamodel.LifeCycleStateCreating,
			Account: &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      "test-account",
			},
			Pool: &datamodel.Pool{
				BaseModel:      datamodel.BaseModel{ID: 1},
				DeploymentName: "test-deployment",
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "",
					CertificateID: "",
				},
			},
			Svm: &datamodel.Svm{
				Name: "test-svm",
			},
		}

		updatedVolume := &datamodel.ExpertModeVolumes{
			BaseModel:    datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:         "test-volume",
			SizeInBytes:  1099511627776,
			Style:        "flexvol",
			State:        datamodel.LifeCycleStateAvailable,
			ExternalUUID: "ontap-uuid-123",
		}

		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil)
		mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Times(2) // PROCESSING, DONE
		env.OnActivity("ResolveOntapNodeForPool", mock.Anything, mock.Anything).Return(&models.Node{EndpointAddress: "127.0.0.1", EndpointAddressesToHostNameMap: map[string]string{"127.0.0.1": "127.0.0.1"}}, nil)
		env.OnActivity("FetchOntapVolumeByName", mock.Anything, mock.Anything, mock.Anything).Return(updatedVolume, nil)
		env.OnActivity("UpdateExpertModeVolumeInDB", mock.Anything, mock.Anything).Return(nil)

		env.ExecuteWorkflow(VolumeCreateReconciliationWorkflow, volume)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.NoError(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Success_VolumeNotFound_MarkedAsDeleted", func(tt *testing.T) {
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
		expertModeActivity := expertmodeactivities.ExpertModeVolumeActivity{SE: mockStorage}
		env.SetHeader(mockHeader)
		env.RegisterActivity(commonActivity.ResolveOntapNodeForPool)
		env.RegisterActivity(commonActivity.UpdateJobStatus)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(expertModeActivity.FetchOntapVolumeByName)
		env.RegisterActivity(expertModeActivity.DeleteExpertModeVolumeInDB)

		volume := &datamodel.ExpertModeVolumes{
			BaseModel:   datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:        "test-volume",
			SizeInBytes: 1099511627776,
			Style:       "flexvol",
			State:       datamodel.LifeCycleStateCreating,
			Account: &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      "test-account",
			},
			Pool: &datamodel.Pool{
				BaseModel:      datamodel.BaseModel{ID: 1},
				DeploymentName: "test-deployment",
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "",
					CertificateID: "",
				},
			},
			Svm: &datamodel.Svm{
				Name: "test-svm",
			},
		}

		notFoundError := vsaerrors.NewVCPError(vsaerrors.ErrResourceNotFound, nil)
		temporalAppError := vsaerrors.WrapAsTemporalApplicationError(notFoundError)

		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil)
		mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Times(2) // PROCESSING, ERROR
		env.OnActivity("ResolveOntapNodeForPool", mock.Anything, mock.Anything).Return(&models.Node{EndpointAddress: "127.0.0.1", EndpointAddressesToHostNameMap: map[string]string{"127.0.0.1": "127.0.0.1"}}, nil)
		env.OnActivity("FetchOntapVolumeByName", mock.Anything, mock.Anything, mock.Anything).Return(nil, temporalAppError)
		env.OnActivity("DeleteExpertModeVolumeInDB", mock.Anything, "test-volume-uuid").Return(nil)

		env.ExecuteWorkflow(VolumeCreateReconciliationWorkflow, volume)

		assert.True(tt, env.IsWorkflowCompleted())
		// Workflow should complete with error (resource not found)
		assert.Error(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Success_OntapUnreachable_MarkedAsDeleted", func(tt *testing.T) {
		// When FetchOntapVolumeByName fails for any reason other than "found but still provisioning"
		// (e.g. ONTAP unreachable / activity timeout, which surfaces as a generic ErrInternalServerError),
		// the volume can never be confirmed and must be marked DELETED instead of stuck in CREATING.
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
		expertModeActivity := expertmodeactivities.ExpertModeVolumeActivity{SE: mockStorage}
		env.SetHeader(mockHeader)
		env.RegisterActivity(commonActivity.ResolveOntapNodeForPool)
		env.RegisterActivity(commonActivity.UpdateJobStatus)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(expertModeActivity.FetchOntapVolumeByName)
		env.RegisterActivity(expertModeActivity.DeleteExpertModeVolumeInDB)

		volume := &datamodel.ExpertModeVolumes{
			BaseModel:   datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:        "test-volume",
			SizeInBytes: 1099511627776,
			Style:       "flexvol",
			State:       datamodel.LifeCycleStateCreating,
			Account: &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      "test-account",
			},
			Pool: &datamodel.Pool{
				BaseModel:      datamodel.BaseModel{ID: 1},
				DeploymentName: "test-deployment",
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "",
					CertificateID: "",
				},
			},
			Svm: &datamodel.Svm{
				Name: "test-svm",
			},
		}

		// Simulate ONTAP unreachable: a generic internal error (no resource-specific tracking ID).
		unreachableError := vsaerrors.NewVCPError(vsaerrors.ErrInternalServerError, nil)
		temporalAppError := vsaerrors.WrapAsTemporalApplicationError(unreachableError)

		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil)
		mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Times(2) // PROCESSING, ERROR
		env.OnActivity("ResolveOntapNodeForPool", mock.Anything, mock.Anything).Return(&models.Node{EndpointAddress: "127.0.0.1", EndpointAddressesToHostNameMap: map[string]string{"127.0.0.1": "127.0.0.1"}}, nil)
		env.OnActivity("FetchOntapVolumeByName", mock.Anything, mock.Anything, mock.Anything).Return(nil, temporalAppError)
		env.OnActivity("DeleteExpertModeVolumeInDB", mock.Anything, "test-volume-uuid").Return(nil).Once()

		env.ExecuteWorkflow(VolumeCreateReconciliationWorkflow, volume)

		assert.True(tt, env.IsWorkflowCompleted())
		// Workflow surfaces the ONTAP error, but the volume is cleaned up (marked DELETED) first.
		assert.Error(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Failure_VolumeNotFound_DeleteExpertModeVolumeInDBFails", func(tt *testing.T) {
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
		expertModeActivity := expertmodeactivities.ExpertModeVolumeActivity{SE: mockStorage}
		env.SetHeader(mockHeader)
		env.RegisterActivity(commonActivity.ResolveOntapNodeForPool)
		env.RegisterActivity(commonActivity.UpdateJobStatus)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(expertModeActivity.FetchOntapVolumeByName)
		env.RegisterActivity(expertModeActivity.DeleteExpertModeVolumeInDB)

		volume := &datamodel.ExpertModeVolumes{
			BaseModel:   datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:        "test-volume",
			SizeInBytes: 1099511627776,
			Style:       "flexvol",
			State:       datamodel.LifeCycleStateCreating,
			Account: &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      "test-account",
			},
			Pool: &datamodel.Pool{
				BaseModel:      datamodel.BaseModel{ID: 1},
				DeploymentName: "test-deployment",
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "",
					CertificateID: "",
				},
			},
			Svm: &datamodel.Svm{
				Name: "test-svm",
			},
		}

		notFoundError := vsaerrors.NewVCPError(vsaerrors.ErrResourceNotFound, nil)
		temporalAppError := vsaerrors.WrapAsTemporalApplicationError(notFoundError)

		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil)
		mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Times(2) // PROCESSING, ERROR
		env.OnActivity("ResolveOntapNodeForPool", mock.Anything, mock.Anything).Return(&models.Node{EndpointAddress: "127.0.0.1", EndpointAddressesToHostNameMap: map[string]string{"127.0.0.1": "127.0.0.1"}}, nil)
		env.OnActivity("FetchOntapVolumeByName", mock.Anything, mock.Anything, mock.Anything).Return(nil, temporalAppError)
		// DeleteExpertModeVolumeInDB fails - this should trigger line 141 error logging
		env.OnActivity("DeleteExpertModeVolumeInDB", mock.Anything, "test-volume-uuid").Return(assert.AnError)

		env.ExecuteWorkflow(VolumeCreateReconciliationWorkflow, volume)

		assert.True(tt, env.IsWorkflowCompleted())
		// Workflow should complete with error (resource not found)
		assert.Error(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Failure_ResolveOntapNodeForPoolActivityFails", func(tt *testing.T) {
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
		env.RegisterActivity(commonActivity.ResolveOntapNodeForPool)
		env.RegisterActivity(commonActivity.UpdateJobStatus)
		env.RegisterActivity(commonActivity.GetJob)

		volume := &datamodel.ExpertModeVolumes{
			BaseModel:   datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:        "test-volume",
			SizeInBytes: 1099511627776,
			Style:       "flexvol",
			State:       datamodel.LifeCycleStateCreating,
			Account: &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      "test-account",
			},
			Pool: &datamodel.Pool{
				BaseModel:      datamodel.BaseModel{ID: 1},
				DeploymentName: "test-deployment",
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "",
					CertificateID: "",
				},
			},
			Svm: &datamodel.Svm{
				Name: "test-svm",
			},
		}

		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil)
		mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Times(2) // PROCESSING, ERROR
		env.OnActivity("ResolveOntapNodeForPool", mock.Anything, mock.Anything).Return(nil, assert.AnError)

		env.ExecuteWorkflow(VolumeCreateReconciliationWorkflow, volume)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Failure_UpdateExpertModeVolumeInDBFails", func(tt *testing.T) {
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
		expertModeActivity := expertmodeactivities.ExpertModeVolumeActivity{SE: mockStorage}
		env.SetHeader(mockHeader)
		env.RegisterActivity(commonActivity.ResolveOntapNodeForPool)
		env.RegisterActivity(commonActivity.UpdateJobStatus)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(expertModeActivity.FetchOntapVolumeByName)
		env.RegisterActivity(expertModeActivity.UpdateExpertModeVolumeInDB)

		volume := &datamodel.ExpertModeVolumes{
			BaseModel:   datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:        "test-volume",
			SizeInBytes: 1099511627776,
			Style:       "flexvol",
			State:       datamodel.LifeCycleStateCreating,
			Account: &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      "test-account",
			},
			Pool: &datamodel.Pool{
				BaseModel:      datamodel.BaseModel{ID: 1},
				DeploymentName: "test-deployment",
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "",
					CertificateID: "",
				},
			},
			Svm: &datamodel.Svm{
				Name: "test-svm",
			},
		}

		updatedVolume := &datamodel.ExpertModeVolumes{
			BaseModel:    datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:         "test-volume",
			SizeInBytes:  1099511627776,
			Style:        "flexvol",
			State:        datamodel.LifeCycleStateAvailable,
			ExternalUUID: "ontap-uuid-123",
		}

		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil)
		mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Times(2) // PROCESSING, ERROR
		env.OnActivity("ResolveOntapNodeForPool", mock.Anything, mock.Anything).Return(&models.Node{EndpointAddress: "127.0.0.1", EndpointAddressesToHostNameMap: map[string]string{"127.0.0.1": "127.0.0.1"}}, nil)
		env.OnActivity("FetchOntapVolumeByName", mock.Anything, mock.Anything, mock.Anything).Return(updatedVolume, nil)
		env.OnActivity("UpdateExpertModeVolumeInDB", mock.Anything, mock.Anything).Return(assert.AnError)

		env.ExecuteWorkflow(VolumeCreateReconciliationWorkflow, volume)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Failure_UpdateJobStatusToProcessingFails", func(tt *testing.T) {
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
		env.RegisterActivity(commonActivity.UpdateJobStatus)
		env.RegisterActivity(commonActivity.GetJob)

		volume := &datamodel.ExpertModeVolumes{
			BaseModel:   datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:        "test-volume",
			SizeInBytes: 1099511627776,
			Style:       "flexvol",
			State:       datamodel.LifeCycleStateCreating,
			Account: &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      "test-account",
			},
			Pool: &datamodel.Pool{
				BaseModel:      datamodel.BaseModel{ID: 1},
				DeploymentName: "test-deployment",
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "",
					CertificateID: "",
				},
			},
			Svm: &datamodel.Svm{
				Name: "test-svm",
			},
		}

		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil)
		// First UpdateJobStatus (PROCESSING) fails, then ERROR update may succeed or fail
		mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(assert.AnError).Once()
		mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

		env.ExecuteWorkflow(VolumeCreateReconciliationWorkflow, volume)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Failure_EnsureJobStateFails", func(tt *testing.T) {
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

		volume := &datamodel.ExpertModeVolumes{
			BaseModel:   datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:        "test-volume",
			SizeInBytes: 1099511627776,
			Style:       "flexvol",
			State:       datamodel.LifeCycleStateCreating,
			Account: &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      "test-account",
			},
			Pool: &datamodel.Pool{
				BaseModel:      datamodel.BaseModel{ID: 1},
				DeploymentName: "test-deployment",
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "",
					CertificateID: "",
				},
			},
			Svm: &datamodel.Svm{
				Name: "test-svm",
			},
		}

		// Job state is not NEW, so EnsureJobState will fail
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStatePROCESSING), // Not NEW
		}, nil)

		env.ExecuteWorkflow(VolumeCreateReconciliationWorkflow, volume)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Failure_SetupWithNilAccount", func(tt *testing.T) {
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

		volume := &datamodel.ExpertModeVolumes{
			BaseModel:   datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:        "test-volume",
			SizeInBytes: 1099511627776,
			Style:       "flexvol",
			State:       datamodel.LifeCycleStateCreating,
			Account:     nil, // Nil account will cause panic in Setup
			Pool: &datamodel.Pool{
				BaseModel:      datamodel.BaseModel{ID: 1},
				DeploymentName: "test-deployment",
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "",
					CertificateID: "",
				},
			},
			Svm: &datamodel.Svm{
				Name: "test-svm",
			},
		}

		env.ExecuteWorkflow(VolumeCreateReconciliationWorkflow, volume)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
	})

	t.Run("Success_QueryWorkflowStatus", func(tt *testing.T) {
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
		expertModeActivity := expertmodeactivities.ExpertModeVolumeActivity{SE: mockStorage}
		env.SetHeader(mockHeader)
		env.RegisterActivity(commonActivity.ResolveOntapNodeForPool)
		env.RegisterActivity(commonActivity.UpdateJobStatus)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(expertModeActivity.FetchOntapVolumeByName)
		env.RegisterActivity(expertModeActivity.UpdateExpertModeVolumeInDB)

		volume := &datamodel.ExpertModeVolumes{
			BaseModel:   datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:        "test-volume",
			SizeInBytes: 1099511627776,
			Style:       "flexvol",
			State:       datamodel.LifeCycleStateCreating,
			Account: &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      "test-account",
			},
			Pool: &datamodel.Pool{
				BaseModel:      datamodel.BaseModel{ID: 1},
				DeploymentName: "test-deployment",
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "",
					CertificateID: "",
				},
			},
			Svm: &datamodel.Svm{
				Name: "test-svm",
			},
		}

		updatedVolume := &datamodel.ExpertModeVolumes{
			BaseModel:    datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:         "test-volume",
			SizeInBytes:  1099511627776,
			Style:        "flexvol",
			State:        datamodel.LifeCycleStateAvailable,
			ExternalUUID: "ontap-uuid-123",
		}

		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil)
		mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Times(2) // PROCESSING, DONE
		env.OnActivity("ResolveOntapNodeForPool", mock.Anything, mock.Anything).Return(&models.Node{EndpointAddress: "127.0.0.1", EndpointAddressesToHostNameMap: map[string]string{"127.0.0.1": "127.0.0.1"}}, nil)
		env.OnActivity("FetchOntapVolumeByName", mock.Anything, mock.Anything, mock.Anything).Return(updatedVolume, nil)
		env.OnActivity("UpdateExpertModeVolumeInDB", mock.Anything, mock.Anything).Return(nil)

		env.ExecuteWorkflow(VolumeCreateReconciliationWorkflow, volume)

		// Query workflow status
		status, err := env.QueryWorkflowByID("default-test-workflow-id", "status")
		assert.NoError(tt, err)
		assert.NotNil(tt, status)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.NoError(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})
}

func TestVolumeDeleteReconciliationWorkflow(t *testing.T) {
	t.Run("Success_VolumeDeleted_MarkedAsDeleted", func(tt *testing.T) {
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
		expertModeActivity := expertmodeactivities.ExpertModeVolumeActivity{SE: mockStorage}
		env.SetHeader(mockHeader)
		env.RegisterActivity(commonActivity.ResolveOntapNodeForPool)
		env.RegisterActivity(commonActivity.UpdateJobStatus)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(expertModeActivity.CheckVolumeDeletedInOntap)
		env.RegisterActivity(expertModeActivity.DeleteExpertModeVolumeInDB)

		volume := &datamodel.ExpertModeVolumes{
			BaseModel:   datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:        "test-volume",
			SizeInBytes: 1099511627776,
			Style:       "flexvol",
			State:       datamodel.LifeCycleStateDeleting,
			Account: &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      "test-account",
			},
			Pool: &datamodel.Pool{
				BaseModel:      datamodel.BaseModel{ID: 1},
				DeploymentName: "test-deployment",
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "",
					CertificateID: "",
				},
			},
			Svm: &datamodel.Svm{
				Name: "test-svm",
			},
		}

		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil)
		mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Times(2) // PROCESSING, DONE
		env.OnActivity("ResolveOntapNodeForPool", mock.Anything, mock.Anything).Return(&models.Node{EndpointAddress: "127.0.0.1", EndpointAddressesToHostNameMap: map[string]string{"127.0.0.1": "127.0.0.1"}}, nil)
		// CheckVolumeDeletedInOntap returns nil when volume is not found (deleted)
		env.OnActivity("CheckVolumeDeletedInOntap", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DeleteExpertModeVolumeInDB", mock.Anything, mock.Anything).Return(nil)

		env.ExecuteWorkflow(VolumeDeleteReconciliationWorkflow, volume)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.NoError(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Success_VolumeStillExists_MarkedAsAvailable", func(tt *testing.T) {
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
		expertModeActivity := expertmodeactivities.ExpertModeVolumeActivity{SE: mockStorage}
		env.SetHeader(mockHeader)
		env.RegisterActivity(commonActivity.ResolveOntapNodeForPool)
		env.RegisterActivity(commonActivity.UpdateJobStatus)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(expertModeActivity.CheckVolumeDeletedInOntap)
		env.RegisterActivity(expertModeActivity.UpdateExpertModeVolumeInDB)

		volume := &datamodel.ExpertModeVolumes{
			BaseModel:   datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:        "test-volume",
			SizeInBytes: 1099511627776,
			Style:       "flexvol",
			State:       datamodel.LifeCycleStateDeleting,
			Account: &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      "test-account",
			},
			Pool: &datamodel.Pool{
				BaseModel:      datamodel.BaseModel{ID: 1},
				DeploymentName: "test-deployment",
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "",
					CertificateID: "",
				},
			},
			Svm: &datamodel.Svm{
				Name: "test-svm",
			},
		}

		// Volume still exists after max retries - ErrResourceStateConflictError
		stateConflictError := vsaerrors.NewVCPError(vsaerrors.ErrResourceStateConflictError, nil)
		temporalAppError := vsaerrors.WrapAsTemporalApplicationError(stateConflictError)

		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil)
		mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Times(2) // PROCESSING, ERROR
		env.OnActivity("ResolveOntapNodeForPool", mock.Anything, mock.Anything).Return(&models.Node{EndpointAddress: "127.0.0.1", EndpointAddressesToHostNameMap: map[string]string{"127.0.0.1": "127.0.0.1"}}, nil)
		env.OnActivity("CheckVolumeDeletedInOntap", mock.Anything, mock.Anything, mock.Anything).Return(temporalAppError)
		env.OnActivity("UpdateExpertModeVolumeInDB", mock.Anything, mock.Anything).Return(nil)

		env.ExecuteWorkflow(VolumeDeleteReconciliationWorkflow, volume)

		assert.True(tt, env.IsWorkflowCompleted())
		// Workflow should complete with error since volume still exists
		assert.Error(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Failure_ResolveOntapNodeForPoolActivityFails", func(tt *testing.T) {
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
		env.RegisterActivity(commonActivity.ResolveOntapNodeForPool)
		env.RegisterActivity(commonActivity.UpdateJobStatus)
		env.RegisterActivity(commonActivity.GetJob)

		volume := &datamodel.ExpertModeVolumes{
			BaseModel:   datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:        "test-volume",
			SizeInBytes: 1099511627776,
			Style:       "flexvol",
			State:       datamodel.LifeCycleStateDeleting,
			Account: &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      "test-account",
			},
			Pool: &datamodel.Pool{
				BaseModel:      datamodel.BaseModel{ID: 1},
				DeploymentName: "test-deployment",
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "",
					CertificateID: "",
				},
			},
			Svm: &datamodel.Svm{
				Name: "test-svm",
			},
		}

		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil)
		mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Times(2) // PROCESSING, ERROR
		env.OnActivity("ResolveOntapNodeForPool", mock.Anything, mock.Anything).Return(nil, assert.AnError)

		env.ExecuteWorkflow(VolumeDeleteReconciliationWorkflow, volume)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Failure_DeleteExpertModeVolumeInDBFails", func(tt *testing.T) {
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
		expertModeActivity := expertmodeactivities.ExpertModeVolumeActivity{SE: mockStorage}
		env.SetHeader(mockHeader)
		env.RegisterActivity(commonActivity.ResolveOntapNodeForPool)
		env.RegisterActivity(commonActivity.UpdateJobStatus)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(expertModeActivity.CheckVolumeDeletedInOntap)
		env.RegisterActivity(expertModeActivity.DeleteExpertModeVolumeInDB)

		volume := &datamodel.ExpertModeVolumes{
			BaseModel:   datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:        "test-volume",
			SizeInBytes: 1099511627776,
			Style:       "flexvol",
			State:       datamodel.LifeCycleStateDeleting,
			Account: &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      "test-account",
			},
			Pool: &datamodel.Pool{
				BaseModel:      datamodel.BaseModel{ID: 1},
				DeploymentName: "test-deployment",
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "",
					CertificateID: "",
				},
			},
			Svm: &datamodel.Svm{
				Name: "test-svm",
			},
		}

		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil)
		mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Times(2) // PROCESSING, ERROR
		env.OnActivity("ResolveOntapNodeForPool", mock.Anything, mock.Anything).Return(&models.Node{EndpointAddress: "127.0.0.1", EndpointAddressesToHostNameMap: map[string]string{"127.0.0.1": "127.0.0.1"}}, nil)
		env.OnActivity("CheckVolumeDeletedInOntap", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DeleteExpertModeVolumeInDB", mock.Anything, mock.Anything).Return(assert.AnError)

		env.ExecuteWorkflow(VolumeDeleteReconciliationWorkflow, volume)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Failure_UpdateJobStatusToProcessingFails", func(tt *testing.T) {
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
		env.RegisterActivity(commonActivity.UpdateJobStatus)
		env.RegisterActivity(commonActivity.GetJob)

		volume := &datamodel.ExpertModeVolumes{
			BaseModel:   datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:        "test-volume",
			SizeInBytes: 1099511627776,
			Style:       "flexvol",
			State:       datamodel.LifeCycleStateDeleting,
			Account: &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      "test-account",
			},
			Pool: &datamodel.Pool{
				BaseModel:      datamodel.BaseModel{ID: 1},
				DeploymentName: "test-deployment",
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "",
					CertificateID: "",
				},
			},
			Svm: &datamodel.Svm{
				Name: "test-svm",
			},
		}

		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil)
		// First UpdateJobStatus (PROCESSING) fails, then ERROR update may succeed or fail
		mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(assert.AnError).Once()
		mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

		env.ExecuteWorkflow(VolumeDeleteReconciliationWorkflow, volume)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Failure_EnsureJobStateFails", func(tt *testing.T) {
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

		volume := &datamodel.ExpertModeVolumes{
			BaseModel:   datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:        "test-volume",
			SizeInBytes: 1099511627776,
			Style:       "flexvol",
			State:       datamodel.LifeCycleStateDeleting,
			Account: &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      "test-account",
			},
			Pool: &datamodel.Pool{
				BaseModel:      datamodel.BaseModel{ID: 1},
				DeploymentName: "test-deployment",
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "",
					CertificateID: "",
				},
			},
			Svm: &datamodel.Svm{
				Name: "test-svm",
			},
		}

		// Job state is not NEW, so EnsureJobState will fail
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStatePROCESSING), // Not NEW
		}, nil)

		env.ExecuteWorkflow(VolumeDeleteReconciliationWorkflow, volume)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Failure_SetupWithNilAccount", func(tt *testing.T) {
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

		volume := &datamodel.ExpertModeVolumes{
			BaseModel:   datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:        "test-volume",
			SizeInBytes: 1099511627776,
			Style:       "flexvol",
			State:       datamodel.LifeCycleStateDeleting,
			Account:     nil, // Nil account will cause panic in Setup
			Pool: &datamodel.Pool{
				BaseModel:      datamodel.BaseModel{ID: 1},
				DeploymentName: "test-deployment",
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "",
					CertificateID: "",
				},
			},
			Svm: &datamodel.Svm{
				Name: "test-svm",
			},
		}

		env.ExecuteWorkflow(VolumeDeleteReconciliationWorkflow, volume)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
	})

	t.Run("Success_QueryWorkflowStatus", func(tt *testing.T) {
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
		expertModeActivity := expertmodeactivities.ExpertModeVolumeActivity{SE: mockStorage}
		env.SetHeader(mockHeader)
		env.RegisterActivity(commonActivity.ResolveOntapNodeForPool)
		env.RegisterActivity(commonActivity.UpdateJobStatus)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(expertModeActivity.CheckVolumeDeletedInOntap)
		env.RegisterActivity(expertModeActivity.DeleteExpertModeVolumeInDB)

		volume := &datamodel.ExpertModeVolumes{
			BaseModel:   datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:        "test-volume",
			SizeInBytes: 1099511627776,
			Style:       "flexvol",
			State:       datamodel.LifeCycleStateDeleting,
			Account: &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      "test-account",
			},
			Pool: &datamodel.Pool{
				BaseModel:      datamodel.BaseModel{ID: 1},
				DeploymentName: "test-deployment",
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "",
					CertificateID: "",
				},
			},
			Svm: &datamodel.Svm{
				Name: "test-svm",
			},
		}

		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil)
		mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Times(2) // PROCESSING, DONE
		env.OnActivity("ResolveOntapNodeForPool", mock.Anything, mock.Anything).Return(&models.Node{EndpointAddress: "127.0.0.1", EndpointAddressesToHostNameMap: map[string]string{"127.0.0.1": "127.0.0.1"}}, nil)
		env.OnActivity("CheckVolumeDeletedInOntap", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DeleteExpertModeVolumeInDB", mock.Anything, mock.Anything).Return(nil)

		env.ExecuteWorkflow(VolumeDeleteReconciliationWorkflow, volume)

		// Query workflow status
		status, err := env.QueryWorkflowByID("default-test-workflow-id", "status")
		assert.NoError(tt, err)
		assert.NotNil(tt, status)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.NoError(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Failure_CheckVolumeDeletedInOntap_OtherError", func(tt *testing.T) {
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
		expertModeActivity := expertmodeactivities.ExpertModeVolumeActivity{SE: mockStorage}
		env.SetHeader(mockHeader)
		env.RegisterActivity(commonActivity.ResolveOntapNodeForPool)
		env.RegisterActivity(commonActivity.UpdateJobStatus)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(expertModeActivity.CheckVolumeDeletedInOntap)

		volume := &datamodel.ExpertModeVolumes{
			BaseModel:   datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:        "test-volume",
			SizeInBytes: 1099511627776,
			Style:       "flexvol",
			State:       datamodel.LifeCycleStateDeleting,
			Account: &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      "test-account",
			},
			Pool: &datamodel.Pool{
				BaseModel:      datamodel.BaseModel{ID: 1},
				DeploymentName: "test-deployment",
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "",
					CertificateID: "",
				},
			},
			Svm: &datamodel.Svm{
				Name: "test-svm",
			},
		}

		// Some other error (not state conflict) - should not update volume to AVAILABLE
		otherError := vsaerrors.NewVCPError(vsaerrors.ErrInternalServerError, nil)
		temporalAppError := vsaerrors.WrapAsTemporalApplicationError(otherError)

		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil)
		mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Times(2) // PROCESSING, ERROR
		env.OnActivity("ResolveOntapNodeForPool", mock.Anything, mock.Anything).Return(&models.Node{EndpointAddress: "127.0.0.1", EndpointAddressesToHostNameMap: map[string]string{"127.0.0.1": "127.0.0.1"}}, nil)
		env.OnActivity("CheckVolumeDeletedInOntap", mock.Anything, mock.Anything, mock.Anything).Return(temporalAppError)

		env.ExecuteWorkflow(VolumeDeleteReconciliationWorkflow, volume)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})
}

func TestVolumeUpdateReconciliationWorkflow(t *testing.T) {
	t.Run("Success_VolumeUpdatedAndValidated", func(tt *testing.T) {
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
		expertModeActivity := expertmodeactivities.ExpertModeVolumeActivity{SE: mockStorage}
		env.SetHeader(mockHeader)
		env.RegisterActivity(commonActivity.ResolveOntapNodeForPool)
		env.RegisterActivity(commonActivity.UpdateJobStatus)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(expertModeActivity.ValidateONTAPVolumeUpdate)
		env.RegisterActivity(expertModeActivity.UpdateExpertModeVolumeInDB)

		oldVolume := &datamodel.ExpertModeVolumes{
			BaseModel:   datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:        "test-volume",
			SizeInBytes: 1099511627776, // 1TB
			Style:       "flexvol",
			State:       datamodel.LifeCycleStateUpdating,
			Account: &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      "test-account",
			},
			Pool: &datamodel.Pool{
				BaseModel:      datamodel.BaseModel{ID: 1},
				DeploymentName: "test-deployment",
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "",
					CertificateID: "",
				},
			},
			Svm: &datamodel.Svm{
				Name: "test-svm",
			},
		}

		volume := &datamodel.ExpertModeVolumes{
			BaseModel:   datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:        "test-volume",
			SizeInBytes: 2199023255552, // 2TB - updated size
			Style:       "flexvol",
			State:       datamodel.LifeCycleStateUpdating,
			Account: &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      "test-account",
			},
			Pool: &datamodel.Pool{
				BaseModel:      datamodel.BaseModel{ID: 1},
				DeploymentName: "test-deployment",
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "",
					CertificateID: "",
				},
			},
			Svm: &datamodel.Svm{
				Name: "test-svm",
			},
		}

		updatedVolume := &datamodel.ExpertModeVolumes{
			BaseModel:    datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:         "test-volume",
			SizeInBytes:  2199023255552, // 2TB - matches expected
			Style:        "flexvol",
			State:        datamodel.LifeCycleStateAvailable,
			ExternalUUID: "ontap-uuid-123",
		}

		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil)
		mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Times(2) // PROCESSING, DONE
		env.OnActivity("ResolveOntapNodeForPool", mock.Anything, mock.Anything).Return(&models.Node{EndpointAddress: "127.0.0.1", EndpointAddressesToHostNameMap: map[string]string{"127.0.0.1": "127.0.0.1"}}, nil)
		env.OnActivity("ValidateONTAPVolumeUpdate", mock.Anything, mock.Anything, mock.Anything).Return(updatedVolume, nil)
		env.OnActivity("UpdateExpertModeVolumeInDB", mock.Anything, mock.Anything).Return(nil)

		env.ExecuteWorkflow(VolumeUpdateReconciliationWorkflow, volume, oldVolume)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.NoError(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Success_VolumeNotUpdatedInOntap_MarkedAsAvailable", func(tt *testing.T) {
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
		expertModeActivity := expertmodeactivities.ExpertModeVolumeActivity{SE: mockStorage}
		env.SetHeader(mockHeader)
		env.RegisterActivity(commonActivity.ResolveOntapNodeForPool)
		env.RegisterActivity(commonActivity.UpdateJobStatus)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(expertModeActivity.ValidateONTAPVolumeUpdate)
		env.RegisterActivity(expertModeActivity.UpdateExpertModeVolumeInDB)

		oldVolume := &datamodel.ExpertModeVolumes{
			BaseModel:   datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:        "test-volume",
			SizeInBytes: 1099511627776, // 1TB
			Style:       "flexvol",
			State:       datamodel.LifeCycleStateUpdating,
			Account: &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      "test-account",
			},
			Pool: &datamodel.Pool{
				BaseModel:      datamodel.BaseModel{ID: 1},
				DeploymentName: "test-deployment",
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "",
					CertificateID: "",
				},
			},
			Svm: &datamodel.Svm{
				Name: "test-svm",
			},
		}

		volume := &datamodel.ExpertModeVolumes{
			BaseModel:   datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:        "test-volume",
			SizeInBytes: 2199023255552, // 2TB - expected size
			Style:       "flexvol",
			State:       datamodel.LifeCycleStateUpdating,
			Account: &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      "test-account",
			},
			Pool: &datamodel.Pool{
				BaseModel:      datamodel.BaseModel{ID: 1},
				DeploymentName: "test-deployment",
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "",
					CertificateID: "",
				},
			},
			Svm: &datamodel.Svm{
				Name: "test-svm",
			},
		}

		stateConflictError := vsaerrors.NewVCPError(vsaerrors.ErrResourceStateConflictError, nil)
		temporalAppError := vsaerrors.WrapAsTemporalApplicationError(stateConflictError)

		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil)
		mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Times(2) // PROCESSING, ERROR
		env.OnActivity("ResolveOntapNodeForPool", mock.Anything, mock.Anything).Return(&models.Node{EndpointAddress: "127.0.0.1", EndpointAddressesToHostNameMap: map[string]string{"127.0.0.1": "127.0.0.1"}}, nil)
		env.OnActivity("ValidateONTAPVolumeUpdate", mock.Anything, mock.Anything, mock.Anything).Return(nil, temporalAppError)
		env.OnActivity("UpdateExpertModeVolumeInDB", mock.Anything, mock.Anything).Return(nil)

		env.ExecuteWorkflow(VolumeUpdateReconciliationWorkflow, volume, oldVolume)

		assert.True(tt, env.IsWorkflowCompleted())
		// Workflow should complete with error (resource state conflict)
		assert.Error(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Success_VolumeNotUpdatedInOntap_FetchOntapVolumeByUUIDSucceeds_MarkedAsAvailable", func(tt *testing.T) {
		// Covers path: ValidateONTAPVolumeUpdate fails -> FetchOntapVolumeByUUID succeeds ->
		// use fetched volume (not oldVolume), set AVAILABLE, UpdateExpertModeVolumeInDB succeeds (lines 391-406)
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
		expertModeActivity := expertmodeactivities.ExpertModeVolumeActivity{SE: mockStorage}
		env.SetHeader(mockHeader)
		env.RegisterActivity(commonActivity.ResolveOntapNodeForPool)
		env.RegisterActivity(commonActivity.UpdateJobStatus)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(expertModeActivity.ValidateONTAPVolumeUpdate)
		env.RegisterActivity(expertModeActivity.FetchOntapVolumeByUUID)
		env.RegisterActivity(expertModeActivity.UpdateExpertModeVolumeInDB)

		oldVolume := &datamodel.ExpertModeVolumes{
			BaseModel:   datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:        "test-volume",
			SizeInBytes: 1099511627776,
			Style:       "flexvol",
			State:       datamodel.LifeCycleStateUpdating,
			Account: &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      "test-account",
			},
			Pool: &datamodel.Pool{
				BaseModel:      datamodel.BaseModel{ID: 1},
				DeploymentName: "test-deployment",
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "",
					CertificateID: "",
				},
			},
			Svm: &datamodel.Svm{
				Name: "test-svm",
			},
		}

		volume := &datamodel.ExpertModeVolumes{
			BaseModel:   datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:        "test-volume",
			SizeInBytes: 2199023255552,
			Style:       "flexvol",
			State:       datamodel.LifeCycleStateUpdating,
			Account: &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      "test-account",
			},
			Pool: &datamodel.Pool{
				BaseModel:      datamodel.BaseModel{ID: 1},
				DeploymentName: "test-deployment",
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "",
					CertificateID: "",
				},
			},
			Svm: &datamodel.Svm{
				Name: "test-svm",
			},
		}

		fetchedVolume := &datamodel.ExpertModeVolumes{
			BaseModel:    datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:         "test-volume",
			SizeInBytes:  1099511627776,
			Style:        "flexvol",
			ExternalUUID: "ontap-uuid-from-fetch",
		}

		stateConflictError := vsaerrors.NewVCPError(vsaerrors.ErrResourceStateConflictError, nil)
		temporalAppError := vsaerrors.WrapAsTemporalApplicationError(stateConflictError)

		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil)
		mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Times(2) // PROCESSING, ERROR
		env.OnActivity("ResolveOntapNodeForPool", mock.Anything, mock.Anything).Return(&models.Node{EndpointAddress: "127.0.0.1", EndpointAddressesToHostNameMap: map[string]string{"127.0.0.1": "127.0.0.1"}}, nil)
		env.OnActivity("ValidateONTAPVolumeUpdate", mock.Anything, mock.Anything, mock.Anything).Return(nil, temporalAppError)
		env.OnActivity("FetchOntapVolumeByUUID", mock.Anything, mock.Anything, mock.Anything).Return(fetchedVolume, nil)
		env.OnActivity("UpdateExpertModeVolumeInDB", mock.Anything, mock.Anything).Return(nil)

		env.ExecuteWorkflow(VolumeUpdateReconciliationWorkflow, volume, oldVolume)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Success_VolumeNotUpdatedInOntap_FetchOntapVolumeByUUIDFails_UsesOldVolume_MarkedAsAvailable", func(tt *testing.T) {
		// Covers path: ValidateONTAPVolumeUpdate fails -> FetchOntapVolumeByUUID fails ->
		// log "Failed to fetch volume from ONTAP", set updatedVolume = oldVolume (lines 394-399), UpdateExpertModeVolumeInDB succeeds
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
		expertModeActivity := expertmodeactivities.ExpertModeVolumeActivity{SE: mockStorage}
		env.SetHeader(mockHeader)
		env.RegisterActivity(commonActivity.ResolveOntapNodeForPool)
		env.RegisterActivity(commonActivity.UpdateJobStatus)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(expertModeActivity.ValidateONTAPVolumeUpdate)
		env.RegisterActivity(expertModeActivity.FetchOntapVolumeByUUID)
		env.RegisterActivity(expertModeActivity.UpdateExpertModeVolumeInDB)

		oldVolume := &datamodel.ExpertModeVolumes{
			BaseModel:    datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:         "test-volume",
			ExternalUUID: "old-external-uuid",
			SizeInBytes:  1099511627776,
			Style:        "flexvol",
			State:        datamodel.LifeCycleStateUpdating,
			Account: &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      "test-account",
			},
			Pool: &datamodel.Pool{
				BaseModel:      datamodel.BaseModel{ID: 1},
				DeploymentName: "test-deployment",
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "",
					CertificateID: "",
				},
			},
			Svm: &datamodel.Svm{
				Name: "test-svm",
			},
		}

		volume := &datamodel.ExpertModeVolumes{
			BaseModel:   datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:        "test-volume",
			SizeInBytes: 2199023255552,
			Style:       "flexvol",
			State:       datamodel.LifeCycleStateUpdating,
			Account: &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      "test-account",
			},
			Pool: &datamodel.Pool{
				BaseModel:      datamodel.BaseModel{ID: 1},
				DeploymentName: "test-deployment",
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "",
					CertificateID: "",
				},
			},
			Svm: &datamodel.Svm{
				Name: "test-svm",
			},
		}

		fetchErr := vsaerrors.NewVCPError(vsaerrors.ErrInternalServerError, nil)
		temporalFetchErr := vsaerrors.WrapAsTemporalApplicationError(fetchErr)
		stateConflictError := vsaerrors.NewVCPError(vsaerrors.ErrResourceStateConflictError, nil)
		temporalAppError := vsaerrors.WrapAsTemporalApplicationError(stateConflictError)

		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil)
		mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Times(2) // PROCESSING, ERROR
		env.OnActivity("ResolveOntapNodeForPool", mock.Anything, mock.Anything).Return(&models.Node{EndpointAddress: "127.0.0.1", EndpointAddressesToHostNameMap: map[string]string{"127.0.0.1": "127.0.0.1"}}, nil)
		env.OnActivity("ValidateONTAPVolumeUpdate", mock.Anything, mock.Anything, mock.Anything).Return(nil, temporalAppError)
		env.OnActivity("FetchOntapVolumeByUUID", mock.Anything, mock.Anything, mock.Anything).Return(nil, temporalFetchErr)
		env.OnActivity("UpdateExpertModeVolumeInDB", mock.Anything, mock.Anything).Return(nil)

		env.ExecuteWorkflow(VolumeUpdateReconciliationWorkflow, volume, oldVolume)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Failure_ResolveOntapNodeForPoolActivityFails", func(tt *testing.T) {
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
		env.RegisterActivity(commonActivity.ResolveOntapNodeForPool)
		env.RegisterActivity(commonActivity.UpdateJobStatus)
		env.RegisterActivity(commonActivity.GetJob)

		oldVolume := &datamodel.ExpertModeVolumes{
			BaseModel:   datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:        "test-volume",
			SizeInBytes: 1099511627776,
			Style:       "flexvol",
			State:       datamodel.LifeCycleStateUpdating,
			Account: &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      "test-account",
			},
			Pool: &datamodel.Pool{
				BaseModel:      datamodel.BaseModel{ID: 1},
				DeploymentName: "test-deployment",
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "",
					CertificateID: "",
				},
			},
			Svm: &datamodel.Svm{
				Name: "test-svm",
			},
		}

		volume := &datamodel.ExpertModeVolumes{
			BaseModel:   datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:        "test-volume",
			SizeInBytes: 2199023255552,
			Style:       "flexvol",
			State:       datamodel.LifeCycleStateUpdating,
			Account: &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      "test-account",
			},
			Pool: &datamodel.Pool{
				BaseModel:      datamodel.BaseModel{ID: 1},
				DeploymentName: "test-deployment",
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "",
					CertificateID: "",
				},
			},
			Svm: &datamodel.Svm{
				Name: "test-svm",
			},
		}

		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil)
		mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Times(2) // PROCESSING, ERROR
		env.OnActivity("ResolveOntapNodeForPool", mock.Anything, mock.Anything).Return(nil, assert.AnError)

		env.ExecuteWorkflow(VolumeUpdateReconciliationWorkflow, volume, oldVolume)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Failure_UpdateExpertModeVolumeInDBFails", func(tt *testing.T) {
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
		expertModeActivity := expertmodeactivities.ExpertModeVolumeActivity{SE: mockStorage}
		env.SetHeader(mockHeader)
		env.RegisterActivity(commonActivity.ResolveOntapNodeForPool)
		env.RegisterActivity(commonActivity.UpdateJobStatus)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(expertModeActivity.ValidateONTAPVolumeUpdate)
		env.RegisterActivity(expertModeActivity.UpdateExpertModeVolumeInDB)

		oldVolume := &datamodel.ExpertModeVolumes{
			BaseModel:   datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:        "test-volume",
			SizeInBytes: 1099511627776,
			Style:       "flexvol",
			State:       datamodel.LifeCycleStateUpdating,
			Account: &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      "test-account",
			},
			Pool: &datamodel.Pool{
				BaseModel:      datamodel.BaseModel{ID: 1},
				DeploymentName: "test-deployment",
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "",
					CertificateID: "",
				},
			},
			Svm: &datamodel.Svm{
				Name: "test-svm",
			},
		}

		volume := &datamodel.ExpertModeVolumes{
			BaseModel:   datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:        "test-volume",
			SizeInBytes: 2199023255552,
			Style:       "flexvol",
			State:       datamodel.LifeCycleStateUpdating,
			Account: &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      "test-account",
			},
			Pool: &datamodel.Pool{
				BaseModel:      datamodel.BaseModel{ID: 1},
				DeploymentName: "test-deployment",
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "",
					CertificateID: "",
				},
			},
			Svm: &datamodel.Svm{
				Name: "test-svm",
			},
		}

		updatedVolume := &datamodel.ExpertModeVolumes{
			BaseModel:    datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:         "test-volume",
			SizeInBytes:  2199023255552,
			Style:        "flexvol",
			State:        datamodel.LifeCycleStateAvailable,
			ExternalUUID: "ontap-uuid-123",
		}

		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil)
		mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Times(2) // PROCESSING, ERROR
		env.OnActivity("ResolveOntapNodeForPool", mock.Anything, mock.Anything).Return(&models.Node{EndpointAddress: "127.0.0.1", EndpointAddressesToHostNameMap: map[string]string{"127.0.0.1": "127.0.0.1"}}, nil)
		env.OnActivity("ValidateONTAPVolumeUpdate", mock.Anything, mock.Anything, mock.Anything).Return(updatedVolume, nil)
		env.OnActivity("UpdateExpertModeVolumeInDB", mock.Anything, mock.Anything).Return(assert.AnError)

		env.ExecuteWorkflow(VolumeUpdateReconciliationWorkflow, volume, oldVolume)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Failure_UpdateJobStatusToProcessingFails", func(tt *testing.T) {
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
		env.RegisterActivity(commonActivity.UpdateJobStatus)
		env.RegisterActivity(commonActivity.GetJob)

		oldVolume := &datamodel.ExpertModeVolumes{
			BaseModel:   datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:        "test-volume",
			SizeInBytes: 1099511627776,
			Style:       "flexvol",
			State:       datamodel.LifeCycleStateUpdating,
			Account: &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      "test-account",
			},
			Pool: &datamodel.Pool{
				BaseModel:      datamodel.BaseModel{ID: 1},
				DeploymentName: "test-deployment",
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "",
					CertificateID: "",
				},
			},
			Svm: &datamodel.Svm{
				Name: "test-svm",
			},
		}

		volume := &datamodel.ExpertModeVolumes{
			BaseModel:   datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:        "test-volume",
			SizeInBytes: 2199023255552,
			Style:       "flexvol",
			State:       datamodel.LifeCycleStateUpdating,
			Account: &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      "test-account",
			},
			Pool: &datamodel.Pool{
				BaseModel:      datamodel.BaseModel{ID: 1},
				DeploymentName: "test-deployment",
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "",
					CertificateID: "",
				},
			},
			Svm: &datamodel.Svm{
				Name: "test-svm",
			},
		}

		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil)
		// First UpdateJobStatus (PROCESSING) fails, then ERROR update may succeed or fail
		mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(assert.AnError).Once()
		mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

		env.ExecuteWorkflow(VolumeUpdateReconciliationWorkflow, volume, oldVolume)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Failure_EnsureJobStateFails", func(tt *testing.T) {
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

		oldVolume := &datamodel.ExpertModeVolumes{
			BaseModel:   datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:        "test-volume",
			SizeInBytes: 1099511627776,
			Style:       "flexvol",
			State:       datamodel.LifeCycleStateUpdating,
			Account: &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      "test-account",
			},
			Pool: &datamodel.Pool{
				BaseModel:      datamodel.BaseModel{ID: 1},
				DeploymentName: "test-deployment",
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "",
					CertificateID: "",
				},
			},
			Svm: &datamodel.Svm{
				Name: "test-svm",
			},
		}

		volume := &datamodel.ExpertModeVolumes{
			BaseModel:   datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:        "test-volume",
			SizeInBytes: 2199023255552,
			Style:       "flexvol",
			State:       datamodel.LifeCycleStateUpdating,
			Account: &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      "test-account",
			},
			Pool: &datamodel.Pool{
				BaseModel:      datamodel.BaseModel{ID: 1},
				DeploymentName: "test-deployment",
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "",
					CertificateID: "",
				},
			},
			Svm: &datamodel.Svm{
				Name: "test-svm",
			},
		}

		// Job state is not NEW, so EnsureJobState will fail
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStatePROCESSING), // Not NEW
		}, nil)

		env.ExecuteWorkflow(VolumeUpdateReconciliationWorkflow, volume, oldVolume)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Failure_SetupWithNilAccount", func(tt *testing.T) {
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

		oldVolume := &datamodel.ExpertModeVolumes{
			BaseModel:   datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:        "test-volume",
			SizeInBytes: 1099511627776,
			Style:       "flexvol",
			State:       datamodel.LifeCycleStateUpdating,
			Account:     nil, // Nil account will cause panic in Setup
			Pool: &datamodel.Pool{
				BaseModel:      datamodel.BaseModel{ID: 1},
				DeploymentName: "test-deployment",
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "",
					CertificateID: "",
				},
			},
			Svm: &datamodel.Svm{
				Name: "test-svm",
			},
		}

		volume := &datamodel.ExpertModeVolumes{
			BaseModel:   datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:        "test-volume",
			SizeInBytes: 2199023255552,
			Style:       "flexvol",
			State:       datamodel.LifeCycleStateUpdating,
			Account:     nil, // Nil account will cause panic in Setup
			Pool: &datamodel.Pool{
				BaseModel:      datamodel.BaseModel{ID: 1},
				DeploymentName: "test-deployment",
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "",
					CertificateID: "",
				},
			},
			Svm: &datamodel.Svm{
				Name: "test-svm",
			},
		}

		env.ExecuteWorkflow(VolumeUpdateReconciliationWorkflow, volume, oldVolume)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
	})

	t.Run("Success_QueryWorkflowStatus", func(tt *testing.T) {
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
		expertModeActivity := expertmodeactivities.ExpertModeVolumeActivity{SE: mockStorage}
		env.SetHeader(mockHeader)
		env.RegisterActivity(commonActivity.ResolveOntapNodeForPool)
		env.RegisterActivity(commonActivity.UpdateJobStatus)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(expertModeActivity.ValidateONTAPVolumeUpdate)
		env.RegisterActivity(expertModeActivity.UpdateExpertModeVolumeInDB)

		oldVolume := &datamodel.ExpertModeVolumes{
			BaseModel:   datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:        "test-volume",
			SizeInBytes: 1099511627776,
			Style:       "flexvol",
			State:       datamodel.LifeCycleStateUpdating,
			Account: &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      "test-account",
			},
			Pool: &datamodel.Pool{
				BaseModel:      datamodel.BaseModel{ID: 1},
				DeploymentName: "test-deployment",
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "",
					CertificateID: "",
				},
			},
			Svm: &datamodel.Svm{
				Name: "test-svm",
			},
		}

		volume := &datamodel.ExpertModeVolumes{
			BaseModel:   datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:        "test-volume",
			SizeInBytes: 2199023255552,
			Style:       "flexvol",
			State:       datamodel.LifeCycleStateUpdating,
			Account: &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      "test-account",
			},
			Pool: &datamodel.Pool{
				BaseModel:      datamodel.BaseModel{ID: 1},
				DeploymentName: "test-deployment",
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "",
					CertificateID: "",
				},
			},
			Svm: &datamodel.Svm{
				Name: "test-svm",
			},
		}

		updatedVolume := &datamodel.ExpertModeVolumes{
			BaseModel:    datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:         "test-volume",
			SizeInBytes:  2199023255552,
			Style:        "flexvol",
			State:        datamodel.LifeCycleStateAvailable,
			ExternalUUID: "ontap-uuid-123",
		}

		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil)
		mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Times(2) // PROCESSING, DONE
		env.OnActivity("ResolveOntapNodeForPool", mock.Anything, mock.Anything).Return(&models.Node{EndpointAddress: "127.0.0.1", EndpointAddressesToHostNameMap: map[string]string{"127.0.0.1": "127.0.0.1"}}, nil)
		env.OnActivity("ValidateONTAPVolumeUpdate", mock.Anything, mock.Anything, mock.Anything).Return(updatedVolume, nil)
		env.OnActivity("UpdateExpertModeVolumeInDB", mock.Anything, mock.Anything).Return(nil)

		env.ExecuteWorkflow(VolumeUpdateReconciliationWorkflow, volume, oldVolume)

		// Query workflow status
		status, err := env.QueryWorkflowByID("default-test-workflow-id", "status")
		assert.NoError(tt, err)
		assert.NotNil(tt, status)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.NoError(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Failure_ValidateONTAPVolumeUpdate_OtherError", func(tt *testing.T) {
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
		expertModeActivity := expertmodeactivities.ExpertModeVolumeActivity{SE: mockStorage}
		env.SetHeader(mockHeader)
		env.RegisterActivity(commonActivity.ResolveOntapNodeForPool)
		env.RegisterActivity(commonActivity.UpdateJobStatus)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(expertModeActivity.ValidateONTAPVolumeUpdate)
		env.RegisterActivity(expertModeActivity.UpdateExpertModeVolumeInDB)

		oldVolume := &datamodel.ExpertModeVolumes{
			BaseModel:   datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:        "test-volume",
			SizeInBytes: 1099511627776,
			Style:       "flexvol",
			State:       datamodel.LifeCycleStateUpdating,
			Account: &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      "test-account",
			},
			Pool: &datamodel.Pool{
				BaseModel:      datamodel.BaseModel{ID: 1},
				DeploymentName: "test-deployment",
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "",
					CertificateID: "",
				},
			},
			Svm: &datamodel.Svm{
				Name: "test-svm",
			},
		}

		volume := &datamodel.ExpertModeVolumes{
			BaseModel:   datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:        "test-volume",
			SizeInBytes: 2199023255552,
			Style:       "flexvol",
			State:       datamodel.LifeCycleStateUpdating,
			Account: &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      "test-account",
			},
			Pool: &datamodel.Pool{
				BaseModel:      datamodel.BaseModel{ID: 1},
				DeploymentName: "test-deployment",
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "",
					CertificateID: "",
				},
			},
			Svm: &datamodel.Svm{
				Name: "test-svm",
			},
		}

		// Some other error (not state conflict) - should update oldVolume to AVAILABLE
		otherError := vsaerrors.NewVCPError(vsaerrors.ErrInternalServerError, nil)
		temporalAppError := vsaerrors.WrapAsTemporalApplicationError(otherError)

		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil)
		mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Times(2) // PROCESSING, ERROR
		env.OnActivity("ResolveOntapNodeForPool", mock.Anything, mock.Anything).Return(&models.Node{EndpointAddress: "127.0.0.1", EndpointAddressesToHostNameMap: map[string]string{"127.0.0.1": "127.0.0.1"}}, nil)
		env.OnActivity("ValidateONTAPVolumeUpdate", mock.Anything, mock.Anything, mock.Anything).Return(nil, temporalAppError)
		env.OnActivity("UpdateExpertModeVolumeInDB", mock.Anything, mock.Anything).Return(nil)

		env.ExecuteWorkflow(VolumeUpdateReconciliationWorkflow, volume, oldVolume)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Failure_VolumeNotUpdated_UpdateExpertModeVolumeInDBFails", func(tt *testing.T) {
		// Covers path: ValidateONTAPVolumeUpdate fails -> FetchOntapVolumeByUUID (use oldVolume) ->
		// UpdateExpertModeVolumeInDB fails (lines 403-404)
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
		expertModeActivity := expertmodeactivities.ExpertModeVolumeActivity{SE: mockStorage}
		env.SetHeader(mockHeader)
		env.RegisterActivity(commonActivity.ResolveOntapNodeForPool)
		env.RegisterActivity(commonActivity.UpdateJobStatus)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(expertModeActivity.ValidateONTAPVolumeUpdate)
		env.RegisterActivity(expertModeActivity.FetchOntapVolumeByUUID)
		env.RegisterActivity(expertModeActivity.UpdateExpertModeVolumeInDB)

		oldVolume := &datamodel.ExpertModeVolumes{
			BaseModel:   datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:        "test-volume",
			SizeInBytes: 1099511627776,
			Style:       "flexvol",
			State:       datamodel.LifeCycleStateUpdating,
			Account: &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      "test-account",
			},
			Pool: &datamodel.Pool{
				BaseModel:      datamodel.BaseModel{ID: 1},
				DeploymentName: "test-deployment",
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "",
					CertificateID: "",
				},
			},
			Svm: &datamodel.Svm{
				Name: "test-svm",
			},
		}

		volume := &datamodel.ExpertModeVolumes{
			BaseModel:   datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:        "test-volume",
			SizeInBytes: 2199023255552,
			Style:       "flexvol",
			State:       datamodel.LifeCycleStateUpdating,
			Account: &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      "test-account",
			},
			Pool: &datamodel.Pool{
				BaseModel:      datamodel.BaseModel{ID: 1},
				DeploymentName: "test-deployment",
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "",
					CertificateID: "",
				},
			},
			Svm: &datamodel.Svm{
				Name: "test-svm",
			},
		}

		stateConflictError := vsaerrors.NewVCPError(vsaerrors.ErrResourceStateConflictError, nil)
		temporalAppError := vsaerrors.WrapAsTemporalApplicationError(stateConflictError)

		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil)
		mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Times(2) // PROCESSING, ERROR
		env.OnActivity("ResolveOntapNodeForPool", mock.Anything, mock.Anything).Return(&models.Node{EndpointAddress: "127.0.0.1", EndpointAddressesToHostNameMap: map[string]string{"127.0.0.1": "127.0.0.1"}}, nil)
		env.OnActivity("ValidateONTAPVolumeUpdate", mock.Anything, mock.Anything, mock.Anything).Return(nil, temporalAppError)
		env.OnActivity("FetchOntapVolumeByUUID", mock.Anything, mock.Anything, mock.Anything).Return(nil, assert.AnError)
		// UpdateExpertModeVolumeInDB fails when trying to update oldVolume to AVAILABLE
		env.OnActivity("UpdateExpertModeVolumeInDB", mock.Anything, mock.Anything).Return(assert.AnError)

		env.ExecuteWorkflow(VolumeUpdateReconciliationWorkflow, volume, oldVolume)

		assert.True(tt, env.IsWorkflowCompleted())
		// Workflow should complete with error (resource state conflict)
		assert.Error(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})
}

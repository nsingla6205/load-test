package replicationWorkflows

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/replicationActivities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/replication"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func TestReverseHybridReplicationPollWorkflow(t *testing.T) {
	t.Run("TestReverseHybridReplicationPollWorkflow_Success", func(tt *testing.T) {
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
		replicationActivity := replicationActivities.ReverseHybridReplicationActivity{SE: mockStorage}
		updateAttrActivity := replicationActivities.UpdateVolumeReplicationAttributesActivity{SE: mockStorage}
		commonActivity := activities.CommonActivities{SE: mockStorage}
		env.SetHeader(mockHeader)
		env.RegisterActivity(replicationActivity.ListSnapmirrorDestinationsForHybridReverse)
		env.RegisterActivity(replicationActivity.HydrateReplicationSateAndTypeForReverseHybridReplication)
		env.RegisterActivity(replicationActivity.UpdateReplicationStateForHybridReverse)
		env.RegisterActivity(replicationActivity.GetDstBasePathForHybridReverse)
		env.RegisterActivity(replicationActivity.GetSignedDstTokenForHybridReverse)
		env.RegisterActivity(updateAttrActivity.UpdateSrcVolumeReplication)
		env.RegisterActivity(replicationActivity.CleanupOldReplicationForHybridReverse)
		env.RegisterActivity(replicationActivity.DescribeRemoteJobOnDstForHybridReverse)
		env.RegisterActivity(commonActivity.UpdateJobStatus)

		dstBasePath := "https://test-base-path"
		dstJwtToken := "test-jwt-token"

		result := &replication.ReverseHybridReplicationResult{
			Event: &replication.ReverseReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-replication-uuid"},
						Name:      "test-replication",
						ReplicationAttributes: &datamodel.ReplicationDetails{
							SourceSvmName:              "source-svm",
							SourceVolumeName:           "source-volume",
							DestinationSvmName:         "dest-svm",
							DestinationVolumeName:      "dest-volume",
							DestinationLocation:        "us-central1",
							DestinationReplicationUUID: "test-dest-replication-uuid",
						},
					},
					DestinationProjectNumber: "987654321",
					XCorrelationID:           stringPtr("test-correlation-id"),
				},
			},
			DbVolReplication: &datamodel.VolumeReplication{
				BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-replication-uuid"},
				Name:      "test-replication",
				ReplicationAttributes: &datamodel.ReplicationDetails{
					SourceSvmName:              "source-svm",
					SourceVolumeName:           "source-volume",
					DestinationSvmName:         "dest-svm",
					DestinationVolumeName:      "dest-volume",
					DestinationLocation:        "us-central1",
					DestinationReplicationUUID: "test-dest-replication-uuid",
				},
			},
			DstProjectNumber: stringPtr("987654321"),
		}

		resultWithBasePath := &replication.ReverseHybridReplicationResult{
			Event:            result.Event,
			DbVolReplication: result.DbVolReplication,
			DstProjectNumber: result.DstProjectNumber,
			DstBasePath:      &dstBasePath,
		}

		jobId := "test-job-id"
		resultWithToken := &replication.ReverseHybridReplicationResult{
			Event:            result.Event,
			DbVolReplication: result.DbVolReplication,
			DstProjectNumber: result.DstProjectNumber,
			DstBasePath:      &dstBasePath,
			DstJwtToken:      &dstJwtToken,
		}

		resultWithJobId := &replication.ReverseHybridReplicationResult{
			Event:            result.Event,
			DbVolReplication: result.DbVolReplication,
			DstProjectNumber: result.DstProjectNumber,
			DstBasePath:      &dstBasePath,
			DstJwtToken:      &dstJwtToken,
			JobId:            &jobId,
		}

		updateAttrResult := &replication.UpdateVolumeReplicationAttributesResult{
			Event: &replication.UpdateVolumeReplicationAttributesEvent{
				UpdateVolumeReplicationAttributesParams: &models.UpdateVolumeReplicationAttributesParams{
					VolumeReplicationId: result.DbVolReplication.ReplicationAttributes.DestinationReplicationUUID,
				},
			},
		}

		mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("ListSnapmirrorDestinationsForHybridReverse", mock.Anything, mock.Anything).Return(result, nil)
		env.OnActivity("HydrateReplicationSateAndTypeForReverseHybridReplication", mock.Anything, mock.Anything).Return(result, nil)
		env.OnActivity("UpdateReplicationStateForHybridReverse", mock.Anything, mock.Anything).Return(result, nil)
		env.OnActivity("GetDstBasePathForHybridReverse", mock.Anything, mock.Anything).Return(resultWithBasePath, nil)
		env.OnActivity("GetSignedDstTokenForHybridReverse", mock.Anything, mock.Anything).Return(resultWithToken, nil)
		env.OnActivity("UpdateSrcVolumeReplication", mock.Anything, mock.Anything).Return(updateAttrResult, nil)
		env.OnActivity("CleanupOldReplicationForHybridReverse", mock.Anything, mock.Anything).Return(resultWithJobId, nil)
		env.OnActivity("DescribeRemoteJobOnDstForHybridReverse", mock.Anything, mock.Anything).Return(nil)

		env.ExecuteWorkflow(ReverseHybridReplicationPollWorkflow, result)

		_, err := env.QueryWorkflowByID("default-test-workflow-id", "status")
		assert.Nil(tt, err)
		assert.True(tt, env.IsWorkflowCompleted())
		assert.NoError(tt, env.GetWorkflowError())
	})

	t.Run("TestReverseHybridReplicationPollWorkflow_ListSnapmirrorDestinationsError", func(tt *testing.T) {
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
		replicationActivity := replicationActivities.ReverseHybridReplicationActivity{SE: mockStorage}
		commonActivity := activities.CommonActivities{SE: mockStorage}
		env.SetHeader(mockHeader)
		env.RegisterActivity(replicationActivity.ListSnapmirrorDestinationsForHybridReverse)
		env.RegisterActivity(replicationActivity.SetReplicationToErrorForReverseHybrid)
		env.RegisterActivity(commonActivity.UpdateJobStatus)

		result := &replication.ReverseHybridReplicationResult{
			Event: &replication.ReverseReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-replication-uuid"},
						Name:      "test-replication",
						ReplicationAttributes: &datamodel.ReplicationDetails{
							SourceSvmName:         "source-svm",
							SourceVolumeName:      "source-volume",
							DestinationSvmName:    "dest-svm",
							DestinationVolumeName: "dest-volume",
						},
					},
				},
			},
			DbVolReplication: &datamodel.VolumeReplication{
				BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-replication-uuid"},
				Name:      "test-replication",
			},
		}

		mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Times(2)
		env.OnActivity("ListSnapmirrorDestinationsForHybridReverse", mock.Anything, mock.Anything).Return(nil, assert.AnError)
		env.OnActivity("SetReplicationToErrorForReverseHybrid", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

		env.ExecuteWorkflow(ReverseHybridReplicationPollWorkflow, result)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
	})

	t.Run("TestReverseHybridReplicationPollWorkflow_UpdateReplicationStateError", func(tt *testing.T) {
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
		replicationActivity := replicationActivities.ReverseHybridReplicationActivity{SE: mockStorage}
		commonActivity := activities.CommonActivities{SE: mockStorage}
		env.SetHeader(mockHeader)
		env.RegisterActivity(replicationActivity.ListSnapmirrorDestinationsForHybridReverse)
		env.RegisterActivity(replicationActivity.HydrateReplicationSateAndTypeForReverseHybridReplication)
		env.RegisterActivity(replicationActivity.UpdateReplicationStateForHybridReverse)
		env.RegisterActivity(replicationActivity.SetReplicationToErrorForReverseHybrid)
		env.RegisterActivity(commonActivity.UpdateJobStatus)

		result := &replication.ReverseHybridReplicationResult{
			Event: &replication.ReverseReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-replication-uuid"},
						Name:      "test-replication",
						ReplicationAttributes: &datamodel.ReplicationDetails{
							SourceSvmName:         "source-svm",
							SourceVolumeName:      "source-volume",
							DestinationSvmName:    "dest-svm",
							DestinationVolumeName: "dest-volume",
						},
					},
				},
			},
			DbVolReplication: &datamodel.VolumeReplication{
				BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-replication-uuid"},
				Name:      "test-replication",
			},
		}

		mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Times(2)
		env.OnActivity("ListSnapmirrorDestinationsForHybridReverse", mock.Anything, mock.Anything).Return(result, nil)
		env.OnActivity("HydrateReplicationSateAndTypeForReverseHybridReplication", mock.Anything, mock.Anything).Return(result, nil)
		env.OnActivity("UpdateReplicationStateForHybridReverse", mock.Anything, mock.Anything).Return(nil, assert.AnError)
		env.OnActivity("SetReplicationToErrorForReverseHybrid", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

		env.ExecuteWorkflow(ReverseHybridReplicationPollWorkflow, result)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
	})

	t.Run("TestReverseHybridReplicationPollWorkflow_UpdateSrcVolumeReplicationError", func(tt *testing.T) {
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
		replicationActivity := replicationActivities.ReverseHybridReplicationActivity{SE: mockStorage}
		updateAttrActivity := replicationActivities.UpdateVolumeReplicationAttributesActivity{SE: mockStorage}
		commonActivity := activities.CommonActivities{SE: mockStorage}
		env.SetHeader(mockHeader)
		env.RegisterActivity(replicationActivity.ListSnapmirrorDestinationsForHybridReverse)
		env.RegisterActivity(replicationActivity.HydrateReplicationSateAndTypeForReverseHybridReplication)
		env.RegisterActivity(replicationActivity.UpdateReplicationStateForHybridReverse)
		env.RegisterActivity(replicationActivity.GetDstBasePathForHybridReverse)
		env.RegisterActivity(replicationActivity.GetSignedDstTokenForHybridReverse)
		env.RegisterActivity(updateAttrActivity.UpdateSrcVolumeReplication)
		env.RegisterActivity(replicationActivity.SetReplicationToErrorForReverseHybrid)
		env.RegisterActivity(commonActivity.UpdateJobStatus)

		dstBasePath := "https://test-base-path"
		dstJwtToken := "test-jwt-token"

		result := &replication.ReverseHybridReplicationResult{
			Event: &replication.ReverseReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-replication-uuid"},
						Name:      "test-replication",
						ReplicationAttributes: &datamodel.ReplicationDetails{
							SourceSvmName:              "source-svm",
							SourceVolumeName:           "source-volume",
							DestinationSvmName:         "dest-svm",
							DestinationVolumeName:      "dest-volume",
							DestinationLocation:        "us-central1",
							DestinationReplicationUUID: "test-dest-replication-uuid",
						},
					},
					DestinationProjectNumber: "987654321",
					XCorrelationID:           stringPtr("test-correlation-id"),
				},
			},
			DbVolReplication: &datamodel.VolumeReplication{
				BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-replication-uuid"},
				Name:      "test-replication",
				ReplicationAttributes: &datamodel.ReplicationDetails{
					SourceSvmName:              "source-svm",
					SourceVolumeName:           "source-volume",
					DestinationSvmName:         "dest-svm",
					DestinationVolumeName:      "dest-volume",
					DestinationLocation:        "us-central1",
					DestinationReplicationUUID: "test-dest-replication-uuid",
				},
			},
			DstProjectNumber: stringPtr("987654321"),
		}

		resultWithBasePath := &replication.ReverseHybridReplicationResult{
			Event:            result.Event,
			DbVolReplication: result.DbVolReplication,
			DstProjectNumber: result.DstProjectNumber,
			DstBasePath:      &dstBasePath,
		}

		resultWithToken := &replication.ReverseHybridReplicationResult{
			Event:            result.Event,
			DbVolReplication: result.DbVolReplication,
			DstProjectNumber: result.DstProjectNumber,
			DstBasePath:      &dstBasePath,
			DstJwtToken:      &dstJwtToken,
		}

		mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("ListSnapmirrorDestinationsForHybridReverse", mock.Anything, mock.Anything).Return(result, nil)
		env.OnActivity("HydrateReplicationSateAndTypeForReverseHybridReplication", mock.Anything, mock.Anything).Return(result, nil)
		env.OnActivity("UpdateReplicationStateForHybridReverse", mock.Anything, mock.Anything).Return(result, nil)
		env.OnActivity("GetDstBasePathForHybridReverse", mock.Anything, mock.Anything).Return(resultWithBasePath, nil)
		env.OnActivity("GetSignedDstTokenForHybridReverse", mock.Anything, mock.Anything).Return(resultWithToken, nil)
		env.OnActivity("UpdateSrcVolumeReplication", mock.Anything, mock.Anything).Return(nil, assert.AnError)
		env.OnActivity("SetReplicationToErrorForReverseHybrid", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

		env.ExecuteWorkflow(ReverseHybridReplicationPollWorkflow, result)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
	})

	t.Run("TestReverseHybridReplicationPollWorkflow_CleanupOldReplicationError", func(tt *testing.T) {
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
		replicationActivity := replicationActivities.ReverseHybridReplicationActivity{SE: mockStorage}
		updateAttrActivity := replicationActivities.UpdateVolumeReplicationAttributesActivity{SE: mockStorage}
		commonActivity := activities.CommonActivities{SE: mockStorage}
		env.SetHeader(mockHeader)
		env.RegisterActivity(replicationActivity.ListSnapmirrorDestinationsForHybridReverse)
		env.RegisterActivity(replicationActivity.HydrateReplicationSateAndTypeForReverseHybridReplication)
		env.RegisterActivity(replicationActivity.UpdateReplicationStateForHybridReverse)
		env.RegisterActivity(replicationActivity.GetDstBasePathForHybridReverse)
		env.RegisterActivity(replicationActivity.GetSignedDstTokenForHybridReverse)
		env.RegisterActivity(updateAttrActivity.UpdateSrcVolumeReplication)
		env.RegisterActivity(replicationActivity.CleanupOldReplicationForHybridReverse)
		env.RegisterActivity(replicationActivity.DescribeRemoteJobOnDstForHybridReverse)
		env.RegisterActivity(replicationActivity.SetReplicationToErrorForReverseHybrid)
		env.RegisterActivity(commonActivity.UpdateJobStatus)

		dstBasePath := "https://test-base-path"
		dstJwtToken := "test-jwt-token"

		result := &replication.ReverseHybridReplicationResult{
			Event: &replication.ReverseReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-replication-uuid"},
						Name:      "test-replication",
						ReplicationAttributes: &datamodel.ReplicationDetails{
							SourceSvmName:              "source-svm",
							SourceVolumeName:           "source-volume",
							DestinationSvmName:         "dest-svm",
							DestinationVolumeName:      "dest-volume",
							DestinationLocation:        "us-central1",
							DestinationReplicationUUID: "test-dest-replication-uuid",
						},
					},
					DestinationProjectNumber: "987654321",
					XCorrelationID:           stringPtr("test-correlation-id"),
				},
			},
			DbVolReplication: &datamodel.VolumeReplication{
				BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-replication-uuid"},
				Name:      "test-replication",
				ReplicationAttributes: &datamodel.ReplicationDetails{
					SourceSvmName:              "source-svm",
					SourceVolumeName:           "source-volume",
					DestinationSvmName:         "dest-svm",
					DestinationVolumeName:      "dest-volume",
					DestinationLocation:        "us-central1",
					DestinationReplicationUUID: "test-dest-replication-uuid",
				},
			},
			DstProjectNumber: stringPtr("987654321"),
		}

		resultWithBasePath := &replication.ReverseHybridReplicationResult{
			Event:            result.Event,
			DbVolReplication: result.DbVolReplication,
			DstProjectNumber: result.DstProjectNumber,
			DstBasePath:      &dstBasePath,
		}

		resultWithToken := &replication.ReverseHybridReplicationResult{
			Event:            result.Event,
			DbVolReplication: result.DbVolReplication,
			DstProjectNumber: result.DstProjectNumber,
			DstBasePath:      &dstBasePath,
			DstJwtToken:      &dstJwtToken,
		}

		updateAttrResult := &replication.UpdateVolumeReplicationAttributesResult{
			Event: &replication.UpdateVolumeReplicationAttributesEvent{
				UpdateVolumeReplicationAttributesParams: &models.UpdateVolumeReplicationAttributesParams{
					VolumeReplicationId: result.DbVolReplication.ReplicationAttributes.DestinationReplicationUUID,
				},
			},
		}

		mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("ListSnapmirrorDestinationsForHybridReverse", mock.Anything, mock.Anything).Return(result, nil)
		env.OnActivity("HydrateReplicationSateAndTypeForReverseHybridReplication", mock.Anything, mock.Anything).Return(result, nil)
		env.OnActivity("UpdateReplicationStateForHybridReverse", mock.Anything, mock.Anything).Return(result, nil)
		env.OnActivity("GetDstBasePathForHybridReverse", mock.Anything, mock.Anything).Return(resultWithBasePath, nil)
		env.OnActivity("GetSignedDstTokenForHybridReverse", mock.Anything, mock.Anything).Return(resultWithToken, nil)
		env.OnActivity("UpdateSrcVolumeReplication", mock.Anything, mock.Anything).Return(updateAttrResult, nil)
		env.OnActivity("CleanupOldReplicationForHybridReverse", mock.Anything, mock.Anything).Return(nil, assert.AnError)
		env.OnActivity("SetReplicationToErrorForReverseHybrid", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

		env.ExecuteWorkflow(ReverseHybridReplicationPollWorkflow, result)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
	})

	t.Run("TestReverseHybridReplicationPollWorkflow_DescribeRemoteJobError", func(tt *testing.T) {
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
		replicationActivity := replicationActivities.ReverseHybridReplicationActivity{SE: mockStorage}
		updateAttrActivity := replicationActivities.UpdateVolumeReplicationAttributesActivity{SE: mockStorage}
		commonActivity := activities.CommonActivities{SE: mockStorage}
		env.SetHeader(mockHeader)
		env.RegisterActivity(replicationActivity.ListSnapmirrorDestinationsForHybridReverse)
		env.RegisterActivity(replicationActivity.HydrateReplicationSateAndTypeForReverseHybridReplication)
		env.RegisterActivity(replicationActivity.UpdateReplicationStateForHybridReverse)
		env.RegisterActivity(replicationActivity.GetDstBasePathForHybridReverse)
		env.RegisterActivity(replicationActivity.GetSignedDstTokenForHybridReverse)
		env.RegisterActivity(updateAttrActivity.UpdateSrcVolumeReplication)
		env.RegisterActivity(replicationActivity.CleanupOldReplicationForHybridReverse)
		env.RegisterActivity(replicationActivity.DescribeRemoteJobOnDstForHybridReverse)
		env.RegisterActivity(replicationActivity.SetReplicationToErrorForReverseHybrid)
		env.RegisterActivity(commonActivity.UpdateJobStatus)

		dstBasePath := "https://test-base-path"
		dstJwtToken := "test-jwt-token"
		jobId := "test-job-id"

		result := &replication.ReverseHybridReplicationResult{
			Event: &replication.ReverseReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-replication-uuid"},
						Name:      "test-replication",
						ReplicationAttributes: &datamodel.ReplicationDetails{
							SourceSvmName:              "source-svm",
							SourceVolumeName:           "source-volume",
							DestinationSvmName:         "dest-svm",
							DestinationVolumeName:      "dest-volume",
							DestinationLocation:        "us-central1",
							DestinationReplicationUUID: "test-dest-replication-uuid",
						},
					},
					DestinationProjectNumber: "987654321",
					XCorrelationID:           stringPtr("test-correlation-id"),
				},
			},
			DbVolReplication: &datamodel.VolumeReplication{
				BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-replication-uuid"},
				Name:      "test-replication",
				ReplicationAttributes: &datamodel.ReplicationDetails{
					SourceSvmName:              "source-svm",
					SourceVolumeName:           "source-volume",
					DestinationSvmName:         "dest-svm",
					DestinationVolumeName:      "dest-volume",
					DestinationLocation:        "us-central1",
					DestinationReplicationUUID: "test-dest-replication-uuid",
				},
			},
			DstProjectNumber: stringPtr("987654321"),
		}

		resultWithBasePath := &replication.ReverseHybridReplicationResult{
			Event:            result.Event,
			DbVolReplication: result.DbVolReplication,
			DstProjectNumber: result.DstProjectNumber,
			DstBasePath:      &dstBasePath,
		}

		resultWithToken := &replication.ReverseHybridReplicationResult{
			Event:            result.Event,
			DbVolReplication: result.DbVolReplication,
			DstProjectNumber: result.DstProjectNumber,
			DstBasePath:      &dstBasePath,
			DstJwtToken:      &dstJwtToken,
		}

		resultWithJobId := &replication.ReverseHybridReplicationResult{
			Event:            result.Event,
			DbVolReplication: result.DbVolReplication,
			DstProjectNumber: result.DstProjectNumber,
			DstBasePath:      &dstBasePath,
			DstJwtToken:      &dstJwtToken,
			JobId:            &jobId,
		}

		updateAttrResult := &replication.UpdateVolumeReplicationAttributesResult{
			Event: &replication.UpdateVolumeReplicationAttributesEvent{
				UpdateVolumeReplicationAttributesParams: &models.UpdateVolumeReplicationAttributesParams{
					VolumeReplicationId: result.DbVolReplication.ReplicationAttributes.DestinationReplicationUUID,
				},
			},
		}

		mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("ListSnapmirrorDestinationsForHybridReverse", mock.Anything, mock.Anything).Return(result, nil)
		env.OnActivity("HydrateReplicationSateAndTypeForReverseHybridReplication", mock.Anything, mock.Anything).Return(result, nil)
		env.OnActivity("UpdateReplicationStateForHybridReverse", mock.Anything, mock.Anything).Return(result, nil)
		env.OnActivity("GetDstBasePathForHybridReverse", mock.Anything, mock.Anything).Return(resultWithBasePath, nil)
		env.OnActivity("GetSignedDstTokenForHybridReverse", mock.Anything, mock.Anything).Return(resultWithToken, nil)
		env.OnActivity("UpdateSrcVolumeReplication", mock.Anything, mock.Anything).Return(updateAttrResult, nil)
		env.OnActivity("CleanupOldReplicationForHybridReverse", mock.Anything, mock.Anything).Return(resultWithJobId, nil)
		env.OnActivity("DescribeRemoteJobOnDstForHybridReverse", mock.Anything, mock.Anything).Return(assert.AnError)
		env.OnActivity("SetReplicationToErrorForReverseHybrid", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

		env.ExecuteWorkflow(ReverseHybridReplicationPollWorkflow, result)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
	})

	t.Run("TestReverseHybridReplicationPollWorkflow_SetupError", func(tt *testing.T) {
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
		replicationActivity := replicationActivities.ReverseHybridReplicationActivity{SE: mockStorage}
		commonActivity := activities.CommonActivities{SE: mockStorage}
		env.SetHeader(mockHeader)
		env.RegisterActivity(commonActivity.UpdateJobStatus)
		env.RegisterActivity(replicationActivity.ListSnapmirrorDestinationsForHybridReverse)

		result := &replication.ReverseHybridReplicationResult{
			Event: &replication.ReverseReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-replication-uuid"},
						Name:      "test-replication",
					},
				},
			},
			DbVolReplication: &datamodel.VolumeReplication{
				BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-replication-uuid"},
				Name:      "test-replication",
			},
		}

		mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(assert.AnError).Once()
		mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

		env.ExecuteWorkflow(ReverseHybridReplicationPollWorkflow, result)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
	})
}

func stringPtr(s string) *string {
	return &s
}

func TestReverseHybridReplicationPollWorkflow_ActivityFailureLogging(t *testing.T) {
	tests := []struct {
		name                  string
		failingActivity       string
		failErrorStatusUpdate bool
	}{
		{name: "hydrate replication fails", failingActivity: "hydrate"},
		{name: "get destination base path fails", failingActivity: "base path"},
		{name: "get destination token fails", failingActivity: "token"},
		{name: "error status update fails", failingActivity: "hydrate", failErrorStatusUpdate: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var testSuite testsuite.WorkflowTestSuite
			env := testSuite.NewTestWorkflowEnvironment()
			env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
			encodedValue, err := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
			assert.NoError(t, err)
			env.SetHeader(&commonpb.Header{
				Fields: map[string]*commonpb.Payload{"logParam": encodedValue},
			})

			mockStorage := database.NewMockStorage(t)
			replicationActivity := &replicationActivities.ReverseHybridReplicationActivity{SE: mockStorage}
			commonActivity := &activities.CommonActivities{SE: mockStorage}
			env.RegisterActivity(replicationActivity.ListSnapmirrorDestinationsForHybridReverse)
			env.RegisterActivity(replicationActivity.HydrateReplicationSateAndTypeForReverseHybridReplication)
			env.RegisterActivity(replicationActivity.UpdateReplicationStateForHybridReverse)
			env.RegisterActivity(replicationActivity.GetDstBasePathForHybridReverse)
			env.RegisterActivity(replicationActivity.GetSignedDstTokenForHybridReverse)
			env.RegisterActivity(replicationActivity.SetReplicationToErrorForReverseHybrid)
			env.RegisterActivity(commonActivity.UpdateJobStatus)

			result := &replication.ReverseHybridReplicationResult{
				DbVolReplication: &datamodel.VolumeReplication{
					BaseModel: datamodel.BaseModel{UUID: "test-replication-uuid"},
				},
			}

			mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
			if test.failErrorStatusUpdate {
				mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(assert.AnError)
			} else {
				mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
			}

			env.OnActivity(replicationActivity.ListSnapmirrorDestinationsForHybridReverse, mock.Anything, mock.Anything).Return(result, nil)
			if test.failingActivity == "hydrate" {
				env.OnActivity(replicationActivity.HydrateReplicationSateAndTypeForReverseHybridReplication, mock.Anything, mock.Anything).Return(nil, assert.AnError)
			} else {
				env.OnActivity(replicationActivity.HydrateReplicationSateAndTypeForReverseHybridReplication, mock.Anything, mock.Anything).Return(result, nil)
				env.OnActivity(replicationActivity.UpdateReplicationStateForHybridReverse, mock.Anything, mock.Anything).Return(result, nil)
				if test.failingActivity == "base path" {
					env.OnActivity(replicationActivity.GetDstBasePathForHybridReverse, mock.Anything, mock.Anything).Return(nil, assert.AnError)
				} else {
					env.OnActivity(replicationActivity.GetDstBasePathForHybridReverse, mock.Anything, mock.Anything).Return(result, nil)
					env.OnActivity(replicationActivity.GetSignedDstTokenForHybridReverse, mock.Anything, mock.Anything).Return(nil, assert.AnError)
				}
			}
			env.OnActivity(replicationActivity.SetReplicationToErrorForReverseHybrid, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

			env.ExecuteWorkflow(ReverseHybridReplicationPollWorkflow, result)

			assert.True(t, env.IsWorkflowCompleted())
			assert.Error(t, env.GetWorkflowError())
			env.AssertExpectations(t)
		})
	}
}

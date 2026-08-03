package vsa

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/go-openapi/strfmt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/client/cluster"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/models"
	ontaprest "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/ontap-rest"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
)

func TestGetClusterHealthStatus_Success(t *testing.T) {
	t.Run("successful health status retrieval with full HA and NVLOG data", func(t *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockClusterClient := new(ontaprest.MockClusterClient)
		originalgetOntapClientFunc := getOntapClientFunc
		defer func() { getOntapClientFunc = originalgetOntapClientFunc }()

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}

		mockClient.On("Cluster").Return(mockClusterClient)

		// Mock the NodesGet call with comprehensive node data
		mockClusterClient.On("NodesGet", mock.MatchedBy(func(params *ontaprest.NodesGetParams) bool {
			// Verify that the correct fields are requested
			expectedFields := []string{"name", "uuid", "ha.takeover", "ha.takeover_check", "nvlog"}
			return assert.Equal(t, expectedFields, params.BaseParams.Fields)
		}), mock.AnythingOfType("ontap_rest.UserCallbackFunc[[]*github.com/vcp-vsa-control-Plane/vsa-control-plane/core/ontap-rest.Node]")).Return(nil).Run(func(args mock.Arguments) {
			callback := args.Get(1).(ontaprest.UserCallbackFunc[[]*ontaprest.Node])

			// Create mock nodes with comprehensive HA and NVLOG data
			nodes := []*ontaprest.Node{
				{
					NodeResponseInlineRecordsInlineArrayItem: models.NodeResponseInlineRecordsInlineArrayItem{
						UUID: nillable.ToPointer(strfmt.UUID("node-1-uuid")),
						Name: nillable.ToPointer("node-1"),
						Ha: &models.NodeResponseInlineRecordsInlineArrayItemInlineHa{
							Takeover: &models.NodeResponseInlineRecordsInlineArrayItemInlineHaInlineTakeover{
								State: nillable.ToPointer("not_attempted"),
								Failure: &models.NodeResponseInlineRecordsInlineArrayItemInlineHaInlineTakeoverInlineFailure{
									Message: nillable.ToPointer("No failure"),
									Code:    nillable.ToPointer(int64(0)),
								},
							},
							TakeoverCheck: &models.NodeResponseInlineRecordsInlineArrayItemInlineHaInlineTakeoverCheck{
								TakeoverPossible: nillable.ToPointer(true),
								Reasons:          []*string{nillable.ToPointer("Partner ready")},
							},
						},
						Nvlog: &models.NodeResponseInlineRecordsInlineArrayItemInlineNvlog{
							SwapMode:    nillable.ToPointer("dynamic"),
							BackingType: nillable.ToPointer("ephemeral_memory"),
						},
					},
				},
				{
					NodeResponseInlineRecordsInlineArrayItem: models.NodeResponseInlineRecordsInlineArrayItem{
						UUID: nillable.ToPointer(strfmt.UUID("node-2-uuid")),
						Name: nillable.ToPointer("node-2"),
						Ha: &models.NodeResponseInlineRecordsInlineArrayItemInlineHa{
							Takeover: &models.NodeResponseInlineRecordsInlineArrayItemInlineHaInlineTakeover{
								State: nillable.ToPointer("taken_over"),
								Failure: &models.NodeResponseInlineRecordsInlineArrayItemInlineHaInlineTakeoverInlineFailure{
									Message: nillable.ToPointer("Storage failure"),
									Code:    nillable.ToPointer(int64(500)),
								},
							},
							TakeoverCheck: &models.NodeResponseInlineRecordsInlineArrayItemInlineHaInlineTakeoverCheck{
								TakeoverPossible: nillable.ToPointer(false),
								Reasons:          []*string{nillable.ToPointer("Node down"), nillable.ToPointer("Network issues")},
							},
						},
						Nvlog: &models.NodeResponseInlineRecordsInlineArrayItemInlineNvlog{
							SwapMode:    nillable.ToPointer("static"),
							BackingType: nillable.ToPointer("ephemeral_disk"),
						},
					},
				},
			}

			_ = callback(nodes) // Ignore callback error in test
		})

		ontapRestProvider := &OntapRestProvider{}
		result, err := ontapRestProvider.GetClusterHealthStatus()

		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, 2, result.NumRecords)
		assert.Len(t, result.Records, 2)

		// Verify first node
		node1 := result.Records[0]
		assert.Equal(t, "node-1-uuid", node1.UUID)
		assert.Equal(t, "node-1", node1.Name)
		assert.NotNil(t, node1.Ha)
		assert.NotNil(t, node1.Ha.Takeover)
		assert.Equal(t, "not_attempted", node1.Ha.Takeover.State)
		assert.NotNil(t, node1.Ha.Takeover.Failure)
		assert.Equal(t, "No failure", node1.Ha.Takeover.Failure.Message)
		assert.Equal(t, 0, node1.Ha.Takeover.Failure.Code)
		assert.NotNil(t, node1.Ha.TakeoverCheck)
		assert.True(t, node1.Ha.TakeoverCheck.TakeoverPossible)
		assert.Len(t, node1.Ha.TakeoverCheck.Reasons, 1)
		assert.Equal(t, "Partner ready", node1.Ha.TakeoverCheck.Reasons[0])
		assert.NotNil(t, node1.NVLog)
		assert.Equal(t, "dynamic", node1.NVLog.SwapMode)
		assert.Equal(t, "ephemeral_memory", node1.NVLog.BackingType)

		// Verify second node
		node2 := result.Records[1]
		assert.Equal(t, "node-2-uuid", node2.UUID)
		assert.Equal(t, "node-2", node2.Name)
		assert.NotNil(t, node2.Ha)
		assert.NotNil(t, node2.Ha.Takeover)
		assert.Equal(t, "taken_over", node2.Ha.Takeover.State)
		assert.NotNil(t, node2.Ha.Takeover.Failure)
		assert.Equal(t, "Storage failure", node2.Ha.Takeover.Failure.Message)
		assert.Equal(t, 500, node2.Ha.Takeover.Failure.Code)
		assert.NotNil(t, node2.Ha.TakeoverCheck)
		assert.False(t, node2.Ha.TakeoverCheck.TakeoverPossible)
		assert.Len(t, node2.Ha.TakeoverCheck.Reasons, 2)
		assert.Equal(t, "Node down", node2.Ha.TakeoverCheck.Reasons[0])
		assert.Equal(t, "Network issues", node2.Ha.TakeoverCheck.Reasons[1])
		assert.NotNil(t, node2.NVLog)
		assert.Equal(t, "static", node2.NVLog.SwapMode)
		assert.Equal(t, "ephemeral_disk", node2.NVLog.BackingType)

		mockClient.AssertExpectations(t)
		mockClusterClient.AssertExpectations(t)
	})

	t.Run("successful health status with minimal data", func(t *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockClusterClient := new(ontaprest.MockClusterClient)
		originalgetOntapClientFunc := getOntapClientFunc
		defer func() { getOntapClientFunc = originalgetOntapClientFunc }()

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}

		mockClient.On("Cluster").Return(mockClusterClient)

		mockClusterClient.On("NodesGet", mock.AnythingOfType("*ontap_rest.NodesGetParams"), mock.AnythingOfType("ontap_rest.UserCallbackFunc[[]*github.com/vcp-vsa-control-Plane/vsa-control-plane/core/ontap-rest.Node]")).Return(nil).Run(func(args mock.Arguments) {
			callback := args.Get(1).(ontaprest.UserCallbackFunc[[]*ontaprest.Node])

			// Create mock nodes with minimal data
			nodes := []*ontaprest.Node{
				{
					NodeResponseInlineRecordsInlineArrayItem: models.NodeResponseInlineRecordsInlineArrayItem{
						UUID: nillable.ToPointer(strfmt.UUID("node-minimal-uuid")),
						Name: nillable.ToPointer("node-minimal"),
						// No Ha or Nvlog data
					},
				},
			}

			_ = callback(nodes) // Ignore callback error in test
		})

		ontapRestProvider := &OntapRestProvider{}
		result, err := ontapRestProvider.GetClusterHealthStatus()

		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, 1, result.NumRecords)
		assert.Len(t, result.Records, 1)

		node := result.Records[0]
		assert.Equal(t, "node-minimal-uuid", node.UUID)
		assert.Equal(t, "node-minimal", node.Name)
		assert.Nil(t, node.Ha)
		assert.Nil(t, node.NVLog)

		mockClient.AssertExpectations(t)
		mockClusterClient.AssertExpectations(t)
	})
}

func TestGetClusterHealthStatus_Errors(t *testing.T) {
	t.Run("client creation error", func(t *testing.T) {
		originalgetOntapClientFunc := getOntapClientFunc
		defer func() { getOntapClientFunc = originalgetOntapClientFunc }()

		expectedErr := errors.New("client creation failed")
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return nil, expectedErr
		}

		ontapRestProvider := &OntapRestProvider{}
		result, err := ontapRestProvider.GetClusterHealthStatus()

		assert.Error(t, err)
		assert.Equal(t, expectedErr, err)
		assert.Nil(t, result)
	})

	t.Run("nodes get API error", func(t *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockClusterClient := new(ontaprest.MockClusterClient)
		originalgetOntapClientFunc := getOntapClientFunc
		defer func() { getOntapClientFunc = originalgetOntapClientFunc }()

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}

		mockClient.On("Cluster").Return(mockClusterClient)

		expectedErr := errors.New("API call failed")
		mockClusterClient.On("NodesGet", mock.AnythingOfType("*ontap_rest.NodesGetParams"), mock.AnythingOfType("ontap_rest.UserCallbackFunc[[]*github.com/vcp-vsa-control-Plane/vsa-control-plane/core/ontap-rest.Node]")).Return(expectedErr)

		ontapRestProvider := &OntapRestProvider{}
		result, err := ontapRestProvider.GetClusterHealthStatus()

		assert.Error(t, err)
		assert.Equal(t, expectedErr, err)
		assert.Nil(t, result)

		mockClient.AssertExpectations(t)
		mockClusterClient.AssertExpectations(t)
	})
}

func TestJSwapTo_Success(t *testing.T) {
	t.Run("successful JSwap operation", func(t *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockClusterClient := new(ontaprest.MockClusterClient)
		originalgetOntapClientFunc := getOntapClientFunc
		defer func() { getOntapClientFunc = originalgetOntapClientFunc }()

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}

		mockClient.On("Cluster").Return(mockClusterClient)

		targetNodeUUID := "test-node-uuid"
		backingType := JSWAPBackingTypeEphemeralDisk
		jobUUID := "test-job-uuid"

		// Mock the ModifyNode call
		mockClusterClient.On("ModifyNode", context.Background(), mock.MatchedBy(func(params *ontaprest.NodeModifyParams) bool {
			return params.UUID == targetNodeUUID &&
				params.Body.NVLog.BackingType == string(backingType)
		})).Return(&cluster.NodeModifyOK{
			Payload: &models.NodeJobLinkResponse{
				Job: &models.JobLink{
					UUID: nillable.ToPointer(strfmt.UUID(jobUUID)),
				},
			},
		}, nil)

		// Mock successful job completion
		mockClusterClient.On("GetJob", jobUUID).Return(&cluster.JobGetOK{
			Payload: &models.Job{
				UUID:    nillable.ToPointer(strfmt.UUID(jobUUID)),
				State:   nillable.ToPointer(models.JobStateSuccess),
				Message: nillable.ToPointer("Operation completed successfully"),
			},
		}, nil)

		ontapRestProvider := &OntapRestProvider{}
		success, err := ontapRestProvider.UpdateJSwapMode(targetNodeUUID, backingType)

		assert.NoError(t, err)
		assert.True(t, success)

		mockClient.AssertExpectations(t)
		mockClusterClient.AssertExpectations(t)
	})
}

func TestJSwapTo_Errors(t *testing.T) {
	t.Run("client creation error", func(t *testing.T) {
		originalgetOntapClientFunc := getOntapClientFunc
		defer func() { getOntapClientFunc = originalgetOntapClientFunc }()

		expectedErr := errors.New("client creation failed")
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return nil, expectedErr
		}

		ontapRestProvider := &OntapRestProvider{}
		success, err := ontapRestProvider.UpdateJSwapMode("test-uuid", JSWAPBackingTypeEphemeralMemory)

		assert.Error(t, err)
		assert.Equal(t, expectedErr, err)
		assert.False(t, success)
	})

	t.Run("modify node API error", func(t *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockClusterClient := new(ontaprest.MockClusterClient)
		originalgetOntapClientFunc := getOntapClientFunc
		defer func() { getOntapClientFunc = originalgetOntapClientFunc }()

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}

		mockClient.On("Cluster").Return(mockClusterClient)

		expectedErr := errors.New("modify node failed")
		mockClusterClient.On("ModifyNode", context.Background(), mock.AnythingOfType("*ontap_rest.NodeModifyParams")).Return(nil, expectedErr)

		ontapRestProvider := &OntapRestProvider{}
		success, err := ontapRestProvider.UpdateJSwapMode("test-uuid", JSWAPBackingTypeEphemeralMemory)

		assert.Error(t, err)
		assert.Equal(t, expectedErr, err)
		assert.False(t, success)

		mockClient.AssertExpectations(t)
		mockClusterClient.AssertExpectations(t)
	})

	t.Run("job fails during polling", func(t *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockClusterClient := new(ontaprest.MockClusterClient)
		originalgetOntapClientFunc := getOntapClientFunc
		defer func() { getOntapClientFunc = originalgetOntapClientFunc }()

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}

		mockClient.On("Cluster").Return(mockClusterClient)

		targetNodeUUID := "test-node-uuid"
		backingType := JSWAPBackingTypeEphemeralMemory
		jobUUID := "test-job-uuid"

		// Mock successful ModifyNode call
		mockClusterClient.On("ModifyNode", context.Background(), mock.AnythingOfType("*ontap_rest.NodeModifyParams")).Return(&cluster.NodeModifyOK{
			Payload: &models.NodeJobLinkResponse{
				Job: &models.JobLink{
					UUID: nillable.ToPointer(strfmt.UUID(jobUUID)),
				},
			},
		}, nil)

		// Mock job failure
		mockClusterClient.On("GetJob", jobUUID).Return(&cluster.JobGetOK{
			Payload: &models.Job{
				UUID:    nillable.ToPointer(strfmt.UUID(jobUUID)),
				State:   nillable.ToPointer(models.JobStateFailure),
				Message: nillable.ToPointer("JSWAP operation failed"),
			},
		}, nil)

		ontapRestProvider := &OntapRestProvider{}
		success, err := ontapRestProvider.UpdateJSwapMode(targetNodeUUID, backingType)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "job failed: JSWAP operation failed")
		assert.False(t, success)

		mockClient.AssertExpectations(t)
		mockClusterClient.AssertExpectations(t)
	})
}

func TestTriggerTakeoverCheck_Success(t *testing.T) {
	t.Run("successful takeover check operation", func(t *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockClusterClient := new(ontaprest.MockClusterClient)
		originalgetOntapClientFunc := getOntapClientFunc
		defer func() { getOntapClientFunc = originalgetOntapClientFunc }()

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}

		mockClient.On("Cluster").Return(mockClusterClient)

		targetNodeUUID := "test-node-uuid"
		jobUUID := "test-job-uuid"

		// Mock the ModifyNode call for takeover check
		mockClusterClient.On("ModifyNode", context.Background(), mock.MatchedBy(func(params *ontaprest.NodeModifyParams) bool {
			return params.UUID == targetNodeUUID &&
				params.Action == NodeActionTakeoverCheck
		})).Return(&cluster.NodeModifyOK{
			Payload: &models.NodeJobLinkResponse{
				Job: &models.JobLink{
					UUID: nillable.ToPointer(strfmt.UUID(jobUUID)),
				},
			},
		}, nil)

		// Mock successful job completion
		mockClusterClient.On("GetJob", jobUUID).Return(&cluster.JobGetOK{
			Payload: &models.Job{
				UUID:    nillable.ToPointer(strfmt.UUID(jobUUID)),
				State:   nillable.ToPointer(models.JobStateSuccess),
				Message: nillable.ToPointer("Takeover check completed successfully"),
			},
		}, nil)

		ontapRestProvider := &OntapRestProvider{}
		success, err := ontapRestProvider.TriggerTakeoverCheck(targetNodeUUID)

		assert.NoError(t, err)
		assert.True(t, success)

		mockClient.AssertExpectations(t)
		mockClusterClient.AssertExpectations(t)
	})
}

func TestTriggerTakeoverCheck_Errors(t *testing.T) {
	t.Run("client creation error", func(t *testing.T) {
		originalgetOntapClientFunc := getOntapClientFunc
		defer func() { getOntapClientFunc = originalgetOntapClientFunc }()

		expectedErr := errors.New("client creation failed")
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return nil, expectedErr
		}

		ontapRestProvider := &OntapRestProvider{}
		success, err := ontapRestProvider.TriggerTakeoverCheck("test-uuid")

		assert.Error(t, err)
		assert.Equal(t, expectedErr, err)
		assert.False(t, success)
	})

	t.Run("modify node API error", func(t *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockClusterClient := new(ontaprest.MockClusterClient)
		originalgetOntapClientFunc := getOntapClientFunc
		defer func() { getOntapClientFunc = originalgetOntapClientFunc }()

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}

		mockClient.On("Cluster").Return(mockClusterClient)

		expectedErr := errors.New("modify node failed")
		mockClusterClient.On("ModifyNode", context.Background(), mock.AnythingOfType("*ontap_rest.NodeModifyParams")).Return(nil, expectedErr)

		ontapRestProvider := &OntapRestProvider{}
		success, err := ontapRestProvider.TriggerTakeoverCheck("test-uuid")

		assert.Error(t, err)
		assert.Equal(t, expectedErr, err)
		assert.False(t, success)

		mockClient.AssertExpectations(t)
		mockClusterClient.AssertExpectations(t)
	})

	t.Run("job fails during polling", func(t *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockClusterClient := new(ontaprest.MockClusterClient)
		originalgetOntapClientFunc := getOntapClientFunc
		defer func() { getOntapClientFunc = originalgetOntapClientFunc }()

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}

		mockClient.On("Cluster").Return(mockClusterClient)

		targetNodeUUID := "test-node-uuid"
		jobUUID := "test-job-uuid"

		// Mock successful ModifyNode call
		mockClusterClient.On("ModifyNode", context.Background(), mock.AnythingOfType("*ontap_rest.NodeModifyParams")).Return(&cluster.NodeModifyOK{
			Payload: &models.NodeJobLinkResponse{
				Job: &models.JobLink{
					UUID: nillable.ToPointer(strfmt.UUID(jobUUID)),
				},
			},
		}, nil)

		// Mock job failure
		mockClusterClient.On("GetJob", jobUUID).Return(&cluster.JobGetOK{
			Payload: &models.Job{
				UUID:    nillable.ToPointer(strfmt.UUID(jobUUID)),
				State:   nillable.ToPointer(models.JobStateFailure),
				Message: nillable.ToPointer("Takeover check operation failed"),
			},
		}, nil)

		ontapRestProvider := &OntapRestProvider{}
		success, err := ontapRestProvider.TriggerTakeoverCheck(targetNodeUUID)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "job failed: Takeover check operation failed")
		assert.False(t, success)

		mockClient.AssertExpectations(t)
		mockClusterClient.AssertExpectations(t)
	})
}

func withFastJobPollingInterval(t *testing.T) {
	t.Helper()
	orig := jobPollingInterval
	jobPollingInterval = time.Millisecond
	t.Cleanup(func() { jobPollingInterval = orig })
	// Clear env so getJobPollingInterval falls back to the package var.
	t.Setenv("JOB_POLLING_INTERVAL", "")
}

func TestPollJobUntilCompletion_Success(t *testing.T) {
	withFastJobPollingInterval(t)
	t.Run("job completes successfully on first poll", func(t *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockClusterClient := new(ontaprest.MockClusterClient)

		jobUUID := "test-job-uuid"

		mockClient.On("Cluster").Return(mockClusterClient)
		mockClusterClient.On("GetJob", jobUUID).Return(&cluster.JobGetOK{
			Payload: &models.Job{
				UUID:    nillable.ToPointer(strfmt.UUID(jobUUID)),
				State:   nillable.ToPointer(models.JobStateSuccess),
				Message: nillable.ToPointer("Operation completed successfully"),
			},
		}, nil)

		ontapRestProvider := &OntapRestProvider{}
		success, err := ontapRestProvider.pollJobUntilCompletion(mockClient, jobUUID)

		assert.NoError(t, err)
		assert.True(t, success)

		mockClient.AssertExpectations(t)
		mockClusterClient.AssertExpectations(t)
	})

	t.Run("job transitions from running to success", func(t *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockClusterClient := new(ontaprest.MockClusterClient)

		jobUUID := "test-job-uuid"

		mockClient.On("Cluster").Return(mockClusterClient)

		// First call - job is running
		mockClusterClient.On("GetJob", jobUUID).Return(&cluster.JobGetOK{
			Payload: &models.Job{
				UUID:    nillable.ToPointer(strfmt.UUID(jobUUID)),
				State:   nillable.ToPointer(models.JobStateRunning),
				Message: nillable.ToPointer("Operation in progress"),
			},
		}, nil).Once()

		// Second call - job is successful
		mockClusterClient.On("GetJob", jobUUID).Return(&cluster.JobGetOK{
			Payload: &models.Job{
				UUID:    nillable.ToPointer(strfmt.UUID(jobUUID)),
				State:   nillable.ToPointer(models.JobStateSuccess),
				Message: nillable.ToPointer("Operation completed successfully"),
			},
		}, nil).Once()

		ontapRestProvider := &OntapRestProvider{}
		success, err := ontapRestProvider.pollJobUntilCompletion(mockClient, jobUUID)

		assert.NoError(t, err)
		assert.True(t, success)

		mockClient.AssertExpectations(t)
		mockClusterClient.AssertExpectations(t)
	})
}

func TestPollJobUntilCompletion_Failure(t *testing.T) {
	withFastJobPollingInterval(t)
	t.Run("job fails", func(t *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockClusterClient := new(ontaprest.MockClusterClient)

		jobUUID := "test-job-uuid"

		mockClient.On("Cluster").Return(mockClusterClient)
		mockClusterClient.On("GetJob", jobUUID).Return(&cluster.JobGetOK{
			Payload: &models.Job{
				UUID:    nillable.ToPointer(strfmt.UUID(jobUUID)),
				State:   nillable.ToPointer(models.JobStateFailure),
				Message: nillable.ToPointer("Operation failed due to network issues"),
			},
		}, nil)

		ontapRestProvider := &OntapRestProvider{}
		success, err := ontapRestProvider.pollJobUntilCompletion(mockClient, jobUUID)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "job failed: Operation failed due to network issues")
		assert.False(t, success)

		mockClient.AssertExpectations(t)
		mockClusterClient.AssertExpectations(t)
	})

	t.Run("job state is nil", func(t *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockClusterClient := new(ontaprest.MockClusterClient)

		jobUUID := "test-job-uuid"

		mockClient.On("Cluster").Return(mockClusterClient)

		// First call - nil state
		mockClusterClient.On("GetJob", jobUUID).Return(&cluster.JobGetOK{
			Payload: &models.Job{
				UUID:  nillable.ToPointer(strfmt.UUID(jobUUID)),
				State: nil, // nil state
			},
		}, nil).Once()

		// Second call - success
		mockClusterClient.On("GetJob", jobUUID).Return(&cluster.JobGetOK{
			Payload: &models.Job{
				UUID:    nillable.ToPointer(strfmt.UUID(jobUUID)),
				State:   nillable.ToPointer(models.JobStateSuccess),
				Message: nillable.ToPointer("Operation completed successfully"),
			},
		}, nil).Once()

		ontapRestProvider := &OntapRestProvider{}
		success, err := ontapRestProvider.pollJobUntilCompletion(mockClient, jobUUID)

		assert.NoError(t, err)
		assert.True(t, success)

		mockClient.AssertExpectations(t)
		mockClusterClient.AssertExpectations(t)
	})

	t.Run("GetJob API error with retry", func(t *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockClusterClient := new(ontaprest.MockClusterClient)

		jobUUID := "test-job-uuid"

		mockClient.On("Cluster").Return(mockClusterClient)

		// First call - API error
		mockClusterClient.On("GetJob", jobUUID).Return(nil, errors.New("temporary network error")).Once()

		// Second call - success
		mockClusterClient.On("GetJob", jobUUID).Return(&cluster.JobGetOK{
			Payload: &models.Job{
				UUID:    nillable.ToPointer(strfmt.UUID(jobUUID)),
				State:   nillable.ToPointer(models.JobStateSuccess),
				Message: nillable.ToPointer("Operation completed successfully"),
			},
		}, nil).Once()

		ontapRestProvider := &OntapRestProvider{}
		success, err := ontapRestProvider.pollJobUntilCompletion(mockClient, jobUUID)

		assert.NoError(t, err)
		assert.True(t, success)

		mockClient.AssertExpectations(t)
		mockClusterClient.AssertExpectations(t)
	})
}

func TestPollJobUntilCompletion_VariousStates(t *testing.T) {
	withFastJobPollingInterval(t)
	testCases := []struct {
		name        string
		jobState    string
		expectRetry bool
	}{
		{"queued state", models.JobStateQueued, true},
		{"running state", models.JobStateRunning, true},
		{"paused state", models.JobStatePaused, true},
		{"unknown state", "unknown_state", true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockClient := new(ontaprest.MockRESTClient)
			mockClusterClient := new(ontaprest.MockClusterClient)

			jobUUID := "test-job-uuid"

			mockClient.On("Cluster").Return(mockClusterClient)

			// First call - return the test state
			mockClusterClient.On("GetJob", jobUUID).Return(&cluster.JobGetOK{
				Payload: &models.Job{
					UUID:    nillable.ToPointer(strfmt.UUID(jobUUID)),
					State:   nillable.ToPointer(tc.jobState),
					Message: nillable.ToPointer("In progress"),
				},
			}, nil).Once()

			// Second call - return success
			mockClusterClient.On("GetJob", jobUUID).Return(&cluster.JobGetOK{
				Payload: &models.Job{
					UUID:    nillable.ToPointer(strfmt.UUID(jobUUID)),
					State:   nillable.ToPointer(models.JobStateSuccess),
					Message: nillable.ToPointer("Operation completed successfully"),
				},
			}, nil).Once()

			ontapRestProvider := &OntapRestProvider{}
			success, err := ontapRestProvider.pollJobUntilCompletion(mockClient, jobUUID)

			assert.NoError(t, err)
			assert.True(t, success)

			mockClient.AssertExpectations(t)
			mockClusterClient.AssertExpectations(t)
		})
	}
}

func TestGetClusterHealthStatus_EdgeCases(t *testing.T) {
	t.Run("empty nodes list", func(t *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockClusterClient := new(ontaprest.MockClusterClient)
		originalgetOntapClientFunc := getOntapClientFunc
		defer func() { getOntapClientFunc = originalgetOntapClientFunc }()

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}

		mockClient.On("Cluster").Return(mockClusterClient)

		mockClusterClient.On("NodesGet", mock.AnythingOfType("*ontap_rest.NodesGetParams"), mock.AnythingOfType("ontap_rest.UserCallbackFunc[[]*github.com/vcp-vsa-control-Plane/vsa-control-plane/core/ontap-rest.Node]")).Return(nil).Run(func(args mock.Arguments) {
			callback := args.Get(1).(ontaprest.UserCallbackFunc[[]*ontaprest.Node])
			_ = callback([]*ontaprest.Node{}) // Empty slice, ignore callback error in test
		})

		ontapRestProvider := &OntapRestProvider{}
		result, err := ontapRestProvider.GetClusterHealthStatus()

		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, 0, result.NumRecords)
		assert.Empty(t, result.Records)

		mockClient.AssertExpectations(t)
		mockClusterClient.AssertExpectations(t)
	})

	t.Run("handles nil pointers in reasons slice", func(t *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockClusterClient := new(ontaprest.MockClusterClient)
		originalgetOntapClientFunc := getOntapClientFunc
		defer func() { getOntapClientFunc = originalgetOntapClientFunc }()

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}

		mockClient.On("Cluster").Return(mockClusterClient)

		mockClusterClient.On("NodesGet", mock.AnythingOfType("*ontap_rest.NodesGetParams"), mock.AnythingOfType("ontap_rest.UserCallbackFunc[[]*github.com/vcp-vsa-control-Plane/vsa-control-plane/core/ontap-rest.Node]")).Return(nil).Run(func(args mock.Arguments) {
			callback := args.Get(1).(ontaprest.UserCallbackFunc[[]*ontaprest.Node])

			nodes := []*ontaprest.Node{
				{
					NodeResponseInlineRecordsInlineArrayItem: models.NodeResponseInlineRecordsInlineArrayItem{
						UUID: nillable.ToPointer(strfmt.UUID("node-with-nil-reasons")),
						Name: nillable.ToPointer("node-nil-reasons"),
						Ha: &models.NodeResponseInlineRecordsInlineArrayItemInlineHa{
							TakeoverCheck: &models.NodeResponseInlineRecordsInlineArrayItemInlineHaInlineTakeoverCheck{
								TakeoverPossible: nillable.ToPointer(false),
								Reasons:          []*string{nil, nillable.ToPointer("Valid reason"), nil}, // Mixed nil/valid reasons
							},
						},
					},
				},
			}

			_ = callback(nodes) // Ignore callback error in test
		})

		ontapRestProvider := &OntapRestProvider{}
		result, err := ontapRestProvider.GetClusterHealthStatus()

		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, 1, result.NumRecords)

		node := result.Records[0]
		assert.Equal(t, "node-with-nil-reasons", node.UUID)
		assert.NotNil(t, node.Ha)
		assert.NotNil(t, node.Ha.TakeoverCheck)
		assert.False(t, node.Ha.TakeoverCheck.TakeoverPossible)
		assert.Len(t, node.Ha.TakeoverCheck.Reasons, 1) // Only one valid reason should be added
		assert.Equal(t, "Valid reason", node.Ha.TakeoverCheck.Reasons[0])

		mockClient.AssertExpectations(t)
		mockClusterClient.AssertExpectations(t)
	})
}

func TestGetJobPollingMaxDuration(t *testing.T) {
	t.Run("returns default duration when no environment variable is set", func(t *testing.T) {
		// Test the default value
		duration := getJobPollingMaxDuration()

		// Default should be 25 seconds
		assert.Equal(t, 25*time.Second, duration)
	})
}

func TestGetJobPollingInterval(t *testing.T) {
	t.Run("returns default interval when no environment variable is set", func(t *testing.T) {
		// Test the default value
		interval := getJobPollingInterval()

		// Default should be 3 seconds
		assert.Equal(t, 3*time.Second, interval)
	})
}

// TestEnvironmentVariableConfiguration tests the new configuration through environment variables
func TestEnvironmentVariableConfiguration(t *testing.T) {
	t.Run("getJobPollingMaxDuration with valid environment variable", func(t *testing.T) {
		// Set environment variable
		originalValue := os.Getenv("JOB_POLLING_MAX_DURATION")
		defer func() {
			if originalValue == "" {
				if err := os.Unsetenv("JOB_POLLING_MAX_DURATION"); err != nil {
					t.Errorf("Failed to unset JOB_POLLING_MAX_DURATION: %v", err)
				}
			} else {
				if err := os.Setenv("JOB_POLLING_MAX_DURATION", originalValue); err != nil {
					t.Errorf("Failed to restore JOB_POLLING_MAX_DURATION: %v", err)
				}
			}
		}()

		if err := os.Setenv("JOB_POLLING_MAX_DURATION", "60"); err != nil {
			t.Fatalf("Failed to set JOB_POLLING_MAX_DURATION: %v", err)
		}

		duration := getJobPollingMaxDuration()
		assert.Equal(t, 60*time.Second, duration)
	})

	t.Run("getJobPollingMaxDuration with invalid environment variable", func(t *testing.T) {
		// Set invalid environment variable
		originalValue := os.Getenv("JOB_POLLING_MAX_DURATION")
		defer func() {
			if originalValue == "" {
				if err := os.Unsetenv("JOB_POLLING_MAX_DURATION"); err != nil {
					t.Errorf("Failed to unset JOB_POLLING_MAX_DURATION: %v", err)
				}
			} else {
				if err := os.Setenv("JOB_POLLING_MAX_DURATION", originalValue); err != nil {
					t.Errorf("Failed to restore JOB_POLLING_MAX_DURATION: %v", err)
				}
			}
		}()

		if err := os.Setenv("JOB_POLLING_MAX_DURATION", "invalid"); err != nil {
			t.Fatalf("Failed to set JOB_POLLING_MAX_DURATION: %v", err)
		}

		duration := getJobPollingMaxDuration()
		assert.Equal(t, 25*time.Second, duration) // Should return default
	})

	t.Run("getJobPollingInterval with valid environment variable", func(t *testing.T) {
		// Set environment variable
		originalValue := os.Getenv("JOB_POLLING_INTERVAL")
		defer func() {
			if originalValue == "" {
				if err := os.Unsetenv("JOB_POLLING_INTERVAL"); err != nil {
					t.Errorf("Failed to unset JOB_POLLING_INTERVAL: %v", err)
				}
			} else {
				if err := os.Setenv("JOB_POLLING_INTERVAL", originalValue); err != nil {
					t.Errorf("Failed to restore JOB_POLLING_INTERVAL: %v", err)
				}
			}
		}()

		if err := os.Setenv("JOB_POLLING_INTERVAL", "5"); err != nil {
			t.Fatalf("Failed to set JOB_POLLING_INTERVAL: %v", err)
		}

		interval := getJobPollingInterval()
		assert.Equal(t, 5*time.Second, interval)
	})

	t.Run("getJobPollingInterval with zero value falls back to default", func(t *testing.T) {
		// Set environment variable to zero
		originalValue := os.Getenv("JOB_POLLING_INTERVAL")
		defer func() {
			if originalValue == "" {
				if err := os.Unsetenv("JOB_POLLING_INTERVAL"); err != nil {
					t.Errorf("Failed to unset JOB_POLLING_INTERVAL: %v", err)
				}
			} else {
				if err := os.Setenv("JOB_POLLING_INTERVAL", originalValue); err != nil {
					t.Errorf("Failed to restore JOB_POLLING_INTERVAL: %v", err)
				}
			}
		}()

		if err := os.Setenv("JOB_POLLING_INTERVAL", "0"); err != nil {
			t.Fatalf("Failed to set JOB_POLLING_INTERVAL: %v", err)
		}

		interval := getJobPollingInterval()
		assert.Equal(t, 3*time.Second, interval) // Should return default
	})
}

// TestClientReusePattern tests the new client reuse functionality
func TestClientReusePattern(t *testing.T) {
	t.Run("GetClusterHealthStatusWithClient reuses provided client", func(t *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockClusterClient := new(ontaprest.MockClusterClient)

		mockClient.On("Cluster").Return(mockClusterClient)

		// Mock the NodesGet call
		mockClusterClient.On("NodesGet", mock.AnythingOfType("*ontap_rest.NodesGetParams"), mock.AnythingOfType("ontap_rest.UserCallbackFunc[[]*github.com/vcp-vsa-control-Plane/vsa-control-plane/core/ontap-rest.Node]")).Return(nil).Run(func(args mock.Arguments) {
			callback := args.Get(1).(ontaprest.UserCallbackFunc[[]*ontaprest.Node])

			nodes := []*ontaprest.Node{
				{
					NodeResponseInlineRecordsInlineArrayItem: models.NodeResponseInlineRecordsInlineArrayItem{
						UUID: nillable.ToPointer(strfmt.UUID("reuse-test-node")),
						Name: nillable.ToPointer("reuse-test"),
					},
				},
			}

			_ = callback(nodes) // Ignore callback error in test
		})

		ontapRestProvider := &OntapRestProvider{}
		result, err := ontapRestProvider.GetClusterHealthStatusWithClient(mockClient)

		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, 1, result.NumRecords)
		assert.Equal(t, "reuse-test-node", result.Records[0].UUID)

		mockClient.AssertExpectations(t)
		mockClusterClient.AssertExpectations(t)
	})

	t.Run("UpdateJSwapModeWithClient reuses provided client", func(t *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockClusterClient := new(ontaprest.MockClusterClient)

		targetNodeUUID := "reuse-node-uuid"
		backingType := JSWAPBackingTypeEphemeralMemory
		jobUUID := "reuse-job-uuid"

		mockClient.On("Cluster").Return(mockClusterClient)

		// Mock the ModifyNode call
		mockClusterClient.On("ModifyNode", context.Background(), mock.MatchedBy(func(params *ontaprest.NodeModifyParams) bool {
			return params.UUID == targetNodeUUID &&
				params.Body.NVLog.BackingType == string(backingType)
		})).Return(&cluster.NodeModifyOK{
			Payload: &models.NodeJobLinkResponse{
				Job: &models.JobLink{
					UUID: nillable.ToPointer(strfmt.UUID(jobUUID)),
				},
			},
		}, nil)

		// Mock successful job completion
		mockClusterClient.On("GetJob", jobUUID).Return(&cluster.JobGetOK{
			Payload: &models.Job{
				UUID:    nillable.ToPointer(strfmt.UUID(jobUUID)),
				State:   nillable.ToPointer(models.JobStateSuccess),
				Message: nillable.ToPointer("JSWAP completed successfully"),
			},
		}, nil)

		ontapRestProvider := &OntapRestProvider{}
		success, err := ontapRestProvider.UpdateJSwapModeWithClient(targetNodeUUID, backingType, mockClient)

		assert.NoError(t, err)
		assert.True(t, success)

		mockClient.AssertExpectations(t)
		mockClusterClient.AssertExpectations(t)
	})

	t.Run("TriggerTakeoverCheckWithClient reuses provided client", func(t *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockClusterClient := new(ontaprest.MockClusterClient)

		targetNodeUUID := "reuse-takeover-node-uuid"
		jobUUID := "reuse-takeover-job-uuid"

		mockClient.On("Cluster").Return(mockClusterClient)

		// Mock the ModifyNode call for takeover check
		mockClusterClient.On("ModifyNode", context.Background(), mock.MatchedBy(func(params *ontaprest.NodeModifyParams) bool {
			return params.UUID == targetNodeUUID &&
				params.Action == NodeActionTakeoverCheck
		})).Return(&cluster.NodeModifyOK{
			Payload: &models.NodeJobLinkResponse{
				Job: &models.JobLink{
					UUID: nillable.ToPointer(strfmt.UUID(jobUUID)),
				},
			},
		}, nil)

		// Mock successful job completion
		mockClusterClient.On("GetJob", jobUUID).Return(&cluster.JobGetOK{
			Payload: &models.Job{
				UUID:    nillable.ToPointer(strfmt.UUID(jobUUID)),
				State:   nillable.ToPointer(models.JobStateSuccess),
				Message: nillable.ToPointer("Takeover check completed successfully"),
			},
		}, nil)

		ontapRestProvider := &OntapRestProvider{}
		success, err := ontapRestProvider.TriggerTakeoverCheckWithClient(targetNodeUUID, mockClient)

		assert.NoError(t, err)
		assert.True(t, success)

		mockClient.AssertExpectations(t)
		mockClusterClient.AssertExpectations(t)
	})
}

// TestTimeoutAndContextManagement tests timeout handling and context management
func TestTimeoutAndContextManagement(t *testing.T) {
	t.Run("job polling respects timeout configuration", func(t *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockClusterClient := new(ontaprest.MockClusterClient)

		// Short, sub-second knobs so the timeout path is exercised quickly.
		origInterval, origMax := jobPollingInterval, jobPollingMaxDuration
		jobPollingInterval = time.Millisecond
		jobPollingMaxDuration = 50 * time.Millisecond
		t.Cleanup(func() {
			jobPollingInterval = origInterval
			jobPollingMaxDuration = origMax
		})
		t.Setenv("JOB_POLLING_INTERVAL", "")
		t.Setenv("JOB_POLLING_MAX_DURATION", "")

		jobUUID := "timeout-test-job-uuid"
		mockClient.On("Cluster").Return(mockClusterClient)

		// Mock job that keeps running (simulating timeout scenario)
		mockClusterClient.On("GetJob", jobUUID).Return(&cluster.JobGetOK{
			Payload: &models.Job{
				UUID:    nillable.ToPointer(strfmt.UUID(jobUUID)),
				State:   nillable.ToPointer(models.JobStateRunning),
				Message: nillable.ToPointer("Long running operation"),
			},
		}, nil)

		ontapRestProvider := &OntapRestProvider{}
		success, err := ontapRestProvider.pollJobUntilCompletion(mockClient, jobUUID)

		// Should timeout and return error
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "timeout")
		assert.False(t, success)

		mockClient.AssertExpectations(t)
		mockClusterClient.AssertExpectations(t)
	})

	t.Run("job polling uses configured interval", func(t *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockClusterClient := new(ontaprest.MockClusterClient)

		// Set custom polling interval
		originalValue := os.Getenv("JOB_POLLING_INTERVAL")
		defer func() {
			if originalValue == "" {
				if err := os.Unsetenv("JOB_POLLING_INTERVAL"); err != nil {
					t.Errorf("Failed to unset JOB_POLLING_INTERVAL: %v", err)
				}
			} else {
				if err := os.Setenv("JOB_POLLING_INTERVAL", originalValue); err != nil {
					t.Errorf("Failed to restore JOB_POLLING_INTERVAL: %v", err)
				}
			}
		}()
		if err := os.Setenv("JOB_POLLING_INTERVAL", "1"); err != nil {
			t.Fatalf("Failed to set JOB_POLLING_INTERVAL: %v", err)
		}

		jobUUID := "interval-test-job-uuid"
		mockClient.On("Cluster").Return(mockClusterClient)

		// First call - running
		mockClusterClient.On("GetJob", jobUUID).Return(&cluster.JobGetOK{
			Payload: &models.Job{
				UUID:    nillable.ToPointer(strfmt.UUID(jobUUID)),
				State:   nillable.ToPointer(models.JobStateRunning),
				Message: nillable.ToPointer("Still running"),
			},
		}, nil).Once()

		// Second call - success (after interval)
		mockClusterClient.On("GetJob", jobUUID).Return(&cluster.JobGetOK{
			Payload: &models.Job{
				UUID:    nillable.ToPointer(strfmt.UUID(jobUUID)),
				State:   nillable.ToPointer(models.JobStateSuccess),
				Message: nillable.ToPointer("Completed"),
			},
		}, nil).Once()

		start := time.Now()
		ontapRestProvider := &OntapRestProvider{}
		success, err := ontapRestProvider.pollJobUntilCompletion(mockClient, jobUUID)
		elapsed := time.Since(start)

		assert.NoError(t, err)
		assert.True(t, success)
		// Should have waited at least the polling interval
		assert.True(t, elapsed >= time.Second)

		mockClient.AssertExpectations(t)
		mockClusterClient.AssertExpectations(t)
	})
}

// TestResourceManagement tests resource management and cleanup patterns
func TestResourceManagement(t *testing.T) {
	t.Run("multiple client operations with proper resource management", func(t *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockClusterClient := new(ontaprest.MockClusterClient)

		mockClient.On("Cluster").Return(mockClusterClient)

		// Simulate multiple operations using the same client instance
		nodeUUID := "resource-test-node"
		jobUUID := "resource-test-job"

		// Operation 1: Get cluster health
		mockClusterClient.On("NodesGet", mock.AnythingOfType("*ontap_rest.NodesGetParams"), mock.AnythingOfType("ontap_rest.UserCallbackFunc[[]*github.com/vcp-vsa-control-Plane/vsa-control-plane/core/ontap-rest.Node]")).Return(nil).Run(func(args mock.Arguments) {
			callback := args.Get(1).(ontaprest.UserCallbackFunc[[]*ontaprest.Node])
			nodes := []*ontaprest.Node{
				{
					NodeResponseInlineRecordsInlineArrayItem: models.NodeResponseInlineRecordsInlineArrayItem{
						UUID: nillable.ToPointer(strfmt.UUID(nodeUUID)),
						Name: nillable.ToPointer("resource-test"),
					},
				},
			}
			_ = callback(nodes)
		}).Once()

		// Operation 2: Trigger takeover check
		mockClusterClient.On("ModifyNode", context.Background(), mock.MatchedBy(func(params *ontaprest.NodeModifyParams) bool {
			return params.UUID == nodeUUID && params.Action == NodeActionTakeoverCheck
		})).Return(&cluster.NodeModifyOK{
			Payload: &models.NodeJobLinkResponse{
				Job: &models.JobLink{
					UUID: nillable.ToPointer(strfmt.UUID(jobUUID)),
				},
			},
		}, nil).Once()

		// Operation 3: Job polling for takeover check
		mockClusterClient.On("GetJob", jobUUID).Return(&cluster.JobGetOK{
			Payload: &models.Job{
				UUID:    nillable.ToPointer(strfmt.UUID(jobUUID)),
				State:   nillable.ToPointer(models.JobStateSuccess),
				Message: nillable.ToPointer("Takeover check completed"),
			},
		}, nil).Once()

		// Operation 4: JSWAP operation
		mockClusterClient.On("ModifyNode", context.Background(), mock.MatchedBy(func(params *ontaprest.NodeModifyParams) bool {
			return params.UUID == nodeUUID && params.Body.NVLog.BackingType == string(JSWAPBackingTypeEphemeralDisk)
		})).Return(&cluster.NodeModifyOK{
			Payload: &models.NodeJobLinkResponse{
				Job: &models.JobLink{
					UUID: nillable.ToPointer(strfmt.UUID(jobUUID + "_jswap")),
				},
			},
		}, nil).Once()

		// Operation 5: Job polling for JSWAP
		mockClusterClient.On("GetJob", jobUUID+"_jswap").Return(&cluster.JobGetOK{
			Payload: &models.Job{
				UUID:    nillable.ToPointer(strfmt.UUID(jobUUID + "_jswap")),
				State:   nillable.ToPointer(models.JobStateSuccess),
				Message: nillable.ToPointer("JSWAP completed"),
			},
		}, nil).Once()

		ontapRestProvider := &OntapRestProvider{}

		// Execute multiple operations with the same client
		_, err1 := ontapRestProvider.GetClusterHealthStatusWithClient(mockClient)
		assert.NoError(t, err1)

		_, err2 := ontapRestProvider.TriggerTakeoverCheckWithClient(nodeUUID, mockClient)
		assert.NoError(t, err2)

		_, err3 := ontapRestProvider.UpdateJSwapModeWithClient(nodeUUID, JSWAPBackingTypeEphemeralDisk, mockClient)
		assert.NoError(t, err3)

		// Verify all operations succeeded and client was reused properly
		mockClient.AssertExpectations(t)
		mockClusterClient.AssertExpectations(t)
	})
}

package workflows

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func volumeRefreshWorkflowInputFromAccount(account *datamodel.Account, volumeIDs []string) VolumeRefreshWorkflowInput {
	if account == nil {
		return VolumeRefreshWorkflowInput{VolumeIDs: volumeIDs}
	}
	return VolumeRefreshWorkflowInput{
		AccountUUID: account.UUID,
		AccountName: account.Name,
		AccountID:   account.ID,
		VolumeIDs:   volumeIDs,
	}
}

func volumeIDsFromVolumes(volumes []*datamodel.Volume) []string {
	ids := make([]string, 0, len(volumes))
	for _, volume := range volumes {
		if volume != nil {
			ids = append(ids, volume.UUID)
		}
	}
	return ids
}

func TestVolumeRefreshWorkflow_InvalidActivityTimeoutUsesDefault(t *testing.T) {
	originalTimeout := VolumeRefreshWorkflowActivityTimeout
	VolumeRefreshWorkflowActivityTimeout = "invalid"
	t.Cleanup(func() {
		VolumeRefreshWorkflowActivityTimeout = originalTimeout
	})

	var ts testsuite.WorkflowTestSuite
	testEnv := ts.NewTestWorkflowEnvironment()
	volumeRefreshActivity := &activities.VolumeRefreshActivity{}
	testEnv.RegisterWorkflow(VolumeRefreshWorkflow)
	testEnv.RegisterActivity(volumeRefreshActivity.GetVolumesForRefresh)
	testEnv.OnActivity(
		volumeRefreshActivity.GetVolumesForRefresh,
		mock.Anything,
		mock.Anything,
	).Return([]*datamodel.Volume{}, nil)

	testEnv.ExecuteWorkflow(VolumeRefreshWorkflow, VolumeRefreshWorkflowInput{
		AccountName: "test-account",
		AccountID:   1,
		VolumeIDs:   []string{"volume-uuid"},
	})

	assert.True(t, testEnv.IsWorkflowCompleted())
	assert.NoError(t, testEnv.GetWorkflowError())
}

type VolumeGetWorkflowTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite

	env *testsuite.TestWorkflowEnvironment
}

func (s *VolumeGetWorkflowTestSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()
	s.env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
	mockHeader := &commonpb.Header{
		Fields: map[string]*commonpb.Payload{
			"logParam": encodedValue,
		},
	}
	s.env.SetHeader(mockHeader)

	// Register workflow
	s.env.RegisterWorkflow(VolumeRefreshWorkflow)

	// Register the new activities used in VolumeRefreshWorkflow
	volumeRefreshActivity := &activities.VolumeRefreshActivity{}
	s.env.RegisterActivity(volumeRefreshActivity.GetVolumesForRefresh)
	s.env.RegisterActivity(volumeRefreshActivity.ProcessVolumePoolMapping)
	s.env.RegisterActivity(volumeRefreshActivity.GetOntapVolumes)
	s.env.RegisterActivity(volumeRefreshActivity.ProcessOntapVolumeMatching)
	s.env.RegisterActivity(volumeRefreshActivity.SyncUpdatedVolumesToDatabase)
	s.env.RegisterActivity(volumeRefreshActivity.UpdateAccountVolumeRefreshTimestamp)
}

func (s *VolumeGetWorkflowTestSuite) AfterTest() {
	s.env.AssertExpectations(s.T())
}

func (s *VolumeGetWorkflowTestSuite) Test_GetVolumeWorkflow_Success() {
	// Test successful volume refresh workflow execution

	// Create test volume
	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{
			UUID: "test-volume-uuid",
		},
		Name: "test-volume",
		Account: &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "account-uuid", ID: 1},
			Name:      "test-account",
		},
		Pool: &datamodel.Pool{
			BaseModel: datamodel.BaseModel{
				ID:   int64(1),
				UUID: "pool-uuid",
			},
			Name: "test-pool",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID: "external-uuid",
		},
	}

	// Mock ProcessVolumePoolMapping activity
	poolMappingResult := &activities.ProcessVolumePoolMappingResult{
		PoolByUUID: map[string]*datamodel.Pool{
			"pool-uuid": volume.Pool,
		},
		PoolUUIDs: []string{"pool-uuid"},
	}
	s.env.OnActivity("ProcessVolumePoolMapping", mock.Anything, mock.Anything).Return(poolMappingResult, nil)

	// Mock GetOntapVolumes activity
	ontapResult := &activities.GetOntapVolumesReturnValue{
		OntapVolumeMap: map[string]*vsa.Volume{
			"external-uuid": {
				Volume: models.Volume{
					Space: &models.VolumeInlineSpace{
						LogicalSpace: &models.VolumeInlineSpaceInlineLogicalSpace{
							Used: nillable.ToPointer(int64(2048)),
						},
					},
				},
			},
		},
	}
	s.env.OnActivity("GetOntapVolumes", mock.Anything, mock.Anything).Return(ontapResult, nil)

	// Mock ProcessOntapVolumeMatching activity
	matchingResult := &activities.ProcessOntapVolumeMatchingResult{
		UpdatedVolumeByUUID: map[string]*datamodel.Volume{
			"test-volume-uuid": {
				BaseModel: datamodel.BaseModel{
					UUID: "test-volume-uuid",
					ID:   volume.ID,
				},
				UsedBytes: 2048,
			},
		},
		OntapVolResponse: map[string]*vsa.VolumeResponse{
			"test-volume-uuid": {
				UsedBytes: 2048,
			},
		},
		VolumesNotFoundInONTAP: []*datamodel.Volume{},
		MatchedCount:           1,
		NotFoundCount:          0,
	}
	s.env.OnActivity("ProcessOntapVolumeMatching", mock.Anything, mock.Anything).Return(matchingResult, nil)

	// Mock SyncUpdatedVolumesToDatabase activity
	s.env.OnActivity("SyncUpdatedVolumesToDatabase", mock.Anything, mock.Anything).Return(nil)

	// Mock UpdateAccountVolumeRefreshTimestamp activity
	s.env.OnActivity("UpdateAccountVolumeRefreshTimestamp", mock.Anything, mock.Anything).Return(nil)

	// Execute workflow
	s.env.OnActivity("GetVolumesForRefresh", mock.Anything, mock.Anything).Return([]*datamodel.Volume{volume}, nil)
	s.env.ExecuteWorkflow(VolumeRefreshWorkflow, volumeRefreshWorkflowInputFromAccount([]*datamodel.Volume{volume}[0].Account, volumeIDsFromVolumes([]*datamodel.Volume{volume})))

	// Assert workflow completed successfully
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
}

func (s *VolumeGetWorkflowTestSuite) Test_GetVolumeWorkflow_StatusQuery() {
	// Test status query handler functionality during workflow execution

	// Create test volume
	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{
			UUID: "test-volume-uuid",
		},
		Name: "test-volume",
		Account: &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "account-uuid", ID: 1},
			Name:      "test-account",
		},
		Pool: &datamodel.Pool{
			BaseModel: datamodel.BaseModel{
				ID:   int64(1),
				UUID: "pool-uuid",
			},
			Name: "test-pool",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID: "external-uuid",
		},
	}

	// Mock ProcessVolumePoolMapping activity with a delay to allow querying
	poolMappingResult := &activities.ProcessVolumePoolMappingResult{
		PoolByUUID: map[string]*datamodel.Pool{
			"pool-uuid": volume.Pool,
		},
		PoolUUIDs: []string{"pool-uuid"},
	}
	s.env.OnActivity("ProcessVolumePoolMapping", mock.Anything, mock.Anything).Return(poolMappingResult, nil)

	// Mock remaining activities
	ontapResult := &activities.GetOntapVolumesReturnValue{
		OntapVolumeMap: map[string]*vsa.Volume{
			"external-uuid": {
				Volume: models.Volume{
					Space: &models.VolumeInlineSpace{
						LogicalSpace: &models.VolumeInlineSpaceInlineLogicalSpace{
							Used: nillable.ToPointer(int64(2048)),
						},
					},
				},
			},
		},
	}
	s.env.OnActivity("GetOntapVolumes", mock.Anything, mock.Anything).Return(ontapResult, nil)

	matchingResult := &activities.ProcessOntapVolumeMatchingResult{
		UpdatedVolumeByUUID: map[string]*datamodel.Volume{
			"test-volume-uuid": {
				BaseModel: datamodel.BaseModel{
					UUID: "test-volume-uuid",
					ID:   volume.ID,
				},
				UsedBytes: 2048,
			},
		},
		OntapVolResponse: map[string]*vsa.VolumeResponse{
			"test-volume-uuid": {
				UsedBytes: 2048,
			},
		},
		VolumesNotFoundInONTAP: []*datamodel.Volume{},
		MatchedCount:           1,
		NotFoundCount:          0,
	}
	s.env.OnActivity("ProcessOntapVolumeMatching", mock.Anything, mock.Anything).Return(matchingResult, nil)
	s.env.OnActivity("SyncUpdatedVolumesToDatabase", mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity("UpdateAccountVolumeRefreshTimestamp", mock.Anything, mock.Anything).Return(nil)

	// Execute workflow
	s.env.OnActivity("GetVolumesForRefresh", mock.Anything, mock.Anything).Return([]*datamodel.Volume{volume}, nil)
	s.env.ExecuteWorkflow(VolumeRefreshWorkflow, volumeRefreshWorkflowInputFromAccount([]*datamodel.Volume{volume}[0].Account, volumeIDsFromVolumes([]*datamodel.Volume{volume})))

	// Query workflow status to test the status query handler
	var statusResult *VolumeRefreshWorkflowStatus
	value, err := s.env.QueryWorkflow("status")
	assert.NoError(s.T(), err)
	err = value.Get(&statusResult)
	assert.NoError(s.T(), err)

	// Assert status query returns expected structure
	assert.NotNil(s.T(), statusResult)
	assert.NotNil(s.T(), statusResult.WorkflowStatus)
	assert.Equal(s.T(), "test-account", statusResult.WorkflowStatus.CustomerID)
	assert.NotNil(s.T(), statusResult.CompletionTime)

	// Assert workflow completed successfully
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
}

func (s *VolumeGetWorkflowTestSuite) Test_GetVolumeWorkflow_NilVolumes() {
	// Test VolumeRefreshWorkflow with empty volume IDs
	s.env.ExecuteWorkflow(VolumeRefreshWorkflow, VolumeRefreshWorkflowInput{})

	// Assert workflow failed with appropriate error
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
	assert.Contains(s.T(), s.env.GetWorkflowError().Error(), "no volumes provided")
}

func (s *VolumeGetWorkflowTestSuite) Test_GetVolumeWorkflow_EmptyVolumes() {
	// Test VolumeRefreshWorkflow with empty volumes slice
	s.env.ExecuteWorkflow(VolumeRefreshWorkflow, VolumeRefreshWorkflowInput{AccountName: "test-account"})

	// Assert workflow failed with appropriate error
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
	assert.Contains(s.T(), s.env.GetWorkflowError().Error(), "no volumes provided")
}

func (s *VolumeGetWorkflowTestSuite) Test_GetVolumeWorkflow_SetupError() {
	// Test workflow setup error - volume with no account
	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{
			UUID: "test-volume-uuid",
		},
		Name:    "test-volume",
		Account: nil, // This will cause setup to fail
	}

	// Execute workflow
	s.env.ExecuteWorkflow(VolumeRefreshWorkflow, volumeRefreshWorkflowInputFromAccount(volume.Account, []string{volume.UUID}))

	// Assert workflow failed
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
	assert.Contains(s.T(), s.env.GetWorkflowError().Error(), "account name is required")
}

func (s *VolumeGetWorkflowTestSuite) Test_GetVolumeWorkflow_NoValidPools() {
	// Test when ProcessVolumePoolMapping returns no valid pools
	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{
			UUID: "test-volume-uuid",
		},
		Name: "test-volume",
		Account: &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "account-uuid", ID: 1},
			Name:      "test-account",
		},
		Pool: &datamodel.Pool{
			BaseModel: datamodel.BaseModel{
				ID:   int64(1),
				UUID: "pool-uuid",
			},
			Name: "test-pool",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID: "external-uuid",
		},
	}

	// Mock ProcessVolumePoolMapping to return empty pool mapping
	poolMappingResult := &activities.ProcessVolumePoolMappingResult{
		PoolByUUID: map[string]*datamodel.Pool{}, // Empty map
		PoolUUIDs:  []string{},                   // Empty slice
	}
	s.env.OnActivity("ProcessVolumePoolMapping", mock.Anything, mock.Anything).Return(poolMappingResult, nil)

	// Execute workflow
	s.env.OnActivity("GetVolumesForRefresh", mock.Anything, mock.Anything).Return([]*datamodel.Volume{volume}, nil)
	s.env.ExecuteWorkflow(VolumeRefreshWorkflow, volumeRefreshWorkflowInputFromAccount([]*datamodel.Volume{volume}[0].Account, volumeIDsFromVolumes([]*datamodel.Volume{volume})))

	// Assert workflow completed successfully (returns early with nil)
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
}

func (s *VolumeGetWorkflowTestSuite) Test_GetVolumeWorkflow_ProcessPoolMappingError() {
	// Test ProcessVolumePoolMapping activity failure
	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{
			UUID: "test-volume-uuid",
		},
		Name: "test-volume",
		Account: &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "account-uuid", ID: 1},
			Name:      "test-account",
		},
		Pool: &datamodel.Pool{
			BaseModel: datamodel.BaseModel{
				ID:   int64(1),
				UUID: "pool-uuid",
			},
			Name: "test-pool",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID: "external-uuid",
		},
	}

	// Mock ProcessVolumePoolMapping to fail
	s.env.OnActivity("ProcessVolumePoolMapping", mock.Anything, mock.Anything).Return(nil, errors.New("failed to process pool mapping"))

	// Execute workflow
	s.env.OnActivity("GetVolumesForRefresh", mock.Anything, mock.Anything).Return([]*datamodel.Volume{volume}, nil)
	s.env.ExecuteWorkflow(VolumeRefreshWorkflow, volumeRefreshWorkflowInputFromAccount([]*datamodel.Volume{volume}[0].Account, volumeIDsFromVolumes([]*datamodel.Volume{volume})))

	// Assert workflow failed
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
	assert.Contains(s.T(), s.env.GetWorkflowError().Error(), "failed to process pool mapping")
}

func (s *VolumeGetWorkflowTestSuite) Test_GetVolumeWorkflow_GetOntapVolumesError() {
	// Test GetOntapVolumes activity failure - workflow should continue processing but skip the failed pool
	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{
			UUID: "test-volume-uuid",
		},
		Name: "test-volume",
		Account: &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "account-uuid", ID: 1},
			Name:      "test-account",
		},
		Pool: &datamodel.Pool{
			BaseModel: datamodel.BaseModel{
				ID:   int64(1),
				UUID: "pool-uuid",
			},
			Name: "test-pool",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID: "external-uuid",
		},
	}

	// Mock ProcessVolumePoolMapping to succeed
	poolMappingResult := &activities.ProcessVolumePoolMappingResult{
		PoolByUUID: map[string]*datamodel.Pool{
			"pool-uuid": volume.Pool,
		},
		PoolUUIDs: []string{"pool-uuid"},
	}
	s.env.OnActivity("ProcessVolumePoolMapping", mock.Anything, mock.Anything).Return(poolMappingResult, nil)

	// Mock GetOntapVolumes to fail
	s.env.OnActivity("GetOntapVolumes", mock.Anything, mock.Anything).Return(nil, errors.New("failed to get ONTAP volumes"))

	// Mock ProcessOntapVolumeMatching to handle empty ONTAP results
	// Since GetOntapVolumes failed, the volume will be marked as not found in ONTAP
	matchingResult := &activities.ProcessOntapVolumeMatchingResult{
		UpdatedVolumeByUUID:    map[string]*datamodel.Volume{},
		OntapVolResponse:       map[string]*vsa.VolumeResponse{},
		VolumesNotFoundInONTAP: []*datamodel.Volume{volume}, // Volume will be marked as not found
		MatchedCount:           0,
		NotFoundCount:          1,
	}
	s.env.OnActivity("ProcessOntapVolumeMatching", mock.Anything, mock.Anything).Return(matchingResult, nil)

	// Execute workflow
	s.env.OnActivity("GetVolumesForRefresh", mock.Anything, mock.Anything).Return([]*datamodel.Volume{volume}, nil)
	s.env.ExecuteWorkflow(VolumeRefreshWorkflow, volumeRefreshWorkflowInputFromAccount([]*datamodel.Volume{volume}[0].Account, volumeIDsFromVolumes([]*datamodel.Volume{volume})))

	// Assert workflow completed successfully (resilient behavior - continues despite GetOntapVolumes failure)
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
}

func (s *VolumeGetWorkflowTestSuite) Test_GetVolumeWorkflow_PartialGetOntapVolumesFailure() {
	// Test mixed success/failure scenario - some pools succeed, one fails
	// This verifies the resilience behavior where workflow continues despite partial failures

	// Create pools
	poolA := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{ID: int64(1), UUID: "pool-a-uuid"},
		Name:      "pool-a",
	}
	poolB := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{ID: int64(2), UUID: "pool-b-uuid"},
		Name:      "pool-b",
	}
	poolC := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{ID: int64(3), UUID: "pool-c-uuid"},
		Name:      "pool-c",
	}

	// Create volumes - 2 per pool (6 total)
	volumeA1 := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "vol-a1-uuid"},
		Name:      "volume-a1",
		UsedBytes: 1024,
		Account: &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "account-uuid", ID: 1},
			Name:      "test-account",
		},
		Pool:             poolA,
		VolumeAttributes: &datamodel.VolumeAttributes{ExternalUUID: "external-a1"},
	}
	volumeA2 := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "vol-a2-uuid"},
		Name:      "volume-a2",
		UsedBytes: 1024,
		Account: &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "account-uuid", ID: 1},
			Name:      "test-account",
		},
		Pool:             poolA,
		VolumeAttributes: &datamodel.VolumeAttributes{ExternalUUID: "external-a2"},
	}
	volumeB1 := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "vol-b1-uuid"},
		Name:      "volume-b1",
		UsedBytes: 1024,
		Account: &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "account-uuid", ID: 1},
			Name:      "test-account",
		},
		Pool:             poolB,
		VolumeAttributes: &datamodel.VolumeAttributes{ExternalUUID: "external-b1"},
	}
	volumeB2 := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "vol-b2-uuid"},
		Name:      "volume-b2",
		UsedBytes: 1024,
		Account: &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "account-uuid", ID: 1},
			Name:      "test-account",
		},
		Pool:             poolB,
		VolumeAttributes: &datamodel.VolumeAttributes{ExternalUUID: "external-b2"},
	}
	volumeC1 := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "vol-c1-uuid"},
		Name:      "volume-c1",
		UsedBytes: 1024,
		Account: &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "account-uuid", ID: 1},
			Name:      "test-account",
		},
		Pool:             poolC,
		VolumeAttributes: &datamodel.VolumeAttributes{ExternalUUID: "external-c1"},
	}
	volumeC2 := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "vol-c2-uuid"},
		Name:      "volume-c2",
		UsedBytes: 1024,
		Account: &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "account-uuid", ID: 1},
			Name:      "test-account",
		},
		Pool:             poolC,
		VolumeAttributes: &datamodel.VolumeAttributes{ExternalUUID: "external-c2"},
	}

	allVolumes := []*datamodel.Volume{volumeA1, volumeA2, volumeB1, volumeB2, volumeC1, volumeC2}

	// Mock ProcessVolumePoolMapping to return all 3 pools
	poolMappingResult := &activities.ProcessVolumePoolMappingResult{
		PoolByUUID: map[string]*datamodel.Pool{
			"pool-a-uuid": poolA,
			"pool-b-uuid": poolB,
			"pool-c-uuid": poolC,
		},
		PoolUUIDs: []string{"pool-a-uuid", "pool-b-uuid", "pool-c-uuid"},
	}
	s.env.OnActivity("ProcessVolumePoolMapping", mock.Anything, mock.Anything).Return(poolMappingResult, nil)

	// Mock GetOntapVolumes - Pool A succeeds, Pool B fails, Pool C succeeds
	// Pool A success
	s.env.OnActivity("GetOntapVolumes", mock.Anything, poolA).Return(&activities.GetOntapVolumesReturnValue{
		OntapVolumeMap: map[string]*vsa.Volume{
			"external-a1": {
				Volume: models.Volume{
					Space: &models.VolumeInlineSpace{
						LogicalSpace: &models.VolumeInlineSpaceInlineLogicalSpace{
							Used: nillable.ToPointer(int64(2048)), // Different from DB (1024)
						},
					},
				},
			},
			"external-a2": {
				Volume: models.Volume{
					Space: &models.VolumeInlineSpace{
						LogicalSpace: &models.VolumeInlineSpaceInlineLogicalSpace{
							Used: nillable.ToPointer(int64(1024)), // Same as DB (no change)
						},
					},
				},
			},
		},
	}, nil)

	// Pool B failure
	s.env.OnActivity("GetOntapVolumes", mock.Anything, poolB).Return(nil, errors.New("failed to get ONTAP volumes for pool B"))

	// Pool C success
	s.env.OnActivity("GetOntapVolumes", mock.Anything, poolC).Return(&activities.GetOntapVolumesReturnValue{
		OntapVolumeMap: map[string]*vsa.Volume{
			"external-c1": {
				Volume: models.Volume{
					Space: &models.VolumeInlineSpace{
						LogicalSpace: &models.VolumeInlineSpaceInlineLogicalSpace{
							Used: nillable.ToPointer(int64(4096)), // Different from DB (1024)
						},
					},
				},
			},
			"external-c2": {
				Volume: models.Volume{
					Space: &models.VolumeInlineSpace{
						LogicalSpace: &models.VolumeInlineSpaceInlineLogicalSpace{
							Used: nillable.ToPointer(int64(1024)), // Same as DB (no change)
						},
					},
				},
			},
		},
	}, nil)

	// Mock ProcessOntapVolumeMatching with expected results
	matchingResult := &activities.ProcessOntapVolumeMatchingResult{
		UpdatedVolumeByUUID: map[string]*datamodel.Volume{
			// Only volumes with changes should be updated (A1 and C1)
			"vol-a1-uuid": {
				BaseModel: datamodel.BaseModel{UUID: "vol-a1-uuid", ID: volumeA1.ID},
				UsedBytes: uint64(2048),
			},
			"vol-c1-uuid": {
				BaseModel: datamodel.BaseModel{UUID: "vol-c1-uuid", ID: volumeC1.ID},
				UsedBytes: uint64(4096),
			},
		},
		OntapVolResponse: map[string]*vsa.VolumeResponse{
			"vol-a1-uuid": {UsedBytes: 2048},
			"vol-c1-uuid": {UsedBytes: 4096},
		},
		VolumesNotFoundInONTAP: []*datamodel.Volume{volumeB1, volumeB2}, // Pool B volumes not found
		MatchedCount:           2,                                       // A1 and C1 had changes
		NotFoundCount:          2,                                       // B1 and B2 not found due to pool failure
	}
	s.env.OnActivity("ProcessOntapVolumeMatching", mock.Anything, mock.Anything).Return(matchingResult, nil)

	// Mock SyncUpdatedVolumesToDatabase for the 2 volumes that need updating
	s.env.OnActivity("SyncUpdatedVolumesToDatabase", mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity("UpdateAccountVolumeRefreshTimestamp", mock.Anything, mock.Anything).Return(nil)

	// Execute workflow
	s.env.OnActivity("GetVolumesForRefresh", mock.Anything, mock.Anything).Return(allVolumes, nil)
	s.env.ExecuteWorkflow(VolumeRefreshWorkflow, volumeRefreshWorkflowInputFromAccount(allVolumes[0].Account, volumeIDsFromVolumes(allVolumes)))

	// Assert workflow completed successfully despite Pool B failure
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
}

func (s *VolumeGetWorkflowTestSuite) Test_GetVolumeWorkflow_ProcessOntapVolumeMatchingError() {
	// Test ProcessOntapVolumeMatching activity failure
	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{
			UUID: "test-volume-uuid",
		},
		Name: "test-volume",
		Account: &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "account-uuid", ID: 1},
			Name:      "test-account",
		},
		Pool: &datamodel.Pool{
			BaseModel: datamodel.BaseModel{
				ID:   int64(1),
				UUID: "pool-uuid",
			},
			Name: "test-pool",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID: "external-uuid",
		},
	}

	// Mock ProcessVolumePoolMapping to succeed
	poolMappingResult := &activities.ProcessVolumePoolMappingResult{
		PoolByUUID: map[string]*datamodel.Pool{
			"pool-uuid": volume.Pool,
		},
		PoolUUIDs: []string{"pool-uuid"},
	}
	s.env.OnActivity("ProcessVolumePoolMapping", mock.Anything, mock.Anything).Return(poolMappingResult, nil)

	// Mock GetOntapVolumes to succeed
	ontapResult := &activities.GetOntapVolumesReturnValue{
		OntapVolumeMap: map[string]*vsa.Volume{
			"external-uuid": {
				Volume: models.Volume{
					Space: &models.VolumeInlineSpace{
						LogicalSpace: &models.VolumeInlineSpaceInlineLogicalSpace{
							Used: nillable.ToPointer(int64(2048)),
						},
					},
				},
			},
		},
	}
	s.env.OnActivity("GetOntapVolumes", mock.Anything, mock.Anything).Return(ontapResult, nil)

	// Mock ProcessOntapVolumeMatching to fail
	s.env.OnActivity("ProcessOntapVolumeMatching", mock.Anything, mock.Anything).Return(nil, errors.New("failed to process ONTAP volume matching"))

	// Execute workflow
	s.env.OnActivity("GetVolumesForRefresh", mock.Anything, mock.Anything).Return([]*datamodel.Volume{volume}, nil)
	s.env.ExecuteWorkflow(VolumeRefreshWorkflow, volumeRefreshWorkflowInputFromAccount([]*datamodel.Volume{volume}[0].Account, volumeIDsFromVolumes([]*datamodel.Volume{volume})))

	// Assert workflow failed
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
	assert.Contains(s.T(), s.env.GetWorkflowError().Error(), "failed to process ONTAP volume matching")
}

func (s *VolumeGetWorkflowTestSuite) Test_GetVolumeWorkflow_SyncDatabaseError() {
	// Test SyncUpdatedVolumesToDatabase activity failure
	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{
			UUID: "test-volume-uuid",
		},
		Name: "test-volume",
		Account: &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "account-uuid", ID: 1},
			Name:      "test-account",
		},
		Pool: &datamodel.Pool{
			BaseModel: datamodel.BaseModel{
				ID:   int64(1),
				UUID: "pool-uuid",
			},
			Name: "test-pool",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID: "external-uuid",
		},
	}

	// Mock ProcessVolumePoolMapping to succeed
	poolMappingResult := &activities.ProcessVolumePoolMappingResult{
		PoolByUUID: map[string]*datamodel.Pool{
			"pool-uuid": volume.Pool,
		},
		PoolUUIDs: []string{"pool-uuid"},
	}
	s.env.OnActivity("ProcessVolumePoolMapping", mock.Anything, mock.Anything).Return(poolMappingResult, nil)

	// Mock GetOntapVolumes to succeed
	ontapResult := &activities.GetOntapVolumesReturnValue{
		OntapVolumeMap: map[string]*vsa.Volume{
			"external-uuid": {
				Volume: models.Volume{
					Space: &models.VolumeInlineSpace{
						LogicalSpace: &models.VolumeInlineSpaceInlineLogicalSpace{
							Used: nillable.ToPointer(int64(2048)),
						},
					},
				},
			},
		},
	}
	s.env.OnActivity("GetOntapVolumes", mock.Anything, mock.Anything).Return(ontapResult, nil)

	// Mock ProcessOntapVolumeMatching to succeed
	matchingResult := &activities.ProcessOntapVolumeMatchingResult{
		UpdatedVolumeByUUID: map[string]*datamodel.Volume{
			"test-volume-uuid": {
				BaseModel: datamodel.BaseModel{
					UUID: "test-volume-uuid",
					ID:   volume.ID,
				},
				UsedBytes: 2048,
			},
		},
		OntapVolResponse: map[string]*vsa.VolumeResponse{
			"test-volume-uuid": {
				UsedBytes: 2048,
			},
		},
		VolumesNotFoundInONTAP: []*datamodel.Volume{},
		MatchedCount:           1,
		NotFoundCount:          0,
	}
	s.env.OnActivity("ProcessOntapVolumeMatching", mock.Anything, mock.Anything).Return(matchingResult, nil)

	// Mock SyncUpdatedVolumesToDatabase to fail
	s.env.OnActivity("SyncUpdatedVolumesToDatabase", mock.Anything, mock.Anything).Return(errors.New("failed to sync database"))

	// Execute workflow
	s.env.OnActivity("GetVolumesForRefresh", mock.Anything, mock.Anything).Return([]*datamodel.Volume{volume}, nil)
	s.env.ExecuteWorkflow(VolumeRefreshWorkflow, volumeRefreshWorkflowInputFromAccount([]*datamodel.Volume{volume}[0].Account, volumeIDsFromVolumes([]*datamodel.Volume{volume})))

	// Assert workflow failed
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
	assert.Contains(s.T(), s.env.GetWorkflowError().Error(), "failed to sync database")
}

func (s *VolumeGetWorkflowTestSuite) Test_GetVolumeWorkflow_VolumesNotFoundInONTAP() {
	// Test case where some volumes are not found in ONTAP
	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{
			UUID: "test-volume-uuid",
		},
		Name: "test-volume",
		Account: &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "account-uuid", ID: 1},
			Name:      "test-account",
		},
		Pool: &datamodel.Pool{
			BaseModel: datamodel.BaseModel{
				ID:   int64(1),
				UUID: "pool-uuid",
			},
			Name: "test-pool",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID: "external-uuid",
		},
	}

	// Mock ProcessVolumePoolMapping to succeed
	poolMappingResult := &activities.ProcessVolumePoolMappingResult{
		PoolByUUID: map[string]*datamodel.Pool{
			"pool-uuid": volume.Pool,
		},
		PoolUUIDs: []string{"pool-uuid"},
	}
	s.env.OnActivity("ProcessVolumePoolMapping", mock.Anything, mock.Anything).Return(poolMappingResult, nil)

	// Mock GetOntapVolumes to succeed
	ontapResult := &activities.GetOntapVolumesReturnValue{
		OntapVolumeMap: map[string]*vsa.Volume{
			"external-uuid": {
				Volume: models.Volume{
					Space: &models.VolumeInlineSpace{
						LogicalSpace: &models.VolumeInlineSpaceInlineLogicalSpace{
							Used: nillable.ToPointer(int64(2048)),
						},
					},
				},
			},
		},
	}
	s.env.OnActivity("GetOntapVolumes", mock.Anything, mock.Anything).Return(ontapResult, nil)

	// Mock ProcessOntapVolumeMatching with some volumes not found
	matchingResult := &activities.ProcessOntapVolumeMatchingResult{
		UpdatedVolumeByUUID: map[string]*datamodel.Volume{
			"test-volume-uuid": {
				BaseModel: datamodel.BaseModel{
					UUID: "test-volume-uuid",
					ID:   volume.ID,
				},
				UsedBytes: 2048,
			},
		},
		OntapVolResponse: map[string]*vsa.VolumeResponse{
			"test-volume-uuid": {
				UsedBytes: 2048,
			},
		},
		VolumesNotFoundInONTAP: []*datamodel.Volume{
			{
				BaseModel: datamodel.BaseModel{UUID: "not-found-volume-uuid"},
				Name:      "not-found-volume",
			},
		},
		MatchedCount:  1,
		NotFoundCount: 1, // This triggers the warning log on line 157
	}
	s.env.OnActivity("ProcessOntapVolumeMatching", mock.Anything, mock.Anything).Return(matchingResult, nil)

	// Mock SyncUpdatedVolumesToDatabase to succeed
	s.env.OnActivity("SyncUpdatedVolumesToDatabase", mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity("UpdateAccountVolumeRefreshTimestamp", mock.Anything, mock.Anything).Return(nil)

	// Execute workflow
	s.env.OnActivity("GetVolumesForRefresh", mock.Anything, mock.Anything).Return([]*datamodel.Volume{volume}, nil)
	s.env.ExecuteWorkflow(VolumeRefreshWorkflow, volumeRefreshWorkflowInputFromAccount([]*datamodel.Volume{volume}[0].Account, volumeIDsFromVolumes([]*datamodel.Volume{volume})))

	// Assert workflow completed successfully
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
}

func (s *VolumeGetWorkflowTestSuite) Test_GetVolumeWorkflow_NoVolumesToUpdate() {
	// Test case where no volumes need updating in database
	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{
			UUID: "test-volume-uuid",
		},
		Name: "test-volume",
		Account: &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "account-uuid", ID: 1},
			Name:      "test-account",
		},
		Pool: &datamodel.Pool{
			BaseModel: datamodel.BaseModel{
				ID:   int64(1),
				UUID: "pool-uuid",
			},
			Name: "test-pool",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID: "external-uuid",
		},
	}

	// Mock ProcessVolumePoolMapping to succeed
	poolMappingResult := &activities.ProcessVolumePoolMappingResult{
		PoolByUUID: map[string]*datamodel.Pool{
			"pool-uuid": volume.Pool,
		},
		PoolUUIDs: []string{"pool-uuid"},
	}
	s.env.OnActivity("ProcessVolumePoolMapping", mock.Anything, mock.Anything).Return(poolMappingResult, nil)

	// Mock GetOntapVolumes to succeed
	ontapResult := &activities.GetOntapVolumesReturnValue{
		OntapVolumeMap: map[string]*vsa.Volume{
			"external-uuid": {
				Volume: models.Volume{
					Space: &models.VolumeInlineSpace{
						LogicalSpace: &models.VolumeInlineSpaceInlineLogicalSpace{
							Used: nillable.ToPointer(int64(2048)),
						},
					},
				},
			},
		},
	}
	s.env.OnActivity("GetOntapVolumes", mock.Anything, mock.Anything).Return(ontapResult, nil)

	// Mock ProcessOntapVolumeMatching with empty UpdatedVolumeByUUID
	matchingResult := &activities.ProcessOntapVolumeMatchingResult{
		UpdatedVolumeByUUID:    map[string]*datamodel.Volume{}, // Empty map - no volumes to update
		OntapVolResponse:       map[string]*vsa.VolumeResponse{},
		VolumesNotFoundInONTAP: []*datamodel.Volume{},
		MatchedCount:           0,
		NotFoundCount:          0,
	}
	s.env.OnActivity("ProcessOntapVolumeMatching", mock.Anything, mock.Anything).Return(matchingResult, nil)

	// SyncUpdatedVolumesToDatabase should NOT be called since there are no volumes to update
	// But UpdateAccountVolumeRefreshTimestamp should still be called
	s.env.OnActivity("UpdateAccountVolumeRefreshTimestamp", mock.Anything, mock.Anything).Return(nil)

	// Execute workflow
	s.env.OnActivity("GetVolumesForRefresh", mock.Anything, mock.Anything).Return([]*datamodel.Volume{volume}, nil)
	s.env.ExecuteWorkflow(VolumeRefreshWorkflow, volumeRefreshWorkflowInputFromAccount([]*datamodel.Volume{volume}[0].Account, volumeIDsFromVolumes([]*datamodel.Volume{volume})))

	// Assert workflow completed successfully (and triggers the log on line 172)
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
}

func (s *VolumeGetWorkflowTestSuite) Test_GetVolumeWorkflow_UpdateAccountTimestampError() {
	// Test that workflow completes successfully even if UpdateAccountVolumeRefreshTimestamp fails
	// This verifies the error handling doesn't fail the workflow
	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{
			UUID: "test-volume-uuid",
		},
		Name: "test-volume",
		Account: &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "account-uuid", ID: 1},
			Name:      "test-account",
		},
		Pool: &datamodel.Pool{
			BaseModel: datamodel.BaseModel{
				ID:   int64(1),
				UUID: "pool-uuid",
			},
			Name: "test-pool",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID: "external-uuid",
		},
	}

	// Mock ProcessVolumePoolMapping to succeed
	poolMappingResult := &activities.ProcessVolumePoolMappingResult{
		PoolByUUID: map[string]*datamodel.Pool{
			"pool-uuid": volume.Pool,
		},
		PoolUUIDs: []string{"pool-uuid"},
	}
	s.env.OnActivity("ProcessVolumePoolMapping", mock.Anything, mock.Anything).Return(poolMappingResult, nil)

	// Mock GetOntapVolumes to succeed
	ontapResult := &activities.GetOntapVolumesReturnValue{
		OntapVolumeMap: map[string]*vsa.Volume{
			"external-uuid": {
				Volume: models.Volume{
					Space: &models.VolumeInlineSpace{
						LogicalSpace: &models.VolumeInlineSpaceInlineLogicalSpace{
							Used: nillable.ToPointer(int64(2048)),
						},
					},
				},
			},
		},
	}
	s.env.OnActivity("GetOntapVolumes", mock.Anything, mock.Anything).Return(ontapResult, nil)

	// Mock ProcessOntapVolumeMatching to succeed
	matchingResult := &activities.ProcessOntapVolumeMatchingResult{
		UpdatedVolumeByUUID: map[string]*datamodel.Volume{
			"test-volume-uuid": {
				BaseModel: datamodel.BaseModel{
					UUID: "test-volume-uuid",
					ID:   volume.ID,
				},
				UsedBytes: 2048,
			},
		},
		OntapVolResponse: map[string]*vsa.VolumeResponse{
			"test-volume-uuid": {
				UsedBytes: 2048,
			},
		},
		VolumesNotFoundInONTAP: []*datamodel.Volume{},
		MatchedCount:           1,
		NotFoundCount:          0,
	}
	s.env.OnActivity("ProcessOntapVolumeMatching", mock.Anything, mock.Anything).Return(matchingResult, nil)

	// Mock SyncUpdatedVolumesToDatabase to succeed
	s.env.OnActivity("SyncUpdatedVolumesToDatabase", mock.Anything, mock.Anything).Return(nil)

	// Mock UpdateAccountVolumeRefreshTimestamp to FAIL
	s.env.OnActivity("UpdateAccountVolumeRefreshTimestamp", mock.Anything, mock.Anything).Return(errors.New("failed to update account timestamp"))

	// Execute workflow
	s.env.OnActivity("GetVolumesForRefresh", mock.Anything, mock.Anything).Return([]*datamodel.Volume{volume}, nil)
	s.env.ExecuteWorkflow(VolumeRefreshWorkflow, volumeRefreshWorkflowInputFromAccount([]*datamodel.Volume{volume}[0].Account, volumeIDsFromVolumes([]*datamodel.Volume{volume})))

	// Assert workflow completed successfully despite UpdateAccountVolumeRefreshTimestamp failure
	// The workflow should log the error but not fail (as per the implementation)
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
}

func (s *VolumeGetWorkflowTestSuite) Test_GetVolumeWorkflow_NoAccountInVolumes() {
	s.env.ExecuteWorkflow(VolumeRefreshWorkflow, VolumeRefreshWorkflowInput{
		AccountUUID: "account-uuid",
		AccountName: "test-account",
		VolumeIDs:   []string{"test-volume-uuid"},
	})

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
	assert.Contains(s.T(), s.env.GetWorkflowError().Error(), "account ID is required")
}

func (s *VolumeGetWorkflowTestSuite) Test_GetVolumeWorkflow_GetVolumesForRefreshError() {
	s.env.OnActivity("GetVolumesForRefresh", mock.Anything, mock.Anything).
		Return(nil, errors.New("failed to load volumes for refresh"))

	s.env.ExecuteWorkflow(VolumeRefreshWorkflow, VolumeRefreshWorkflowInput{
		AccountUUID: "account-uuid",
		AccountName: "test-account",
		AccountID:   1,
		VolumeIDs:   []string{"test-volume-uuid"},
	})

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
	assert.Contains(s.T(), s.env.GetWorkflowError().Error(), "failed to load volumes for refresh")
}

func (s *VolumeGetWorkflowTestSuite) Test_GetVolumeWorkflow_NoVolumesLoadedFromDB() {
	s.env.OnActivity("GetVolumesForRefresh", mock.Anything, mock.Anything).
		Return([]*datamodel.Volume{}, nil)

	s.env.ExecuteWorkflow(VolumeRefreshWorkflow, VolumeRefreshWorkflowInput{
		AccountUUID: "account-uuid",
		AccountName: "test-account",
		AccountID:   1,
		VolumeIDs:   []string{"test-volume-uuid"},
	})

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
}

func TestVolumeGetWorkflowTestSuite(t *testing.T) {
	suite.Run(t, new(VolumeGetWorkflowTestSuite))
}

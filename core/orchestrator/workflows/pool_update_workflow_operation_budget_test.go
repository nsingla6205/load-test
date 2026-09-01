package workflows

import (
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/vlm"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/backgroundactivities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
	workflowtemporal "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/temporal"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

const poolOperationBudgetTestStartTime = "2006-01-02T15:04:05Z"

// PoolUpdateOperationBudgetTestSuite holds focused operation-budget tests without the broad
// SetupTest mocks from pool_workflows_test.go (which would override GetJob timing).
type PoolUpdateOperationBudgetTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite

	env                      *testsuite.TestWorkflowEnvironment
	savedUpdatePoolTimeout   string
	savedUpdatePoolTimeoutLV string
}

func TestPoolUpdateOperationBudgetTestSuite(t *testing.T) {
	suite.Run(t, new(PoolUpdateOperationBudgetTestSuite))
}

func (s *PoolUpdateOperationBudgetTestSuite) SetupTest() {
	s.savedUpdatePoolTimeout = workflowtemporal.UpdatePoolWorkflowTimeoutMinutes
	s.savedUpdatePoolTimeoutLV = workflowtemporal.UpdatePoolWorkflowTimeoutMinutesLV
	workflowtemporal.UpdatePoolWorkflowTimeoutMinutes = "30"
	workflowtemporal.UpdatePoolWorkflowTimeoutMinutesLV = "30"
	s.env = s.NewTestWorkflowEnvironment()
	s.env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
	s.env.SetHeader(&commonpb.Header{
		Fields: map[string]*commonpb.Payload{"logParam": encodedValue},
	})
	s.env.RegisterWorkflow(UpdatePoolWorkflow)
}

func (s *PoolUpdateOperationBudgetTestSuite) AfterTest(_ string, _ string) {
	workflowtemporal.UpdatePoolWorkflowTimeoutMinutes = s.savedUpdatePoolTimeout
	workflowtemporal.UpdatePoolWorkflowTimeoutMinutesLV = s.savedUpdatePoolTimeoutLV
	s.env.AssertExpectations(s.T())
}

func parsePoolOperationBudgetTestStart(t *testing.T) time.Time {
	t.Helper()
	start, err := time.Parse(time.RFC3339, poolOperationBudgetTestStartTime)
	require.NoError(t, err)
	return start
}

func (s *PoolUpdateOperationBudgetTestSuite) registerJobActivities(
	mockStorage *database.MockStorage,
	commonActivity activities.CommonActivities,
	getJobDelay time.Duration,
) {
	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetJob)

	call := s.env.OnActivity(commonActivity.GetJob, mock.Anything, mock.Anything).
		Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil)
	if getJobDelay > 0 {
		call.After(getJobDelay)
	}
}

func operationBudgetTestPOPool() *datamodel.Pool {
	iops := int64(100000)
	throughput := int64(1280)
	return &datamodel.Pool{
		BaseModel:     datamodel.BaseModel{ID: 1, UUID: "po-budget-test-pool"},
		HostUUID:      "host-uuid-1",
		SizeInBytes:   int64(common.BytesPerTiB),
		ServiceLevel:  common.ServiceLevelExtreme,
		APIAccessMode: common.ONTAPMode,
		Description:   "before",
		PoolAttributes: &datamodel.PoolAttributes{
			ServiceLevel:    common.ServiceLevelExtreme,
			Iops:            iops,
			ThroughputMibps: throughput,
		},
	}
}

func operationBudgetTestPOParams(pool *datamodel.Pool) *common.UpdatePoolParams {
	return &common.UpdatePoolParams{
		AccountName:          "test-account",
		PoolId:               pool.UUID,
		Description:          "after",
		SizeInBytes:          uint64(pool.SizeInBytes),
		TotalThroughputMibps: pool.PoolAttributes.ThroughputMibps,
		TotalIops:            &pool.PoolAttributes.Iops,
	}
}

func operationBudgetTestVLMResizePoolAndParams() (*common.UpdatePoolParams, *datamodel.Pool) {
	params := &common.UpdatePoolParams{
		AccountName:          "test-account",
		PoolId:               "vlm-budget-test-pool",
		Region:               "us-central1",
		SizeInBytes:          2 * 1024 * 1024 * 1024 * 1024,
		TotalThroughputMibps: 128,
		TotalIops:            nillable.ToPointer(int64(2048)),
	}
	pool := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{UUID: "vlm-budget-test-pool"},
		PoolCredentials: &datamodel.PoolCredentials{
			Password: "test-password",
		},
		ClusterDetails: datamodel.ClusterDetails{
			RegionalTenantProject: "test-regional-project",
			Network:               "test-network",
		},
		BuildInfo: &datamodel.PoolBuildInfo{OntapVersion: "9.18.1"},
		SizeInBytes: 456,
		PoolAttributes: &datamodel.PoolAttributes{
			PrimaryZone:     "us-central1-a",
			SecondaryZone:   "us-central1-b",
			Iops:            2048,
			ThroughputMibps: 128,
		},
		VLMConfig: `{"deployment": {"vsa_instance_type": "c3-standard-8-lssd"}}`,
	}
	return params, pool
}

func (s *PoolUpdateOperationBudgetTestSuite) installVSAClientMock() (*vlm.MockVlmWorkflowClient, func()) {
	mockVSA := new(vlm.MockVlmWorkflowClient)
	origFactory := GetNewVSAClientWorkflowManager
	GetNewVSAClientWorkflowManager = func() vlm.VlmWorkflowClient {
		return mockVSA
	}
	return mockVSA, func() { GetNewVSAClientWorkflowManager = origFactory }
}

func (s *PoolUpdateOperationBudgetTestSuite) registerVLMUpdatePathActivities(
	poolActivity activities.PoolActivity,
	svmActivity activities.SvmActivity,
	commonActivity activities.CommonActivities,
) {
	s.env.RegisterActivity(poolActivity.ParseVlmConfig)
	s.env.RegisterActivity(poolActivity.IdentifyVMs)
	s.env.RegisterActivity(poolActivity.DetermineVMScalingDirection)
	s.env.RegisterActivity(poolActivity.GetOnTapCredentials)
	s.env.RegisterActivity(poolActivity.CalculateBatchPlanForUpdate)
	s.env.RegisterActivity(poolActivity.UpdatedPool)
	s.env.RegisterActivity(poolActivity.UpdatedPoolWithVLMConfig)
	s.env.RegisterActivity(commonActivity.GetNode)
	s.env.RegisterActivity(svmActivity.ModifyQoSPolicyAndApplyToSVM)

	instanceType := "c3-standard-8-lssd"
	vlmConfig := &vlm.VLMConfig{
		Deployment: vlm.DeploymentConfig{
			VSAInstanceType: instanceType,
			NumHAPair:       2,
		},
	}

	s.env.OnActivity(poolActivity.ParseVlmConfig, mock.Anything, mock.Anything).Return(vlmConfig, nil)
	s.env.OnActivity(poolActivity.IdentifyVMs, mock.Anything, mock.Anything).Return(vlmConfig, nil)
	s.env.OnActivity(poolActivity.DetermineVMScalingDirection, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(true, nil) // scale up → QoS after batches
	s.env.OnActivity(poolActivity.GetOnTapCredentials, mock.Anything, mock.Anything).
		Return(&vlm.OntapCredentials{AdminPassword: "ontap-pw"}, nil)
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).
		Return([]*datamodel.Node{{BaseModel: datamodel.BaseModel{ID: 1}, Name: "node-1"}}, nil)
	s.env.OnActivity(svmActivity.ModifyQoSPolicyAndApplyToSVM, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil).
		Maybe()
	s.env.OnActivity(poolActivity.CalculateBatchPlanForUpdate, mock.Anything, mock.Anything).
		Return(&activities.CalculateBatchPlanActivityOutput{
			NumHAPairs:       2,
			BatchSize:        1,
			NumWorkflowCalls: 2,
			BatchIndices:     [][]int{{1}, {2}},
		}, nil)
	s.env.OnActivity(poolActivity.UpdatedPoolWithVLMConfig, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, nil).
		Maybe()
}

func operationBudgetTestZoneSwitchPoolAndParams() (*common.UpdatePoolParams, *datamodel.Pool) {
	sizeBytes := uint64(2 * 1024 * 1024 * 1024 * 1024)
	throughput := int64(128)
	iops := int64(2048)
	params := &common.UpdatePoolParams{
		AccountName:          "test-account",
		PoolId:               "zone-budget-test-pool",
		Region:               "us-east4",
		CurrentZone:          "test-secondary-zone",
		SizeInBytes:          sizeBytes,
		TotalThroughputMibps: throughput,
		TotalIops:            nillable.ToPointer(iops),
		QosType:              "Manual",
		Description:          "same-description",
	}
	pool := &datamodel.Pool{
		BaseModel:   datamodel.BaseModel{UUID: "zone-budget-test-pool"},
		Description: "same-description",
		QosType:     "Manual",
		SizeInBytes: int64(sizeBytes),
		PoolCredentials: &datamodel.PoolCredentials{
			Password: "test-password",
		},
		ClusterDetails: datamodel.ClusterDetails{
			ExternalName:          "test-cluster",
			Network:               "test-network",
			RegionalTenantProject: "test-regional-project",
			SnHostProject:         "test-host-project",
		},
		BuildInfo: &datamodel.PoolBuildInfo{OntapVersion: "9.18.1"},
		VLMConfig: `{"deployment":{"deployment_id":"dep-1"}}`,
		PoolAttributes: &datamodel.PoolAttributes{
			PrimaryZone:     "test-primary-zone",
			SecondaryZone:   "test-secondary-zone",
			ThroughputMibps: throughput,
			Iops:            iops,
			IsRegionalHA:    true,
			IsZoneSwitched:  false,
		},
		AutoTieringConfig: &datamodel.AutoTieringConfig{BucketName: "test-bucket"},
	}
	return params, pool
}

func (s *PoolUpdateOperationBudgetTestSuite) registerZoneSwitchBudgetTestActivities(
	poolActivity activities.PoolActivity,
	params *common.UpdatePoolParams,
	pool *datamodel.Pool,
) {
	s.env.RegisterActivity(backgroundactivities.EmitZoneSwitchWorkflowMetric)
	s.env.RegisterActivity(poolActivity.UpdateZoneSwitchPoolAttributes)
	s.env.RegisterActivity(poolActivity.ParseVlmConfig)
	s.env.RegisterActivity(poolActivity.GetOnTapCredentials)
	s.env.RegisterActivity(poolActivity.UpdatedPool)
	s.env.RegisterActivity(poolActivity.UpdatedPoolWithVLMConfig)

	s.env.OnActivity(poolActivity.UpdateZoneSwitchPoolAttributes, mock.Anything, mock.Anything, datamodel.ZoneSwitching).
		Return(nil).
		Once()
	s.env.OnActivity(poolActivity.ParseVlmConfig, mock.Anything, mock.Anything).
		Return(&vlm.VLMConfig{Deployment: vlm.DeploymentConfig{DeploymentID: "dep-1"}}, nil)
	s.env.OnActivity(poolActivity.GetOnTapCredentials, mock.Anything, mock.Anything).
		Return(&vlm.OntapCredentials{AdminPassword: "ontap-pw"}, nil)
	s.env.OnActivity("EmitZoneSwitchWorkflowMetric", mock.Anything, pool.UUID, mock.Anything, ZoneSwitch, zoneSwitchMetricStatusFailure, zoneSwitchFailureStepZoneSwitch).
		Return(nil).
		Once()
	s.env.OnActivity(poolActivity.UpdatedPool, mock.Anything, mock.MatchedBy(func(p *datamodel.Pool) bool {
		return p.UUID == pool.UUID && p.Description == pool.Description
	})).Return(nil, nil).Once() // explicit zone-switch rollback (not defer)
	s.env.OnActivity(poolActivity.UpdatedPoolWithVLMConfig, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, nil).
		Maybe()
	_ = params
}

func (s *PoolUpdateOperationBudgetTestSuite) Test_OperationBudget_QueueTooLong() {
	s.env.SetStartTime(parsePoolOperationBudgetTestStart(s.T()))

	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	poolActivity := activities.PoolActivity{SE: mockStorage}

	s.registerJobActivities(mockStorage, commonActivity, 31*time.Minute)
	s.env.RegisterActivity(poolActivity.UpdatedPool)

	pool := operationBudgetTestPOPool()
	params := operationBudgetTestPOParams(pool)

	s.env.OnActivity(poolActivity.UpdatedPool, mock.Anything, mock.Anything).
		Return(nil, nil).
		Maybe()

	s.env.ExecuteWorkflow(UpdatePoolWorkflow, params, pool, nil)

	require.True(s.T(), s.env.IsWorkflowCompleted())
	require.Error(s.T(), s.env.GetWorkflowError())
	s.env.AssertActivityNumberOfCalls(s.T(), "UpdatedPool", 1)
}

func (s *PoolUpdateOperationBudgetTestSuite) Test_OperationBudget_ExceededAfterActivity() {
	s.env.SetStartTime(parsePoolOperationBudgetTestStart(s.T()))

	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	poolActivity := activities.PoolActivity{SE: mockStorage}

	s.registerJobActivities(mockStorage, commonActivity, 0)
	s.env.RegisterActivity(poolActivity.ParseVlmConfig)
	s.env.RegisterActivity(poolActivity.UpdatedPool)

	params, pool := operationBudgetTestVLMResizePoolAndParams()

	s.env.OnActivity(poolActivity.ParseVlmConfig, mock.Anything, mock.Anything).
		After(31*time.Minute).
		Return(&vlm.VLMConfig{
			Deployment: vlm.DeploymentConfig{VSAInstanceType: "c3-standard-8-lssd"},
		}, nil)
	s.env.OnActivity(poolActivity.UpdatedPool, mock.Anything, mock.Anything).
		Return(nil, nil).
		Maybe()

	s.env.ExecuteWorkflow(UpdatePoolWorkflow, params, pool, nil)

	require.True(s.T(), s.env.IsWorkflowCompleted())
	require.Error(s.T(), s.env.GetWorkflowError())
	s.env.AssertActivityNumberOfCalls(s.T(), "ParseVlmConfig", 1)
	s.env.AssertActivityNumberOfCalls(s.T(), "IdentifyVMs", 0)
}

func (s *PoolUpdateOperationBudgetTestSuite) Test_OperationBudget_ExceededWithRollback() {
	s.env.SetStartTime(parsePoolOperationBudgetTestStart(s.T()))

	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	poolActivity := activities.PoolActivity{SE: mockStorage}

	s.registerJobActivities(mockStorage, commonActivity, 0)
	s.env.RegisterActivity(poolActivity.ParseVlmConfig)
	s.env.RegisterActivity(poolActivity.IdentifyVMs)
	s.env.RegisterActivity(poolActivity.UpdatedPool)

	params, pool := operationBudgetTestVLMResizePoolAndParams()

	s.env.OnActivity(poolActivity.ParseVlmConfig, mock.Anything, mock.Anything).
		Return(&vlm.VLMConfig{
			Deployment: vlm.DeploymentConfig{VSAInstanceType: "c3-standard-8-lssd"},
		}, nil)
	s.env.OnActivity(poolActivity.IdentifyVMs, mock.Anything, mock.Anything).
		After(31 * time.Minute).
		Return(&vlm.VLMConfig{
			Deployment: vlm.DeploymentConfig{VSAInstanceType: "c3-standard-8-lssd"},
		}, nil)
	s.env.OnActivity(poolActivity.UpdatedPool, mock.Anything, mock.Anything).
		Return(nil, nil)

	s.env.ExecuteWorkflow(UpdatePoolWorkflow, params, pool, nil)

	require.True(s.T(), s.env.IsWorkflowCompleted())
	require.Error(s.T(), s.env.GetWorkflowError())
	s.env.AssertActivityNumberOfCalls(s.T(), "ParseVlmConfig", 1)
	s.env.AssertActivityNumberOfCalls(s.T(), "IdentifyVMs", 1)
	s.env.AssertActivityNumberOfCalls(s.T(), "UpdatedPool", 1)
}

func (s *PoolUpdateOperationBudgetTestSuite) Test_OperationBudget_ExceededAfterVLMBatch() {
	s.env.SetStartTime(parsePoolOperationBudgetTestStart(s.T()))

	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	poolActivity := activities.PoolActivity{SE: mockStorage}
	svmActivity := activities.SvmActivity{SE: mockStorage}

	s.registerJobActivities(mockStorage, commonActivity, 0)
	s.registerVLMUpdatePathActivities(poolActivity, svmActivity, commonActivity)

	mockVSA, cleanup := s.installVSAClientMock()
	s.T().Cleanup(cleanup)

	params, pool := operationBudgetTestVLMResizePoolAndParams()
	vlmConfigResponse := vlm.VLMConfig{
		Deployment: vlm.DeploymentConfig{
			DeploymentID:    "dep-1",
			VSAInstanceType: "c3-standard-8-lssd",
		},
	}

	mockVSA.On("UpdateVSAClusterDeployment", mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			ctx := args[0].(workflow.Context)
			_ = workflow.Sleep(ctx, 31*time.Minute)
		}).
		Return(&vlm.UpdateVSAClusterDeploymentResponse{VLMConfig: vlmConfigResponse}, nil).
		Once()

	s.env.OnActivity(poolActivity.UpdatedPool, mock.Anything, mock.Anything).
		Return(nil, nil).
		Once() // defer rollback after batch budget failure

	s.env.ExecuteWorkflow(UpdatePoolWorkflow, params, pool, nil)

	require.True(s.T(), s.env.IsWorkflowCompleted())
	require.Error(s.T(), s.env.GetWorkflowError())
	s.env.AssertActivityNumberOfCalls(s.T(), "UpdatedPool", 1)
	s.env.AssertNotCalled(s.T(), "UpdatedPoolWithVLMConfig", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	s.env.AssertNotCalled(s.T(), "ModifyQoSPolicyAndApplyToSVM", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	mockVSA.AssertNumberOfCalls(s.T(), "UpdateVSAClusterDeployment", 1)
}

func (s *PoolUpdateOperationBudgetTestSuite) Test_OperationBudget_ExceededAfterZoneSwitch() {
	s.env.SetStartTime(parsePoolOperationBudgetTestStart(s.T()))

	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	poolActivity := activities.PoolActivity{SE: mockStorage}

	s.registerJobActivities(mockStorage, commonActivity, 0)

	mockVSA, cleanup := s.installVSAClientMock()
	s.T().Cleanup(cleanup)

	params, pool := operationBudgetTestZoneSwitchPoolAndParams()
	s.registerZoneSwitchBudgetTestActivities(poolActivity, params, pool)

	mockVSA.On("ZoneSwitch", mock.Anything, mock.MatchedBy(func(req *vlm.ZoneSwitchRequest) bool {
		return req != nil && req.Action == ZoneSwitch
	})).Run(func(args mock.Arguments) {
		ctx := args[0].(workflow.Context)
		_ = workflow.Sleep(ctx, 31*time.Minute)
	}).Return(&vlm.ZoneSwitchResponse{
		VLMConfig: vlm.VLMConfig{Deployment: vlm.DeploymentConfig{DeploymentID: "dep-after"}},
	}, nil).Once()

	s.env.ExecuteWorkflow(UpdatePoolWorkflow, params, pool, nil)

	require.True(s.T(), s.env.IsWorkflowCompleted())
	require.Error(s.T(), s.env.GetWorkflowError())
	s.env.AssertActivityNumberOfCalls(s.T(), "EmitZoneSwitchWorkflowMetric", 1)
	s.env.AssertActivityNumberOfCalls(s.T(), "UpdatedPool", 1)
	s.env.AssertNotCalled(s.T(), "UpdatedPoolWithVLMConfig", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	mockVSA.AssertExpectations(s.T())
}

func (s *PoolUpdateOperationBudgetTestSuite) Test_OperationBudget_ZeroWorkflowStartTime() {
	// Intentionally omit SetStartTime so WorkflowStartTime is zero and the budget
	// anchors to orchestrator.Now (Temporal test env may leave start time unset).

	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	poolActivity := activities.PoolActivity{SE: mockStorage}

	s.registerJobActivities(mockStorage, commonActivity, 31*time.Minute)
	s.env.RegisterActivity(poolActivity.UpdatedPool)

	pool := operationBudgetTestPOPool()
	params := operationBudgetTestPOParams(pool)

	s.env.OnActivity(poolActivity.UpdatedPool, mock.Anything, mock.Anything).
		Return(nil, nil).
		Maybe()

	s.env.ExecuteWorkflow(UpdatePoolWorkflow, params, pool, nil)

	require.True(s.T(), s.env.IsWorkflowCompleted())
	require.Error(s.T(), s.env.GetWorkflowError())
	s.env.AssertActivityNumberOfCalls(s.T(), "UpdatedPool", 1)
}

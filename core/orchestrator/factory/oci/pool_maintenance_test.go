package oci

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	utilserrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	workflowenginemock "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine"
	"go.temporal.io/api/serviceerror"
	"gorm.io/gorm"
)

const (
	testMaintTenancyOCID = "ocid1.tenancy.oc1..maint"
	testMaintPoolOCID    = "ocid1.pool.oc1..maint"
	testMaintInstanceVM1 = "ocid1.instance.oc1.iad.vm1"
	testMaintInstanceVM2 = "ocid1.instance.oc1.iad.vm2"
	testMaintInstanceMed = "ocid1.instance.oc1.iad.mediator"
	testMaintForeignVM   = "ocid1.instance.oc1.iad.foreign"
)

func twoNodePoolVLMConfig() string {
	return `{"cloud":{"ha_pair":[{"vm1":{"vm_instance_id":"` + testMaintInstanceVM1 +
		`"},"vm2":{"vm_instance_id":"` + testMaintInstanceVM2 +
		`"},"mediator":{"vm_instance_id":"` + testMaintInstanceMed + `"}}]}}`
}

func TestValidateInstancesBelongToPool(t *testing.T) {
	tests := []struct {
		name          string
		vlmConfig     string
		instanceOCIDs []string
		wantErr       bool
		isNotFound    bool
		isNotReady    bool
	}{
		{
			name:          "all instances belong to pool",
			vlmConfig:     twoNodePoolVLMConfig(),
			instanceOCIDs: []string{testMaintInstanceVM1, testMaintInstanceVM2},
		},
		{
			name:          "mediator instance belongs to pool",
			vlmConfig:     twoNodePoolVLMConfig(),
			instanceOCIDs: []string{testMaintInstanceMed},
		},
		{
			name:          "trims whitespace before comparison",
			vlmConfig:     twoNodePoolVLMConfig(),
			instanceOCIDs: []string{"  " + testMaintInstanceVM1 + "  "},
		},
		{
			name:          "no instances to check is a no-op",
			vlmConfig:     twoNodePoolVLMConfig(),
			instanceOCIDs: nil,
		},
		{
			name:          "foreign instance not in pool",
			vlmConfig:     twoNodePoolVLMConfig(),
			instanceOCIDs: []string{testMaintInstanceVM1, testMaintForeignVM},
			wantErr:       true,
			isNotFound:    true,
		},
		{
			name:          "empty vlm config",
			vlmConfig:     "",
			instanceOCIDs: []string{testMaintInstanceVM1},
			wantErr:       true,
			isNotReady:    true,
		},
		{
			name:          "unparseable vlm config",
			vlmConfig:     "{not-json",
			instanceOCIDs: []string{testMaintInstanceVM1},
			wantErr:       true,
			isNotReady:    true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateInstancesBelongToPool(tc.vlmConfig, tc.instanceOCIDs)
			if !tc.wantErr {
				assert.NoError(t, err)
				return
			}
			assert.Error(t, err)
			if tc.isNotFound {
				assert.True(t, utilserrors.IsNotFoundErr(err), "expected NotFound, got %v", err)
			}
			if tc.isNotReady {
				assert.True(t, utilserrors.IsNotReadyErr(err), "expected NotReady, got %v", err)
			}
		})
	}
}

func maintTestCtx() context.Context {
	return context.WithValue(context.Background(), middleware.ContextSLoggerKey, log.NewLogger())
}

func TestExecutePoolMaintenance_SucceedsWhenInstancesBelong(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	acc := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: testMaintTenancyOCID}
	mockStorage.EXPECT().GetAccount(mock.Anything, testMaintTenancyOCID).Return(acc, nil)
	mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(&datamodel.PoolView{
		Pool: datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 7, UUID: "pool-uuid"},
			State:     datamodel.LifeCycleStateREADY,
			VLMConfig: twoNodePoolVLMConfig(),
		},
	}, nil)
	mockStorage.EXPECT().GetClusterUpgradeJobsByClusterID(mock.Anything, "pool-uuid").Return(nil, nil)

	var updatingPool *datamodel.Pool
	mockStorage.EXPECT().UpdatingPool(mock.Anything, mock.Anything).
		Run(func(_ context.Context, p *datamodel.Pool) { updatingPool = p }).
		Return(&datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 7, UUID: "pool-uuid"}, State: datamodel.LifeCycleStateUpdating}, nil)

	mockTemporal := workflowenginemock.NewMockTemporalTestClient(t)
	mockTemporal.EXPECT().
		ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, nil)

	orch := &OCIOrchestrator{storage: mockStorage, temporal: mockTemporal}

	workflowID, err := orch.ExecutePoolMaintenance(maintTestCtx(), &commonparams.PoolMaintenanceParams{
		AccountName:   testMaintTenancyOCID,
		PoolOCID:      testMaintPoolOCID,
		InstanceOCIDs: []string{testMaintInstanceVM1, testMaintInstanceVM2},
		OperationType: commonparams.MaintenanceOperationReboot,
		WorkflowID:    testSuppliedWorkflowID,
	})

	assert.NoError(t, err)
	assert.Equal(t, testSuppliedWorkflowID, workflowID)
	assert.NotNil(t, updatingPool)
	assert.Equal(t, testSuppliedWorkflowID, updatingPool.WorkflowID)
}

func TestStartAndFinishPoolMaintenance_NotImplemented(t *testing.T) {
	orch := &OCIOrchestrator{}

	_, startErr := orch.StartPoolMaintenance(maintTestCtx(), &commonparams.PoolMaintenanceParams{})
	assert.Error(t, startErr)
	assert.True(t, utilserrors.IsNotImplementedYetErr(startErr))

	_, finishErr := orch.FinishPoolMaintenance(maintTestCtx(), &commonparams.PoolMaintenanceParams{})
	assert.Error(t, finishErr)
	assert.True(t, utilserrors.IsNotImplementedYetErr(finishErr))
}

func TestExecutePoolMaintenance_NilParams(t *testing.T) {
	orch := &OCIOrchestrator{storage: database.NewMockStorage(t)}

	_, err := orch.ExecutePoolMaintenance(maintTestCtx(), nil)

	assert.Error(t, err)
	assert.True(t, utilserrors.IsBadRequestErr(err))
}

func TestExecutePoolMaintenance_AccountNotFound(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	mockStorage.EXPECT().GetAccount(mock.Anything, testMaintTenancyOCID).Return(nil, gorm.ErrRecordNotFound)

	orch := &OCIOrchestrator{storage: mockStorage}

	_, err := orch.ExecutePoolMaintenance(maintTestCtx(), &commonparams.PoolMaintenanceParams{
		AccountName: testMaintTenancyOCID,
		PoolOCID:    testMaintPoolOCID,
	})

	assert.Error(t, err)
	assert.True(t, utilserrors.IsNotFoundErr(err))
}

func TestExecutePoolMaintenance_AccountLookupError(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	mockStorage.EXPECT().GetAccount(mock.Anything, testMaintTenancyOCID).Return(nil, errors.New("db down"))

	orch := &OCIOrchestrator{storage: mockStorage}

	_, err := orch.ExecutePoolMaintenance(maintTestCtx(), &commonparams.PoolMaintenanceParams{
		AccountName: testMaintTenancyOCID,
		PoolOCID:    testMaintPoolOCID,
	})

	assert.Error(t, err)
	assert.False(t, utilserrors.IsNotFoundErr(err))
}

func TestExecutePoolMaintenance_PoolNotFound(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	acc := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: testMaintTenancyOCID}
	mockStorage.EXPECT().GetAccount(mock.Anything, testMaintTenancyOCID).Return(acc, nil)
	mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(nil, gorm.ErrRecordNotFound)

	orch := &OCIOrchestrator{storage: mockStorage}

	_, err := orch.ExecutePoolMaintenance(maintTestCtx(), &commonparams.PoolMaintenanceParams{
		AccountName: testMaintTenancyOCID,
		PoolOCID:    testMaintPoolOCID,
	})

	assert.Error(t, err)
	assert.True(t, utilserrors.IsNotFoundErr(err))
}

func TestExecutePoolMaintenance_PoolLookupError(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	acc := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: testMaintTenancyOCID}
	mockStorage.EXPECT().GetAccount(mock.Anything, testMaintTenancyOCID).Return(acc, nil)
	mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(nil, errors.New("db down"))

	orch := &OCIOrchestrator{storage: mockStorage}

	_, err := orch.ExecutePoolMaintenance(maintTestCtx(), &commonparams.PoolMaintenanceParams{
		AccountName: testMaintTenancyOCID,
		PoolOCID:    testMaintPoolOCID,
	})

	assert.Error(t, err)
	assert.False(t, utilserrors.IsNotFoundErr(err))
}

func TestExecutePoolMaintenance_InstanceNotInPool(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	acc := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: testMaintTenancyOCID}
	mockStorage.EXPECT().GetAccount(mock.Anything, testMaintTenancyOCID).Return(acc, nil)
	mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(&datamodel.PoolView{
		Pool: datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 7, UUID: "pool-uuid"},
			State:     datamodel.LifeCycleStateREADY,
			VLMConfig: twoNodePoolVLMConfig(),
		},
	}, nil)

	orch := &OCIOrchestrator{storage: mockStorage}

	_, err := orch.ExecutePoolMaintenance(maintTestCtx(), &commonparams.PoolMaintenanceParams{
		AccountName:   testMaintTenancyOCID,
		PoolOCID:      testMaintPoolOCID,
		InstanceOCIDs: []string{testMaintForeignVM},
	})

	assert.Error(t, err)
	assert.True(t, utilserrors.IsNotFoundErr(err))
}

func TestExecutePoolMaintenance_InvalidState(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	acc := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: testMaintTenancyOCID}
	mockStorage.EXPECT().GetAccount(mock.Anything, testMaintTenancyOCID).Return(acc, nil)
	mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(&datamodel.PoolView{
		Pool: datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 7, UUID: "pool-uuid"},
			State:     datamodel.LifeCycleStateCreating,
			VLMConfig: twoNodePoolVLMConfig(),
		},
	}, nil)

	orch := &OCIOrchestrator{storage: mockStorage}

	_, err := orch.ExecutePoolMaintenance(maintTestCtx(), &commonparams.PoolMaintenanceParams{
		AccountName:   testMaintTenancyOCID,
		PoolOCID:      testMaintPoolOCID,
		InstanceOCIDs: []string{testMaintInstanceVM1},
	})

	assert.Error(t, err)
	assert.True(t, utilserrors.IsConflictErr(err))
}

func TestExecutePoolMaintenance_ClusterUpgradeActive(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	acc := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: testMaintTenancyOCID}
	mockStorage.EXPECT().GetAccount(mock.Anything, testMaintTenancyOCID).Return(acc, nil)
	mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(&datamodel.PoolView{
		Pool: datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 7, UUID: "pool-uuid"},
			State:     datamodel.LifeCycleStateREADY,
			VLMConfig: twoNodePoolVLMConfig(),
		},
	}, nil)
	mockStorage.EXPECT().GetClusterUpgradeJobsByClusterID(mock.Anything, "pool-uuid").Return(activeUpgradeJob(), nil)

	orch := &OCIOrchestrator{storage: mockStorage}

	_, err := orch.ExecutePoolMaintenance(maintTestCtx(), &commonparams.PoolMaintenanceParams{
		AccountName:   testMaintTenancyOCID,
		PoolOCID:      testMaintPoolOCID,
		InstanceOCIDs: []string{testMaintInstanceVM1},
	})

	assert.Error(t, err)
	assert.True(t, utilserrors.IsConflictErr(err))
}

func TestExecutePoolMaintenance_ResumeBlockedByUpgrade(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	acc := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: testMaintTenancyOCID}
	mockStorage.EXPECT().GetAccount(mock.Anything, testMaintTenancyOCID).Return(acc, nil)
	mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(&datamodel.PoolView{
		Pool: datamodel.Pool{
			BaseModel:  datamodel.BaseModel{ID: 7, UUID: "pool-uuid"},
			State:      datamodel.LifeCycleStateUpdating,
			WorkflowID: testSuppliedWorkflowID,
			VLMConfig:  twoNodePoolVLMConfig(),
		},
	}, nil)
	mockStorage.EXPECT().GetClusterUpgradeJobsByClusterID(mock.Anything, "pool-uuid").Return(activeUpgradeJob(), nil)
	mockStorage.EXPECT().ErroredResource(mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

	orch := &OCIOrchestrator{storage: mockStorage}

	_, err := orch.ExecutePoolMaintenance(maintTestCtx(), &commonparams.PoolMaintenanceParams{
		AccountName:   testMaintTenancyOCID,
		PoolOCID:      testMaintPoolOCID,
		InstanceOCIDs: []string{testMaintInstanceVM1},
		WorkflowID:    testSuppliedWorkflowID,
	})

	assert.Error(t, err)
	assert.True(t, utilserrors.IsConflictErr(err))
}

func TestExecutePoolMaintenance_UpdatingPoolError(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	acc := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: testMaintTenancyOCID}
	mockStorage.EXPECT().GetAccount(mock.Anything, testMaintTenancyOCID).Return(acc, nil)
	mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(&datamodel.PoolView{
		Pool: datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 7, UUID: "pool-uuid"},
			State:     datamodel.LifeCycleStateREADY,
			VLMConfig: twoNodePoolVLMConfig(),
		},
	}, nil)
	mockStorage.EXPECT().GetClusterUpgradeJobsByClusterID(mock.Anything, "pool-uuid").Return(nil, nil)
	mockStorage.EXPECT().UpdatingPool(mock.Anything, mock.Anything).Return(nil, errors.New("update failed"))

	orch := &OCIOrchestrator{storage: mockStorage}

	_, err := orch.ExecutePoolMaintenance(maintTestCtx(), &commonparams.PoolMaintenanceParams{
		AccountName:   testMaintTenancyOCID,
		PoolOCID:      testMaintPoolOCID,
		InstanceOCIDs: []string{testMaintInstanceVM1},
		WorkflowID:    testSuppliedWorkflowID,
	})

	assert.Error(t, err)
}

func TestExecutePoolMaintenance_WorkflowAlreadyStarted(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	acc := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: testMaintTenancyOCID}
	mockStorage.EXPECT().GetAccount(mock.Anything, testMaintTenancyOCID).Return(acc, nil)
	mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(&datamodel.PoolView{
		Pool: datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 7, UUID: "pool-uuid"},
			State:     datamodel.LifeCycleStateREADY,
			VLMConfig: twoNodePoolVLMConfig(),
		},
	}, nil)
	mockStorage.EXPECT().GetClusterUpgradeJobsByClusterID(mock.Anything, "pool-uuid").Return(nil, nil)
	mockStorage.EXPECT().UpdatingPool(mock.Anything, mock.Anything).
		Return(&datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 7, UUID: "pool-uuid"}}, nil)

	mockTemporal := workflowenginemock.NewMockTemporalTestClient(t)
	mockTemporal.EXPECT().
		ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, serviceerror.NewWorkflowExecutionAlreadyStarted("already started", "", ""))

	orch := &OCIOrchestrator{storage: mockStorage, temporal: mockTemporal}

	workflowID, err := orch.ExecutePoolMaintenance(maintTestCtx(), &commonparams.PoolMaintenanceParams{
		AccountName:   testMaintTenancyOCID,
		PoolOCID:      testMaintPoolOCID,
		InstanceOCIDs: []string{testMaintInstanceVM1},
		WorkflowID:    testSuppliedWorkflowID,
	})

	assert.NoError(t, err)
	assert.Equal(t, testSuppliedWorkflowID, workflowID)
}

func TestExecutePoolMaintenance_ExecuteWorkflowErrorRollsBack(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	acc := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: testMaintTenancyOCID}
	mockStorage.EXPECT().GetAccount(mock.Anything, testMaintTenancyOCID).Return(acc, nil)
	mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(&datamodel.PoolView{
		Pool: datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 7, UUID: "pool-uuid"},
			State:     datamodel.LifeCycleStateREADY,
			VLMConfig: twoNodePoolVLMConfig(),
		},
	}, nil)
	mockStorage.EXPECT().GetClusterUpgradeJobsByClusterID(mock.Anything, "pool-uuid").Return(nil, nil)
	mockStorage.EXPECT().UpdatingPool(mock.Anything, mock.Anything).
		Return(&datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 7, UUID: "pool-uuid"}}, nil)

	var rollbackCalled bool
	mockStorage.EXPECT().ErroredResource(mock.Anything, mock.Anything, mock.Anything).
		Run(func(_ context.Context, _ interface{}, _ string) { rollbackCalled = true }).
		Return(nil, nil)

	mockTemporal := workflowenginemock.NewMockTemporalTestClient(t)
	mockTemporal.EXPECT().
		ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, errors.New("temporal unavailable"))

	orch := &OCIOrchestrator{storage: mockStorage, temporal: mockTemporal}

	_, err := orch.ExecutePoolMaintenance(maintTestCtx(), &commonparams.PoolMaintenanceParams{
		AccountName:   testMaintTenancyOCID,
		PoolOCID:      testMaintPoolOCID,
		InstanceOCIDs: []string{testMaintInstanceVM1},
		WorkflowID:    testSuppliedWorkflowID,
	})

	assert.Error(t, err)
	assert.True(t, rollbackCalled)
}

func TestValidatePoolMaintenanceState(t *testing.T) {
	tests := []struct {
		name       string
		state      string
		wantErr    bool
		isConflict bool
		isBadReq   bool
	}{
		{name: "ready allowed", state: datamodel.LifeCycleStateREADY},
		{name: "error allowed", state: datamodel.LifeCycleStateError},
		{name: "deleted rejected", state: datamodel.LifeCycleStateDeleted, wantErr: true, isBadReq: true},
		{name: "updating rejected", state: datamodel.LifeCycleStateUpdating, wantErr: true, isConflict: true},
		{name: "creating rejected", state: datamodel.LifeCycleStateCreating, wantErr: true, isConflict: true},
		{name: "unknown rejected", state: "SOME_UNKNOWN_STATE", wantErr: true, isConflict: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validatePoolMaintenanceState(tc.state, testMaintPoolOCID)
			if !tc.wantErr {
				assert.NoError(t, err)
				return
			}
			assert.Error(t, err)
			if tc.isConflict {
				assert.True(t, utilserrors.IsConflictErr(err), "expected conflict, got %v", err)
			}
			if tc.isBadReq {
				assert.True(t, utilserrors.IsBadRequestErr(err), "expected bad request, got %v", err)
			}
		})
	}
}

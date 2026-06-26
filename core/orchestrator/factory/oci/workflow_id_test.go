package oci

import (
	"context"
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
)

const testSuppliedWorkflowID = "abcd1234-5678-49ab-8cde-0123456789ab"

func TestResolveWorkflowID(t *testing.T) {
	t.Run("returns the supplied id when non-empty", func(tt *testing.T) {
		assert.Equal(tt, testSuppliedWorkflowID, resolveWorkflowID(testSuppliedWorkflowID))
	})

	t.Run("trims surrounding whitespace", func(tt *testing.T) {
		assert.Equal(tt, testSuppliedWorkflowID, resolveWorkflowID("  "+testSuppliedWorkflowID+"  "))
	})

	t.Run("generates a uuid when supplied id is empty", func(tt *testing.T) {
		got := resolveWorkflowID("")
		assert.NotEmpty(tt, got)
		assert.Len(tt, got, len("xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx"))
	})

	t.Run("generates a uuid when supplied id is whitespace only", func(tt *testing.T) {
		got := resolveWorkflowID("   ")
		assert.NotEmpty(tt, got)
		assert.NotEqual(tt, "   ", got)
	})
}

func TestCreatePool_UsesSuppliedWorkflowIDAndPersistsIt(t *testing.T) {
	withOCIOntapAdminCreds(t, "admin", "Netapp1!")

	ctx := context.Background()
	ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, log.NewLogger())

	store, err := database.SetupStorageForTest(log.NewLogger())
	assert.NoError(t, err)
	assert.NoError(t, database.ClearInMemoryDB(store.DB()))

	mockTemporal := workflowenginemock.NewMockTemporalTestClient(t)
	mockTemporal.EXPECT().
		ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, nil)

	orch := &OCIOrchestrator{storage: store, temporal: mockTemporal}

	params := &commonparams.CreatePoolParams{
		AccountName:    "ocid1.tenancy.oc1..wf",
		Name:           "wf-pool",
		PoolOCID:       "ocid1.pool.oc1..wfpersist",
		SizeInBytes:    1024 * 1024 * 1024,
		VendorSubNetID: "subnet",
		PrimaryZone:    "ad1",
		WorkflowID:     testSuppliedWorkflowID,
		CustomPerformanceParams: &commonparams.CustomPerformanceParams{
			ThroughputMibps: 64,
		},
	}

	result, workflowID, err := orch.CreatePool(ctx, params)
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, testSuppliedWorkflowID, workflowID)

	view, err := store.GetPoolByName(ctx, [][]interface{}{{"vendor_id = ?", params.PoolOCID}})
	assert.NoError(t, err)
	assert.Equal(t, testSuppliedWorkflowID, view.WorkflowID)
}

func TestCreatePool_WorkflowAlreadyStarted_ReturnsSuccessWithoutCleanup(t *testing.T) {
	withOCIOntapAdminCreds(t, "admin", "Netapp1!")

	ctx := context.Background()
	ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, log.NewLogger())

	store, err := database.SetupStorageForTest(log.NewLogger())
	assert.NoError(t, err)
	assert.NoError(t, database.ClearInMemoryDB(store.DB()))

	mockTemporal := workflowenginemock.NewMockTemporalTestClient(t)
	mockTemporal.EXPECT().
		ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, serviceerror.NewWorkflowExecutionAlreadyStarted("already started", "", ""))

	orch := &OCIOrchestrator{storage: store, temporal: mockTemporal}

	params := &commonparams.CreatePoolParams{
		AccountName:    "ocid1.tenancy.oc1..dup",
		Name:           "dup-pool",
		PoolOCID:       "ocid1.pool.oc1..dup",
		SizeInBytes:    1024 * 1024 * 1024,
		VendorSubNetID: "subnet",
		PrimaryZone:    "ad1",
		WorkflowID:     testSuppliedWorkflowID,
		CustomPerformanceParams: &commonparams.CustomPerformanceParams{
			ThroughputMibps: 64,
		},
	}

	result, workflowID, err := orch.CreatePool(ctx, params)
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, testSuppliedWorkflowID, workflowID)

	view, err := store.GetPoolByName(ctx, [][]interface{}{{"vendor_id = ?", params.PoolOCID}})
	assert.NoError(t, err)
	assert.Equal(t, datamodel.LifeCycleStateCreating, view.State)
}

func TestCreatePool_GeneratesWorkflowIDWhenParamEmpty(t *testing.T) {
	withOCIOntapAdminCreds(t, "admin", "Netapp1!")

	ctx := context.Background()
	ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, log.NewLogger())

	store, err := database.SetupStorageForTest(log.NewLogger())
	assert.NoError(t, err)
	assert.NoError(t, database.ClearInMemoryDB(store.DB()))

	mockTemporal := workflowenginemock.NewMockTemporalTestClient(t)
	mockTemporal.EXPECT().
		ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, nil)

	orch := &OCIOrchestrator{storage: store, temporal: mockTemporal}

	params := &commonparams.CreatePoolParams{
		AccountName:    "ocid1.tenancy.oc1..nowf",
		Name:           "nowf-pool",
		PoolOCID:       "ocid1.pool.oc1..nowf",
		SizeInBytes:    1024 * 1024 * 1024,
		VendorSubNetID: "subnet",
		PrimaryZone:    "ad1",
		CustomPerformanceParams: &commonparams.CustomPerformanceParams{
			ThroughputMibps: 64,
		},
	}

	_, workflowID, err := orch.CreatePool(ctx, params)
	assert.NoError(t, err)
	assert.NotEmpty(t, workflowID)

	view, err := store.GetPoolByName(ctx, [][]interface{}{{"vendor_id = ?", params.PoolOCID}})
	assert.NoError(t, err)
	assert.Equal(t, workflowID, view.WorkflowID)
}

func TestDeletePool_UsesSuppliedWorkflowID(t *testing.T) {
	ctx := context.Background()
	ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, log.NewLogger())

	mockStorage := database.NewMockStorage(t)
	acc := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "ocid1.tenancy..del"}
	mockStorage.EXPECT().GetAccount(mock.Anything, "ocid1.tenancy..del").Return(acc, nil)
	mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(&datamodel.PoolView{
		Pool: datamodel.Pool{
			BaseModel:      datamodel.BaseModel{UUID: "pool-uuid"},
			State:          datamodel.LifeCycleStateREADY,
			PoolAttributes: &datamodel.PoolAttributes{PrimaryZone: "ad1", SecondaryZone: "ad2"},
		},
	}, nil)
	mockStorage.EXPECT().GetClusterUpgradeJobsByClusterID(mock.Anything, mock.Anything).Return(nil, nil)
	mockStorage.EXPECT().ActiveSvmExistsByPoolID(mock.Anything, mock.Anything).Return(false, nil)

	var deletingPool *datamodel.Pool
	mockStorage.EXPECT().DeletingPool(mock.Anything, mock.Anything).
		Run(func(_ context.Context, p *datamodel.Pool) { deletingPool = p }).
		Return(nil)

	mockTemporal := workflowenginemock.NewMockTemporalTestClient(t)
	mockTemporal.EXPECT().
		ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, nil)

	orch := &OCIOrchestrator{storage: mockStorage, temporal: mockTemporal}

	_, workflowID, err := orch.DeletePool(ctx, &commonparams.DeletePoolParams{
		AccountName: "ocid1.tenancy..del",
		PoolOCID:    "ocid1.pool.oc1..del",
		WorkflowID:  testSuppliedWorkflowID,
	})
	assert.NoError(t, err)
	assert.Equal(t, testSuppliedWorkflowID, workflowID)
	assert.NotNil(t, deletingPool)
	assert.Equal(t, testSuppliedWorkflowID, deletingPool.WorkflowID)
}

func TestDeletePool_ResumesWhenDeletingRowMatchesWorkflowID(t *testing.T) {
	ctx := context.Background()
	ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, log.NewLogger())

	mockStorage := database.NewMockStorage(t)
	acc := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "ocid1.tenancy..del"}
	mockStorage.EXPECT().GetAccount(mock.Anything, "ocid1.tenancy..del").Return(acc, nil)
	mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(&datamodel.PoolView{
		Pool: datamodel.Pool{
			BaseModel:      datamodel.BaseModel{UUID: "pool-uuid"},
			State:          datamodel.LifeCycleStateDeleting,
			WorkflowID:     testSuppliedWorkflowID,
			PoolAttributes: &datamodel.PoolAttributes{PrimaryZone: "ad1", SecondaryZone: "ad2"},
		},
	}, nil)
	mockStorage.EXPECT().GetClusterUpgradeJobsByClusterID(mock.Anything, "pool-uuid").Return(nil, nil)

	mockTemporal := workflowenginemock.NewMockTemporalTestClient(t)
	mockTemporal.EXPECT().
		ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, nil)

	orch := &OCIOrchestrator{storage: mockStorage, temporal: mockTemporal}

	_, workflowID, err := orch.DeletePool(ctx, &commonparams.DeletePoolParams{
		AccountName: "ocid1.tenancy..del",
		PoolOCID:    "ocid1.pool.oc1..del",
		WorkflowID:  testSuppliedWorkflowID,
	})
	assert.NoError(t, err)
	assert.Equal(t, testSuppliedWorkflowID, workflowID)
	mockStorage.AssertNotCalled(t, "DeletingPool", mock.Anything, mock.Anything)
}

// TestDeletePool_ResumeBypassesSvmGuard verifies that the live-SVM guard does
// NOT run on a crash-resume (pool already DELETING with a matching workflowID).
// Re-running the SVM check on resume could reject the resume on a stray/legacy
// live SVM and strand the pool in DELETING, so resume must re-drive the
// in-flight deletion without consulting SVMs. ActiveSvmExistsByPoolID is intentionally
// not stubbed: if the code called it, the mock would fail the test.
func TestDeletePool_ResumeBypassesSvmGuard(t *testing.T) {
	ctx := context.Background()
	ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, log.NewLogger())

	mockStorage := database.NewMockStorage(t)
	acc := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "ocid1.tenancy..del"}
	mockStorage.EXPECT().GetAccount(mock.Anything, "ocid1.tenancy..del").Return(acc, nil)
	mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(&datamodel.PoolView{
		Pool: datamodel.Pool{
			BaseModel:      datamodel.BaseModel{ID: 42, UUID: "pool-uuid"},
			State:          datamodel.LifeCycleStateDeleting,
			WorkflowID:     testSuppliedWorkflowID,
			PoolAttributes: &datamodel.PoolAttributes{PrimaryZone: "ad1", SecondaryZone: "ad2"},
		},
	}, nil)
	mockStorage.EXPECT().GetClusterUpgradeJobsByClusterID(mock.Anything, "pool-uuid").Return(nil, nil)

	mockTemporal := workflowenginemock.NewMockTemporalTestClient(t)
	mockTemporal.EXPECT().
		ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, nil)

	orch := &OCIOrchestrator{storage: mockStorage, temporal: mockTemporal}

	_, workflowID, err := orch.DeletePool(ctx, &commonparams.DeletePoolParams{
		AccountName: "ocid1.tenancy..del",
		PoolOCID:    "ocid1.pool.oc1..del",
		WorkflowID:  testSuppliedWorkflowID,
	})
	assert.NoError(t, err, "a crash-resume must not be blocked by the SVM guard")
	assert.Equal(t, testSuppliedWorkflowID, workflowID)
	mockStorage.AssertNotCalled(t, "ActiveSvmExistsByPoolID", mock.Anything, mock.Anything)
	mockStorage.AssertNotCalled(t, "DeletingPool", mock.Anything, mock.Anything)
}

func TestDeletePool_ConflictsWhenDeletingRowHasDifferentWorkflowID(t *testing.T) {
	ctx := context.Background()
	ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, log.NewLogger())

	mockStorage := database.NewMockStorage(t)
	acc := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "ocid1.tenancy..del"}
	mockStorage.EXPECT().GetAccount(mock.Anything, "ocid1.tenancy..del").Return(acc, nil)
	mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(&datamodel.PoolView{
		Pool: datamodel.Pool{
			BaseModel:      datamodel.BaseModel{UUID: "pool-uuid"},
			State:          datamodel.LifeCycleStateDeleting,
			WorkflowID:     "wf-other",
			PoolAttributes: &datamodel.PoolAttributes{PrimaryZone: "ad1", SecondaryZone: "ad2"},
		},
	}, nil)

	orch := &OCIOrchestrator{storage: mockStorage, temporal: workflowenginemock.NewMockTemporalTestClient(t)}

	_, _, err := orch.DeletePool(ctx, &commonparams.DeletePoolParams{
		AccountName: "ocid1.tenancy..del",
		PoolOCID:    "ocid1.pool.oc1..del",
		WorkflowID:  testSuppliedWorkflowID,
	})
	assert.Error(t, err)
	assert.True(t, utilserrors.IsConflictErr(err))
}

func TestDeleteSvm_ResumesWhenSvmDeletingMatchesWorkflowID(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	mockStorage.EXPECT().GetClusterUpgradeJobsByClusterID(mock.Anything, mock.Anything).Return(nil, nil).Maybe()
	acc := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "tenancy"}
	mockStorage.EXPECT().GetAccount(mock.Anything, "tenancy").Return(acc, nil)
	mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(&datamodel.PoolView{
		Pool: datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 10, UUID: "pool-uuid"}, AccountID: 1, WorkflowID: "wf-pool-clobbered-by-concurrent-svm"},
	}, nil)
	targetSvm := &datamodel.Svm{
		BaseModel:             datamodel.BaseModel{UUID: "svm-uuid"},
		SvmExternalIdentifier: "ocid1.svm..a",
		PoolID:                10,
		State:                 string(datamodel.LifeCycleStateDeleting),
		Name:                  "svm1",
		WorkflowID:            testSuppliedWorkflowID,
	}
	mockStorage.EXPECT().GetSvmByExternalIdentifier(mock.Anything, "ocid1.svm..a", int64(1)).Return(targetSvm, nil)

	mockTemporal := workflowenginemock.NewMockTemporalTestClient(t)
	mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, nil)

	orch := &OCIOrchestrator{storage: mockStorage, temporal: mockTemporal}
	wfID, err := orch.DeleteSvm(svmTestCtx(), &commonparams.DeleteSvmParams{
		SvmID:       "ocid1.svm..a",
		AccountName: "tenancy",
		PoolOCID:    "ocid1.pool..a",
		WorkflowID:  testSuppliedWorkflowID,
	})
	assert.NoError(t, err)
	assert.Equal(t, testSuppliedWorkflowID, wfID)
	mockStorage.AssertNotCalled(t, "TransitionSvmToDeleting", mock.Anything, mock.Anything)
	mockStorage.AssertNotCalled(t, "UpdatePoolFields", mock.Anything, mock.Anything, mock.Anything)
}

func TestCreateSvm_ResumesWhenSvmCreatingMatchesWorkflowID(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	mockStorage.EXPECT().GetClusterUpgradeJobsByClusterID(mock.Anything, mock.Anything).Return(nil, nil).Maybe()
	acc := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "tenancy"}
	mockStorage.EXPECT().GetAccount(mock.Anything, "tenancy").Return(acc, nil)
	mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(&datamodel.PoolView{
		Pool: datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 10, UUID: "pool-uuid"}, AccountID: 1, State: string(datamodel.LifeCycleStateREADY), WorkflowID: "wf-pool-clobbered-by-concurrent-svm", VLMConfig: "cfg"},
	}, nil)
	mockStorage.EXPECT().SvmExistsByExternalIdentifier(mock.Anything, "ocid1.svm..a", int64(1)).Return(true, nil)
	existingSvm := &datamodel.Svm{
		BaseModel:             datamodel.BaseModel{UUID: "svm-uuid"},
		SvmExternalIdentifier: "ocid1.svm..a",
		PoolID:                10,
		State:                 string(datamodel.LifeCycleStateCreating),
		Name:                  "svm1",
		WorkflowID:            testSuppliedWorkflowID,
	}
	mockStorage.EXPECT().GetSvmByExternalIdentifier(mock.Anything, "ocid1.svm..a", int64(1)).Return(existingSvm, nil)

	mockTemporal := workflowenginemock.NewMockTemporalTestClient(t)
	mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, nil)

	orch := &OCIOrchestrator{storage: mockStorage, temporal: mockTemporal}
	wfID, err := orch.CreateSvm(svmTestCtx(), &commonparams.CreateSvmParams{
		PoolOCID:              "ocid1.pool..a",
		SvmExternalIdentifier: "ocid1.svm..a",
		Name:                  "svm1",
		AccountName:           "tenancy",
		EnableIscsi:           true,
		WorkflowID:            testSuppliedWorkflowID,
	})
	assert.NoError(t, err)
	assert.Equal(t, testSuppliedWorkflowID, wfID)
	mockStorage.AssertNotCalled(t, "CreateSvmInCreatingState", mock.Anything, mock.Anything)
	mockStorage.AssertNotCalled(t, "UpdatePoolFields", mock.Anything, mock.Anything, mock.Anything)
}

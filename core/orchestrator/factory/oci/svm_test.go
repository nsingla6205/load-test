package oci

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	commonfactory "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/factory/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	utilserrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	workflowenginemock "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine"
	"go.temporal.io/api/serviceerror"
	"gorm.io/gorm"
)

func svmTestCtx() context.Context {
	ctx := context.Background()
	return context.WithValue(ctx, middleware.ContextSLoggerKey, log.NewLogger())
}

// ---------------------------------------------------------------------------
// CreateSvm
// ---------------------------------------------------------------------------

func TestCreateSvm_EmptyPoolOCID(t *testing.T) {
	orch := &OCIOrchestrator{
		storage:  database.NewMockStorage(t),
		temporal: workflowenginemock.NewMockTemporalTestClient(t),
	}
	_, err := orch.CreateSvm(svmTestCtx(), &commonparams.CreateSvmParams{
		PoolOCID:              "",
		SvmExternalIdentifier: "ocid1.svm..a",
		Name:                  "svm1",
		AccountName:           "tenancy",
	})
	require.Error(t, err)
	assert.True(t, utilserrors.IsBadRequestErr(err))
	assert.Contains(t, err.Error(), "PoolOCID")
}

func TestCreateSvm_EmptySvmOCID(t *testing.T) {
	orch := &OCIOrchestrator{
		storage:  database.NewMockStorage(t),
		temporal: workflowenginemock.NewMockTemporalTestClient(t),
	}
	_, err := orch.CreateSvm(svmTestCtx(), &commonparams.CreateSvmParams{
		PoolOCID:              "ocid1.pool..a",
		SvmExternalIdentifier: "",
		Name:                  "svm1",
		AccountName:           "tenancy",
	})
	require.Error(t, err)
	assert.True(t, utilserrors.IsBadRequestErr(err))
	assert.Contains(t, err.Error(), "SvmOCID")
}

func TestCreateSvm_EmptyName(t *testing.T) {
	orch := &OCIOrchestrator{
		storage:  database.NewMockStorage(t),
		temporal: workflowenginemock.NewMockTemporalTestClient(t),
	}
	_, err := orch.CreateSvm(svmTestCtx(), &commonparams.CreateSvmParams{
		PoolOCID:              "ocid1.pool..a",
		SvmExternalIdentifier: "ocid1.svm..a",
		Name:                  "",
		AccountName:           "tenancy",
	})
	require.Error(t, err)
	assert.True(t, utilserrors.IsBadRequestErr(err))
	assert.Contains(t, err.Error(), "Name")
}

func TestCreateSvm_EmptyAccountName(t *testing.T) {
	orch := &OCIOrchestrator{
		storage:  database.NewMockStorage(t),
		temporal: workflowenginemock.NewMockTemporalTestClient(t),
	}
	_, err := orch.CreateSvm(svmTestCtx(), &commonparams.CreateSvmParams{
		PoolOCID:              "ocid1.pool..a",
		SvmExternalIdentifier: "ocid1.svm..a",
		Name:                  "svm1",
		AccountName:           "   ",
	})
	require.Error(t, err)
	assert.True(t, utilserrors.IsBadRequestErr(err))
	assert.Contains(t, err.Error(), "Tenancy-Ocid")
}

func TestCreateSvm_GetOrCreateAccountFails(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	mockStorage.EXPECT().GetAccount(mock.Anything, "tenancy").Return(nil, gorm.ErrRecordNotFound)
	mockStorage.EXPECT().CreateAccount(mock.Anything, mock.Anything).Return(nil, errors.New("create failed"))
	mockStorage.EXPECT().GetAccount(mock.Anything, "tenancy").Return(nil, errors.New("still down"))

	orch := &OCIOrchestrator{storage: mockStorage, temporal: workflowenginemock.NewMockTemporalTestClient(t)}
	_, err := orch.CreateSvm(svmTestCtx(), &commonparams.CreateSvmParams{
		PoolOCID:              "ocid1.pool..a",
		SvmExternalIdentifier: "ocid1.svm..a",
		Name:                  "svm1",
		AccountName:           "tenancy",
	})
	require.Error(t, err)
}

func TestCreateSvm_DefaultProtocol(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	acc := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "tenancy"}
	mockStorage.EXPECT().GetAccount(mock.Anything, "tenancy").Return(acc, nil)
	mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(nil, gorm.ErrRecordNotFound)

	orch := &OCIOrchestrator{storage: mockStorage, temporal: workflowenginemock.NewMockTemporalTestClient(t)}
	params := &commonparams.CreateSvmParams{
		PoolOCID:              "ocid1.pool..a",
		SvmExternalIdentifier: "ocid1.svm..a",
		Name:                  "svm1",
		AccountName:           "tenancy",
	}
	_, _ = orch.CreateSvm(svmTestCtx(), params)

	assert.True(t, params.EnableIscsi)
	assert.Empty(t, params.IPSpace, "factory must not default IPspace; activity reads it from VLM config")
}

func TestCreateSvm_PoolNotFound(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	acc := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "tenancy"}
	mockStorage.EXPECT().GetAccount(mock.Anything, "tenancy").Return(acc, nil)
	mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(nil, gorm.ErrRecordNotFound)

	orch := &OCIOrchestrator{storage: mockStorage, temporal: workflowenginemock.NewMockTemporalTestClient(t)}
	_, err := orch.CreateSvm(svmTestCtx(), &commonparams.CreateSvmParams{
		PoolOCID:              "ocid1.pool..a",
		SvmExternalIdentifier: "ocid1.svm..a",
		Name:                  "svm1",
		AccountName:           "tenancy",
	})
	require.Error(t, err)
	assert.True(t, utilserrors.IsNotFoundErr(err))
}

func TestCreateSvm_GetPoolByNameGenericError(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	acc := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "tenancy"}
	mockStorage.EXPECT().GetAccount(mock.Anything, "tenancy").Return(acc, nil)
	mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(nil, errors.New("db error"))

	orch := &OCIOrchestrator{storage: mockStorage, temporal: workflowenginemock.NewMockTemporalTestClient(t)}
	_, err := orch.CreateSvm(svmTestCtx(), &commonparams.CreateSvmParams{
		PoolOCID:              "ocid1.pool..a",
		SvmExternalIdentifier: "ocid1.svm..a",
		Name:                  "svm1",
		AccountName:           "tenancy",
	})
	require.Error(t, err)
	assert.Equal(t, "db error", err.Error())
}

// TestCreateSvm_DuplicateSvmOCID verifies the factory's quiet pre-check
// short-circuits with 409 before doing any further validation work when an
// SVM with the same OCID already exists.
func TestCreateSvm_DuplicateSvmOCID(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	acc := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "tenancy"}
	mockStorage.EXPECT().GetAccount(mock.Anything, "tenancy").Return(acc, nil)
	poolView := &datamodel.PoolView{
		Pool: datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 10},
			AccountID: 1,
			State:     string(datamodel.LifeCycleStateREADY),
			VLMConfig: "cfg",
		},
	}
	mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(poolView, nil)
	mockStorage.EXPECT().SvmExistsByExternalIdentifier(mock.Anything, "ocid1.svm..a", int64(1)).
		Return(true, nil)
	mockStorage.EXPECT().GetSvmByExternalIdentifier(mock.Anything, "ocid1.svm..a", int64(1)).
		Return(&datamodel.Svm{BaseModel: datamodel.BaseModel{UUID: "svm-uuid"}, State: string(datamodel.LifeCycleStateREADY)}, nil)

	orch := &OCIOrchestrator{storage: mockStorage, temporal: workflowenginemock.NewMockTemporalTestClient(t)}
	_, err := orch.CreateSvm(svmTestCtx(), &commonparams.CreateSvmParams{
		PoolOCID:              "ocid1.pool..a",
		SvmExternalIdentifier: "ocid1.svm..a",
		Name:                  "svm1",
		AccountName:           "tenancy",
		EnableIscsi:           true,
	})
	require.Error(t, err)
	assert.True(t, utilserrors.IsConflictErr(err))
}

// TestCreateSvm_PreCheckLookupError verifies that an unexpected error from
// the existence check (not a benign no-rows case) propagates out of the
// factory.
func TestCreateSvm_PreCheckLookupError(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	acc := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "tenancy"}
	mockStorage.EXPECT().GetAccount(mock.Anything, "tenancy").Return(acc, nil)
	mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(&datamodel.PoolView{
		Pool: datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 10}, AccountID: 1},
	}, nil)
	mockStorage.EXPECT().SvmExistsByExternalIdentifier(mock.Anything, "ocid1.svm..a", int64(1)).
		Return(false, errors.New("lookup failed"))

	orch := &OCIOrchestrator{storage: mockStorage, temporal: workflowenginemock.NewMockTemporalTestClient(t)}
	_, err := orch.CreateSvm(svmTestCtx(), &commonparams.CreateSvmParams{
		PoolOCID:              "ocid1.pool..a",
		SvmExternalIdentifier: "ocid1.svm..a",
		Name:                  "svm1",
		AccountName:           "tenancy",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "lookup failed")
}

func TestCreateSvm_NotFoundProceedsWithCreate(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	acc := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "tenancy"}
	mockStorage.EXPECT().GetAccount(mock.Anything, "tenancy").Return(acc, nil)
	poolView := &datamodel.PoolView{
		Pool: datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 10},
			AccountID: 1,
			State:     string(datamodel.LifeCycleStateREADY),
			VLMConfig: "cfg",
		},
	}
	mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(poolView, nil)
	mockStorage.EXPECT().SvmExistsByExternalIdentifier(mock.Anything, "ocid1.svm..a", int64(1)).
		Return(false, nil)
	mockStorage.EXPECT().GetNodesByPoolID(mock.Anything, mock.Anything).Return([]*datamodel.Node{
		{BaseModel: datamodel.BaseModel{ID: 1}, Name: "n1", State: datamodel.LifeCycleStateREADY},
		{BaseModel: datamodel.BaseModel{ID: 2}, Name: "n2", State: datamodel.LifeCycleStateREADY},
	}, nil)
	mockStorage.EXPECT().GetSvmByNameAndPoolID(mock.Anything, "svm1", int64(10)).
		Return(nil, utilserrors.NewNotFoundErr("svm", nil))
	mockStorage.EXPECT().GetNodesByPoolID(mock.Anything, mock.Anything).Return([]*datamodel.Node{
		{BaseModel: datamodel.BaseModel{ID: 1}, Name: "n1", State: datamodel.LifeCycleStateREADY},
		{BaseModel: datamodel.BaseModel{ID: 2}, Name: "n2", State: datamodel.LifeCycleStateREADY},
	}, nil)
	preallocatedSvm := &datamodel.Svm{
		BaseModel:             datamodel.BaseModel{UUID: "svm-uuid"},
		Name:                  "svm1",
		SvmExternalIdentifier: "ocid1.svm..a",
		State:                 datamodel.LifeCycleStateCreating,
	}
	mockStorage.EXPECT().CreateSvmInCreatingState(mock.Anything, mock.Anything).Return(preallocatedSvm, nil)
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
	})
	assert.NoError(t, err)
	assert.NotEmpty(t, wfID)
}

// Two concurrent CreateSvm calls for the same SvmOCID are decided atomically
// by CreateSvmInCreatingState (backed by the partial unique index on
// svm_external_identifier). The loser receives a typed ConflictErr that the
// API translates to 409 instead of returning 202 in_process. This guards
// against the original race where the pre-check pass and the loser wound up
// stranded in the workflow.
func TestCreateSvm_RaceLosesAtPreallocationReturnsConflict(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	acc := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "tenancy"}
	mockStorage.EXPECT().GetAccount(mock.Anything, "tenancy").Return(acc, nil)
	poolView := &datamodel.PoolView{
		Pool: datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 10},
			AccountID: 1,
			State:     string(datamodel.LifeCycleStateREADY),
			VLMConfig: "cfg",
		},
	}
	mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(poolView, nil)
	mockStorage.EXPECT().SvmExistsByExternalIdentifier(mock.Anything, "ocid1.svm..race", int64(1)).
		Return(false, nil)
	mockStorage.EXPECT().GetNodesByPoolID(mock.Anything, mock.Anything).Return([]*datamodel.Node{
		{BaseModel: datamodel.BaseModel{ID: 1}, Name: "n1", State: datamodel.LifeCycleStateREADY},
		{BaseModel: datamodel.BaseModel{ID: 2}, Name: "n2", State: datamodel.LifeCycleStateREADY},
	}, nil)
	mockStorage.EXPECT().GetSvmByNameAndPoolID(mock.Anything, "svm1", int64(10)).
		Return(nil, utilserrors.NewNotFoundErr("svm", nil))
	mockStorage.EXPECT().GetNodesByPoolID(mock.Anything, mock.Anything).Return([]*datamodel.Node{
		{BaseModel: datamodel.BaseModel{ID: 1}, Name: "n1", State: datamodel.LifeCycleStateREADY},
		{BaseModel: datamodel.BaseModel{ID: 2}, Name: "n2", State: datamodel.LifeCycleStateREADY},
	}, nil)
	mockStorage.EXPECT().CreateSvmInCreatingState(mock.Anything, mock.Anything).
		Return(nil, utilserrors.NewConflictErr("svm already exists"))

	orch := &OCIOrchestrator{storage: mockStorage, temporal: workflowenginemock.NewMockTemporalTestClient(t)}
	_, err := orch.CreateSvm(svmTestCtx(), &commonparams.CreateSvmParams{
		PoolOCID:              "ocid1.pool..a",
		SvmExternalIdentifier: "ocid1.svm..race",
		Name:                  "svm1",
		AccountName:           "tenancy",
		EnableIscsi:           true,
	})
	require.Error(t, err)
	assert.True(t, utilserrors.IsConflictErr(err))
}

// TestCreateSvm_NameAlreadyInUseInPool verifies that when a non-DELETED SVM
// with the same name already exists in the same pool, CreateSvm rejects the
// request with ConflictErr before pre-allocating a row or starting the
// workflow. A prior SVM in DELETED state is filtered out by GetSvmByNameAndPoolID
// (default scope), so the same name can be reused after a successful delete.
func TestCreateSvm_NameAlreadyInUseInPool(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	acc := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "tenancy"}
	mockStorage.EXPECT().GetAccount(mock.Anything, "tenancy").Return(acc, nil)
	poolView := &datamodel.PoolView{
		Pool: datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 10},
			AccountID: 1,
			State:     string(datamodel.LifeCycleStateREADY),
			VLMConfig: "cfg",
		},
	}
	mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(poolView, nil)
	mockStorage.EXPECT().SvmExistsByExternalIdentifier(mock.Anything, "ocid1.svm..new", int64(1)).
		Return(false, nil)
	mockStorage.EXPECT().GetNodesByPoolID(mock.Anything, mock.Anything).Return([]*datamodel.Node{
		{BaseModel: datamodel.BaseModel{ID: 1}, Name: "n1", State: datamodel.LifeCycleStateREADY},
		{BaseModel: datamodel.BaseModel{ID: 2}, Name: "n2", State: datamodel.LifeCycleStateREADY},
	}, nil)
	mockStorage.EXPECT().GetSvmByNameAndPoolID(mock.Anything, "svm1", int64(10)).
		Return(&datamodel.Svm{Name: "svm1", PoolID: 10, State: datamodel.LifeCycleStateREADY}, nil)

	orch := &OCIOrchestrator{storage: mockStorage, temporal: workflowenginemock.NewMockTemporalTestClient(t)}
	_, err := orch.CreateSvm(svmTestCtx(), &commonparams.CreateSvmParams{
		PoolOCID:              "ocid1.pool..a",
		SvmExternalIdentifier: "ocid1.svm..new",
		Name:                  "svm1",
		AccountName:           "tenancy",
		EnableIscsi:           true,
	})
	require.Error(t, err)
	assert.True(t, utilserrors.IsConflictErr(err))
	assert.Contains(t, err.Error(), "already exists in this pool")
}

func TestCreateSvm_ValidationFails(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	acc := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "tenancy"}
	mockStorage.EXPECT().GetAccount(mock.Anything, "tenancy").Return(acc, nil)
	// Pool not in READY state triggers validation failure
	poolView := &datamodel.PoolView{
		Pool: datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 10},
			AccountID: 1,
			State:     "CREATING",
			VLMConfig: "cfg",
		},
	}
	mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(poolView, nil)
	mockStorage.EXPECT().SvmExistsByExternalIdentifier(mock.Anything, "ocid1.svm..new", int64(1)).
		Return(false, nil)

	orch := &OCIOrchestrator{storage: mockStorage, temporal: workflowenginemock.NewMockTemporalTestClient(t)}
	_, err := orch.CreateSvm(svmTestCtx(), &commonparams.CreateSvmParams{
		PoolOCID:              "ocid1.pool..a",
		SvmExternalIdentifier: "ocid1.svm..new",
		Name:                  "svm1",
		AccountName:           "tenancy",
		EnableIscsi:           true,
	})
	require.Error(t, err)
	assert.True(t, utilserrors.IsConflictErr(err))
}

func TestCreateSvm_WorkflowStartFails(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	acc := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "tenancy"}
	mockStorage.EXPECT().GetAccount(mock.Anything, "tenancy").Return(acc, nil)
	poolView := &datamodel.PoolView{
		Pool: datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 10},
			AccountID: 1,
			State:     string(datamodel.LifeCycleStateREADY),
			VLMConfig: "cfg",
		},
	}
	mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(poolView, nil)
	mockStorage.EXPECT().SvmExistsByExternalIdentifier(mock.Anything, "ocid1.svm..new", int64(1)).
		Return(false, nil)
	mockStorage.EXPECT().GetNodesByPoolID(mock.Anything, mock.Anything).Return([]*datamodel.Node{
		{BaseModel: datamodel.BaseModel{ID: 1}, Name: "n1", State: datamodel.LifeCycleStateREADY},
		{BaseModel: datamodel.BaseModel{ID: 2}, Name: "n2", State: datamodel.LifeCycleStateREADY},
	}, nil)
	mockStorage.EXPECT().GetSvmByNameAndPoolID(mock.Anything, "svm1", int64(10)).
		Return(nil, utilserrors.NewNotFoundErr("svm", nil))
	mockStorage.EXPECT().GetNodesByPoolID(mock.Anything, mock.Anything).Return([]*datamodel.Node{
		{BaseModel: datamodel.BaseModel{ID: 1}, Name: "n1", State: datamodel.LifeCycleStateREADY},
		{BaseModel: datamodel.BaseModel{ID: 2}, Name: "n2", State: datamodel.LifeCycleStateREADY},
	}, nil)
	preallocatedSvm := &datamodel.Svm{
		BaseModel:             datamodel.BaseModel{UUID: "svm-uuid"},
		Name:                  "svm1",
		SvmExternalIdentifier: "ocid1.svm..new",
		State:                 datamodel.LifeCycleStateCreating,
	}
	mockStorage.EXPECT().CreateSvmInCreatingState(mock.Anything, mock.Anything).Return(preallocatedSvm, nil) // Workflow start failure must trigger compensation: the row we just
	// inserted in CREATING has no workflow driving it, so the factory must
	// flip it to ERROR before returning.
	compensationCalled := false
	mockStorage.EXPECT().ErroredSVM(mock.Anything, preallocatedSvm, mock.Anything).
		Run(func(_ context.Context, _ *datamodel.Svm, _ string) { compensationCalled = true }).
		Return(nil)

	mockTemporal := workflowenginemock.NewMockTemporalTestClient(t)
	mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, errors.New("temporal unavailable"))

	orch := &OCIOrchestrator{storage: mockStorage, temporal: mockTemporal}
	_, err := orch.CreateSvm(svmTestCtx(), &commonparams.CreateSvmParams{
		PoolOCID:              "ocid1.pool..a",
		SvmExternalIdentifier: "ocid1.svm..new",
		Name:                  "svm1",
		AccountName:           "tenancy",
		EnableIscsi:           true,
	})
	require.Error(t, err)
	assert.True(t, compensationCalled, "ErroredSVM compensation must run when workflow start fails")
}

func TestCreateSvm_Success(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	acc := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "tenancy"}
	mockStorage.EXPECT().GetAccount(mock.Anything, "tenancy").Return(acc, nil)
	poolView := &datamodel.PoolView{
		Pool: datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 10, UUID: "pool-uuid"},
			AccountID: 1,
			State:     string(datamodel.LifeCycleStateREADY),
			VLMConfig: "cfg",
		},
	}
	mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(poolView, nil)
	mockStorage.EXPECT().SvmExistsByExternalIdentifier(mock.Anything, "ocid1.svm..new", int64(1)).
		Return(false, nil)
	mockStorage.EXPECT().GetNodesByPoolID(mock.Anything, mock.Anything).Return([]*datamodel.Node{
		{BaseModel: datamodel.BaseModel{ID: 1}, Name: "n1", State: datamodel.LifeCycleStateREADY},
		{BaseModel: datamodel.BaseModel{ID: 2}, Name: "n2", State: datamodel.LifeCycleStateREADY},
	}, nil)
	mockStorage.EXPECT().GetSvmByNameAndPoolID(mock.Anything, "svm1", int64(10)).
		Return(nil, utilserrors.NewNotFoundErr("svm", nil))
	mockStorage.EXPECT().GetNodesByPoolID(mock.Anything, mock.Anything).Return([]*datamodel.Node{
		{BaseModel: datamodel.BaseModel{ID: 1}, Name: "n1", State: datamodel.LifeCycleStateREADY},
		{BaseModel: datamodel.BaseModel{ID: 2}, Name: "n2", State: datamodel.LifeCycleStateREADY},
	}, nil)
	preallocatedSvm := &datamodel.Svm{
		BaseModel:             datamodel.BaseModel{UUID: "svm-uuid"},
		Name:                  "svm1",
		SvmExternalIdentifier: "ocid1.svm..new",
		State:                 datamodel.LifeCycleStateCreating,
	}
	mockStorage.EXPECT().CreateSvmInCreatingState(mock.Anything, mock.Anything).Return(preallocatedSvm, nil)
	mockTemporal := workflowenginemock.NewMockTemporalTestClient(t)
	mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, nil)

	orch := &OCIOrchestrator{storage: mockStorage, temporal: mockTemporal}
	wfID, err := orch.CreateSvm(svmTestCtx(), &commonparams.CreateSvmParams{
		PoolOCID:              "ocid1.pool..a",
		SvmExternalIdentifier: "ocid1.svm..new",
		Name:                  "svm1",
		AccountName:           "tenancy",
		EnableIscsi:           true,
	})
	assert.NoError(t, err)
	assert.NotEmpty(t, wfID)
}

// ---------------------------------------------------------------------------
// DeleteSvm
// ---------------------------------------------------------------------------

func TestDeleteSvm_EmptySvmID(t *testing.T) {
	orch := &OCIOrchestrator{
		storage:  database.NewMockStorage(t),
		temporal: workflowenginemock.NewMockTemporalTestClient(t),
	}
	_, err := orch.DeleteSvm(svmTestCtx(), &commonparams.DeleteSvmParams{
		SvmID:       "",
		AccountName: "tenancy",
		PoolOCID:    "ocid1.pool..a",
	})
	require.Error(t, err)
	assert.True(t, utilserrors.IsBadRequestErr(err))
}

func TestDeleteSvm_EmptyAccountName(t *testing.T) {
	orch := &OCIOrchestrator{
		storage:  database.NewMockStorage(t),
		temporal: workflowenginemock.NewMockTemporalTestClient(t),
	}
	_, err := orch.DeleteSvm(svmTestCtx(), &commonparams.DeleteSvmParams{
		SvmID:       "ocid1.svm..a",
		AccountName: "",
		PoolOCID:    "ocid1.pool..a",
	})
	require.Error(t, err)
	assert.True(t, utilserrors.IsBadRequestErr(err))
}

func TestDeleteSvm_EmptyPoolOCID(t *testing.T) {
	orch := &OCIOrchestrator{
		storage:  database.NewMockStorage(t),
		temporal: workflowenginemock.NewMockTemporalTestClient(t),
	}
	_, err := orch.DeleteSvm(svmTestCtx(), &commonparams.DeleteSvmParams{
		SvmID:       "ocid1.svm..a",
		AccountName: "tenancy",
		PoolOCID:    "  ",
	})
	require.Error(t, err)
	assert.True(t, utilserrors.IsBadRequestErr(err))
}

func TestDeleteSvm_GetOrCreateAccountFails(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	mockStorage.EXPECT().GetAccount(mock.Anything, "tenancy").Return(nil, gorm.ErrRecordNotFound)
	mockStorage.EXPECT().CreateAccount(mock.Anything, mock.Anything).Return(nil, errors.New("create failed"))
	mockStorage.EXPECT().GetAccount(mock.Anything, "tenancy").Return(nil, errors.New("still down"))

	orch := &OCIOrchestrator{storage: mockStorage, temporal: workflowenginemock.NewMockTemporalTestClient(t)}
	_, err := orch.DeleteSvm(svmTestCtx(), &commonparams.DeleteSvmParams{
		SvmID:       "ocid1.svm..a",
		AccountName: "tenancy",
		PoolOCID:    "ocid1.pool..a",
	})
	require.Error(t, err)
}

func TestDeleteSvm_PoolNotFound(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	acc := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "tenancy"}
	mockStorage.EXPECT().GetAccount(mock.Anything, "tenancy").Return(acc, nil)
	mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(nil, gorm.ErrRecordNotFound)

	orch := &OCIOrchestrator{storage: mockStorage, temporal: workflowenginemock.NewMockTemporalTestClient(t)}
	_, err := orch.DeleteSvm(svmTestCtx(), &commonparams.DeleteSvmParams{
		SvmID:       "ocid1.svm..a",
		AccountName: "tenancy",
		PoolOCID:    "ocid1.pool..a",
	})
	require.Error(t, err)
	assert.True(t, utilserrors.IsNotFoundErr(err))
}

func TestDeleteSvm_GetPoolGenericError(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	acc := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "tenancy"}
	mockStorage.EXPECT().GetAccount(mock.Anything, "tenancy").Return(acc, nil)
	mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(nil, errors.New("db error"))

	orch := &OCIOrchestrator{storage: mockStorage, temporal: workflowenginemock.NewMockTemporalTestClient(t)}
	_, err := orch.DeleteSvm(svmTestCtx(), &commonparams.DeleteSvmParams{
		SvmID:       "ocid1.svm..a",
		AccountName: "tenancy",
		PoolOCID:    "ocid1.pool..a",
	})
	require.Error(t, err)
	assert.Equal(t, "db error", err.Error())
}

func TestDeleteSvm_GetSvmByExternalIdentifierError(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	acc := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "tenancy"}
	mockStorage.EXPECT().GetAccount(mock.Anything, "tenancy").Return(acc, nil)
	mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(&datamodel.PoolView{
		Pool: datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 10}, AccountID: 1},
	}, nil)
	mockStorage.EXPECT().GetSvmByExternalIdentifier(mock.Anything, "ocid1.svm..a", int64(1)).
		Return(nil, errors.New("lookup failed"))

	orch := &OCIOrchestrator{storage: mockStorage, temporal: workflowenginemock.NewMockTemporalTestClient(t)}
	_, err := orch.DeleteSvm(svmTestCtx(), &commonparams.DeleteSvmParams{
		SvmID:       "ocid1.svm..a",
		AccountName: "tenancy",
		PoolOCID:    "ocid1.pool..a",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "lookup failed")
}

func TestDeleteSvm_SvmNotFound(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	acc := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "tenancy"}
	mockStorage.EXPECT().GetAccount(mock.Anything, "tenancy").Return(acc, nil)
	mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(&datamodel.PoolView{
		Pool: datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 10}, AccountID: 1},
	}, nil)
	mockStorage.EXPECT().GetSvmByExternalIdentifier(mock.Anything, "ocid1.svm..a", int64(1)).
		Return(nil, utilserrors.NewNotFoundErr("svm", nil))

	orch := &OCIOrchestrator{storage: mockStorage, temporal: workflowenginemock.NewMockTemporalTestClient(t)}
	_, err := orch.DeleteSvm(svmTestCtx(), &commonparams.DeleteSvmParams{
		SvmID:       "ocid1.svm..a",
		AccountName: "tenancy",
		PoolOCID:    "ocid1.pool..a",
	})
	require.Error(t, err)
	assert.True(t, utilserrors.IsNotFoundErr(err))
}

func TestDeleteSvm_SvmBelongsToDifferentPool(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	acc := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "tenancy"}
	mockStorage.EXPECT().GetAccount(mock.Anything, "tenancy").Return(acc, nil)
	mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(&datamodel.PoolView{
		Pool: datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 10}, AccountID: 1},
	}, nil)
	svm := &datamodel.Svm{SvmExternalIdentifier: "ocid1.svm..a", PoolID: 99}
	mockStorage.EXPECT().GetSvmByExternalIdentifier(mock.Anything, "ocid1.svm..a", int64(1)).Return(svm, nil)

	orch := &OCIOrchestrator{storage: mockStorage, temporal: workflowenginemock.NewMockTemporalTestClient(t)}
	_, err := orch.DeleteSvm(svmTestCtx(), &commonparams.DeleteSvmParams{
		SvmID:       "ocid1.svm..a",
		AccountName: "tenancy",
		PoolOCID:    "ocid1.pool..a",
	})
	require.Error(t, err)
	assert.True(t, utilserrors.IsNotFoundErr(err))
}

func TestDeleteSvm_ConflictWhenDeleting(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	acc := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "tenancy"}
	mockStorage.EXPECT().GetAccount(mock.Anything, "tenancy").Return(acc, nil)
	mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(&datamodel.PoolView{
		Pool: datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 10}, AccountID: 1},
	}, nil)
	svm := &datamodel.Svm{
		SvmExternalIdentifier: "ocid1.svm..a",
		PoolID:                10,
		State:                 string(datamodel.LifeCycleStateDeleting),
	}
	mockStorage.EXPECT().GetSvmByExternalIdentifier(mock.Anything, "ocid1.svm..a", int64(1)).Return(svm, nil)

	orch := &OCIOrchestrator{storage: mockStorage, temporal: workflowenginemock.NewMockTemporalTestClient(t)}
	_, err := orch.DeleteSvm(svmTestCtx(), &commonparams.DeleteSvmParams{
		SvmID:       "ocid1.svm..a",
		AccountName: "tenancy",
		PoolOCID:    "ocid1.pool..a",
	})
	require.Error(t, err)
	assert.True(t, utilserrors.IsConflictErr(err))
}

func TestDeleteSvm_ConflictWhenCreating(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	acc := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "tenancy"}
	mockStorage.EXPECT().GetAccount(mock.Anything, "tenancy").Return(acc, nil)
	mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(&datamodel.PoolView{
		Pool: datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 10}, AccountID: 1},
	}, nil)
	svm := &datamodel.Svm{
		SvmExternalIdentifier: "ocid1.svm..a",
		PoolID:                10,
		State:                 string(datamodel.LifeCycleStateCreating),
	}
	mockStorage.EXPECT().GetSvmByExternalIdentifier(mock.Anything, "ocid1.svm..a", int64(1)).Return(svm, nil)

	orch := &OCIOrchestrator{storage: mockStorage, temporal: workflowenginemock.NewMockTemporalTestClient(t)}
	_, err := orch.DeleteSvm(svmTestCtx(), &commonparams.DeleteSvmParams{
		SvmID:       "ocid1.svm..a",
		AccountName: "tenancy",
		PoolOCID:    "ocid1.pool..a",
	})
	require.Error(t, err)
	assert.True(t, utilserrors.IsConflictErr(err))
}

// Matrix Section 5: when GetSvmByExternalIdentifier (Unscoped) returns a
// soft-deleted row, validateSvmDeletionState must surface a 404
// "svm deleted already" rather than attempting another deletion. This is the
// terminal state for the OCID — the partial unique index keeps the slot
// occupied, but from the API's perspective the SVM no longer exists.
func TestDeleteSvm_NotFoundWhenSoftDeleted(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	acc := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "tenancy"}
	mockStorage.EXPECT().GetAccount(mock.Anything, "tenancy").Return(acc, nil)
	mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(&datamodel.PoolView{
		Pool: datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 10}, AccountID: 1},
	}, nil)
	svm := &datamodel.Svm{
		SvmExternalIdentifier: "ocid1.svm..a",
		PoolID:                10,
		State:                 datamodel.LifeCycleStateDeleted,
	}
	mockStorage.EXPECT().GetSvmByExternalIdentifier(mock.Anything, "ocid1.svm..a", int64(1)).Return(svm, nil)

	orch := &OCIOrchestrator{storage: mockStorage, temporal: workflowenginemock.NewMockTemporalTestClient(t)}
	_, err := orch.DeleteSvm(svmTestCtx(), &commonparams.DeleteSvmParams{
		SvmID:       "ocid1.svm..a",
		AccountName: "tenancy",
		PoolOCID:    "ocid1.pool..a",
	})
	require.Error(t, err)
	assert.True(t, utilserrors.IsNotFoundErr(err), "expected NotFoundErr (404), got %T: %v", err, err)
}

// Two concurrent DELETE calls for the same SVM both pass
// validateSvmDeletionState (the SVM is READY in the SELECT), but only one
// wins the atomic CAS in TransitionSvmToDeleting. The loser receives a typed
// ConflictErr that the API translates to 409 instead of starting a second
// workflow against an already-DELETING row.
func TestDeleteSvm_RaceLosesAtTransitionReturnsConflict(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	acc := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "tenancy"}
	mockStorage.EXPECT().GetAccount(mock.Anything, "tenancy").Return(acc, nil)
	mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(&datamodel.PoolView{
		Pool: datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 10}, AccountID: 1},
	}, nil)
	svm := &datamodel.Svm{
		BaseModel:             datamodel.BaseModel{UUID: "svm-uuid"},
		SvmExternalIdentifier: "ocid1.svm..a",
		PoolID:                10,
		State:                 string(datamodel.LifeCycleStateREADY),
		Name:                  "svm1",
	}
	mockStorage.EXPECT().GetSvmByExternalIdentifier(mock.Anything, "ocid1.svm..a", int64(1)).Return(svm, nil)
	mockStorage.EXPECT().TransitionSvmToDeleting(mock.Anything, svm).
		Return(nil, utilserrors.NewConflictErr("SVM delete is already in progress"))

	orch := &OCIOrchestrator{storage: mockStorage, temporal: workflowenginemock.NewMockTemporalTestClient(t)}
	_, err := orch.DeleteSvm(svmTestCtx(), &commonparams.DeleteSvmParams{
		SvmID:       "ocid1.svm..a",
		AccountName: "tenancy",
		PoolOCID:    "ocid1.pool..a",
	})
	require.Error(t, err)
	assert.True(t, utilserrors.IsConflictErr(err))
}

// TransitionSvmToDeleting returns a non-conflict error (e.g. DB outage). The
// orchestrator must propagate the raw error and must NOT invoke ErroredSVM —
// no row was flipped to DELETING, so the compensation defer's `deletingSvm !=
// nil` guard keeps it from running.
func TestDeleteSvm_TransitionGenericError(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	acc := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "tenancy"}
	mockStorage.EXPECT().GetAccount(mock.Anything, "tenancy").Return(acc, nil)
	mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(&datamodel.PoolView{
		Pool: datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 10}, AccountID: 1},
	}, nil)
	svm := &datamodel.Svm{
		BaseModel:             datamodel.BaseModel{UUID: "svm-uuid"},
		SvmExternalIdentifier: "ocid1.svm..a",
		PoolID:                10,
		State:                 string(datamodel.LifeCycleStateREADY),
		Name:                  "svm1",
	}
	mockStorage.EXPECT().GetSvmByExternalIdentifier(mock.Anything, "ocid1.svm..a", int64(1)).Return(svm, nil)

	dbErr := errors.New("db down")
	mockStorage.EXPECT().TransitionSvmToDeleting(mock.Anything, svm).Return(nil, dbErr)
	// Intentionally no .EXPECT().ErroredSVM(...): mockery will fail the test
	// if it is called, locking in the deletingSvm-nil guard behaviour.

	orch := &OCIOrchestrator{storage: mockStorage, temporal: workflowenginemock.NewMockTemporalTestClient(t)}
	_, err := orch.DeleteSvm(svmTestCtx(), &commonparams.DeleteSvmParams{
		SvmID:       "ocid1.svm..a",
		AccountName: "tenancy",
		PoolOCID:    "ocid1.pool..a",
	})
	require.Error(t, err)
	assert.Equal(t, dbErr, err, "non-conflict transition error must propagate verbatim")
	assert.False(t, utilserrors.IsConflictErr(err), "must not be wrapped as ConflictErr")
}

func TestDeleteSvm_WorkflowStartFails(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	acc := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "tenancy"}
	mockStorage.EXPECT().GetAccount(mock.Anything, "tenancy").Return(acc, nil)
	mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(&datamodel.PoolView{
		Pool: datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 10}, AccountID: 1},
	}, nil)
	svm := &datamodel.Svm{
		BaseModel:             datamodel.BaseModel{UUID: "svm-uuid"},
		SvmExternalIdentifier: "ocid1.svm..a",
		PoolID:                10,
		State:                 string(datamodel.LifeCycleStateREADY),
		Name:                  "svm1",
	}
	mockStorage.EXPECT().GetSvmByExternalIdentifier(mock.Anything, "ocid1.svm..a", int64(1)).Return(svm, nil)

	deletingSvm := &datamodel.Svm{
		BaseModel:             datamodel.BaseModel{UUID: "svm-uuid"},
		SvmExternalIdentifier: "ocid1.svm..a",
		PoolID:                10,
		State:                 string(datamodel.LifeCycleStateDeleting),
		Name:                  "svm1",
	}
	mockStorage.EXPECT().TransitionSvmToDeleting(mock.Anything, svm).Return(deletingSvm, nil)
	// Workflow start failure must trigger compensation: the row was just
	// flipped to DELETING and has no workflow driving it, so the factory must
	// flip it to ERROR before returning.
	compensationCalled := false
	mockStorage.EXPECT().ErroredSVM(mock.Anything, deletingSvm, mock.Anything).
		Run(func(_ context.Context, _ *datamodel.Svm, _ string) { compensationCalled = true }).
		Return(nil)

	mockTemporal := workflowenginemock.NewMockTemporalTestClient(t)
	mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, errors.New("temporal down"))

	orch := &OCIOrchestrator{storage: mockStorage, temporal: mockTemporal}
	_, err := orch.DeleteSvm(svmTestCtx(), &commonparams.DeleteSvmParams{
		SvmID:       "ocid1.svm..a",
		AccountName: "tenancy",
		PoolOCID:    "ocid1.pool..a",
	})
	require.Error(t, err)
	assert.True(t, compensationCalled, "ErroredSVM compensation must run when workflow start fails")
}

func TestDeleteSvm_WorkflowAlreadyStarted_ReturnsSuccessWithoutCompensation(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	acc := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "tenancy"}
	mockStorage.EXPECT().GetAccount(mock.Anything, "tenancy").Return(acc, nil)
	mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(&datamodel.PoolView{
		Pool: datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 10}, AccountID: 1},
	}, nil)
	svm := &datamodel.Svm{
		BaseModel:             datamodel.BaseModel{UUID: "svm-uuid"},
		SvmExternalIdentifier: "ocid1.svm..a",
		PoolID:                10,
		State:                 string(datamodel.LifeCycleStateREADY),
		Name:                  "svm1",
	}
	mockStorage.EXPECT().GetSvmByExternalIdentifier(mock.Anything, "ocid1.svm..a", int64(1)).Return(svm, nil)

	deletingSvm := &datamodel.Svm{
		BaseModel:             datamodel.BaseModel{UUID: "svm-uuid"},
		SvmExternalIdentifier: "ocid1.svm..a",
		PoolID:                10,
		State:                 string(datamodel.LifeCycleStateDeleting),
		Name:                  "svm1",
	}
	mockStorage.EXPECT().TransitionSvmToDeleting(mock.Anything, svm).Return(deletingSvm, nil)

	mockTemporal := workflowenginemock.NewMockTemporalTestClient(t)
	mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, serviceerror.NewWorkflowExecutionAlreadyStarted("already started", "", ""))

	orch := &OCIOrchestrator{storage: mockStorage, temporal: mockTemporal}
	wfID, err := orch.DeleteSvm(svmTestCtx(), &commonparams.DeleteSvmParams{
		SvmID:       "ocid1.svm..a",
		AccountName: "tenancy",
		PoolOCID:    "ocid1.pool..a",
		WorkflowID:  testSuppliedWorkflowID,
	})
	assert.NoError(t, err)
	assert.Equal(t, testSuppliedWorkflowID, wfID)
	mockStorage.AssertNotCalled(t, "ErroredSVM", mock.Anything, mock.Anything, mock.Anything)
}

// When workflow start fails AND the deferred compensation ErroredSVM also
// fails, the orchestrator must surface the original error (workflow start)
// rather than the compensation error, and the logger.Error inside the defer
// must execute. This exercises the previously-uncovered compensation error
// branch in CreateSvm.
func TestCreateSvm_WorkflowStartFails_AndCompensationAlsoFails(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	acc := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "tenancy"}
	mockStorage.EXPECT().GetAccount(mock.Anything, "tenancy").Return(acc, nil)
	poolView := &datamodel.PoolView{
		Pool: datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 10},
			AccountID: 1,
			State:     string(datamodel.LifeCycleStateREADY),
			VLMConfig: "cfg",
		},
	}
	mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(poolView, nil)
	mockStorage.EXPECT().SvmExistsByExternalIdentifier(mock.Anything, "ocid1.svm..new", int64(1)).
		Return(false, nil)
	mockStorage.EXPECT().GetNodesByPoolID(mock.Anything, mock.Anything).Return([]*datamodel.Node{
		{BaseModel: datamodel.BaseModel{ID: 1}, Name: "n1", State: datamodel.LifeCycleStateREADY},
		{BaseModel: datamodel.BaseModel{ID: 2}, Name: "n2", State: datamodel.LifeCycleStateREADY},
	}, nil)
	mockStorage.EXPECT().GetSvmByNameAndPoolID(mock.Anything, "svm1", int64(10)).
		Return(nil, utilserrors.NewNotFoundErr("svm", nil))
	mockStorage.EXPECT().GetNodesByPoolID(mock.Anything, mock.Anything).Return([]*datamodel.Node{
		{BaseModel: datamodel.BaseModel{ID: 1}, Name: "n1", State: datamodel.LifeCycleStateREADY},
		{BaseModel: datamodel.BaseModel{ID: 2}, Name: "n2", State: datamodel.LifeCycleStateREADY},
	}, nil)
	preallocatedSvm := &datamodel.Svm{
		BaseModel:             datamodel.BaseModel{UUID: "svm-uuid"},
		Name:                  "svm1",
		SvmExternalIdentifier: "ocid1.svm..new",
		State:                 datamodel.LifeCycleStateCreating,
	}
	mockStorage.EXPECT().CreateSvmInCreatingState(mock.Anything, mock.Anything).Return(preallocatedSvm, nil)
	mockStorage.EXPECT().ErroredSVM(mock.Anything, preallocatedSvm, mock.Anything).
		Return(errors.New("compensation also down"))

	mockTemporal := workflowenginemock.NewMockTemporalTestClient(t)
	mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, errors.New("temporal unavailable"))

	orch := &OCIOrchestrator{storage: mockStorage, temporal: mockTemporal}
	_, err := orch.CreateSvm(svmTestCtx(), &commonparams.CreateSvmParams{
		PoolOCID:              "ocid1.pool..a",
		SvmExternalIdentifier: "ocid1.svm..new",
		Name:                  "svm1",
		AccountName:           "tenancy",
		EnableIscsi:           true,
	})
	require.Error(t, err)
	// The original workflow-start error is what the caller sees.
	assert.Contains(t, err.Error(), "temporal unavailable")
}

func TestCreateSvm_WorkflowAlreadyStarted_ReturnsSuccessWithoutCompensation(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	acc := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "tenancy"}
	mockStorage.EXPECT().GetAccount(mock.Anything, "tenancy").Return(acc, nil)
	poolView := &datamodel.PoolView{
		Pool: datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 10},
			AccountID: 1,
			State:     string(datamodel.LifeCycleStateREADY),
			VLMConfig: "cfg",
		},
	}
	mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(poolView, nil)
	mockStorage.EXPECT().SvmExistsByExternalIdentifier(mock.Anything, "ocid1.svm..new", int64(1)).
		Return(false, nil)
	mockStorage.EXPECT().GetNodesByPoolID(mock.Anything, mock.Anything).Return([]*datamodel.Node{
		{BaseModel: datamodel.BaseModel{ID: 1}, Name: "n1", State: datamodel.LifeCycleStateREADY},
		{BaseModel: datamodel.BaseModel{ID: 2}, Name: "n2", State: datamodel.LifeCycleStateREADY},
	}, nil)
	mockStorage.EXPECT().GetSvmByNameAndPoolID(mock.Anything, "svm1", int64(10)).
		Return(nil, utilserrors.NewNotFoundErr("svm", nil))
	mockStorage.EXPECT().GetNodesByPoolID(mock.Anything, mock.Anything).Return([]*datamodel.Node{
		{BaseModel: datamodel.BaseModel{ID: 1}, Name: "n1", State: datamodel.LifeCycleStateREADY},
		{BaseModel: datamodel.BaseModel{ID: 2}, Name: "n2", State: datamodel.LifeCycleStateREADY},
	}, nil)
	preallocatedSvm := &datamodel.Svm{
		BaseModel:             datamodel.BaseModel{UUID: "svm-uuid"},
		Name:                  "svm1",
		SvmExternalIdentifier: "ocid1.svm..new",
		State:                 datamodel.LifeCycleStateCreating,
	}
	mockStorage.EXPECT().CreateSvmInCreatingState(mock.Anything, mock.Anything).Return(preallocatedSvm, nil)

	mockTemporal := workflowenginemock.NewMockTemporalTestClient(t)
	mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, serviceerror.NewWorkflowExecutionAlreadyStarted("already started", "", ""))

	orch := &OCIOrchestrator{storage: mockStorage, temporal: mockTemporal}
	wfID, err := orch.CreateSvm(svmTestCtx(), &commonparams.CreateSvmParams{
		PoolOCID:              "ocid1.pool..a",
		SvmExternalIdentifier: "ocid1.svm..new",
		Name:                  "svm1",
		AccountName:           "tenancy",
		EnableIscsi:           true,
		WorkflowID:            testSuppliedWorkflowID,
	})
	assert.NoError(t, err)
	assert.Equal(t, testSuppliedWorkflowID, wfID)
	mockStorage.AssertNotCalled(t, "ErroredSVM", mock.Anything, mock.Anything, mock.Anything)
}

// Mirror of TestCreateSvm_WorkflowStartFails_AndCompensationAlsoFails for the
// delete flow: when DeleteSvm's workflow start fails AND the deferred
// ErroredSVM compensation also fails, the logger.Error inside the defer must
// execute.
func TestDeleteSvm_WorkflowStartFails_AndCompensationAlsoFails(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	acc := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "tenancy"}
	mockStorage.EXPECT().GetAccount(mock.Anything, "tenancy").Return(acc, nil)
	mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(&datamodel.PoolView{
		Pool: datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 10}, AccountID: 1},
	}, nil)
	svm := &datamodel.Svm{
		BaseModel:             datamodel.BaseModel{UUID: "svm-uuid"},
		SvmExternalIdentifier: "ocid1.svm..a",
		PoolID:                10,
		State:                 string(datamodel.LifeCycleStateREADY),
		Name:                  "svm1",
	}
	mockStorage.EXPECT().GetSvmByExternalIdentifier(mock.Anything, "ocid1.svm..a", int64(1)).Return(svm, nil)

	deletingSvm := &datamodel.Svm{
		BaseModel:             datamodel.BaseModel{UUID: "svm-uuid"},
		SvmExternalIdentifier: "ocid1.svm..a",
		PoolID:                10,
		State:                 string(datamodel.LifeCycleStateDeleting),
		Name:                  "svm1",
	}
	mockStorage.EXPECT().TransitionSvmToDeleting(mock.Anything, svm).Return(deletingSvm, nil)
	mockStorage.EXPECT().ErroredSVM(mock.Anything, deletingSvm, mock.Anything).
		Return(errors.New("compensation also down"))

	mockTemporal := workflowenginemock.NewMockTemporalTestClient(t)
	mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, errors.New("temporal down"))

	orch := &OCIOrchestrator{storage: mockStorage, temporal: mockTemporal}
	_, err := orch.DeleteSvm(svmTestCtx(), &commonparams.DeleteSvmParams{
		SvmID:       "ocid1.svm..a",
		AccountName: "tenancy",
		PoolOCID:    "ocid1.pool..a",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "temporal down")
}

func TestDeleteSvm_Success(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	acc := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "tenancy"}
	mockStorage.EXPECT().GetAccount(mock.Anything, "tenancy").Return(acc, nil)
	mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(&datamodel.PoolView{
		Pool: datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 10}, AccountID: 1},
	}, nil)
	svm := &datamodel.Svm{
		BaseModel:             datamodel.BaseModel{UUID: "svm-uuid"},
		SvmExternalIdentifier: "ocid1.svm..a",
		PoolID:                10,
		State:                 string(datamodel.LifeCycleStateREADY),
		Name:                  "svm1",
	}
	mockStorage.EXPECT().GetSvmByExternalIdentifier(mock.Anything, "ocid1.svm..a", int64(1)).Return(svm, nil)

	deletingSvm := &datamodel.Svm{
		BaseModel:             datamodel.BaseModel{UUID: "svm-uuid"},
		SvmExternalIdentifier: "ocid1.svm..a",
		PoolID:                10,
		State:                 string(datamodel.LifeCycleStateDeleting),
		Name:                  "svm1",
	}
	mockStorage.EXPECT().TransitionSvmToDeleting(mock.Anything, svm).Return(deletingSvm, nil)
	mockTemporal := workflowenginemock.NewMockTemporalTestClient(t)
	mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, nil)

	orch := &OCIOrchestrator{storage: mockStorage, temporal: mockTemporal}
	wfID, err := orch.DeleteSvm(svmTestCtx(), &commonparams.DeleteSvmParams{
		SvmID:       "ocid1.svm..a",
		AccountName: "tenancy",
		PoolOCID:    "ocid1.pool..a",
	})
	assert.NoError(t, err)
	assert.NotEmpty(t, wfID)
}

// ---------------------------------------------------------------------------
// UpdateSvm
// ---------------------------------------------------------------------------

func validUpdateSvmParams() *commonparams.UpdateSvmParams {
	return &commonparams.UpdateSvmParams{
		PoolOCID:              "ocid1.pool.oc1..aabbccdd",
		SvmExternalIdentifier: "ocid1.svm.oc1..aabbccdd",
		AccountName:           "ocid1.tenancy.oc1..aabbccdd",
		SvmAdminPassword:      &commonparams.OciAdminPassword{Ocid: "ocid1.secret.oc1..aabbccdd", Version: 1},
	}
}

func TestUpdateSvm_EmptyPoolOCID(t *testing.T) {
	orch := &OCIOrchestrator{
		storage:  database.NewMockStorage(t),
		temporal: workflowenginemock.NewMockTemporalTestClient(t),
	}
	params := validUpdateSvmParams()
	params.PoolOCID = ""
	_, err := orch.UpdateSvm(svmTestCtx(), params)
	require.Error(t, err)
	assert.True(t, utilserrors.IsBadRequestErr(err))
	assert.Contains(t, err.Error(), "PoolOCID")
}

func TestUpdateSvm_EmptySvmOCID(t *testing.T) {
	orch := &OCIOrchestrator{
		storage:  database.NewMockStorage(t),
		temporal: workflowenginemock.NewMockTemporalTestClient(t),
	}
	params := validUpdateSvmParams()
	params.SvmExternalIdentifier = ""
	_, err := orch.UpdateSvm(svmTestCtx(), params)
	require.Error(t, err)
	assert.True(t, utilserrors.IsBadRequestErr(err))
	assert.Contains(t, err.Error(), "svmOCID")
}

func TestUpdateSvm_EmptyAccountName(t *testing.T) {
	orch := &OCIOrchestrator{
		storage:  database.NewMockStorage(t),
		temporal: workflowenginemock.NewMockTemporalTestClient(t),
	}
	params := validUpdateSvmParams()
	params.AccountName = "  "
	_, err := orch.UpdateSvm(svmTestCtx(), params)
	require.Error(t, err)
	assert.True(t, utilserrors.IsBadRequestErr(err))
	assert.Contains(t, err.Error(), "Tenancy-Ocid")
}

func TestUpdateSvm_NilAdminPassword(t *testing.T) {
	orch := &OCIOrchestrator{
		storage:  database.NewMockStorage(t),
		temporal: workflowenginemock.NewMockTemporalTestClient(t),
	}
	params := validUpdateSvmParams()
	params.SvmAdminPassword = nil
	_, err := orch.UpdateSvm(svmTestCtx(), params)
	require.Error(t, err)
	assert.True(t, utilserrors.IsBadRequestErr(err))
	assert.Contains(t, err.Error(), "svmAdminPassword")
}

func TestUpdateSvm_GetAccountFails(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	mockStorage.EXPECT().GetAccount(mock.Anything, "ocid1.tenancy.oc1..aabbccdd").Return(nil, gorm.ErrRecordNotFound)

	orch := &OCIOrchestrator{storage: mockStorage, temporal: workflowenginemock.NewMockTemporalTestClient(t)}
	_, err := orch.UpdateSvm(svmTestCtx(), validUpdateSvmParams())
	require.Error(t, err)
}

func TestUpdateSvm_PoolNotFound(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	acc := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "ocid1.tenancy.oc1..aabbccdd"}
	mockStorage.EXPECT().GetAccount(mock.Anything, "ocid1.tenancy.oc1..aabbccdd").Return(acc, nil)
	mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(nil, gorm.ErrRecordNotFound)

	orch := &OCIOrchestrator{storage: mockStorage, temporal: workflowenginemock.NewMockTemporalTestClient(t)}
	_, err := orch.UpdateSvm(svmTestCtx(), validUpdateSvmParams())
	require.Error(t, err)
	assert.True(t, utilserrors.IsNotFoundErr(err))
}

func TestUpdateSvm_GetPoolGenericError(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	acc := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "ocid1.tenancy.oc1..aabbccdd"}
	mockStorage.EXPECT().GetAccount(mock.Anything, "ocid1.tenancy.oc1..aabbccdd").Return(acc, nil)
	mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(nil, errors.New("db error"))

	orch := &OCIOrchestrator{storage: mockStorage, temporal: workflowenginemock.NewMockTemporalTestClient(t)}
	_, err := orch.UpdateSvm(svmTestCtx(), validUpdateSvmParams())
	require.Error(t, err)
	assert.Equal(t, "db error", err.Error())
}

func TestUpdateSvm_SvmNotFound(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	acc := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "ocid1.tenancy.oc1..aabbccdd"}
	mockStorage.EXPECT().GetAccount(mock.Anything, "ocid1.tenancy.oc1..aabbccdd").Return(acc, nil)
	mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(&datamodel.PoolView{
		Pool: datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 10}, AccountID: 1},
	}, nil)
	mockStorage.EXPECT().GetSvmByExternalIdentifier(mock.Anything, "ocid1.svm.oc1..aabbccdd", int64(1)).
		Return(nil, utilserrors.NewNotFoundErr("svm", nil))

	orch := &OCIOrchestrator{storage: mockStorage, temporal: workflowenginemock.NewMockTemporalTestClient(t)}
	_, err := orch.UpdateSvm(svmTestCtx(), validUpdateSvmParams())
	require.Error(t, err)
	assert.True(t, utilserrors.IsNotFoundErr(err))
}

func TestUpdateSvm_SvmGetGenericError(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	acc := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "ocid1.tenancy.oc1..aabbccdd"}
	mockStorage.EXPECT().GetAccount(mock.Anything, "ocid1.tenancy.oc1..aabbccdd").Return(acc, nil)
	mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(&datamodel.PoolView{
		Pool: datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 10}, AccountID: 1},
	}, nil)
	mockStorage.EXPECT().GetSvmByExternalIdentifier(mock.Anything, "ocid1.svm.oc1..aabbccdd", int64(1)).
		Return(nil, errors.New("lookup failed"))

	orch := &OCIOrchestrator{storage: mockStorage, temporal: workflowenginemock.NewMockTemporalTestClient(t)}
	_, err := orch.UpdateSvm(svmTestCtx(), validUpdateSvmParams())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "lookup failed")
}

func TestUpdateSvm_SvmBelongsToDifferentPool(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	acc := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "ocid1.tenancy.oc1..aabbccdd"}
	mockStorage.EXPECT().GetAccount(mock.Anything, "ocid1.tenancy.oc1..aabbccdd").Return(acc, nil)
	mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(&datamodel.PoolView{
		Pool: datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 10}, AccountID: 1},
	}, nil)
	svm := &datamodel.Svm{SvmExternalIdentifier: "ocid1.svm.oc1..aabbccdd", PoolID: 99}
	mockStorage.EXPECT().GetSvmByExternalIdentifier(mock.Anything, "ocid1.svm.oc1..aabbccdd", int64(1)).Return(svm, nil)

	orch := &OCIOrchestrator{storage: mockStorage, temporal: workflowenginemock.NewMockTemporalTestClient(t)}
	_, err := orch.UpdateSvm(svmTestCtx(), validUpdateSvmParams())
	require.Error(t, err)
	assert.True(t, utilserrors.IsNotFoundErr(err))
}

func TestUpdateSvm_PoolDisabled(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	acc := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "ocid1.tenancy.oc1..aabbccdd"}
	mockStorage.EXPECT().GetAccount(mock.Anything, "ocid1.tenancy.oc1..aabbccdd").Return(acc, nil)
	mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(&datamodel.PoolView{
		Pool: datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 10}, AccountID: 1, State: datamodel.LifeCycleStateDisabled},
	}, nil)
	svm := &datamodel.Svm{
		SvmExternalIdentifier: "ocid1.svm.oc1..aabbccdd",
		PoolID:                10,
		State:                 string(datamodel.LifeCycleStateREADY),
	}
	mockStorage.EXPECT().GetSvmByExternalIdentifier(mock.Anything, "ocid1.svm.oc1..aabbccdd", int64(1)).Return(svm, nil)

	orch := &OCIOrchestrator{storage: mockStorage, temporal: workflowenginemock.NewMockTemporalTestClient(t)}
	_, err := orch.UpdateSvm(svmTestCtx(), validUpdateSvmParams())
	require.Error(t, err)
	assert.True(t, utilserrors.IsConflictErr(err))
	assert.Contains(t, err.Error(), "disabled")
}

func TestUpdateSvm_SvmStateConflict(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	acc := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "ocid1.tenancy.oc1..aabbccdd"}
	mockStorage.EXPECT().GetAccount(mock.Anything, "ocid1.tenancy.oc1..aabbccdd").Return(acc, nil)
	mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(&datamodel.PoolView{
		Pool: datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 10}, AccountID: 1, State: datamodel.LifeCycleStateREADY},
	}, nil)
	svm := &datamodel.Svm{
		SvmExternalIdentifier: "ocid1.svm.oc1..aabbccdd",
		PoolID:                10,
		State:                 string(datamodel.LifeCycleStateDeleting),
	}
	mockStorage.EXPECT().GetSvmByExternalIdentifier(mock.Anything, "ocid1.svm.oc1..aabbccdd", int64(1)).Return(svm, nil)

	orch := &OCIOrchestrator{storage: mockStorage, temporal: workflowenginemock.NewMockTemporalTestClient(t)}
	_, err := orch.UpdateSvm(svmTestCtx(), validUpdateSvmParams())
	require.Error(t, err)
	assert.True(t, utilserrors.IsConflictErr(err))
}

func TestUpdateSvm_TransitionConflict(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	acc := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "ocid1.tenancy.oc1..aabbccdd"}
	mockStorage.EXPECT().GetAccount(mock.Anything, "ocid1.tenancy.oc1..aabbccdd").Return(acc, nil)
	mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(&datamodel.PoolView{
		Pool: datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 10}, AccountID: 1, State: datamodel.LifeCycleStateREADY},
	}, nil)
	svm := &datamodel.Svm{
		BaseModel:             datamodel.BaseModel{UUID: "svm-uuid"},
		SvmExternalIdentifier: "ocid1.svm.oc1..aabbccdd",
		PoolID:                10,
		State:                 string(datamodel.LifeCycleStateREADY),
		Name:                  "svm1",
	}
	mockStorage.EXPECT().GetSvmByExternalIdentifier(mock.Anything, "ocid1.svm.oc1..aabbccdd", int64(1)).Return(svm, nil)
	mockStorage.EXPECT().UpdateSvmState(mock.Anything, svm,
		datamodel.LifeCycleStateUpdating, datamodel.LifeCycleStateUpdatingDetails, mock.Anything).
		Return(utilserrors.NewConflictErr("SVM state transition to UPDATING rejected: current state is UPDATING"))

	orch := &OCIOrchestrator{storage: mockStorage, temporal: workflowenginemock.NewMockTemporalTestClient(t)}
	_, err := orch.UpdateSvm(svmTestCtx(), validUpdateSvmParams())
	require.Error(t, err)
	assert.True(t, utilserrors.IsConflictErr(err))
}

func TestUpdateSvm_TransitionGenericError(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	acc := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "ocid1.tenancy.oc1..aabbccdd"}
	mockStorage.EXPECT().GetAccount(mock.Anything, "ocid1.tenancy.oc1..aabbccdd").Return(acc, nil)
	mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(&datamodel.PoolView{
		Pool: datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 10}, AccountID: 1, State: datamodel.LifeCycleStateREADY},
	}, nil)
	svm := &datamodel.Svm{
		BaseModel:             datamodel.BaseModel{UUID: "svm-uuid"},
		SvmExternalIdentifier: "ocid1.svm.oc1..aabbccdd",
		PoolID:                10,
		State:                 string(datamodel.LifeCycleStateREADY),
		Name:                  "svm1",
	}
	mockStorage.EXPECT().GetSvmByExternalIdentifier(mock.Anything, "ocid1.svm.oc1..aabbccdd", int64(1)).Return(svm, nil)

	dbErr := errors.New("db down")
	mockStorage.EXPECT().UpdateSvmState(mock.Anything, svm,
		datamodel.LifeCycleStateUpdating, datamodel.LifeCycleStateUpdatingDetails, mock.Anything).
		Return(dbErr)

	orch := &OCIOrchestrator{storage: mockStorage, temporal: workflowenginemock.NewMockTemporalTestClient(t)}
	_, err := orch.UpdateSvm(svmTestCtx(), validUpdateSvmParams())
	require.Error(t, err)
	assert.Equal(t, dbErr, err)
}

func TestUpdateSvm_WorkflowStartFails(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	acc := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "ocid1.tenancy.oc1..aabbccdd"}
	mockStorage.EXPECT().GetAccount(mock.Anything, "ocid1.tenancy.oc1..aabbccdd").Return(acc, nil)
	mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(&datamodel.PoolView{
		Pool: datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 10}, AccountID: 1, State: datamodel.LifeCycleStateREADY},
	}, nil)
	svm := &datamodel.Svm{
		BaseModel:             datamodel.BaseModel{UUID: "svm-uuid"},
		SvmExternalIdentifier: "ocid1.svm.oc1..aabbccdd",
		PoolID:                10,
		State:                 string(datamodel.LifeCycleStateREADY),
		Name:                  "svm1",
	}
	mockStorage.EXPECT().GetSvmByExternalIdentifier(mock.Anything, "ocid1.svm.oc1..aabbccdd", int64(1)).Return(svm, nil)
	mockStorage.EXPECT().UpdateSvmState(mock.Anything, svm,
		datamodel.LifeCycleStateUpdating, datamodel.LifeCycleStateUpdatingDetails, mock.Anything).
		Return(nil)

	compensationCalled := false
	mockStorage.EXPECT().UpdateSvmState(mock.Anything, svm,
		datamodel.LifeCycleStateREADY, "").
		Run(func(_ context.Context, _ *datamodel.Svm, _ string, _ string, _ ...[]interface{}) { compensationCalled = true }).
		Return(nil)

	mockTemporal := workflowenginemock.NewMockTemporalTestClient(t)
	mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, errors.New("temporal down"))

	orch := &OCIOrchestrator{storage: mockStorage, temporal: mockTemporal}
	_, err := orch.UpdateSvm(svmTestCtx(), validUpdateSvmParams())
	require.Error(t, err)
	assert.True(t, compensationCalled, "compensation must revert SVM to previous state when workflow start fails")
}

func TestUpdateSvm_WorkflowStartFails_AndCompensationAlsoFails(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	acc := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "ocid1.tenancy.oc1..aabbccdd"}
	mockStorage.EXPECT().GetAccount(mock.Anything, "ocid1.tenancy.oc1..aabbccdd").Return(acc, nil)
	mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(&datamodel.PoolView{
		Pool: datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 10}, AccountID: 1, State: datamodel.LifeCycleStateREADY},
	}, nil)
	svm := &datamodel.Svm{
		BaseModel:             datamodel.BaseModel{UUID: "svm-uuid"},
		SvmExternalIdentifier: "ocid1.svm.oc1..aabbccdd",
		PoolID:                10,
		State:                 string(datamodel.LifeCycleStateREADY),
		Name:                  "svm1",
	}
	mockStorage.EXPECT().GetSvmByExternalIdentifier(mock.Anything, "ocid1.svm.oc1..aabbccdd", int64(1)).Return(svm, nil)
	mockStorage.EXPECT().UpdateSvmState(mock.Anything, svm,
		datamodel.LifeCycleStateUpdating, datamodel.LifeCycleStateUpdatingDetails, mock.Anything).
		Return(nil)
	mockStorage.EXPECT().UpdateSvmState(mock.Anything, svm,
		datamodel.LifeCycleStateREADY, "").
		Return(errors.New("compensation also down"))

	mockTemporal := workflowenginemock.NewMockTemporalTestClient(t)
	mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, errors.New("temporal unavailable"))

	orch := &OCIOrchestrator{storage: mockStorage, temporal: mockTemporal}
	_, err := orch.UpdateSvm(svmTestCtx(), validUpdateSvmParams())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "temporal unavailable")
}

func TestUpdateSvm_WorkflowAlreadyStarted_ReturnsSuccessWithoutCompensation(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	acc := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "ocid1.tenancy.oc1..aabbccdd"}
	mockStorage.EXPECT().GetAccount(mock.Anything, "ocid1.tenancy.oc1..aabbccdd").Return(acc, nil)
	mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(&datamodel.PoolView{
		Pool: datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 10}, AccountID: 1, State: datamodel.LifeCycleStateREADY},
	}, nil)
	svm := &datamodel.Svm{
		BaseModel:             datamodel.BaseModel{UUID: "svm-uuid"},
		SvmExternalIdentifier: "ocid1.svm.oc1..aabbccdd",
		PoolID:                10,
		State:                 string(datamodel.LifeCycleStateREADY),
		Name:                  "svm1",
	}
	mockStorage.EXPECT().GetSvmByExternalIdentifier(mock.Anything, "ocid1.svm.oc1..aabbccdd", int64(1)).Return(svm, nil)
	mockStorage.EXPECT().UpdateSvmState(mock.Anything, svm,
		datamodel.LifeCycleStateUpdating, datamodel.LifeCycleStateUpdatingDetails, mock.Anything).
		Return(nil)

	mockTemporal := workflowenginemock.NewMockTemporalTestClient(t)
	mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, serviceerror.NewWorkflowExecutionAlreadyStarted("already started", "", ""))

	orch := &OCIOrchestrator{storage: mockStorage, temporal: mockTemporal}
	params := validUpdateSvmParams()
	params.WorkflowID = testSuppliedWorkflowID
	wfID, err := orch.UpdateSvm(svmTestCtx(), params)
	assert.NoError(t, err)
	assert.Equal(t, testSuppliedWorkflowID, wfID)
}

func TestUpdateSvm_CrashResume_SkipsValidationAndTransition(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	acc := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "ocid1.tenancy.oc1..aabbccdd"}
	mockStorage.EXPECT().GetAccount(mock.Anything, "ocid1.tenancy.oc1..aabbccdd").Return(acc, nil)
	mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(&datamodel.PoolView{
		Pool: datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 10}, AccountID: 1, State: datamodel.LifeCycleStateREADY},
	}, nil)
	svm := &datamodel.Svm{
		BaseModel:             datamodel.BaseModel{UUID: "svm-uuid"},
		SvmExternalIdentifier: "ocid1.svm.oc1..aabbccdd",
		PoolID:                10,
		State:                 datamodel.LifeCycleStateUpdating,
		WorkflowID:            testSuppliedWorkflowID,
		Name:                  "svm1",
	}
	mockStorage.EXPECT().GetSvmByExternalIdentifier(mock.Anything, "ocid1.svm.oc1..aabbccdd", int64(1)).Return(svm, nil)

	mockTemporal := workflowenginemock.NewMockTemporalTestClient(t)
	mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, nil)

	orch := &OCIOrchestrator{storage: mockStorage, temporal: mockTemporal}
	params := validUpdateSvmParams()
	params.WorkflowID = testSuppliedWorkflowID
	wfID, err := orch.UpdateSvm(svmTestCtx(), params)
	assert.NoError(t, err)
	assert.Equal(t, testSuppliedWorkflowID, wfID)
	mockStorage.AssertNotCalled(t, "UpdateSvmState", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestUpdateSvm_Success(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	acc := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "ocid1.tenancy.oc1..aabbccdd"}
	mockStorage.EXPECT().GetAccount(mock.Anything, "ocid1.tenancy.oc1..aabbccdd").Return(acc, nil)
	mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(&datamodel.PoolView{
		Pool: datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 10}, AccountID: 1, State: datamodel.LifeCycleStateREADY},
	}, nil)
	svm := &datamodel.Svm{
		BaseModel:             datamodel.BaseModel{UUID: "svm-uuid"},
		SvmExternalIdentifier: "ocid1.svm.oc1..aabbccdd",
		PoolID:                10,
		State:                 string(datamodel.LifeCycleStateREADY),
		Name:                  "svm1",
	}
	mockStorage.EXPECT().GetSvmByExternalIdentifier(mock.Anything, "ocid1.svm.oc1..aabbccdd", int64(1)).Return(svm, nil)
	mockStorage.EXPECT().UpdateSvmState(mock.Anything, svm,
		datamodel.LifeCycleStateUpdating, datamodel.LifeCycleStateUpdatingDetails, mock.Anything).
		Return(nil)

	mockTemporal := workflowenginemock.NewMockTemporalTestClient(t)
	mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, nil)

	orch := &OCIOrchestrator{storage: mockStorage, temporal: mockTemporal}
	wfID, err := orch.UpdateSvm(svmTestCtx(), validUpdateSvmParams())
	assert.NoError(t, err)
	assert.NotEmpty(t, wfID)
}

func TestGenerateResourceName_OciSvm(t *testing.T) {
	const prefix = "oci-svm-"
	gen := func() string { return commonfactory.GenerateSvmResourceName(prefix) }

	t.Run("has oci-svm- prefix", func(tt *testing.T) {
		name := gen()
		assert.True(tt, strings.HasPrefix(name, prefix), "got %q", name)
	})

	t.Run("unique on successive calls", func(tt *testing.T) {
		assert.NotEqual(tt, gen(), gen())
	})

	t.Run("passes ONTAP name validation", func(tt *testing.T) {
		name := gen()
		require.NoError(tt, validateSvmName(name),
			"generated name %q must satisfy ONTAP naming constraints", name)
	})

	t.Run("length within VLM 40-char limit", func(tt *testing.T) {
		name := gen()
		assert.Equal(tt, 40, len(name),
			"generated name %q should be exactly 40 chars (8 prefix + 32 hex)", name)
	})

	t.Run("starts with a letter", func(tt *testing.T) {
		name := gen()
		first := rune(name[0])
		assert.True(tt, (first >= 'a' && first <= 'z') || (first >= 'A' && first <= 'Z'),
			"ONTAP SVM name must start with a letter or underscore, got %q", name)
	})

	t.Run("only contains ONTAP-valid characters", func(tt *testing.T) {
		name := gen()
		assert.True(tt, svmNameRegex.MatchString(name),
			"generated name %q contains invalid ONTAP characters", name)
	})
}

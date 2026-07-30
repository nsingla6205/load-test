package oci

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	utilserrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	workflowenginemock "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine"
	"gorm.io/gorm"
)

func validResetPasswordParams() *commonparams.ResetOciAdminPasswordParams {
	return &commonparams.ResetOciAdminPasswordParams{
		AccountName:  "ocid1.compartment..x",
		PoolOCID:     "ocid1.pool.oc1..y",
		TempPassword: "Netapp1234",
	}
}

func resetPoolView(state string) *datamodel.PoolView {
	return &datamodel.PoolView{
		Pool: datamodel.Pool{
			BaseModel:       datamodel.BaseModel{UUID: "pool-uuid", ID: 1},
			Name:            "p1",
			State:           state,
			PoolCredentials: &datamodel.PoolCredentials{Password: "ontap-pw"},
		},
	}
}

// TestResetAdminPasswordForPool_NilParams pins the nil-params guard: a nil
// request must be rejected as a bad request before any storage or temporal
// access, so neither mock has expectations.
func TestResetAdminPasswordForPool_NilParams(t *testing.T) {
	orch := &OCIOrchestrator{
		storage:  database.NewMockStorage(t),
		temporal: workflowenginemock.NewMockTemporalTestClient(t),
	}

	wfID, err := orch.ResetAdminPasswordForPool(rotateCtx(), nil)
	require.Error(t, err)
	assert.True(t, utilserrors.IsBadRequestErr(err), "nil params must reject as bad request")
	assert.Equal(t, "", wfID, "no workflow may start for nil params")
}

func TestResetAdminPasswordForPool_AccountNotFound(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	mockStorage.EXPECT().GetAccount(mock.Anything, "ocid1.compartment..x").Return(nil, gorm.ErrRecordNotFound)
	orch := &OCIOrchestrator{storage: mockStorage, temporal: workflowenginemock.NewMockTemporalTestClient(t)}

	_, err := orch.ResetAdminPasswordForPool(rotateCtx(), validResetPasswordParams())
	require.Error(t, err)
	assert.True(t, utilserrors.IsNotFoundErr(err))
}

func TestResetAdminPasswordForPool_PoolNotFound(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	acc := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "ocid1.compartment..x"}
	mockStorage.EXPECT().GetAccount(mock.Anything, "ocid1.compartment..x").Return(acc, nil)
	mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(nil, gorm.ErrRecordNotFound)
	orch := &OCIOrchestrator{storage: mockStorage, temporal: workflowenginemock.NewMockTemporalTestClient(t)}

	_, err := orch.ResetAdminPasswordForPool(rotateCtx(), validResetPasswordParams())
	require.Error(t, err)
	assert.True(t, utilserrors.IsNotFoundErr(err))
}

// TestResetAdminPasswordForPool_RejectsDeletedStates pins the non-deleted gate:
// DELETING/DELETED pools must be refused as bad requests and MUST NOT dispatch a
// workflow.
func TestResetAdminPasswordForPool_RejectsDeletedStates(t *testing.T) {
	for _, state := range []string{datamodel.LifeCycleStateDeleting, datamodel.LifeCycleStateDeleted} {
		t.Run(state, func(tt *testing.T) {
			mockStorage := database.NewMockStorage(tt)
			acc := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "ocid1.compartment..x"}
			mockStorage.EXPECT().GetAccount(mock.Anything, "ocid1.compartment..x").Return(acc, nil)
			mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(resetPoolView(state), nil)

			// temporal mock has no expectations: a dispatch here would fail the test.
			orch := &OCIOrchestrator{storage: mockStorage, temporal: workflowenginemock.NewMockTemporalTestClient(tt)}
			wfID, err := orch.ResetAdminPasswordForPool(rotateCtx(), validResetPasswordParams())
			require.Error(tt, err)
			assert.True(tt, utilserrors.IsBadRequestErr(err),
				"deleted/deleting pool must reject as bad request")
			assert.Contains(tt, err.Error(), state)
			assert.Equal(tt, "", wfID, "no workflow may start for a deleted pool")
		})
	}
}

// TestResetAdminPasswordForPool_AllowsNonDeletedStates confirms the gate only
// blocks delete states: creation-time and steady states must still dispatch,
// matching the decision to keep creation-time resets working.
func TestResetAdminPasswordForPool_AllowsNonDeletedStates(t *testing.T) {
	for _, state := range []string{
		datamodel.LifeCycleStateCreating,
		datamodel.LifeCycleStateREADY,
		datamodel.LifeCycleStateAvailable,
		datamodel.LifeCycleStateUpdating,
		datamodel.LifeCycleStateError,
	} {
		t.Run(state, func(tt *testing.T) {
			mockStorage := database.NewMockStorage(tt)
			acc := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "ocid1.compartment..x"}
			mockStorage.EXPECT().GetAccount(mock.Anything, "ocid1.compartment..x").Return(acc, nil)
			mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(resetPoolView(state), nil)

			mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
			mockTemporal.EXPECT().
				ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
				Return(nil, nil)

			orch := &OCIOrchestrator{storage: mockStorage, temporal: mockTemporal}
			wfID, err := orch.ResetAdminPasswordForPool(rotateCtx(), validResetPasswordParams())
			require.NoError(tt, err)
			assert.NotEmpty(tt, wfID)
		})
	}
}

// TestResetAdminPasswordForPool_UsesSuppliedWorkflowID pins the contract that a
// caller-supplied Opc-Work-Request-Id (plumbed via params.WorkflowID) is used
// verbatim as the Temporal workflow id and echoed back, so the response ids and
// the workflow id all match and repeated requests stay idempotent.
func TestResetAdminPasswordForPool_UsesSuppliedWorkflowID(t *testing.T) {
	const suppliedWorkflowID = "opc-work-request-id-reset-dup"

	mockStorage := database.NewMockStorage(t)
	acc := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "ocid1.compartment..x"}
	mockStorage.EXPECT().GetAccount(mock.Anything, "ocid1.compartment..x").Return(acc, nil)
	mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(resetPoolView(datamodel.LifeCycleStateREADY), nil)

	mockTemporal := workflowenginemock.NewMockTemporalTestClient(t)
	mockTemporal.EXPECT().
		ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, nil)

	orch := &OCIOrchestrator{storage: mockStorage, temporal: mockTemporal}
	params := validResetPasswordParams()
	params.WorkflowID = suppliedWorkflowID
	wfID, err := orch.ResetAdminPasswordForPool(rotateCtx(), params)
	require.NoError(t, err)
	assert.Equal(t, suppliedWorkflowID, wfID, "the supplied opc-work-request-id must be returned as the workflow id")
}

func TestResetAdminPasswordForPool_DispatchFailure(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	acc := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "ocid1.compartment..x"}
	mockStorage.EXPECT().GetAccount(mock.Anything, "ocid1.compartment..x").Return(acc, nil)
	mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(resetPoolView(datamodel.LifeCycleStateREADY), nil)

	mockTemporal := workflowenginemock.NewMockTemporalTestClient(t)
	mockTemporal.EXPECT().
		ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, errors.New("temporal start failed"))

	orch := &OCIOrchestrator{storage: mockStorage, temporal: mockTemporal}
	_, err := orch.ResetAdminPasswordForPool(rotateCtx(), validResetPasswordParams())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "temporal start failed")
}

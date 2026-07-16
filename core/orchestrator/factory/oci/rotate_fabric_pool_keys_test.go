package oci

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/vlm"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	utilserrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	workflowenginemock "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine"
	"go.temporal.io/api/serviceerror"
	"gorm.io/gorm"
)

func init() {
	vlm.SetActiveProvider(vlm.OCICloud)
}

// rotateCtx returns a context with the same slogger wiring the other OCI
// orchestrator tests use, so structured-logging calls inside the function
// under test don't panic on a nil logger.
func rotateCtx() context.Context {
	return context.WithValue(context.Background(), middleware.ContextSLoggerKey, log.NewLogger())
}

// poolWithFabricPoolSecret builds a minimal PoolView whose stored VLMConfig
// has the given fabric-pool secret OCID, so the no-change short-circuit can
// observe a non-empty "current" value.
func poolWithFabricPoolSecret(state, currentSecretOcid string) *datamodel.PoolView {
	raw := fmt.Sprintf(`{"deployment":{"provider_config":{"fabric_pool_config":{"secret_ocid":%q}}}}`, currentSecretOcid)
	return &datamodel.PoolView{
		Pool: datamodel.Pool{
			BaseModel:      datamodel.BaseModel{UUID: "pool-uuid", ID: 1},
			Name:           "p1",
			State:          state,
			VLMConfig:      raw,
			PoolAttributes: &datamodel.PoolAttributes{},
		},
	}
}

func validRotateParams(newOCID string) *commonparams.RotateFabricPoolKeysParams {
	return &commonparams.RotateFabricPoolKeysParams{
		AccountName:   "ocid1.compartment..x",
		PoolOCID:      "ocid1.pool.oc1..y",
		NewSecretOCID: newOCID,
	}
}

func TestRotateFabricPoolKeys_NilParams(t *testing.T) {
	orch := &OCIOrchestrator{
		storage:  database.NewMockStorage(t),
		temporal: workflowenginemock.NewMockTemporalTestClient(t),
	}
	_, err := orch.RotateFabricPoolKeys(rotateCtx(), nil)
	require.Error(t, err)
	assert.True(t, utilserrors.IsBadRequestErr(err))
}

func TestRotateFabricPoolKeys_AccountNotFound(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	mockStorage.EXPECT().GetAccount(mock.Anything, "ocid1.compartment..x").
		Return(nil, gorm.ErrRecordNotFound)
	orch := &OCIOrchestrator{storage: mockStorage, temporal: workflowenginemock.NewMockTemporalTestClient(t)}

	_, err := orch.RotateFabricPoolKeys(rotateCtx(), validRotateParams("ocid1.vaultsecret..new"))
	require.Error(t, err)
	assert.True(t, utilserrors.IsNotFoundErr(err))
}

func TestRotateFabricPoolKeys_AccountLookupOtherError(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	mockStorage.EXPECT().GetAccount(mock.Anything, "ocid1.compartment..x").
		Return(nil, errors.New("db down"))
	orch := &OCIOrchestrator{storage: mockStorage, temporal: workflowenginemock.NewMockTemporalTestClient(t)}

	_, err := orch.RotateFabricPoolKeys(rotateCtx(), validRotateParams("ocid1.vaultsecret..new"))
	require.Error(t, err)
	assert.Equal(t, "db down", err.Error())
}

func TestRotateFabricPoolKeys_PoolNotFound(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	acc := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "ocid1.compartment..x"}
	mockStorage.EXPECT().GetAccount(mock.Anything, "ocid1.compartment..x").Return(acc, nil)
	mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(nil, gorm.ErrRecordNotFound)
	orch := &OCIOrchestrator{storage: mockStorage, temporal: workflowenginemock.NewMockTemporalTestClient(t)}

	_, err := orch.RotateFabricPoolKeys(rotateCtx(), validRotateParams("ocid1.vaultsecret..new"))
	require.Error(t, err)
	assert.True(t, utilserrors.IsNotFoundErr(err))
}

func TestRotateFabricPoolKeys_StateGate_RejectsTransitionalStates(t *testing.T) {
	transitional := []string{
		datamodel.LifeCycleStateUpdating,
		datamodel.LifeCycleStateCreating,
		datamodel.LifeCycleStateDeleting,
		datamodel.LifeCycleStatePreparing,
		datamodel.LifeCycleStateMigrating,
	}
	for _, state := range transitional {
		t.Run(state, func(tt *testing.T) {
			mockStorage := database.NewMockStorage(tt)
			acc := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "ocid1.compartment..x"}
			mockStorage.EXPECT().GetAccount(mock.Anything, "ocid1.compartment..x").Return(acc, nil)
			mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(
				poolWithFabricPoolSecret(state, "ocid1.vaultsecret..old"), nil)

			orch := &OCIOrchestrator{storage: mockStorage, temporal: workflowenginemock.NewMockTemporalTestClient(tt)}
			_, err := orch.RotateFabricPoolKeys(rotateCtx(), validRotateParams("ocid1.vaultsecret..new"))
			require.Error(tt, err)
			assert.True(tt, utilserrors.IsConflictErr(err),
				"transitional state %q must surface as a conflict", state)
		})
	}
}

func TestRotateFabricPoolKeys_StateGate_RejectsDeleted(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	acc := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "ocid1.compartment..x"}
	mockStorage.EXPECT().GetAccount(mock.Anything, "ocid1.compartment..x").Return(acc, nil)
	mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(
		poolWithFabricPoolSecret(datamodel.LifeCycleStateDeleted, "ocid1.vaultsecret..old"), nil)

	orch := &OCIOrchestrator{storage: mockStorage, temporal: workflowenginemock.NewMockTemporalTestClient(t)}
	_, err := orch.RotateFabricPoolKeys(rotateCtx(), validRotateParams("ocid1.vaultsecret..new"))
	require.Error(t, err)
	assert.True(t, utilserrors.IsBadRequestErr(err),
		"deleted pool must reject as bad request, mirroring UpdatePool semantics")
}

// TestRotateFabricPoolKeys_RejectsActiveClusterUpgrade pins the cluster-upgrade gate:
// a pool that passes the state check must still be refused when an upgrade job is
// PENDING/IN_PROGRESS. The conflict must fire before any state transition or
// dispatch, so neither UpdatingPool nor ExecuteWorkflow may be called. A terminal
// job row in the same set must not mask the active one.
func TestRotateFabricPoolKeys_RejectsActiveClusterUpgrade(t *testing.T) {
	cases := []struct {
		name   string
		status string
	}{
		{name: "pending", status: string(models.UpgradeStatusPending)},
		{name: "in_progress", status: string(models.UpgradeStatusInProgress)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(tt *testing.T) {
			mockStorage := database.NewMockStorage(tt)
			acc := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "ocid1.compartment..x"}
			mockStorage.EXPECT().GetAccount(mock.Anything, "ocid1.compartment..x").Return(acc, nil)
			mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(
				poolWithFabricPoolSecret(datamodel.LifeCycleStateREADY, "ocid1.vaultsecret..old"), nil)
			mockStorage.EXPECT().GetClusterUpgradeJobsByClusterID(mock.Anything, "pool-uuid").Return(
				[]*datamodel.ClusterUpgradeJob{
					{
						BaseModel: datamodel.BaseModel{UUID: "job-old-completed"},
						ClusterID: "pool-uuid",
						PoolID:    "pool-uuid",
						Status:    string(models.UpgradeStatusCompleted),
					},
					{
						BaseModel: datamodel.BaseModel{UUID: "job-active-1"},
						ClusterID: "pool-uuid",
						PoolID:    "pool-uuid",
						Status:    tc.status,
						Metadata:  withClusterUpgradeWorkflowIDMetadata(nil, "upgrade-workflow-active-1"),
					},
				}, nil)

			orch := &OCIOrchestrator{storage: mockStorage, temporal: workflowenginemock.NewMockTemporalTestClient(tt)}
			wfID, err := orch.RotateFabricPoolKeys(rotateCtx(), validRotateParams("ocid1.vaultsecret..new"))
			require.Error(tt, err)
			assert.True(tt, utilserrors.IsConflictErr(err),
				"an active cluster upgrade must surface as a conflict")
			assert.Contains(tt, err.Error(), "upgrade-workflow-active-1")
			assert.Contains(tt, err.Error(), tc.status)
			assert.Equal(tt, "", wfID, "no workflow may start while a cluster upgrade is active")
		})
	}
}

// TestRotateFabricPoolKeys_ClusterUpgradeLookupError ensures a failure listing
// upgrade jobs is surfaced as a DB read error and aborts the rotation before any
// state transition or dispatch — we must not rotate keys on an unverified pool.
func TestRotateFabricPoolKeys_ClusterUpgradeLookupError(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	acc := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "ocid1.compartment..x"}
	mockStorage.EXPECT().GetAccount(mock.Anything, "ocid1.compartment..x").Return(acc, nil)
	mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(
		poolWithFabricPoolSecret(datamodel.LifeCycleStateREADY, "ocid1.vaultsecret..old"), nil)
	mockStorage.EXPECT().GetClusterUpgradeJobsByClusterID(mock.Anything, "pool-uuid").
		Return(nil, errors.New("db down"))

	orch := &OCIOrchestrator{storage: mockStorage, temporal: workflowenginemock.NewMockTemporalTestClient(t)}
	_, err := orch.RotateFabricPoolKeys(rotateCtx(), validRotateParams("ocid1.vaultsecret..new"))
	require.Error(t, err)
	var vcpErr *vsaerrors.CustomError
	require.True(t, errors.As(err, &vcpErr),
		"lookup failure must surface as a VCP error so the endpoint maps it to 5xx, not a silent bypass")
	require.NotNil(t, vcpErr.OriginalErr, "VCP error must wrap the underlying DB failure for ops triage")
	assert.Contains(t, vcpErr.OriginalErr.Error(), "db down",
		"underlying DB error must be preserved in the chain")
}

func TestRotateFabricPoolKeys_NoFabricPoolConfigured_BadRequest(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	acc := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "ocid1.compartment..x"}
	mockStorage.EXPECT().GetAccount(mock.Anything, "ocid1.compartment..x").Return(acc, nil)
	// Pool exists and is in a rotatable state, but its VLM config has no fabric
	// pool configured ("" current secret) — there is nothing to rotate, so the
	// orchestrator must reject up front without transitioning state or dispatching.
	mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(
		poolWithFabricPoolSecret(datamodel.LifeCycleStateREADY, ""), nil)
	mockStorage.EXPECT().GetClusterUpgradeJobsByClusterID(mock.Anything, "pool-uuid").Return([]*datamodel.ClusterUpgradeJob{}, nil)

	orch := &OCIOrchestrator{storage: mockStorage, temporal: workflowenginemock.NewMockTemporalTestClient(t)}
	_, err := orch.RotateFabricPoolKeys(rotateCtx(), validRotateParams("ocid1.vaultsecret..new"))
	require.Error(t, err)
	assert.True(t, utilserrors.IsBadRequestErr(err),
		"a pool without a fabric pool configured must reject as bad request")
}

func TestRotateFabricPoolKeys_SameOCIDStillRotates(t *testing.T) {
	// An OCI Vault secret OCID is stable across versions: the same OCID can
	// point at newly rotated key material. Rotation must therefore proceed even
	// when the stored OCID equals the requested one, so ONTAP re-fetches the
	// latest secret version.
	mockStorage := database.NewMockStorage(t)
	acc := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "ocid1.compartment..x"}
	mockStorage.EXPECT().GetAccount(mock.Anything, "ocid1.compartment..x").Return(acc, nil)
	mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(
		poolWithFabricPoolSecret(datamodel.LifeCycleStateREADY, "ocid1.vaultsecret..same"), nil)
	mockStorage.EXPECT().GetClusterUpgradeJobsByClusterID(mock.Anything, "pool-uuid").Return([]*datamodel.ClusterUpgradeJob{}, nil)
	mockStorage.EXPECT().UpdatingPool(mock.Anything, mock.Anything).Return(&datamodel.Pool{
		BaseModel: datamodel.BaseModel{UUID: "pool-uuid", ID: 1},
		Name:      "p1",
		State:     datamodel.LifeCycleStateUpdating,
	}, nil)

	mockTemporal := workflowenginemock.NewMockTemporalTestClient(t)
	mockTemporal.EXPECT().
		ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, nil)

	orch := &OCIOrchestrator{storage: mockStorage, temporal: mockTemporal}
	wfID, err := orch.RotateFabricPoolKeys(rotateCtx(),
		&commonparams.RotateFabricPoolKeysParams{
			AccountName:   "ocid1.compartment..x",
			PoolOCID:      "ocid1.pool.oc1..y",
			NewSecretOCID: "ocid1.vaultsecret..same",
		})
	require.NoError(t, err)
	assert.NotEmpty(t, wfID, "a workflow must start so ONTAP re-fetches the latest secret version")
}

func TestRotateFabricPoolKeys_HappyPath(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	acc := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "ocid1.compartment..x"}
	mockStorage.EXPECT().GetAccount(mock.Anything, "ocid1.compartment..x").Return(acc, nil)
	mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(
		poolWithFabricPoolSecret(datamodel.LifeCycleStateREADY, "ocid1.vaultsecret..old"), nil)
	mockStorage.EXPECT().GetClusterUpgradeJobsByClusterID(mock.Anything, "pool-uuid").Return([]*datamodel.ClusterUpgradeJob{}, nil)
	mockStorage.EXPECT().UpdatingPool(mock.Anything, mock.Anything).Return(&datamodel.Pool{
		BaseModel: datamodel.BaseModel{UUID: "pool-uuid", ID: 1},
		Name:      "p1",
		State:     datamodel.LifeCycleStateUpdating,
	}, nil)

	mockTemporal := workflowenginemock.NewMockTemporalTestClient(t)
	mockTemporal.EXPECT().
		ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, nil)

	orch := &OCIOrchestrator{storage: mockStorage, temporal: mockTemporal}
	wfID, err := orch.RotateFabricPoolKeys(rotateCtx(), validRotateParams("ocid1.vaultsecret..new"))
	require.NoError(t, err)
	assert.NotEmpty(t, wfID)
}

func TestRotateFabricPoolKeys_HappyPath_FromErrorState(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	acc := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "ocid1.compartment..x"}
	mockStorage.EXPECT().GetAccount(mock.Anything, "ocid1.compartment..x").Return(acc, nil)
	mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(
		poolWithFabricPoolSecret(datamodel.LifeCycleStateError, "ocid1.vaultsecret..old"), nil)
	mockStorage.EXPECT().GetClusterUpgradeJobsByClusterID(mock.Anything, "pool-uuid").Return([]*datamodel.ClusterUpgradeJob{}, nil)
	mockStorage.EXPECT().UpdatingPool(mock.Anything, mock.Anything).Return(&datamodel.Pool{
		BaseModel: datamodel.BaseModel{UUID: "pool-uuid", ID: 1},
		State:     datamodel.LifeCycleStateUpdating,
	}, nil)

	mockTemporal := workflowenginemock.NewMockTemporalTestClient(t)
	mockTemporal.EXPECT().
		ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, nil)

	orch := &OCIOrchestrator{storage: mockStorage, temporal: mockTemporal}
	wfID, err := orch.RotateFabricPoolKeys(rotateCtx(), validRotateParams("ocid1.vaultsecret..new"))
	require.NoError(t, err)
	assert.NotEmpty(t, wfID, "retry from ERROR must proceed (matches UpdatePool retry semantics)")
	// This is the retry-after-partial-failure path documented in the workflow's
	// rollback comment: persist failed on the previous attempt, pool flipped
	// to ERROR with stale OCID still in DB. The retry re-runs the workflow,
	// which calls VLM again with the same NewSecretOCID; design A4 guarantees
	// VLM either no-ops (already programmed) or safely re-programs.
}

func TestRotateFabricPoolKeys_UpdatingPoolFails(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	acc := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "ocid1.compartment..x"}
	mockStorage.EXPECT().GetAccount(mock.Anything, "ocid1.compartment..x").Return(acc, nil)
	mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(
		poolWithFabricPoolSecret(datamodel.LifeCycleStateREADY, "ocid1.vaultsecret..old"), nil)
	mockStorage.EXPECT().GetClusterUpgradeJobsByClusterID(mock.Anything, "pool-uuid").Return([]*datamodel.ClusterUpgradeJob{}, nil)
	mockStorage.EXPECT().UpdatingPool(mock.Anything, mock.Anything).Return(nil, errors.New("state transition failed"))

	orch := &OCIOrchestrator{storage: mockStorage, temporal: workflowenginemock.NewMockTemporalTestClient(t)}
	_, err := orch.RotateFabricPoolKeys(rotateCtx(), validRotateParams("ocid1.vaultsecret..new"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "state transition failed")
}

func TestRotateFabricPoolKeys_DispatchFailure_RollsBackToError(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	acc := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "ocid1.compartment..x"}
	mockStorage.EXPECT().GetAccount(mock.Anything, "ocid1.compartment..x").Return(acc, nil)
	mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(
		poolWithFabricPoolSecret(datamodel.LifeCycleStateREADY, "ocid1.vaultsecret..old"), nil)
	mockStorage.EXPECT().GetClusterUpgradeJobsByClusterID(mock.Anything, "pool-uuid").Return([]*datamodel.ClusterUpgradeJob{}, nil)
	mockStorage.EXPECT().UpdatingPool(mock.Anything, mock.Anything).Return(&datamodel.Pool{
		BaseModel: datamodel.BaseModel{UUID: "pool-uuid", ID: 1},
		State:     datamodel.LifeCycleStateUpdating,
	}, nil)
	// Critical: dispatch failure MUST trigger ErroredResource rollback so the
	// pool is not stranded in UPDATING. We assert on the call itself.
	mockStorage.EXPECT().ErroredResource(mock.Anything, mock.Anything, mock.Anything).
		Return(nil, nil).Once()

	mockTemporal := workflowenginemock.NewMockTemporalTestClient(t)
	mockTemporal.EXPECT().
		ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, errors.New("temporal start failed"))

	orch := &OCIOrchestrator{storage: mockStorage, temporal: mockTemporal}
	_, err := orch.RotateFabricPoolKeys(rotateCtx(), validRotateParams("ocid1.vaultsecret..new"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "temporal start failed")
}

// TestRotateFabricPoolKeys_WorkflowAlreadyStarted_ReturnsIdempotentSuccess pins
// the idempotency contract: when a duplicate opc-work-request-id resolves to a
// workflow that is already running, dispatch returns WorkflowExecutionAlreadyStarted
// and the orchestrator must treat it as success (no rollback to ERROR) so the
// caller sees the in-flight rotation instead of a spurious failure.
func TestRotateFabricPoolKeys_WorkflowAlreadyStarted_ReturnsIdempotentSuccess(t *testing.T) {
	const suppliedWorkflowID = "opc-work-request-id-rotate-dup"

	mockStorage := database.NewMockStorage(t)
	acc := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "ocid1.compartment..x"}
	mockStorage.EXPECT().GetAccount(mock.Anything, "ocid1.compartment..x").Return(acc, nil)
	mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(
		poolWithFabricPoolSecret(datamodel.LifeCycleStateREADY, "ocid1.vaultsecret..old"), nil)
	mockStorage.EXPECT().GetClusterUpgradeJobsByClusterID(mock.Anything, "pool-uuid").Return([]*datamodel.ClusterUpgradeJob{}, nil)
	mockStorage.EXPECT().UpdatingPool(mock.Anything, mock.Anything).Return(&datamodel.Pool{
		BaseModel: datamodel.BaseModel{UUID: "pool-uuid", ID: 1},
		State:     datamodel.LifeCycleStateUpdating,
	}, nil)

	mockTemporal := workflowenginemock.NewMockTemporalTestClient(t)
	mockTemporal.EXPECT().
		ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, serviceerror.NewWorkflowExecutionAlreadyStarted("already started", "", ""))

	orch := &OCIOrchestrator{storage: mockStorage, temporal: mockTemporal}
	params := validRotateParams("ocid1.vaultsecret..new")
	params.WorkflowID = suppliedWorkflowID
	wfID, err := orch.RotateFabricPoolKeys(rotateCtx(), params)
	require.NoError(t, err, "an already-started workflow must be idempotent success, not an error")
	assert.Equal(t, suppliedWorkflowID, wfID, "the supplied opc-work-request-id must be returned as the workflow id")
	mockStorage.AssertNotCalled(t, "ErroredResource", mock.Anything, mock.Anything, mock.Anything)
}

func TestCurrentFabricPoolConfig_DegradedStates(t *testing.T) {
	cases := []struct {
		name    string
		pool    *datamodel.Pool
		want    string
		wantErr bool
	}{
		{name: "nil pool", pool: nil, want: ""},
		{name: "empty config", pool: &datamodel.Pool{}, want: ""},
		{name: "malformed json", pool: &datamodel.Pool{VLMConfig: "{not json"}, wantErr: true},
		{name: "missing fabric_pool_config", pool: &datamodel.Pool{VLMConfig: `{"deployment":{"provider_config":{}}}`}, want: ""},
		{name: "present", pool: &datamodel.Pool{VLMConfig: `{"deployment":{"provider_config":{"fabric_pool_config":{"secret_ocid":"ocid1.vaultsecret..x"}}}}`}, want: "ocid1.vaultsecret..x"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(tt *testing.T) {
			fpc, err := currentFabricPoolConfig(tc.pool)
			if tc.wantErr {
				require.Error(tt, err)
				assert.Nil(tt, fpc)
				return
			}
			require.NoError(tt, err)
			if tc.want == "" {
				assert.Nil(tt, fpc)
				return
			}
			require.NotNil(tt, fpc)
			assert.Equal(tt, tc.want, fpc.SecretOcid)
		})
	}
}

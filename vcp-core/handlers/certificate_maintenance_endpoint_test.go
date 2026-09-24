package api

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	coremodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/factory"
	oasgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcp-core/servergen"
)

type fakeCertificateMaintenanceService struct {
	factory.OrchestratorFactory
	reconcileParams *commonparams.PoolCertificateMaintenanceParams
	forceParams     *commonparams.PoolCertificateMaintenanceParams
	workflowID      string
	jobID           string
	progress        *coremodels.CertificateMaintenanceProgress
	err             error
}

func (f *fakeCertificateMaintenanceService) StartVsaCAReconciliation(
	_ context.Context,
	params *commonparams.PoolCertificateMaintenanceParams,
) (string, string, error) {
	f.reconcileParams = params
	return f.workflowID, f.jobID, f.err
}

func (f *fakeCertificateMaintenanceService) StartForcedVsaCertificateRotation(
	_ context.Context,
	params *commonparams.PoolCertificateMaintenanceParams,
) (string, string, error) {
	f.forceParams = params
	return f.workflowID, f.jobID, f.err
}

func (f *fakeCertificateMaintenanceService) GetCertificateMaintenanceStatus(
	_ context.Context,
	_ string,
) (*coremodels.CertificateMaintenanceProgress, error) {
	return f.progress, f.err
}

func TestV1ReconcilePoolCertificateAuthority_PoolList(t *testing.T) {
	first := uuid.New()
	second := uuid.New()
	service := &fakeCertificateMaintenanceService{workflowID: "reconcile-vsa-ca-id", jobID: uuid.NewString()}
	handler := Handler{Orchestrator: service}

	result, err := handler.V1ReconcilePoolCertificateAuthority(
		context.Background(),
		&oasgenserver.ReconcilePoolCertificateAuthorityRequestV1{
			SelectionType: oasgenserver.PoolCertificateMaintenanceSelectionV1PoolList,
			PoolIds:       []uuid.UUID{first, second},
		},
	)

	require.NoError(t, err)
	response, ok := result.(*oasgenserver.PoolCertificateMaintenanceResponseV1)
	require.True(t, ok)
	assert.Equal(t, "reconcile-vsa-ca-id", response.WorkflowId)
	assert.Equal(t, []string{first.String(), second.String()}, service.reconcileParams.PoolUUIDs)
	assert.Empty(t, service.reconcileParams.DesiredCAURI)
}

func TestV1ReconcilePoolCertificateAuthority_AllOmitsPoolIDs(t *testing.T) {
	service := &fakeCertificateMaintenanceService{workflowID: "reconcile-vsa-ca-id", jobID: uuid.NewString()}
	handler := Handler{Orchestrator: service}

	result, err := handler.V1ReconcilePoolCertificateAuthority(
		context.Background(),
		&oasgenserver.ReconcilePoolCertificateAuthorityRequestV1{
			SelectionType: oasgenserver.PoolCertificateMaintenanceSelectionV1All,
		},
	)

	require.NoError(t, err)
	_, ok := result.(*oasgenserver.PoolCertificateMaintenanceResponseV1)
	require.True(t, ok)
	assert.Empty(t, service.reconcileParams.DesiredCAURI)
	assert.Empty(t, service.reconcileParams.PoolUUIDs)
}

func TestCertificateMaintenanceRequestValidation_AllAllowsOmittedOptionalFields(t *testing.T) {
	reconcileRequest := &oasgenserver.ReconcilePoolCertificateAuthorityRequestV1{
		SelectionType: oasgenserver.PoolCertificateMaintenanceSelectionV1All,
	}
	forceRequest := &oasgenserver.ForcePoolCertificateRotationRequestV1{
		SelectionType: oasgenserver.PoolCertificateMaintenanceSelectionV1All,
	}

	require.NoError(t, reconcileRequest.Validate())
	require.NoError(t, forceRequest.Validate())
}

func TestV1ForcePoolCertificateRotation_SinglePool(t *testing.T) {
	poolID := uuid.New()
	service := &fakeCertificateMaintenanceService{workflowID: "force-vsa-certificate-rotation-id", jobID: uuid.NewString()}
	handler := Handler{Orchestrator: service}

	result, err := handler.V1ForcePoolCertificateRotation(
		context.Background(),
		&oasgenserver.ForcePoolCertificateRotationRequestV1{
			SelectionType: oasgenserver.PoolCertificateMaintenanceSelectionV1Pool,
			PoolId:        oasgenserver.NewOptUUID(poolID),
		},
	)

	require.NoError(t, err)
	_, ok := result.(*oasgenserver.PoolCertificateMaintenanceResponseV1)
	require.True(t, ok)
	assert.Equal(t, []string{poolID.String()}, service.forceParams.PoolUUIDs)
}

func certificateMaintenanceProgressFixture(
	jobID uuid.UUID,
	childJobID uuid.UUID,
	poolID uuid.UUID,
	operation coremodels.CertificateMaintenanceOperation,
) *coremodels.CertificateMaintenanceProgress {
	return &coremodels.CertificateMaintenanceProgress{
		JobID:           jobID.String(),
		WorkflowID:      "certificate-maintenance-" + jobID.String(),
		Operation:       operation,
		Status:          "PROCESSING",
		SelectionType:   "pool",
		DesiredCAURI:    "project/pool/ca",
		TotalPools:      1,
		InProgressPools: 1,
		Pools: []coremodels.CertificateMaintenancePoolStatus{{
			PoolID: poolID.String(), JobID: childJobID.String(),
			WorkflowID: "child-workflow", Status: "PROCESSING",
		}},
	}
}

func TestV1GetPoolCertificateAuthorityReconciliationStatus(t *testing.T) {
	jobID := uuid.New()
	poolID := uuid.New()
	service := &fakeCertificateMaintenanceService{
		progress: certificateMaintenanceProgressFixture(
			jobID, uuid.New(), poolID,
			coremodels.CertificateMaintenanceOperationCAReconciliation,
		),
	}
	handler := Handler{Orchestrator: service}

	result, err := handler.V1GetPoolCertificateAuthorityReconciliationStatus(
		context.Background(),
		oasgenserver.V1GetPoolCertificateAuthorityReconciliationStatusParams{JobId: jobID},
	)

	require.NoError(t, err)
	response, ok := result.(*oasgenserver.PoolCertificateMaintenanceStatusResponseV1)
	require.True(t, ok)
	assert.Equal(t, jobID, response.JobId)
	assert.Equal(t, 1, response.TotalPools)
	assert.Equal(t, 1, response.InProgressPools)
	require.Len(t, response.Pools, 1)
	assert.Equal(t, poolID, response.Pools[0].PoolId)
}

func TestV1GetPoolCertificateRotationStatus(t *testing.T) {
	jobID := uuid.New()
	poolID := uuid.New()
	service := &fakeCertificateMaintenanceService{
		progress: certificateMaintenanceProgressFixture(
			jobID, uuid.New(), poolID,
			coremodels.CertificateMaintenanceOperationForcedRotation,
		),
	}
	handler := Handler{Orchestrator: service}

	result, err := handler.V1GetPoolCertificateRotationStatus(
		context.Background(),
		oasgenserver.V1GetPoolCertificateRotationStatusParams{JobId: jobID},
	)

	require.NoError(t, err)
	response, ok := result.(*oasgenserver.PoolCertificateMaintenanceStatusResponseV1)
	require.True(t, ok)
	assert.Equal(t, jobID, response.JobId)
	assert.Equal(
		t,
		oasgenserver.PoolCertificateMaintenanceStatusResponseV1OperationFORCEDROTATION,
		response.Operation,
	)
}

func TestCertificateMaintenanceStatus_RejectsJobFromOtherOperation(t *testing.T) {
	jobID := uuid.New()
	service := &fakeCertificateMaintenanceService{
		progress: certificateMaintenanceProgressFixture(
			jobID, uuid.New(), uuid.New(),
			coremodels.CertificateMaintenanceOperationForcedRotation,
		),
	}
	handler := Handler{Orchestrator: service}

	result, err := handler.V1GetPoolCertificateAuthorityReconciliationStatus(
		context.Background(),
		oasgenserver.V1GetPoolCertificateAuthorityReconciliationStatusParams{JobId: jobID},
	)

	require.NoError(t, err)
	_, ok := result.(*oasgenserver.V1GetPoolCertificateAuthorityReconciliationStatusNotFound)
	assert.True(t, ok)
}

func TestV1ForcePoolCertificateRotation_RejectsInvalidSelectionPayload(t *testing.T) {
	handler := Handler{Orchestrator: &fakeCertificateMaintenanceService{}}

	result, err := handler.V1ForcePoolCertificateRotation(
		context.Background(),
		&oasgenserver.ForcePoolCertificateRotationRequestV1{
			SelectionType: oasgenserver.PoolCertificateMaintenanceSelectionV1Pool,
		},
	)

	require.NoError(t, err)
	_, ok := result.(*oasgenserver.V1ForcePoolCertificateRotationBadRequest)
	assert.True(t, ok)
}

func TestBuildPoolCertificateMaintenanceParams(t *testing.T) {
	poolID := uuid.New()
	otherPoolID := uuid.New()
	tests := []struct {
		name          string
		selection     oasgenserver.PoolCertificateMaintenanceSelectionV1
		poolID        oasgenserver.OptUUID
		poolIDs       []uuid.UUID
		wantPoolUUIDs []string
		wantErr       string
	}{
		{
			name:      "all",
			selection: oasgenserver.PoolCertificateMaintenanceSelectionV1All,
		},
		{
			name:      "all rejects pool ID",
			selection: oasgenserver.PoolCertificateMaintenanceSelectionV1All,
			poolID:    oasgenserver.NewOptUUID(poolID),
			wantErr:   "must be omitted",
		},
		{
			name:      "all rejects pool IDs",
			selection: oasgenserver.PoolCertificateMaintenanceSelectionV1All,
			poolIDs:   []uuid.UUID{poolID},
			wantErr:   "must be omitted",
		},
		{
			name:      "pool requires pool ID",
			selection: oasgenserver.PoolCertificateMaintenanceSelectionV1Pool,
			wantErr:   "poolId is required",
		},
		{
			name:      "pool rejects pool IDs",
			selection: oasgenserver.PoolCertificateMaintenanceSelectionV1Pool,
			poolID:    oasgenserver.NewOptUUID(poolID),
			poolIDs:   []uuid.UUID{otherPoolID},
			wantErr:   "poolIds must be omitted",
		},
		{
			name:          "pool",
			selection:     oasgenserver.PoolCertificateMaintenanceSelectionV1Pool,
			poolID:        oasgenserver.NewOptUUID(poolID),
			wantPoolUUIDs: []string{poolID.String()},
		},
		{
			name:      "pool list rejects pool ID",
			selection: oasgenserver.PoolCertificateMaintenanceSelectionV1PoolList,
			poolID:    oasgenserver.NewOptUUID(poolID),
			poolIDs:   []uuid.UUID{otherPoolID},
			wantErr:   "poolId must be omitted",
		},
		{
			name:      "pool list requires pool IDs",
			selection: oasgenserver.PoolCertificateMaintenanceSelectionV1PoolList,
			wantErr:   "poolIds is required",
		},
		{
			name:          "pool list",
			selection:     oasgenserver.PoolCertificateMaintenanceSelectionV1PoolList,
			poolIDs:       []uuid.UUID{poolID, otherPoolID},
			wantPoolUUIDs: []string{poolID.String(), otherPoolID.String()},
		},
		{
			name:      "unsupported selection",
			selection: oasgenserver.PoolCertificateMaintenanceSelectionV1("unsupported"),
			wantErr:   "unsupported selectionType",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			params, err := buildPoolCertificateMaintenanceParams(test.selection, test.poolID, test.poolIDs)
			if test.wantErr != "" {
				require.ErrorContains(t, err, test.wantErr)
				assert.Nil(t, params)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, commonparams.PoolCertificateMaintenanceSelection(test.selection), params.SelectionType)
			assert.Equal(t, test.wantPoolUUIDs, params.PoolUUIDs)
		})
	}
}

func TestV1ReconcilePoolCertificateAuthority_ErrorResponses(t *testing.T) {
	t.Run("invalid selection payload", func(t *testing.T) {
		result, err := (Handler{}).V1ReconcilePoolCertificateAuthority(
			context.Background(),
			&oasgenserver.ReconcilePoolCertificateAuthorityRequestV1{
				SelectionType: oasgenserver.PoolCertificateMaintenanceSelectionV1Pool,
			},
		)

		require.NoError(t, err)
		_, ok := result.(*oasgenserver.V1ReconcilePoolCertificateAuthorityBadRequest)
		assert.True(t, ok)
	})

	t.Run("service unavailable", func(t *testing.T) {
		result, err := (Handler{}).V1ReconcilePoolCertificateAuthority(
			context.Background(),
			&oasgenserver.ReconcilePoolCertificateAuthorityRequestV1{
				SelectionType: oasgenserver.PoolCertificateMaintenanceSelectionV1All,
			},
		)

		require.NoError(t, err)
		response, ok := result.(*oasgenserver.V1ReconcilePoolCertificateAuthorityInternalServerError)
		require.True(t, ok)
		assert.Equal(t, "certificate maintenance service is unavailable", response.Message)
	})

	t.Run("service failure", func(t *testing.T) {
		handler := Handler{Orchestrator: &fakeCertificateMaintenanceService{
			err: errors.New("start failed"),
		}}
		result, err := handler.V1ReconcilePoolCertificateAuthority(
			context.Background(),
			&oasgenserver.ReconcilePoolCertificateAuthorityRequestV1{
				SelectionType: oasgenserver.PoolCertificateMaintenanceSelectionV1All,
			},
		)

		require.NoError(t, err)
		response, ok := result.(*oasgenserver.V1ReconcilePoolCertificateAuthorityInternalServerError)
		require.True(t, ok)
		assert.Equal(t, "start failed", response.Message)
	})
}

func TestV1ForcePoolCertificateRotation_ErrorResponses(t *testing.T) {
	request := &oasgenserver.ForcePoolCertificateRotationRequestV1{
		SelectionType: oasgenserver.PoolCertificateMaintenanceSelectionV1All,
	}

	t.Run("service unavailable", func(t *testing.T) {
		result, err := (Handler{}).V1ForcePoolCertificateRotation(context.Background(), request)

		require.NoError(t, err)
		response, ok := result.(*oasgenserver.V1ForcePoolCertificateRotationInternalServerError)
		require.True(t, ok)
		assert.Equal(t, "certificate maintenance service is unavailable", response.Message)
	})

	t.Run("service failure", func(t *testing.T) {
		handler := Handler{Orchestrator: &fakeCertificateMaintenanceService{
			err: errors.New("start failed"),
		}}
		result, err := handler.V1ForcePoolCertificateRotation(context.Background(), request)

		require.NoError(t, err)
		response, ok := result.(*oasgenserver.V1ForcePoolCertificateRotationInternalServerError)
		require.True(t, ok)
		assert.Equal(t, "start failed", response.Message)
	})
}

func TestCertificateMaintenanceResponseSkipsInvalidPoolUUIDs(t *testing.T) {
	validPoolID := uuid.New()

	response := certificateMaintenanceResponse(
		uuid.NewString(),
		"workflow-id",
		oasgenserver.PoolCertificateMaintenanceSelectionV1PoolList,
		[]string{"not-a-uuid", validPoolID.String()},
	)

	assert.Equal(t, "workflow-id", response.WorkflowId)
	assert.Equal(t, []uuid.UUID{validPoolID}, response.PoolIds)
}

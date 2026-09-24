package gcp

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	coremodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	workflowenginemock "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine"
	"go.temporal.io/api/serviceerror"
)

func useHelmCA(t *testing.T, projectID, poolName, caName string) {
	t.Helper()
	originalProjectID := env.CaPoolDeployedProjectID
	originalPoolName := env.CaPoolName
	originalCAName := env.CaName
	t.Cleanup(func() {
		env.CaPoolDeployedProjectID = originalProjectID
		env.CaPoolName = originalPoolName
		env.CaName = originalCAName
	})
	env.CaPoolDeployedProjectID = projectID
	env.CaPoolName = poolName
	env.CaName = caName
}

func TestCanonicalCertificateMaintenanceParams_SortsPools(t *testing.T) {
	first := commonparams.PoolCertificateMaintenanceParams{
		SelectionType: commonparams.PoolCertificateMaintenancePoolList,
		PoolUUIDs:     []string{"pool-b", "pool-a"},
		DesiredCAURI:  "project/pool/ca",
	}
	second := first
	second.PoolUUIDs = []string{"pool-a", "pool-b"}

	first = canonicalCertificateMaintenanceParams(&first)
	second = canonicalCertificateMaintenanceParams(&second)

	assert.Equal(t, first, second)
}

func expectCertificateMaintenanceJob(
	storage *database.MockStorage,
	ctx context.Context,
) {
	storage.EXPECT().
		CreateJobWithWorkflowID(ctx, mock.AnythingOfType("*datamodel.Job"), mock.AnythingOfType("string")).
		RunAndReturn(func(_ context.Context, job *datamodel.Job, workflowID string) (*datamodel.Job, error) {
			job.WorkflowID = workflowID
			return job, nil
		}).
		Once()
}

func TestStartVsaCAReconciliation_PersistsFleetCAAfterWorkflowAccepted(t *testing.T) {
	useHelmCA(t, "project", "pool", "ca")
	ctx := context.Background()
	storage := database.NewMockStorage(t)
	temporalClient := workflowenginemock.NewMockTemporalTestClient(t)
	expectCertificateMaintenanceJob(storage, ctx)
	storage.EXPECT().
		UpsertAppConfig(ctx, VSACALastAcceptedURIAppConfigKey, "project/pool/ca").
		Return(nil).
		Once()
	temporalClient.EXPECT().
		ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, nil).
		Once()
	orchestrator := NewGCPOrchestrator(storage, temporalClient)

	workflowID, jobID, err := orchestrator.StartVsaCAReconciliation(
		ctx,
		&commonparams.PoolCertificateMaintenanceParams{
			SelectionType: commonparams.PoolCertificateMaintenanceAll,
		},
	)

	require.NoError(t, err)
	assert.Contains(t, workflowID, "reconcile-vsa-ca-")
	assert.NotEmpty(t, jobID)
}

func TestStartVsaCAReconciliation_TargetedRequestDoesNotReplaceFleetCA(t *testing.T) {
	useHelmCA(t, "project", "pool", "ca")
	ctx := context.Background()
	storage := database.NewMockStorage(t)
	temporalClient := workflowenginemock.NewMockTemporalTestClient(t)
	expectCertificateMaintenanceJob(storage, ctx)
	temporalClient.EXPECT().
		ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, nil).
		Once()
	orchestrator := NewGCPOrchestrator(storage, temporalClient)

	_, _, err := orchestrator.StartVsaCAReconciliation(
		ctx,
		&commonparams.PoolCertificateMaintenanceParams{
			SelectionType: commonparams.PoolCertificateMaintenancePool,
			PoolUUIDs:     []string{"pool-a"},
		},
	)

	require.NoError(t, err)
}

func TestStartVsaCAReconciliation_UsesHelmCAAndIgnoresParamsDesiredCAURI(t *testing.T) {
	useHelmCA(t, "helm-project", "helm-pool", "helm-ca")

	storage := database.NewMockStorage(t)
	expectCertificateMaintenanceJob(storage, context.Background())
	temporalClient := workflowenginemock.NewMockTemporalTestClient(t)
	temporalClient.EXPECT().
		ExecuteWorkflow(
			mock.Anything,
			mock.Anything,
			mock.Anything,
			mock.MatchedBy(func(params commonparams.PoolCertificateMaintenanceParams) bool {
				return params.DesiredCAURI == "helm-project/helm-pool/helm-ca"
			}),
		).
		Return(nil, nil).
		Once()

	_, _, err := NewGCPOrchestrator(storage, temporalClient).StartVsaCAReconciliation(
		context.Background(),
		&commonparams.PoolCertificateMaintenanceParams{
			SelectionType: commonparams.PoolCertificateMaintenancePool,
			PoolUUIDs:     []string{"pool-a"},
			DesiredCAURI:  "request-project/request-pool/request-ca",
		},
	)

	require.NoError(t, err)
}

func TestNormalizeCAURI(t *testing.T) {
	tests := []struct {
		name      string
		caURI     string
		want      string
		wantError string
	}{
		{
			name:  "trims outer whitespace",
			caURI: " project/pool/ca ",
			want:  "project/pool/ca",
		},
		{
			name:      "requires three components",
			caURI:     "project/pool",
			wantError: "project/CA-pool/CA-name format",
		},
		{
			name:      "rejects empty component",
			caURI:     "project//ca",
			wantError: "non-empty components",
		},
		{
			name:      "rejects component whitespace",
			caURI:     "project/ pool/ca",
			wantError: "without surrounding whitespace",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := normalizeCAURI(test.caURI)
			if test.wantError != "" {
				require.ErrorContains(t, err, test.wantError)
				assert.Empty(t, got)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, test.want, got)
		})
	}
}

func TestStartVsaCAReconciliation_ErrorPaths(t *testing.T) {
	ctx := context.Background()

	t.Run("nil parameters", func(t *testing.T) {
		_, _, err := NewGCPOrchestrator(nil, nil).StartVsaCAReconciliation(ctx, nil)
		require.ErrorContains(t, err, "parameters are required")
	})

	t.Run("invalid Helm CA URI", func(t *testing.T) {
		useHelmCA(t, "", "", "")
		_, _, err := NewGCPOrchestrator(nil, nil).StartVsaCAReconciliation(
			ctx,
			&commonparams.PoolCertificateMaintenanceParams{},
		)
		require.ErrorContains(t, err, "project/CA-pool/CA-name format")
	})

	t.Run("workflow start failure", func(t *testing.T) {
		useHelmCA(t, "project", "pool", "ca")
		storage := database.NewMockStorage(t)
		expectCertificateMaintenanceJob(storage, ctx)
		storage.EXPECT().
			UpdateJob(ctx, mock.AnythingOfType("string"), string(datamodel.JobsStateERROR), 0, mock.AnythingOfType("string")).
			Return(nil).
			Once()
		temporalClient := workflowenginemock.NewMockTemporalTestClient(t)
		temporalClient.EXPECT().
			ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(nil, errors.New("Temporal unavailable")).
			Once()

		_, _, err := NewGCPOrchestrator(storage, temporalClient).StartVsaCAReconciliation(
			ctx,
			&commonparams.PoolCertificateMaintenanceParams{
				SelectionType: commonparams.PoolCertificateMaintenancePool,
				PoolUUIDs:     []string{"pool-a"},
			},
		)
		require.ErrorContains(t, err, "start certificate maintenance workflow")
	})

	t.Run("fleet CA persistence failure", func(t *testing.T) {
		useHelmCA(t, "project", "pool", "ca")
		storage := database.NewMockStorage(t)
		expectCertificateMaintenanceJob(storage, ctx)
		temporalClient := workflowenginemock.NewMockTemporalTestClient(t)
		temporalClient.EXPECT().
			ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(nil, nil).
			Once()
		storage.EXPECT().
			UpsertAppConfig(ctx, VSACALastAcceptedURIAppConfigKey, "project/pool/ca").
			Return(errors.New("database unavailable")).
			Once()

		_, _, err := NewGCPOrchestrator(storage, temporalClient).StartVsaCAReconciliation(
			ctx,
			&commonparams.PoolCertificateMaintenanceParams{
				SelectionType: commonparams.PoolCertificateMaintenanceAll,
			},
		)
		require.ErrorContains(t, err, "persist last accepted VSA CA URI")
	})
}

func TestStartForcedVsaCertificateRotation(t *testing.T) {
	ctx := context.Background()

	t.Run("nil parameters", func(t *testing.T) {
		_, _, err := NewGCPOrchestrator(nil, nil).StartForcedVsaCertificateRotation(ctx, nil)
		require.ErrorContains(t, err, "parameters are required")
	})

	t.Run("Temporal client is required", func(t *testing.T) {
		_, _, err := NewGCPOrchestrator(nil, nil).StartForcedVsaCertificateRotation(
			ctx,
			&commonparams.PoolCertificateMaintenanceParams{
				SelectionType: commonparams.PoolCertificateMaintenanceAll,
			},
		)
		require.ErrorContains(t, err, "Temporal client is required")
	})

	t.Run("starts workflow", func(t *testing.T) {
		storage := database.NewMockStorage(t)
		expectCertificateMaintenanceJob(storage, ctx)
		temporalClient := workflowenginemock.NewMockTemporalTestClient(t)
		temporalClient.EXPECT().
			ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(nil, nil).
			Once()

		workflowID, jobID, err := NewGCPOrchestrator(storage, temporalClient).StartForcedVsaCertificateRotation(
			ctx,
			&commonparams.PoolCertificateMaintenanceParams{
				SelectionType: commonparams.PoolCertificateMaintenancePoolList,
				PoolUUIDs:     []string{"pool-b", "pool-a"},
			},
		)
		require.NoError(t, err)
		assert.Contains(t, workflowID, "force-vsa-certificate-rotation-")
		assert.NotEmpty(t, jobID)
	})

	t.Run("returns workflow start failure", func(t *testing.T) {
		storage := database.NewMockStorage(t)
		expectCertificateMaintenanceJob(storage, ctx)
		storage.EXPECT().
			UpdateJob(ctx, mock.AnythingOfType("string"), string(datamodel.JobsStateERROR), 0, mock.AnythingOfType("string")).
			Return(nil).
			Once()
		temporalClient := workflowenginemock.NewMockTemporalTestClient(t)
		temporalClient.EXPECT().
			ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(nil, errors.New("Temporal unavailable")).
			Once()

		_, _, err := NewGCPOrchestrator(storage, temporalClient).StartForcedVsaCertificateRotation(
			ctx,
			&commonparams.PoolCertificateMaintenanceParams{
				SelectionType: commonparams.PoolCertificateMaintenanceAll,
			},
		)
		require.ErrorContains(t, err, "Temporal unavailable")
	})
}

func TestStartCertificateMaintenanceWorkflow_TreatsDuplicateAsAccepted(t *testing.T) {
	temporalClient := workflowenginemock.NewMockTemporalTestClient(t)
	temporalClient.EXPECT().
		ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, serviceerror.NewWorkflowExecutionAlreadyStarted("already running", "", "")).
		Once()

	err := startCertificateMaintenanceWorkflow(
		context.Background(),
		temporalClient,
		"workflow-id",
		func() {},
		commonparams.PoolCertificateMaintenanceParams{},
	)

	require.NoError(t, err)
}

func TestGetCertificateMaintenanceStatus_AggregatesChildren(t *testing.T) {
	ctx := context.Background()
	storage := database.NewMockStorage(t)
	parentJobID := "0cae8b69-7790-4fe6-a819-5cb755e95c5f"
	storage.EXPECT().
		GetJob(ctx, parentJobID).
		Return(&datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: parentJobID},
			Type:       string(datamodel.JobTypeReconcilePoolCertificateAuthority),
			State:      string(datamodel.JobsStatePROCESSING),
			WorkflowID: "reconcile-workflow",
			JobAttributes: &datamodel.JobAttributes{
				Operation:     string(coremodels.CertificateMaintenanceOperationCAReconciliation),
				SelectionType: string(commonparams.PoolCertificateMaintenancePoolList),
				DesiredCAURI:  "project/pool/ca",
			},
		}, nil).
		Once()
	storage.EXPECT().
		GetJobsWithCondition(ctx, mock.Anything).
		Return([]*datamodel.Job{
			{
				BaseModel:    datamodel.BaseModel{UUID: "first-job"},
				State:        string(datamodel.JobsStateDONE),
				WorkflowID:   "first-workflow",
				ResourceUUID: "pool-b",
			},
			{
				BaseModel:    datamodel.BaseModel{UUID: "second-job"},
				State:        string(datamodel.JobsStateERROR),
				WorkflowID:   "second-workflow",
				ResourceUUID: "pool-a",
				ErrorDetails: "rotation failed",
			},
		}, nil).
		Once()

	progress, err := NewGCPOrchestrator(storage, nil).GetCertificateMaintenanceStatus(ctx, parentJobID)

	require.NoError(t, err)
	assert.Equal(t, 2, progress.TotalPools)
	assert.Equal(t, 1, progress.CompletedPools)
	assert.Equal(t, 1, progress.FailedPools)
	assert.Equal(t, "pool-a", progress.Pools[0].PoolID)
	assert.Equal(t, "rotation failed", progress.Pools[0].Error)
}

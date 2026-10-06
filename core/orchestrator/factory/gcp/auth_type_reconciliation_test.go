package gcp

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows/backgroundworkflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	dbutils "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	workflowenginemock "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
)

func TestValidateAuthTypeReconciliationParams(t *testing.T) {
	validUUID := uuid.NewString()
	tests := []struct {
		name     string
		params   *commonparams.ReconcilePoolAuthTypeParams
		wantUUID string
		wantErr  string
	}{
		{
			name: "all pools",
			params: &commonparams.ReconcilePoolAuthTypeParams{
				SelectionType: "all",
				FromAuthType:  env.USERNAME_PWD_SEC_MGR,
				ToAuthType:    env.USER_CERTIFICATE,
			},
		},
		{
			name: "trims explicit UUID",
			params: &commonparams.ReconcilePoolAuthTypeParams{
				SelectionType: "poolList",
				FromAuthType:  env.USERNAME_PWD_SEC_MGR,
				ToAuthType:    env.USER_CERTIFICATE,
				PoolUUIDs:     []string{" " + validUUID + " "},
			},
			wantUUID: validUUID,
		},
		{
			name: "invalid UUID remains an error",
			params: &commonparams.ReconcilePoolAuthTypeParams{
				SelectionType: "poolList",
				FromAuthType:  env.USERNAME_PWD_SEC_MGR,
				ToAuthType:    env.USER_CERTIFICATE,
				PoolUUIDs:     []string{"invalid"},
			},
			wantErr: "is invalid",
		},
		{
			name: "duplicate UUID",
			params: &commonparams.ReconcilePoolAuthTypeParams{
				SelectionType: "poolList",
				FromAuthType:  env.USERNAME_PWD_SEC_MGR,
				ToAuthType:    env.USER_CERTIFICATE,
				PoolUUIDs:     []string{validUUID, validUUID},
			},
			wantErr: "duplicated",
		},
		{
			name: "unsupported transition",
			params: &commonparams.ReconcilePoolAuthTypeParams{
				SelectionType: "all",
				FromAuthType:  env.USER_CERTIFICATE,
				ToAuthType:    env.USERNAME_PWD_SEC_MGR,
			},
			wantErr: "only fromAuthType 1 toAuthType 2",
		},
		{
			name:    "missing request",
			wantErr: "request body is required",
		},
		{
			name: "pool list without UUIDs",
			params: &commonparams.ReconcilePoolAuthTypeParams{
				SelectionType: "poolList",
				FromAuthType:  env.USERNAME_PWD_SEC_MGR,
				ToAuthType:    env.USER_CERTIFICATE,
			},
			wantErr: "poolUuids are required",
		},
		{
			name: "all with explicit UUIDs",
			params: &commonparams.ReconcilePoolAuthTypeParams{
				SelectionType: "all",
				FromAuthType:  env.USERNAME_PWD_SEC_MGR,
				ToAuthType:    env.USER_CERTIFICATE,
				PoolUUIDs:     []string{validUUID},
			},
			wantErr: "poolUuids must be empty",
		},
		{
			name: "invalid selection type",
			params: &commonparams.ReconcilePoolAuthTypeParams{
				SelectionType: "percent",
				FromAuthType:  env.USERNAME_PWD_SEC_MGR,
				ToAuthType:    env.USER_CERTIFICATE,
			},
			wantErr: "selectionType must be all or poolList",
		},
		{
			name: "batch size too large",
			params: &commonparams.ReconcilePoolAuthTypeParams{
				SelectionType: "all",
				FromAuthType:  env.USERNAME_PWD_SEC_MGR,
				ToAuthType:    env.USER_CERTIFICATE,
				BatchSize:     101,
			},
			wantErr: "batchSize must be between 1 and 100",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateAuthTypeReconciliationParams(test.params)
			if test.wantErr != "" {
				require.ErrorContains(t, err, test.wantErr)
				return
			}
			require.NoError(t, err)
			if test.wantUUID != "" {
				require.Equal(t, test.wantUUID, test.params.PoolUUIDs[0])
			}
		})
	}
}

func TestReconcilePoolAuthTypes_StartsOneParentWorkflow(t *testing.T) {
	useHelmCA(t, "project", "pool", "ca")
	ctx := context.Background()
	store := database.NewMockStorage(t)
	temporalClient := workflowenginemock.NewMockTemporalTestClient(t)
	poolUUID := uuid.NewString()
	parentUUID := uuid.NewString()
	params := &commonparams.ReconcilePoolAuthTypeParams{
		SelectionType: "poolList",
		FromAuthType:  env.USERNAME_PWD_SEC_MGR,
		ToAuthType:    env.USER_CERTIFICATE,
		PoolUUIDs:     []string{poolUUID},
	}
	expectNoInFlightParent(store, ctx)
	store.On("GetPoolsByUUIDs", ctx, []string{poolUUID}).
		Return([]*datamodel.Pool{demotedAuthTypePool(poolUUID)}, nil).Once()
	store.On("CreateJob", ctx, mock.MatchedBy(func(job *datamodel.Job) bool {
		results := resultsFromAuthTypeReconciliationJob(job)
		return job.Type == string(datamodel.JobTypeAuthTypeReconciliation) &&
			job.State == string(datamodel.JobsStateNEW) &&
			len(results) == 1 &&
			results[0].Status == "QUEUED"
	})).Return(&datamodel.Job{
		BaseModel:     datamodel.BaseModel{UUID: parentUUID, CreatedAt: time.Now()},
		JobAttributes: &datamodel.JobAttributes{},
	}, nil).Once()
	temporalClient.On(
		"ExecuteWorkflow",
		mock.Anything,
		mock.MatchedBy(func(options client.StartWorkflowOptions) bool {
			return options.ID == parentUUID &&
				options.WorkflowRunTimeout == backgroundworkflows.AuthTypeReconciliationWorkflowRunTimeout(
					defaultAuthTypeReconciliationParentTimeout,
				)
		}),
		mock.Anything,
		mock.MatchedBy(func(input backgroundworkflows.ReconcilePoolAuthTypesAPIWorkflowParams) bool {
			return input.JobID == parentUUID &&
				input.BatchSize == defaultAuthTypeReconciliationBatchSize &&
				len(input.JobPayload.Results) == 1 &&
				input.JobPayload.DesiredCAURI == "project/pool/ca"
		}),
	).Return(nil, nil).Once()

	response, err := reconcilePoolAuthTypes(ctx, store, temporalClient, params)

	require.NoError(t, err)
	require.Equal(t, parentUUID, response.JobID)
	require.Equal(t, 1, response.QueuedPools)
	require.Equal(t, "QUEUED", response.Results[0].Status)
}

func TestReconcilePoolAuthTypes_RejectsInvalidHelmCA(t *testing.T) {
	useHelmCA(t, "", "", "")
	ctx := context.Background()
	store := database.NewMockStorage(t)
	temporalClient := workflowenginemock.NewMockTemporalTestClient(t)
	poolUUID := uuid.NewString()
	expectNoInFlightParent(store, ctx)
	store.On("GetPoolsByUUIDs", ctx, []string{poolUUID}).
		Return([]*datamodel.Pool{demotedAuthTypePool(poolUUID)}, nil).Once()

	response, err := reconcilePoolAuthTypes(ctx, store, temporalClient, &commonparams.ReconcilePoolAuthTypeParams{
		SelectionType: "poolList",
		FromAuthType:  env.USERNAME_PWD_SEC_MGR,
		ToAuthType:    env.USER_CERTIFICATE,
		PoolUUIDs:     []string{poolUUID},
	})

	require.Nil(t, response)
	require.True(t, customerrors.IsBadRequestErr(err))
	require.ErrorContains(t, err, "Helm CA URI must have project/caPool/ca format")
}

func TestReconcilePoolAuthTypes_RejectsConcurrentParent(t *testing.T) {
	ctx := context.Background()
	store := database.NewMockStorage(t)
	temporalClient := workflowenginemock.NewMockTemporalTestClient(t)
	activeID := uuid.NewString()
	store.On("GetJobsWithCondition", ctx, mock.Anything).
		Return([]*datamodel.Job{{
			BaseModel: datamodel.BaseModel{UUID: activeID},
			Type:      string(datamodel.JobTypeAuthTypeReconciliation),
			State:     string(datamodel.JobsStatePROCESSING),
		}}, nil).Once()

	response, err := reconcilePoolAuthTypes(ctx, store, temporalClient, &commonparams.ReconcilePoolAuthTypeParams{
		SelectionType: "all",
		FromAuthType:  env.USERNAME_PWD_SEC_MGR,
		ToAuthType:    env.USER_CERTIFICATE,
	})

	require.Nil(t, response)
	require.True(t, customerrors.IsConflictErr(err))
	require.ErrorContains(t, err, activeID)
}

func TestReconcilePoolAuthTypes_ParentStartFailureMarksJobError(t *testing.T) {
	useHelmCA(t, "project", "pool", "ca")
	ctx := context.Background()
	store := database.NewMockStorage(t)
	temporalClient := workflowenginemock.NewMockTemporalTestClient(t)
	poolUUID := uuid.NewString()
	parentUUID := uuid.NewString()
	expectNoInFlightParent(store, ctx)
	store.On("GetPoolsByUUIDs", ctx, []string{poolUUID}).
		Return([]*datamodel.Pool{demotedAuthTypePool(poolUUID)}, nil).Once()
	store.On("CreateJob", ctx, mock.Anything).
		Return(&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: parentUUID}}, nil).Once()
	temporalClient.On("ExecuteWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, errors.New("Temporal unavailable")).Once()
	store.On(
		"UpdateJob",
		mock.Anything,
		parentUUID,
		string(datamodel.JobsStateERROR),
		0,
		"Temporal unavailable",
	).Return(nil).Once()

	response, err := reconcilePoolAuthTypes(ctx, store, temporalClient, &commonparams.ReconcilePoolAuthTypeParams{
		SelectionType: "poolList",
		FromAuthType:  env.USERNAME_PWD_SEC_MGR,
		ToAuthType:    env.USER_CERTIFICATE,
		PoolUUIDs:     []string{poolUUID},
	})

	require.Nil(t, response)
	require.ErrorContains(t, err, "start auth-type reconciliation workflow")
}

func TestReconcilePoolAuthTypes_TemporalSingleFlightRaceReturnsConflict(t *testing.T) {
	useHelmCA(t, "project", "pool", "ca")
	ctx := context.Background()
	store := database.NewMockStorage(t)
	temporalClient := workflowenginemock.NewMockTemporalTestClient(t)
	poolUUID := uuid.NewString()
	parentUUID := uuid.NewString()
	expectNoInFlightParent(store, ctx)
	store.On("GetPoolsByUUIDs", ctx, []string{poolUUID}).
		Return([]*datamodel.Pool{demotedAuthTypePool(poolUUID)}, nil).Once()
	store.On("CreateJob", ctx, mock.Anything).
		Return(&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: parentUUID}}, nil).Once()
	temporalClient.On("ExecuteWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, serviceerror.NewWorkflowExecutionAlreadyStarted("running", "", "")).Once()
	store.On(
		"UpdateJob",
		mock.Anything,
		parentUUID,
		string(datamodel.JobsStateERROR),
		0,
		"running",
	).Return(nil).Once()

	response, err := reconcilePoolAuthTypes(ctx, store, temporalClient, &commonparams.ReconcilePoolAuthTypeParams{
		SelectionType: "poolList",
		FromAuthType:  env.USERNAME_PWD_SEC_MGR,
		ToAuthType:    env.USER_CERTIFICATE,
		PoolUUIDs:     []string{poolUUID},
	})

	require.Nil(t, response)
	require.True(t, customerrors.IsConflictErr(err))
}

func TestGetPoolAuthTypeReconciliationStatus_FromParentAttributes(t *testing.T) {
	ctx := context.Background()
	store := database.NewMockStorage(t)
	jobID := uuid.NewString()
	payload := models.AuthTypeReconciliationJobPayload{
		Results: []models.AuthTypeReconciliationPoolResult{
			{PoolUUID: "pool-1", Status: "DONE", JobID: "child-1"},
			{PoolUUID: "pool-2", Status: "ERROR", Reason: "failed"},
			{PoolUUID: "pool-3", Status: "SKIPPED", Reason: "not eligible"},
		},
	}
	store.On("GetJob", ctx, jobID).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: jobID},
		Type:      string(datamodel.JobTypeAuthTypeReconciliation),
		State:     string(datamodel.JobsStateERROR),
		JobAttributes: &datamodel.JobAttributes{
			PayloadAttributes: payload.Attributes(),
		},
	}, nil).Once()

	status, err := getPoolAuthTypeReconciliationStatus(ctx, store, jobID)

	require.NoError(t, err)
	require.Equal(t, "PARTIAL", status.Status)
	require.Equal(t, 3, status.TotalPools)
	require.Equal(t, 1, status.SucceededPools)
	require.Equal(t, 1, status.FailedPools)
	require.Equal(t, 1, status.SkippedPools)
}

func TestAuthTypeReconciliationTimeoutEnvNamesMatchHelm(t *testing.T) {
	require.Equal(t, "AUTH_TYPE_RECONCILIATION_CHILD_WORKFLOW_TIMEOUT_MINUTES", authTypeReconciliationChildTimeoutEnv)
	require.Equal(t, "AUTH_TYPE_RECONCILIATION_PARENT_WORKFLOW_TIMEOUT_MINUTES", authTypeReconciliationParentTimeoutEnv)
}

func TestAuthTypeReconciliationTimeouts(t *testing.T) {
	t.Setenv(authTypeReconciliationChildTimeoutEnv, "25")
	t.Setenv(authTypeReconciliationParentTimeoutEnv, "60")

	require.Equal(t, 25*time.Minute, authTypeReconciliationChildWorkflowTimeout())
	require.Equal(t, time.Hour, authTypeReconciliationParentTimeout())
}

func TestAuthTypeReconciliationTimeouts_Defaults(t *testing.T) {
	t.Setenv(authTypeReconciliationChildTimeoutEnv, "0")
	t.Setenv(authTypeReconciliationParentTimeoutEnv, "-1")

	require.Equal(t, defaultAuthTypeReconciliationChildTimeout, authTypeReconciliationChildWorkflowTimeout())
	require.Equal(t, defaultAuthTypeReconciliationParentTimeout, authTypeReconciliationParentTimeout())
}

func TestGCPOrchestratorAuthTypeWrappers(t *testing.T) {
	ctx := context.Background()
	store := database.NewMockStorage(t)
	orch := &GCPOrchestrator{storage: store}

	t.Run("missing dependencies", func(t *testing.T) {
		response, err := orch.ReconcilePoolAuthTypes(ctx, &commonparams.ReconcilePoolAuthTypeParams{
			SelectionType: "all",
			FromAuthType:  env.USERNAME_PWD_SEC_MGR,
			ToAuthType:    env.USER_CERTIFICATE,
		})
		require.Nil(t, response)
		require.True(t, customerrors.IsUnavailableErr(err))
	})

	t.Run("invalid job id", func(t *testing.T) {
		status, err := orch.GetPoolAuthTypeReconciliationStatus(ctx, "not-a-uuid")
		require.Nil(t, status)
		require.True(t, customerrors.IsBadRequestErr(err))
	})

	t.Run("invalid request body", func(t *testing.T) {
		orch := &GCPOrchestrator{storage: store, temporal: workflowenginemock.NewMockTemporalTestClient(t)}
		response, err := orch.ReconcilePoolAuthTypes(ctx, nil)
		require.Nil(t, response)
		require.True(t, customerrors.IsBadRequestErr(err))
	})
}

func TestGetPoolAuthTypeReconciliationStatus_Variants(t *testing.T) {
	ctx := context.Background()
	jobID := uuid.NewString()

	t.Run("retrieve error", func(t *testing.T) {
		store := database.NewMockStorage(t)
		store.On("GetJob", ctx, jobID).Return(nil, errors.New("db down")).Once()
		status, err := getPoolAuthTypeReconciliationStatus(ctx, store, jobID)
		require.Nil(t, status)
		require.ErrorContains(t, err, "retrieve auth-type reconciliation job")
	})

	t.Run("not found", func(t *testing.T) {
		store := database.NewMockStorage(t)
		store.On("GetJob", ctx, jobID).Return(nil, customerrors.NewNotFoundErr("job", &jobID)).Once()
		status, err := getPoolAuthTypeReconciliationStatus(ctx, store, jobID)
		require.Nil(t, status)
		require.True(t, customerrors.IsNotFoundErr(err))
	})

	t.Run("wrong job type", func(t *testing.T) {
		store := database.NewMockStorage(t)
		store.On("GetJob", ctx, jobID).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: jobID},
			Type:      "CREATE_POOL",
		}, nil).Once()
		status, err := getPoolAuthTypeReconciliationStatus(ctx, store, jobID)
		require.Nil(t, status)
		require.True(t, customerrors.IsNotFoundErr(err))
	})

	t.Run("processing keeps parent state", func(t *testing.T) {
		store := database.NewMockStorage(t)
		payload := models.AuthTypeReconciliationJobPayload{
			Results: []models.AuthTypeReconciliationPoolResult{
				{PoolUUID: "b-pool", Status: "QUEUED"},
				{PoolUUID: "a-pool", Status: "PROCESSING"},
			},
		}
		store.On("GetJob", ctx, jobID).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: jobID},
			Type:      string(datamodel.JobTypeAuthTypeReconciliation),
			State:     string(datamodel.JobsStatePROCESSING),
			JobAttributes: &datamodel.JobAttributes{
				PayloadAttributes: payload.Attributes(),
			},
		}, nil).Once()
		status, err := getPoolAuthTypeReconciliationStatus(ctx, store, jobID)
		require.NoError(t, err)
		require.Equal(t, "PROCESSING", status.Status)
		require.Equal(t, "a-pool", status.Results[0].PoolUUID)
		require.Equal(t, 1, status.QueuedPools)
		require.Equal(t, 1, status.ProcessingPools)
	})

	t.Run("all succeeded reports done", func(t *testing.T) {
		store := database.NewMockStorage(t)
		payload := models.AuthTypeReconciliationJobPayload{
			Results: []models.AuthTypeReconciliationPoolResult{{PoolUUID: "pool-1", Status: "DONE"}},
		}
		store.On("GetJob", ctx, jobID).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: jobID},
			Type:      string(datamodel.JobTypeAuthTypeReconciliation),
			State:     string(datamodel.JobsStateDONE),
			JobAttributes: &datamodel.JobAttributes{
				PayloadAttributes: payload.Attributes(),
			},
		}, nil).Once()
		status, err := getPoolAuthTypeReconciliationStatus(ctx, store, jobID)
		require.NoError(t, err)
		require.Equal(t, "DONE", status.Status)
	})
}

func TestReconcilePoolAuthTypes_NoEligiblePools(t *testing.T) {
	ctx := context.Background()
	store := database.NewMockStorage(t)
	temporalClient := workflowenginemock.NewMockTemporalTestClient(t)
	missingUUID := uuid.NewString()
	expectNoInFlightParent(store, ctx)
	store.On("GetPoolsByUUIDs", ctx, []string{missingUUID}).
		Return([]*datamodel.Pool{}, nil).Once()

	response, err := reconcilePoolAuthTypes(ctx, store, temporalClient, &commonparams.ReconcilePoolAuthTypeParams{
		SelectionType: "poolList",
		FromAuthType:  env.USERNAME_PWD_SEC_MGR,
		ToAuthType:    env.USER_CERTIFICATE,
		PoolUUIDs:     []string{missingUUID},
	})
	require.Nil(t, response)
	require.True(t, customerrors.IsBadRequestErr(err))
	require.ErrorContains(t, err, "no eligible demoted auth_type=1 pools found")
}

func TestReconcilePoolAuthTypes_InFlightLookupError(t *testing.T) {
	ctx := context.Background()
	store := database.NewMockStorage(t)
	temporalClient := workflowenginemock.NewMockTemporalTestClient(t)
	store.On("GetJobsWithCondition", ctx, mock.Anything).
		Return(nil, errors.New("jobs unavailable")).Once()

	response, err := reconcilePoolAuthTypes(ctx, store, temporalClient, &commonparams.ReconcilePoolAuthTypeParams{
		SelectionType: "all",
		FromAuthType:  env.USERNAME_PWD_SEC_MGR,
		ToAuthType:    env.USER_CERTIFICATE,
	})
	require.Nil(t, response)
	require.ErrorContains(t, err, "check in-flight auth-type reconciliation")
}

func TestReconcilePoolAuthTypes_CreateJobError(t *testing.T) {
	useHelmCA(t, "project", "pool", "ca")
	ctx := context.Background()
	store := database.NewMockStorage(t)
	temporalClient := workflowenginemock.NewMockTemporalTestClient(t)
	poolUUID := uuid.NewString()
	expectNoInFlightParent(store, ctx)
	store.On("GetPoolsByUUIDs", ctx, []string{poolUUID}).
		Return([]*datamodel.Pool{demotedAuthTypePool(poolUUID)}, nil).Once()
	store.On("CreateJob", ctx, mock.Anything).Return(nil, errors.New("insert failed")).Once()

	response, err := reconcilePoolAuthTypes(ctx, store, temporalClient, &commonparams.ReconcilePoolAuthTypeParams{
		SelectionType: "poolList",
		FromAuthType:  env.USERNAME_PWD_SEC_MGR,
		ToAuthType:    env.USER_CERTIFICATE,
		PoolUUIDs:     []string{poolUUID},
	})
	require.Nil(t, response)
	require.ErrorContains(t, err, "create auth-type reconciliation job")
}

func TestSelectAuthTypeReconciliationPools_PoolListSkipped(t *testing.T) {
	ctx := context.Background()
	store := database.NewMockStorage(t)
	missingUUID := uuid.NewString()
	store.On("GetPoolsByUUIDs", ctx, []string{missingUUID}).
		Return([]*datamodel.Pool{}, nil).Once()

	pools, skipped, err := selectAuthTypeReconciliationPools(ctx, store, &commonparams.ReconcilePoolAuthTypeParams{
		SelectionType: "poolList",
		PoolUUIDs:     []string{missingUUID},
	}, 30)

	require.NoError(t, err)
	require.Empty(t, pools)
	require.Len(t, skipped, 1)
	require.Equal(t, "SKIPPED", skipped[0].Status)
}

func TestResultsFromAuthTypeReconciliationJob(t *testing.T) {
	require.Nil(t, resultsFromAuthTypeReconciliationJob(nil))
	require.Nil(t, resultsFromAuthTypeReconciliationJob(&datamodel.Job{}))
	require.Nil(t, resultsFromAuthTypeReconciliationJob(&datamodel.Job{
		JobAttributes: &datamodel.JobAttributes{PayloadAttributes: map[string]interface{}{}},
	}))
	results := resultsFromAuthTypeReconciliationJob(&datamodel.Job{
		JobAttributes: &datamodel.JobAttributes{
			PayloadAttributes: map[string]interface{}{
				"results": []interface{}{
					"skip-me",
					map[string]interface{}{"poolUuid": "pool-1", "status": "DONE", "jobId": "job-1", "reason": ""},
				},
			},
		},
	})
	require.Len(t, results, 1)
	require.Equal(t, "pool-1", results[0].PoolUUID)
}

func TestFindInFlightAuthTypeReconciliationParentJob_SkipsNilAndTerminal(t *testing.T) {
	ctx := context.Background()
	store := database.NewMockStorage(t)
	activeID := uuid.NewString()
	store.On("GetJobsWithCondition", ctx, mock.MatchedBy(authTypeReconciliationInFlightFilter)).Return([]*datamodel.Job{
		nil,
		{State: string(datamodel.JobsStateDONE), Type: string(datamodel.JobTypeAuthTypeReconciliation)},
		{BaseModel: datamodel.BaseModel{UUID: activeID}, State: string(datamodel.JobsStateNEW), Type: string(datamodel.JobTypeAuthTypeReconciliation)},
	}, nil).Once()

	job, err := findInFlightAuthTypeReconciliationParentJob(ctx, store)
	require.NoError(t, err)
	require.Equal(t, activeID, job.UUID)
}

func TestFindInFlightAuthTypeReconciliationParentJob(t *testing.T) {
	ctx := context.Background()
	processingID := uuid.NewString()
	tests := []struct {
		name    string
		jobs    []*datamodel.Job
		readErr error
		wantID  string
		wantErr string
	}{
		{
			name: "returns processing job",
			jobs: []*datamodel.Job{{
				BaseModel: datamodel.BaseModel{UUID: processingID},
				State:     string(datamodel.JobsStatePROCESSING),
			}},
			wantID: processingID,
		},
		{
			name: "no in-flight job",
			jobs: []*datamodel.Job{{State: string(datamodel.JobsStateERROR)}},
		},
		{
			name:    "query failure",
			readErr: errors.New("database unavailable"),
			wantErr: "database unavailable",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := database.NewMockStorage(t)
			store.On("GetJobsWithCondition", ctx, mock.MatchedBy(authTypeReconciliationInFlightFilter)).
				Return(test.jobs, test.readErr).Once()

			job, err := findInFlightAuthTypeReconciliationParentJob(ctx, store)
			if test.wantErr != "" {
				require.ErrorContains(t, err, test.wantErr)
				require.Nil(t, job)
				return
			}
			require.NoError(t, err)
			if test.wantID == "" {
				require.Nil(t, job)
				return
			}
			require.Equal(t, test.wantID, job.UUID)
		})
	}
}

func authTypeReconciliationInFlightFilter(filter dbutils.Filter) bool {
	var hasType, hasInFlightStates bool
	for _, condition := range filter.Conditions {
		if condition == nil {
			continue
		}
		if condition.Field == "type" && condition.Op == "=" && condition.Value == string(datamodel.JobTypeAuthTypeReconciliation) {
			hasType = true
		}
		if condition.Field == "state" && condition.Op == "in" {
			states, ok := condition.Value.([]string)
			hasInFlightStates = ok && len(states) == 2 &&
				states[0] == string(datamodel.JobsStateNEW) &&
				states[1] == string(datamodel.JobsStatePROCESSING)
		}
	}
	return hasType && hasInFlightStates
}

func TestPoolFromView(t *testing.T) {
	require.Nil(t, poolFromView(nil))
	pool := poolFromView(&datamodel.PoolView{Pool: datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-1"}}})
	require.Equal(t, "pool-1", pool.UUID)
}

func TestSelectAuthTypeReconciliationPools_All(t *testing.T) {
	ctx := context.Background()
	store := database.NewMockStorage(t)
	eligible := demotedAuthTypePool(uuid.NewString())
	native := *eligible
	native.UUID = uuid.NewString()
	native.PoolCredentials = &datamodel.PoolCredentials{AuthType: env.USERNAME_PWD_SEC_MGR}
	store.On(
		"ListPoolsWithFilterAndPaginationOrderedByUUID",
		ctx,
		mock.AnythingOfType("*utils.Filter"),
		&dbutils.Pagination{Limit: 30, Offset: 0},
	).Return([]*datamodel.PoolView{{Pool: *eligible}, {Pool: native}}, nil).Once()

	pools, skipped, err := selectAuthTypeReconciliationPools(ctx, store, &commonparams.ReconcilePoolAuthTypeParams{
		SelectionType: "all",
	}, 30)

	require.NoError(t, err)
	require.Len(t, pools, 1)
	require.Empty(t, skipped)
	require.Equal(t, eligible.UUID, pools[0].UUID)
}

func TestSelectAuthTypeReconciliationPools_ErrorsAndPagination(t *testing.T) {
	ctx := context.Background()

	t.Run("pool list retrieve error", func(t *testing.T) {
		store := database.NewMockStorage(t)
		poolUUID := uuid.NewString()
		store.On("GetPoolsByUUIDs", ctx, []string{poolUUID}).
			Return(nil, errors.New("pools unavailable")).Once()
		_, _, err := selectAuthTypeReconciliationPools(ctx, store, &commonparams.ReconcilePoolAuthTypeParams{
			SelectionType: "poolList",
			PoolUUIDs:     []string{poolUUID},
		}, 30)
		require.ErrorContains(t, err, "retrieve requested pools")
	})

	t.Run("all list error", func(t *testing.T) {
		store := database.NewMockStorage(t)
		store.On(
			"ListPoolsWithFilterAndPaginationOrderedByUUID",
			ctx,
			mock.AnythingOfType("*utils.Filter"),
			&dbutils.Pagination{Limit: 1, Offset: 0},
		).Return(nil, errors.New("list failed")).Once()
		_, _, err := selectAuthTypeReconciliationPools(ctx, store, &commonparams.ReconcilePoolAuthTypeParams{
			SelectionType: "all",
		}, 1)
		require.ErrorContains(t, err, "list demoted auth_type=1 pools")
	})

	t.Run("paginates and skips nil views", func(t *testing.T) {
		store := database.NewMockStorage(t)
		page1 := demotedAuthTypePool(uuid.NewString())
		page2 := demotedAuthTypePool(uuid.NewString())
		store.On(
			"ListPoolsWithFilterAndPaginationOrderedByUUID",
			ctx,
			mock.AnythingOfType("*utils.Filter"),
			&dbutils.Pagination{Limit: 2, Offset: 0},
		).Return([]*datamodel.PoolView{nil, {Pool: *page1}}, nil).Once()
		store.On(
			"ListPoolsWithFilterAndPaginationOrderedByUUID",
			ctx,
			mock.AnythingOfType("*utils.Filter"),
			&dbutils.Pagination{Limit: 2, Offset: 2},
		).Return([]*datamodel.PoolView{{Pool: *page2}}, nil).Once()

		pools, skipped, err := selectAuthTypeReconciliationPools(ctx, store, &commonparams.ReconcilePoolAuthTypeParams{
			SelectionType: "all",
		}, 2)
		require.NoError(t, err)
		require.Empty(t, skipped)
		require.Equal(t, []string{page1.UUID, page2.UUID}, []string{pools[0].UUID, pools[1].UUID})
	})
}

func expectNoInFlightParent(store *database.MockStorage, ctx context.Context) {
	store.On("GetJobsWithCondition", ctx, mock.Anything).
		Return([]*datamodel.Job{}, nil).Once()
}

func demotedAuthTypePool(poolUUID string) *datamodel.Pool {
	return &datamodel.Pool{
		BaseModel:    datamodel.BaseModel{UUID: poolUUID},
		State:        "READY",
		ServiceLevel: "FLEX",
		PoolCredentials: &datamodel.PoolCredentials{
			AuthType:      env.USERNAME_PWD_SEC_MGR,
			CertificateID: "certificate-id",
			CaURI:         "project/pool/ca",
		},
	}
}

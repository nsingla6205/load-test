package api

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/cvpapi"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/cvpapi/kms_configurations"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/models"
	coremodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/factory"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	gcpgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	utilerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
)

func validTrialModeOpt(t *testing.T) gcpgenserver.OptTrialModeV1beta {
	t.Helper()
	return gcpgenserver.NewOptTrialModeV1beta(gcpgenserver.TrialModeV1beta{
		StartTime: time.Date(2025, 5, 14, 11, 0, 0, 0, time.UTC),
		EndTime:   time.Date(2025, 6, 13, 11, 0, 0, 0, time.UTC),
	})
}

func invalidTrialModeOptEndBeforeStart() gcpgenserver.OptTrialModeV1beta {
	return gcpgenserver.NewOptTrialModeV1beta(gcpgenserver.TrialModeV1beta{
		StartTime: time.Date(2025, 6, 13, 11, 0, 0, 0, time.UTC),
		EndTime:   time.Date(2025, 5, 14, 11, 0, 0, 0, time.UTC),
	})
}

func stubValidRegionParse() {
	parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
		return "us-central1", "us-central1", nil
	}
}

func stubValidRegionParseEast4() {
	parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
		return "us-east4", "us-east4", nil
	}
}

// withVCPAsyncADCreatePath forces async orchestrator create with trialMode mapped at the handler.
func withVCPAsyncADCreatePath(t *testing.T) func() {
	t.Helper()
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = ""
	return func() {
		cvp.CVP_HOST = origCVPHost
	}
}

// withSDEAsyncADCreatePath forces async orchestrator create (CVP_HOST unset).
func withSDEAsyncADCreatePath(t *testing.T) func() {
	t.Helper()
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = ""
	return func() {
		cvp.CVP_HOST = origCVPHost
	}
}

// withSDESyncADCreatePath forces createActiveDirectorySyncViaCVP (SDE sync create).
func withSDESyncADCreatePath(t *testing.T) func() {
	t.Helper()
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	return func() {
		cvp.CVP_HOST = origCVPHost
	}
}

func minimalActiveDirectoryForHandlerResponse(adName string) *coremodels.ActiveDirectory {
	return &coremodels.ActiveDirectory{
		BaseModel: coremodels.BaseModel{
			UUID:      "ad-uuid",
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		},
		AdName:                    adName,
		Username:                  "user",
		Domain:                    "domain",
		DNS:                       "dns",
		NetBIOS:                   "netbios",
		ActiveDirectoryAttributes: &coremodels.ActiveDirectoryAttributes{},
	}
}

func minimalPoolCreateRequest(trial gcpgenserver.OptTrialModeV1beta) *gcpgenserver.PoolV1beta {
	return &gcpgenserver.PoolV1beta{
		ResourceId:               "test-pool",
		Unified:                  gcpgenserver.NewOptBool(true),
		ServiceLevel:             gcpgenserver.PoolV1betaServiceLevelFLEX,
		SizeInBytes:              2199023255552,
		QosType:                  gcpgenserver.NewOptNilString("auto"),
		CustomPerformanceEnabled: gcpgenserver.NewOptBool(true),
		TotalThroughputMibps:     gcpgenserver.NewOptNilFloat64(64),
		Network:                  "test-network",
		TrialMode:                trial,
	}
}

func poolCreateParams() gcpgenserver.V1betaCreatePoolParams {
	return gcpgenserver.V1betaCreatePoolParams{
		LocationId:    "us-east4-a",
		ProjectNumber: "project-number",
	}
}

func stubPoolZonalRegionParse() {
	parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
		return "us-east4", "us-east4-a", nil
	}
}

func expectSDEJobMaybe(mockOrchestrator *factory.MockOrchestratorFactory) {
	job := &datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "sde-job-id"}}
	mockOrchestrator.EXPECT().CreateJob(mock.Anything, mock.Anything).Maybe().Return(job, nil)
	mockOrchestrator.EXPECT().
		UpdateJobStatus(mock.Anything, job.UUID, mock.Anything, mock.Anything, mock.Anything).
		Maybe().
		Return(nil)
	mockOrchestrator.EXPECT().
		UpdateJobAttributes(mock.Anything, job.UUID, mock.Anything).
		Maybe().
		Return(nil)
}

func Test_convertCreateRequestToCreateBackupPolicyParams_TrialMode(t *testing.T) {
	start := time.Date(2025, 5, 14, 11, 0, 0, 0, time.UTC)
	end := time.Date(2025, 6, 13, 11, 0, 0, 0, time.UTC)

	t.Run("does not map trial on create params", func(t *testing.T) {
		req := &gcpgenserver.BackupPolicyCreateV1beta{
			ResourceId: "policy-1",
			TrialMode: gcpgenserver.NewOptTrialModeV1beta(gcpgenserver.TrialModeV1beta{
				StartTime: start,
				EndTime:   end,
			}),
		}
		got := convertCreateRequestToCreateBackupPolicyParams(req, "us-central1", "1234567890")
		assert.Nil(t, got.TrialMode)
	})
}

func TestV1betaCreateActiveDirectory_TrialMode(t *testing.T) {
	params := gcpgenserver.V1betaCreateActiveDirectoryParams{
		ProjectNumber: "pn",
		LocationId:    "us-central1",
	}

	baseReq := func(trial gcpgenserver.OptTrialModeV1beta) *gcpgenserver.ActiveDirectoryV1beta {
		return &gcpgenserver.ActiveDirectoryV1beta{
			Username:   "user",
			ResourceId: "ad-name",
			Password:   "pass",
			Domain:     "domain",
			DNS:        "dns",
			NetBIOS:    "netbios",
			TrialMode:  trial,
		}
	}

	t.Run("ReturnsBadRequestWhenTrialModeInvalid", func(t *testing.T) {
		defer withVCPAsyncADCreatePath(t)()

		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		mockOrchestrator.EXPECT().CreateActiveDirectory(mock.Anything, mock.Anything).
			Return(nil, "", utilerrors.NewUserInputValidationErr("trialMode startTime must be before endTime"))

		handler := Handler{Orchestrator: mockOrchestrator}
		res, err := handler.V1betaCreateActiveDirectory(context.Background(), baseReq(invalidTrialModeOptEndBeforeStart()), params)
		assert.NoError(t, err)
		badReq, ok := res.(*gcpgenserver.V1betaCreateActiveDirectoryBadRequest)
		require.True(t, ok)
		assert.Equal(t, http.StatusBadRequest, int(badReq.Code))
		assert.Equal(t, "trialMode startTime must be before endTime", badReq.Message)
	})

	t.Run("PassesTrialModeToOrchestratorWhenValid", func(t *testing.T) {
		defer withVCPAsyncADCreatePath(t)()

		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		start := time.Date(2025, 5, 14, 11, 0, 0, 0, time.UTC)
		end := time.Date(2025, 6, 13, 11, 0, 0, 0, time.UTC)

		mockOrchestrator.EXPECT().CreateActiveDirectory(mock.Anything, mock.MatchedBy(func(p *common.CreateActiveDirectoryParams) bool {
			if p.TrialMode == nil || p.TrialMode.Start == nil || p.TrialMode.End == nil {
				return false
			}
			return p.TrialMode.Start.Equal(start) && p.TrialMode.End.Equal(end)
		})).Return(minimalActiveDirectoryForHandlerResponse("ad-name"), "job-uuid", nil)

		handler := Handler{Orchestrator: mockOrchestrator}
		res, err := handler.V1betaCreateActiveDirectory(context.Background(), baseReq(validTrialModeOpt(t)), params)
		assert.NoError(t, err)
		_, ok := res.(*gcpgenserver.OperationV1beta)
		assert.True(t, ok)
	})

	t.Run("SDE_Async_ReturnsBadRequestWhenTrialModeInvalid", func(t *testing.T) {
		defer withSDEAsyncADCreatePath(t)()

		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		mockOrchestrator.EXPECT().CreateActiveDirectory(mock.Anything, mock.MatchedBy(func(p *common.CreateActiveDirectoryParams) bool {
			return p.TrialMode != nil && p.TrialMode.Start != nil && p.TrialMode.End != nil &&
				p.TrialMode.End.Before(*p.TrialMode.Start)
		})).Return(nil, "", utilerrors.NewUserInputValidationErr("trialMode startTime must be before endTime"))

		handler := Handler{Orchestrator: mockOrchestrator}
		res, err := handler.V1betaCreateActiveDirectory(context.Background(), baseReq(invalidTrialModeOptEndBeforeStart()), params)
		assert.NoError(t, err)
		badReq, ok := res.(*gcpgenserver.V1betaCreateActiveDirectoryBadRequest)
		require.True(t, ok)
		assert.Equal(t, http.StatusBadRequest, int(badReq.Code))
		assert.Equal(t, "trialMode startTime must be before endTime", badReq.Message)
		mockOrchestrator.AssertNotCalled(t, "PersistAccountTrialMetadataIfSet", mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("SDE_Sync_ReturnsBadRequestWhenTrialModeInvalid", func(t *testing.T) {
		defer withSDESyncADCreatePath(t)()

		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		mockOrchestrator.EXPECT().PersistAccountTrialMetadataIfSet(mock.Anything, params.ProjectNumber, mock.Anything).
			Return(utilerrors.NewUserInputValidationErr("trialMode startTime must be before endTime"))

		handler := Handler{Orchestrator: mockOrchestrator}
		res, err := handler.V1betaCreateActiveDirectory(context.Background(), baseReq(invalidTrialModeOptEndBeforeStart()), params)
		assert.NoError(t, err)
		badReq, ok := res.(*gcpgenserver.V1betaCreateActiveDirectoryBadRequest)
		require.True(t, ok)
		assert.Equal(t, http.StatusBadRequest, int(badReq.Code))
		assert.Equal(t, "trialMode startTime must be before endTime", badReq.Message)
		mockOrchestrator.AssertNotCalled(t, "CreateActiveDirectory", mock.Anything, mock.Anything)
	})

	t.Run("SDE_Sync_ReturnsInternalServerErrorWhenTrialPersistFails", func(t *testing.T) {
		defer withSDESyncADCreatePath(t)()

		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		mockOrchestrator.EXPECT().PersistAccountTrialMetadataIfSet(mock.Anything, params.ProjectNumber, mock.Anything).
			Return(errors.New("trial metadata persist failed"))

		handler := Handler{Orchestrator: mockOrchestrator}
		res, err := handler.V1betaCreateActiveDirectory(context.Background(), baseReq(validTrialModeOpt(t)), params)
		assert.NoError(t, err)
		serverErr, ok := res.(*gcpgenserver.V1betaCreateActiveDirectoryInternalServerError)
		require.True(t, ok)
		assert.Equal(t, http.StatusInternalServerError, int(serverErr.Code))
		assert.Equal(t, "trial metadata persist failed", serverErr.Message)
		mockOrchestrator.AssertNotCalled(t, "CreateActiveDirectory", mock.Anything, mock.Anything)
	})
}

func TestV1betaCreatePool_TrialMode(t *testing.T) {
	params := poolCreateParams()

	t.Run("ReturnsBadRequestWhenTrialModeInvalid", func(t *testing.T) {
		oldParse := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = oldParse }()
		stubPoolZonalRegionParse()

		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		mockOrchestrator.EXPECT().GetPoolByVendorID(mock.Anything, mock.Anything, mock.Anything).
			Return(nil, utilerrors.NewNotFoundErr("pool", nil))
		mockOrchestrator.EXPECT().CreatePool(mock.Anything, mock.Anything).
			Return(nil, "", utilerrors.NewUserInputValidationErr("trialMode startTime must be before endTime"))

		res, err := Handler{Orchestrator: mockOrchestrator}.V1betaCreatePool(
			context.Background(),
			minimalPoolCreateRequest(invalidTrialModeOptEndBeforeStart()),
			params,
		)
		assert.NoError(t, err)
		badReq, ok := res.(*gcpgenserver.V1betaCreatePoolBadRequest)
		require.True(t, ok)
		assert.Equal(t, http.StatusBadRequest, int(badReq.Code))
		assert.Equal(t, "trialMode startTime must be before endTime", badReq.Message)
	})

	t.Run("PassesTrialModeToOrchestratorWhenValid", func(t *testing.T) {
		oldParse := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = oldParse }()
		stubPoolZonalRegionParse()

		start := time.Date(2025, 5, 14, 11, 0, 0, 0, time.UTC)
		end := time.Date(2025, 6, 13, 11, 0, 0, 0, time.UTC)

		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		mockOrchestrator.EXPECT().GetPoolByVendorID(mock.Anything, mock.Anything, mock.Anything).
			Return(nil, utilerrors.NewNotFoundErr("pool", nil))
		mockOrchestrator.EXPECT().CreatePool(mock.Anything, mock.MatchedBy(func(p *common.CreatePoolParams) bool {
			if p.TrialMode == nil || p.TrialMode.Start == nil || p.TrialMode.End == nil {
				return false
			}
			return p.TrialMode.Start.Equal(start) && p.TrialMode.End.Equal(end)
		})).Return(&coremodels.Pool{
			BaseModel:      coremodels.BaseModel{UUID: "pool-uuid"},
			PoolAttributes: &coremodels.PoolAttributes{},
		}, "operation-id", nil)

		res, err := Handler{Orchestrator: mockOrchestrator}.V1betaCreatePool(
			context.Background(),
			minimalPoolCreateRequest(gcpgenserver.NewOptTrialModeV1beta(gcpgenserver.TrialModeV1beta{
				StartTime: start,
				EndTime:   end,
			})),
			params,
		)
		assert.NoError(t, err)
		_, ok := res.(*gcpgenserver.OperationV1beta)
		assert.True(t, ok)
	})

	t.Run("OmitsTrialModeWhenNotSet", func(t *testing.T) {
		oldParse := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = oldParse }()
		stubPoolZonalRegionParse()

		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		mockOrchestrator.EXPECT().GetPoolByVendorID(mock.Anything, mock.Anything, mock.Anything).
			Return(nil, utilerrors.NewNotFoundErr("pool", nil))
		mockOrchestrator.EXPECT().CreatePool(mock.Anything, mock.MatchedBy(func(p *common.CreatePoolParams) bool {
			return p.TrialMode == nil
		})).Return(&coremodels.Pool{
			BaseModel:      coremodels.BaseModel{UUID: "pool-uuid"},
			PoolAttributes: &coremodels.PoolAttributes{},
		}, "operation-id", nil)

		res, err := Handler{Orchestrator: mockOrchestrator}.V1betaCreatePool(
			context.Background(),
			minimalPoolCreateRequest(gcpgenserver.OptTrialModeV1beta{}),
			params,
		)
		assert.NoError(t, err)
		_, ok := res.(*gcpgenserver.OperationV1beta)
		assert.True(t, ok)
	})
}

func TestV1betaCreateBackupPolicy_TrialMode(t *testing.T) {
	ctx := context.Background()
	params := gcpgenserver.V1betaCreateBackupPolicyParams{
		LocationId:    "us-central1",
		ProjectNumber: "1234567890",
	}

	t.Run("ReturnsBadRequestWhenTrialModeInvalid", func(t *testing.T) {
		oldBackupEnabled := backupEnabled
		oldUseVCPRegion := cvp.CVP_HOST
		oldParse := parseAndValidateRegionAndZone
		defer func() {
			backupEnabled = oldBackupEnabled
			cvp.CVP_HOST = oldUseVCPRegion
			parseAndValidateRegionAndZone = oldParse
		}()
		backupEnabled = true
		cvp.CVP_HOST = ""
		stubValidRegionParse()

		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		mockOrchestrator.EXPECT().PersistAccountTrialMetadataIfSet(mock.Anything, "1234567890", mock.Anything).
			Return(utilerrors.NewUserInputValidationErr("trialMode startTime must be before endTime"))

		req := &gcpgenserver.BackupPolicyCreateV1beta{
			ResourceId: "policy-1",
			TrialMode:  invalidTrialModeOptEndBeforeStart(),
		}
		res, err := Handler{Orchestrator: mockOrchestrator}.V1betaCreateBackupPolicy(ctx, req, params)
		assert.NoError(t, err)
		badReq, ok := res.(*gcpgenserver.V1betaCreateBackupPolicyBadRequest)
		require.True(t, ok)
		assert.Equal(t, float64(400), badReq.Code)
		assert.Equal(t, "trialMode startTime must be before endTime", badReq.Message)
		mockOrchestrator.AssertNotCalled(t, "CreateBackupPolicy", mock.Anything, mock.Anything)
	})

	t.Run("VCP_PersistsTrialAtHandlerBeforeCreate", func(t *testing.T) {
		oldBackupEnabled := backupEnabled
		oldUseVCPRegion := cvp.CVP_HOST
		oldParse := parseAndValidateRegionAndZone
		defer func() {
			backupEnabled = oldBackupEnabled
			cvp.CVP_HOST = oldUseVCPRegion
			parseAndValidateRegionAndZone = oldParse
		}()
		backupEnabled = true
		cvp.CVP_HOST = ""
		stubValidRegionParse()

		start := time.Date(2025, 5, 14, 11, 0, 0, 0, time.UTC)
		end := time.Date(2025, 6, 13, 11, 0, 0, 0, time.UTC)

		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		mockOrchestrator.EXPECT().PersistAccountTrialMetadataIfSet(mock.Anything, "1234567890", mock.MatchedBy(func(trial *common.TrialModeParams) bool {
			return trial != nil && trial.Start.Equal(start) && trial.End.Equal(end)
		})).Return(nil)
		mockOrchestrator.EXPECT().GetBackupPolicyByNameAndOwnerID(ctx, "policy-1", "1234567890").
			Return(nil, utilerrors.NewNotFoundErr("backup policy", nil))
		mockOrchestrator.EXPECT().CreateBackupPolicy(ctx, mock.MatchedBy(func(p *common.CreateBackupPolicyParams) bool {
			return p.TrialMode == nil
		})).Return(&coremodels.BackupPolicy{
			BackupPolicyUUID: "uuid-1",
			ResourceID:       "policy-1",
		}, nil)
		mockOrchestrator.EXPECT().ListBackupPoliciesAndVolumeCount(ctx, "1234567890", []string{"uuid-1"}).
			Return(map[string]int64{"uuid-1": 0}, map[string]*coremodels.BackupPolicy{}, nil)

		req := &gcpgenserver.BackupPolicyCreateV1beta{
			ResourceId: "policy-1",
			TrialMode: gcpgenserver.NewOptTrialModeV1beta(gcpgenserver.TrialModeV1beta{
				StartTime: start,
				EndTime:   end,
			}),
		}
		res, err := Handler{Orchestrator: mockOrchestrator}.V1betaCreateBackupPolicy(ctx, req, params)
		assert.NoError(t, err)
		_, ok := res.(*gcpgenserver.OperationV1beta)
		assert.True(t, ok)
	})

	t.Run("SDE_ReturnsBadRequestWhenTrialModeInvalid", func(t *testing.T) {
		oldBackupEnabled := backupEnabled
		oldUseVCPRegion := cvp.CVP_HOST
		oldParse := parseAndValidateRegionAndZone
		oldCreateClient := createClient
		defer func() {
			backupEnabled = oldBackupEnabled
			cvp.CVP_HOST = oldUseVCPRegion
			parseAndValidateRegionAndZone = oldParse
			createClient = oldCreateClient
		}()
		backupEnabled = true
		cvp.CVP_HOST = "http://cvp-host"
		stubValidRegionParse()

		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		mockOrchestrator.EXPECT().PersistAccountTrialMetadataIfSet(mock.Anything, "1234567890", mock.Anything).
			Return(utilerrors.NewUserInputValidationErr("trialMode startTime must be before endTime"))

		req := &gcpgenserver.BackupPolicyCreateV1beta{
			ResourceId: "policy-1",
			TrialMode:  invalidTrialModeOptEndBeforeStart(),
		}
		res, err := Handler{Orchestrator: mockOrchestrator}.V1betaCreateBackupPolicy(ctx, req, params)
		assert.NoError(t, err)
		badReq, ok := res.(*gcpgenserver.V1betaCreateBackupPolicyBadRequest)
		require.True(t, ok)
		assert.Equal(t, "trialMode startTime must be before endTime", badReq.Message)
		mockOrchestrator.AssertNotCalled(t, "GetBackupPolicyByNameAndOwnerID", mock.Anything, mock.Anything, mock.Anything)
		mockOrchestrator.AssertNotCalled(t, "CreateBackupPolicy", mock.Anything, mock.Anything)
	})

	t.Run("ReturnsInternalServerErrorWhenTrialPersistFails", func(t *testing.T) {
		oldBackupEnabled := backupEnabled
		oldUseVCPRegion := cvp.CVP_HOST
		oldParse := parseAndValidateRegionAndZone
		defer func() {
			backupEnabled = oldBackupEnabled
			cvp.CVP_HOST = oldUseVCPRegion
			parseAndValidateRegionAndZone = oldParse
		}()
		backupEnabled = true
		cvp.CVP_HOST = ""
		stubValidRegionParse()

		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		mockOrchestrator.EXPECT().PersistAccountTrialMetadataIfSet(mock.Anything, "1234567890", mock.Anything).
			Return(errors.New("trial metadata persist failed"))

		req := &gcpgenserver.BackupPolicyCreateV1beta{ResourceId: "policy-1", TrialMode: validTrialModeOpt(t)}
		res, err := Handler{Orchestrator: mockOrchestrator}.V1betaCreateBackupPolicy(ctx, req, params)
		assert.NoError(t, err)
		serverErr, ok := res.(*gcpgenserver.V1betaCreateBackupPolicyInternalServerError)
		require.True(t, ok)
		assert.Equal(t, float64(http.StatusInternalServerError), serverErr.Code)
		assert.Equal(t, "trial metadata persist failed", serverErr.Message)
		mockOrchestrator.AssertNotCalled(t, "GetBackupPolicyByNameAndOwnerID", mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("VCP_ReturnsBadRequestWhenCreateBackupPolicyTrialInvalid", func(t *testing.T) {
		oldBackupEnabled := backupEnabled
		oldUseVCPRegion := cvp.CVP_HOST
		oldParse := parseAndValidateRegionAndZone
		defer func() {
			backupEnabled = oldBackupEnabled
			cvp.CVP_HOST = oldUseVCPRegion
			parseAndValidateRegionAndZone = oldParse
		}()
		backupEnabled = true
		cvp.CVP_HOST = ""
		stubValidRegionParse()

		start := time.Date(2025, 5, 14, 11, 0, 0, 0, time.UTC)
		end := time.Date(2025, 6, 13, 11, 0, 0, 0, time.UTC)

		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		mockOrchestrator.EXPECT().PersistAccountTrialMetadataIfSet(mock.Anything, "1234567890", mock.MatchedBy(func(trial *common.TrialModeParams) bool {
			return trial != nil && trial.Start.Equal(start) && trial.End.Equal(end)
		})).Return(nil)
		mockOrchestrator.EXPECT().GetBackupPolicyByNameAndOwnerID(ctx, "policy-1", "1234567890").
			Return(nil, utilerrors.NewNotFoundErr("backup policy", nil))
		mockOrchestrator.EXPECT().CreateBackupPolicy(ctx, mock.Anything).
			Return(nil, utilerrors.NewUserInputValidationErr("trialMode startTime must be before endTime"))

		req := &gcpgenserver.BackupPolicyCreateV1beta{
			ResourceId: "policy-1",
			TrialMode: gcpgenserver.NewOptTrialModeV1beta(gcpgenserver.TrialModeV1beta{
				StartTime: start,
				EndTime:   end,
			}),
		}
		res, err := Handler{Orchestrator: mockOrchestrator}.V1betaCreateBackupPolicy(ctx, req, params)
		assert.NoError(t, err)
		badReq, ok := res.(*gcpgenserver.V1betaCreateBackupPolicyBadRequest)
		require.True(t, ok)
		assert.Equal(t, float64(400), badReq.Code)
		assert.Equal(t, "trialMode startTime must be before endTime", badReq.Message)
	})
}

func TestV1betaCreateKmsConfiguration_TrialMode(t *testing.T) {
	ctx := context.Background()
	keyFullPath := "projects/test-project/locations/us-east4/keyRings/test-keyring/cryptoKeys/test-key"
	params := gcpgenserver.V1betaCreateKmsConfigurationParams{
		LocationId:     "us-east4",
		ProjectNumber:  "test-project",
		XCorrelationID: gcpgenserver.NewOptString("corr-id"),
	}

	origCVPHost := cvp.CVP_HOST
	origForce := utils.ForceVCPKMSPathForTesting
	origParse := parseAndValidateRegionAndZone
	defer func() {
		cvp.CVP_HOST = origCVPHost
		utils.ForceVCPKMSPathForTesting = origForce
		parseAndValidateRegionAndZone = origParse
	}()
	cvp.CVP_HOST = "localhost:8009"
	utils.ForceVCPKMSPathForTesting = true
	stubValidRegionParseEast4()

	t.Run("ReturnsBadRequestWhenTrialModeInvalid", func(t *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		mockOrchestrator.EXPECT().GetExistingKmsConfig(ctx, mock.Anything).
			Return(nil, utilerrors.NewNotFoundErr("KMS configuration", nil))
		mockOrchestrator.EXPECT().CreateKmsConfig(ctx, mock.Anything).
			Return(nil, "", utilerrors.NewUserInputValidationErr("trialMode startTime must be before endTime"))

		req := &gcpgenserver.KmsConfigV1beta{
			KeyFullPath: keyFullPath,
			ResourceId:  gcpgenserver.NewOptString("kms-res"),
			TrialMode:   invalidTrialModeOptEndBeforeStart(),
		}
		res, err := Handler{Orchestrator: mockOrchestrator}.V1betaCreateKmsConfiguration(ctx, req, params)
		assert.NoError(t, err)
		badReq, ok := res.(*gcpgenserver.V1betaCreateKmsConfigurationBadRequest)
		require.True(t, ok)
		assert.Equal(t, float64(400), badReq.Code)
		assert.Equal(t, "trialMode startTime must be before endTime", badReq.Message)
	})

	t.Run("PassesTrialModeToOrchestratorWhenValid", func(t *testing.T) {
		start := time.Date(2025, 5, 14, 11, 0, 0, 0, time.UTC)
		end := time.Date(2025, 6, 13, 11, 0, 0, 0, time.UTC)

		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		mockOrchestrator.EXPECT().GetExistingKmsConfig(ctx, mock.Anything).
			Return(nil, utilerrors.NewNotFoundErr("KMS configuration", nil))
		mockOrchestrator.EXPECT().CreateKmsConfig(ctx, mock.MatchedBy(func(p *common.CreateKmsConfigParams) bool {
			return p.TrialMode != nil && p.TrialMode.Start.Equal(start) && p.TrialMode.End.Equal(end)
		})).Return(&coremodels.KmsConfig{KmsAttributes: &coremodels.KmsAttributes{}}, "op-1", nil)

		req := &gcpgenserver.KmsConfigV1beta{
			KeyFullPath: keyFullPath,
			ResourceId:  gcpgenserver.NewOptString("kms-res"),
			TrialMode: gcpgenserver.NewOptTrialModeV1beta(gcpgenserver.TrialModeV1beta{
				StartTime: start,
				EndTime:   end,
			}),
		}
		res, err := Handler{Orchestrator: mockOrchestrator}.V1betaCreateKmsConfiguration(ctx, req, params)
		assert.NoError(t, err)
		_, ok := res.(*gcpgenserver.OperationV1beta)
		assert.True(t, ok)
	})

	t.Run("SDE_ReturnsBadRequestWhenTrialModeInvalid", func(t *testing.T) {
		utils.ForceVCPKMSPathForTesting = false
		oldCreateClient := createClient
		defer func() { createClient = oldCreateClient }()

		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		expectSDEJobMaybe(mockOrchestrator)
		mockOrchestrator.EXPECT().GetExistingKmsConfig(ctx, mock.Anything).
			Return(nil, utilerrors.NewNotFoundErr("KMS configuration", nil))
		mockOrchestrator.EXPECT().CreateKmsConfig(ctx, mock.MatchedBy(func(p *common.CreateKmsConfigParams) bool {
			return p.TrialMode != nil && p.TrialMode.Start != nil && p.TrialMode.End != nil &&
				p.TrialMode.End.Before(*p.TrialMode.Start)
		})).Return(nil, "", utilerrors.NewUserInputValidationErr("trialMode startTime must be before endTime"))

		mockClient := kms_configurations.NewMockClientService(t)
		mockClient.EXPECT().V1betaCreateKmsConfiguration(mock.Anything).Return(&kms_configurations.V1betaCreateKmsConfigurationAccepted{
			Payload: &models.OperationV1beta{
				Name:     "operation-id",
				Done:     nillable.GetBoolPtr(true),
				Response: models.KmsConfigV1beta{UUID: "kms-uuid"},
			},
		}, nil)
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return cvpapi.Cvp{KmsConfigurations: mockClient}
		}

		req := &gcpgenserver.KmsConfigV1beta{
			KeyFullPath: keyFullPath,
			ResourceId:  gcpgenserver.NewOptString("kms-res"),
			TrialMode:   invalidTrialModeOptEndBeforeStart(),
		}
		res, err := Handler{Orchestrator: mockOrchestrator}.V1betaCreateKmsConfiguration(ctx, req, params)
		assert.NoError(t, err)
		badReq, ok := res.(*gcpgenserver.V1betaCreateKmsConfigurationBadRequest)
		require.True(t, ok)
		assert.Equal(t, float64(400), badReq.Code)
		assert.Equal(t, "trialMode startTime must be before endTime", badReq.Message)
	})
}

func TestV1betaCreateHostGroup_TrialMode(t *testing.T) {
	params := gcpgenserver.V1betaCreateHostGroupParams{
		LocationId:    "us-east4",
		ProjectNumber: "project-number",
	}

	baseReq := func(trial gcpgenserver.OptTrialModeV1beta) *gcpgenserver.HostGroupV1beta {
		return &gcpgenserver.HostGroupV1beta{
			ResourceId: "hg-1",
			Hosts:      []string{"iqn.1998-01.com.vmware:host1"},
			OsType:     gcpgenserver.HostGroupV1betaOsTypeLINUX,
			TrialMode:  trial,
		}
	}

	t.Run("ReturnsBadRequestWhenTrialModeInvalid", func(t *testing.T) {
		oldParse := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = oldParse }()
		stubValidRegionParse()

		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		mockOrchestrator.EXPECT().CreateHostGroup(mock.Anything, mock.Anything).
			Return(nil, utilerrors.NewUserInputValidationErr("trialMode startTime must be before endTime"))

		res, err := Handler{Orchestrator: mockOrchestrator}.V1betaCreateHostGroup(context.Background(), baseReq(invalidTrialModeOptEndBeforeStart()), params)
		assert.NoError(t, err)
		badReq, ok := res.(*gcpgenserver.V1betaCreateHostGroupBadRequest)
		require.True(t, ok)
		assert.Equal(t, float64(400), badReq.Code)
		assert.Equal(t, "trialMode startTime must be before endTime", badReq.Message)
	})

	t.Run("PassesTrialModeToOrchestratorWhenValid", func(t *testing.T) {
		oldParse := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = oldParse }()
		stubValidRegionParseEast4()

		start := time.Date(2025, 5, 14, 11, 0, 0, 0, time.UTC)
		end := time.Date(2025, 6, 13, 11, 0, 0, 0, time.UTC)

		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		mockOrchestrator.EXPECT().CreateHostGroup(mock.Anything, mock.MatchedBy(func(p *common.CreateHostGroupParams) bool {
			return p.TrialMode != nil && p.TrialMode.Start.Equal(start) && p.TrialMode.End.Equal(end)
		})).Return(&coremodels.HostGroup{Name: "hg-1"}, nil)

		res, err := Handler{Orchestrator: mockOrchestrator}.V1betaCreateHostGroup(
			context.Background(),
			baseReq(gcpgenserver.NewOptTrialModeV1beta(gcpgenserver.TrialModeV1beta{StartTime: start, EndTime: end})),
			params,
		)
		assert.NoError(t, err)
		_, ok := res.(*gcpgenserver.V1betaCreateHostGroupOK)
		assert.True(t, ok)
	})
}

func TestV1betaCreateBackupVault_TrialMode(t *testing.T) {
	ctx := context.Background()
	origBackupEnabled := backupEnabled
	origUseVCPRegion := cvp.CVP_HOST
	origParse := parseAndValidateRegionAndZone
	defer func() {
		backupEnabled = origBackupEnabled
		cvp.CVP_HOST = origUseVCPRegion
		parseAndValidateRegionAndZone = origParse
	}()
	backupEnabled = true
	stubValidRegionParseEast4()

	params := gcpgenserver.V1betaCreateBackupVaultParams{
		LocationId:    "us-east4",
		ProjectNumber: "12345",
	}
	baseReq := func(trial gcpgenserver.OptTrialModeV1beta) *gcpgenserver.BackupVaultCreateV1beta {
		return &gcpgenserver.BackupVaultCreateV1beta{
			ResourceId: gcpgenserver.NewOptString("bv-1"),
			TrialMode:  trial,
		}
	}

	t.Run("VCP_ReturnsBadRequestWhenTrialModeInvalid", func(t *testing.T) {
		cvp.CVP_HOST = ""
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		mockOrchestrator.EXPECT().PersistAccountTrialMetadataIfSet(mock.Anything, "12345", mock.Anything).
			Return(utilerrors.NewUserInputValidationErr("trialMode startTime must be before endTime"))

		res, err := Handler{Orchestrator: mockOrchestrator}.V1betaCreateBackupVault(ctx, baseReq(invalidTrialModeOptEndBeforeStart()), params)
		assert.NoError(t, err)
		badReq, ok := res.(*gcpgenserver.V1betaCreateBackupVaultBadRequest)
		require.True(t, ok)
		assert.Equal(t, float64(400), badReq.Code)
		assert.Equal(t, "trialMode startTime must be before endTime", badReq.Message)
		mockOrchestrator.AssertNotCalled(t, "CreateBackupVault", mock.Anything, mock.Anything)
	})

	t.Run("VCP_PersistsTrialAtHandlerBeforeCreate", func(t *testing.T) {
		cvp.CVP_HOST = ""
		start := time.Date(2025, 5, 14, 11, 0, 0, 0, time.UTC)
		end := time.Date(2025, 6, 13, 11, 0, 0, 0, time.UTC)

		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		mockOrchestrator.EXPECT().PersistAccountTrialMetadataIfSet(mock.Anything, "12345", mock.MatchedBy(func(trial *common.TrialModeParams) bool {
			return trial != nil && trial.Start.Equal(start) && trial.End.Equal(end)
		})).Return(nil)
		mockOrchestrator.EXPECT().GetBackupVaultByNameAndOwnerID(ctx, "bv-1", "12345").
			Return(nil, utilerrors.NewNotFoundErr("backup vault", nil))
		mockOrchestrator.EXPECT().CreateBackupVault(ctx, mock.MatchedBy(func(p *common.CreateBackupVaultParams) bool {
			return p.TrialMode == nil
		})).Return(&coremodels.BackupVaultV1beta{Name: "bv-1"}, nil)

		res, err := Handler{Orchestrator: mockOrchestrator}.V1betaCreateBackupVault(ctx, baseReq(gcpgenserver.NewOptTrialModeV1beta(gcpgenserver.TrialModeV1beta{
			StartTime: start,
			EndTime:   end,
		})), params)
		assert.NoError(t, err)
		_, ok := res.(*gcpgenserver.OperationV1beta)
		assert.True(t, ok)
	})

	t.Run("SDE_ReturnsBadRequestWhenTrialModeInvalid", func(t *testing.T) {
		cvp.CVP_HOST = "http://cvp-host"
		oldCvpCreateClient := cvpCreateClient
		defer func() { cvpCreateClient = oldCvpCreateClient }()

		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		mockOrchestrator.EXPECT().PersistAccountTrialMetadataIfSet(mock.Anything, "12345", mock.Anything).
			Return(utilerrors.NewUserInputValidationErr("trialMode startTime must be before endTime"))

		res, err := Handler{Orchestrator: mockOrchestrator}.V1betaCreateBackupVault(ctx, baseReq(invalidTrialModeOptEndBeforeStart()), params)
		assert.NoError(t, err)
		badReq, ok := res.(*gcpgenserver.V1betaCreateBackupVaultBadRequest)
		require.True(t, ok)
		assert.Equal(t, "trialMode startTime must be before endTime", badReq.Message)
		mockOrchestrator.AssertNotCalled(t, "GetBackupVaultByNameAndOwnerID", mock.Anything, mock.Anything, mock.Anything)
		mockOrchestrator.AssertNotCalled(t, "CreateBackupVault", mock.Anything, mock.Anything)
	})

	t.Run("ReturnsInternalServerErrorWhenTrialPersistFails", func(t *testing.T) {
		cvp.CVP_HOST = ""
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		mockOrchestrator.EXPECT().PersistAccountTrialMetadataIfSet(mock.Anything, "12345", mock.Anything).
			Return(errors.New("trial metadata persist failed"))

		res, err := Handler{Orchestrator: mockOrchestrator}.V1betaCreateBackupVault(ctx, baseReq(validTrialModeOpt(t)), params)
		assert.NoError(t, err)
		serverErr, ok := res.(*gcpgenserver.V1betaCreateBackupVaultInternalServerError)
		require.True(t, ok)
		assert.Equal(t, float64(http.StatusInternalServerError), serverErr.Code)
		assert.Equal(t, "trial metadata persist failed", serverErr.Message)
		mockOrchestrator.AssertNotCalled(t, "GetBackupVaultByNameAndOwnerID", mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("VCP_ReturnsBadRequestWhenCreateBackupVaultTrialInvalid", func(t *testing.T) {
		cvp.CVP_HOST = ""
		start := time.Date(2025, 5, 14, 11, 0, 0, 0, time.UTC)
		end := time.Date(2025, 6, 13, 11, 0, 0, 0, time.UTC)

		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		mockOrchestrator.EXPECT().PersistAccountTrialMetadataIfSet(mock.Anything, "12345", mock.MatchedBy(func(trial *common.TrialModeParams) bool {
			return trial != nil && trial.Start.Equal(start) && trial.End.Equal(end)
		})).Return(nil)
		mockOrchestrator.EXPECT().GetBackupVaultByNameAndOwnerID(ctx, "bv-1", "12345").
			Return(nil, utilerrors.NewNotFoundErr("backup vault", nil))
		mockOrchestrator.EXPECT().CreateBackupVault(ctx, mock.Anything).
			Return(nil, utilerrors.NewUserInputValidationErr("trialMode startTime must be before endTime"))

		res, err := Handler{Orchestrator: mockOrchestrator}.V1betaCreateBackupVault(ctx, baseReq(gcpgenserver.NewOptTrialModeV1beta(gcpgenserver.TrialModeV1beta{
			StartTime: start,
			EndTime:   end,
		})), params)
		assert.NoError(t, err)
		badReq, ok := res.(*gcpgenserver.V1betaCreateBackupVaultBadRequest)
		require.True(t, ok)
		assert.Equal(t, float64(400), badReq.Code)
		assert.Equal(t, "trialMode startTime must be before endTime", badReq.Message)
	})
}

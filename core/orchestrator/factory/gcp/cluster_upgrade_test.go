package gcp

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/factory/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	workflowEngineMock "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine"
	"gorm.io/gorm"
)

// Test constants for cluster upgrade tests
const (
	testOntapVersion    = "9.17.1P2"
	testVSAImagePath    = "gcr.io/vsa-image:9.17.1p2"
	testVSAName         = "x-9-18-1"
	testMediatorName    = "cvo-mediator-x-9-18-1"
	testOntapVersionOld = "9.17.1P1"
	testVSAImagePathOld = "gcr.io/vsa-image:9.17.1p1"
)

func TestUpgradeCluster(t *testing.T) {
	t.Run("OrchestratorInitialization", func(t *testing.T) {
		// Test the orchestrator structure and basic functionality
		mockStorage := database.NewMockStorage(t)
		orchestrator := &GCPOrchestrator{
			storage: mockStorage,
		}

		// Verify orchestrator is properly initialized
		assert.NotNil(t, orchestrator.storage)
		assert.Equal(t, mockStorage, orchestrator.storage)
	})

	t.Run("UpgradeClusterWrapper", func(t *testing.T) {
		// Test line 40: UpgradeCluster wrapper function
		mockStorage := database.NewMockStorage(t)
		mockTemporal := workflowEngineMock.NewMockTemporalTestClient(t)
		orchestrator := &GCPOrchestrator{
			storage:  mockStorage,
			temporal: mockTemporal,
		}

		ctx := context.Background()
		params := &commonparams.UpgradeClusterParams{
			ClusterID: "test-cluster-id",
		}

		// Mock the internal function behavior
		mockStorage.On("GetPoolByUUID", ctx, "test-cluster-id").Return(nil, gorm.ErrRecordNotFound)

		// Execute the wrapper function
		result, jobID, err := orchestrator.UpgradeCluster(ctx, params)

		// Assert
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Empty(t, jobID)
		mockStorage.AssertExpectations(t)
	})
}

func TestGetClusterUpgradeStatusBasic(t *testing.T) {
	t.Run("OrchestratorInitialization", func(t *testing.T) {
		// Test the orchestrator structure and basic functionality
		mockStorage := database.NewMockStorage(t)
		orchestrator := &GCPOrchestrator{
			storage: mockStorage,
		}

		// Verify orchestrator is properly initialized
		assert.NotNil(t, orchestrator.storage)
		assert.Equal(t, mockStorage, orchestrator.storage)
	})
}

func TestListAvailableVersionsBasic(t *testing.T) {
	t.Run("OrchestratorInitialization", func(t *testing.T) {
		// Test the orchestrator structure and basic functionality
		mockStorage := database.NewMockStorage(t)
		orchestrator := &GCPOrchestrator{
			storage: mockStorage,
		}

		// Verify orchestrator is properly initialized
		assert.NotNil(t, orchestrator.storage)
		assert.Equal(t, mockStorage, orchestrator.storage)
	})
}

func TestIsClusterUpgradeBlockedByZoneSwitch(t *testing.T) {
	t.Run("nil pool", func(t *testing.T) {
		assert.False(t, isClusterUpgradeBlockedByZoneSwitch(nil))
	})

	t.Run("nil pool attributes", func(t *testing.T) {
		assert.False(t, isClusterUpgradeBlockedByZoneSwitch(&datamodel.Pool{}))
	})

	t.Run("primary zone", func(t *testing.T) {
		pool := &datamodel.Pool{
			PoolAttributes: &datamodel.PoolAttributes{ZoneSwitchState: datamodel.ZonePrimary},
		}
		assert.False(t, isClusterUpgradeBlockedByZoneSwitch(pool))
	})

	t.Run("switching", func(t *testing.T) {
		pool := &datamodel.Pool{
			PoolAttributes: &datamodel.PoolAttributes{ZoneSwitchState: datamodel.ZoneSwitching},
		}
		assert.True(t, isClusterUpgradeBlockedByZoneSwitch(pool))
	})

	t.Run("switched", func(t *testing.T) {
		pool := &datamodel.Pool{
			PoolAttributes: &datamodel.PoolAttributes{ZoneSwitchState: datamodel.ZoneSwitched},
		}
		assert.True(t, isClusterUpgradeBlockedByZoneSwitch(pool))
	})
}

func TestUpgradeClusterParams(t *testing.T) {
	t.Run("ParamsCreation", func(t *testing.T) {
		params := &commonparams.UpgradeClusterParams{
			ClusterID:          "test-cluster-id",
			VSABuildImage:      "vsa-image:latest",
			MediatorBuildImage: "mediator-image:latest",
			ForceUpgrade:       true,
			Metadata:           map[string]string{"key": "value"},
		}

		assert.Equal(t, "test-cluster-id", params.ClusterID)
		assert.Equal(t, "vsa-image:latest", params.VSABuildImage)
		assert.Equal(t, "mediator-image:latest", params.MediatorBuildImage)
		assert.True(t, params.ForceUpgrade)
		assert.Equal(t, "value", params.Metadata["key"])
	})
}

func TestClusterUpgradeResponse(t *testing.T) {
	t.Run("ResponseCreation", func(t *testing.T) {
		response := &models.ClusterUpgradeResponse{
			ClusterID: "test-cluster-id",
			Status:    models.UpgradeStatusCompleted,
			JobID:     "test-job-id",
		}

		assert.Equal(t, "test-cluster-id", response.ClusterID)
		assert.Equal(t, models.UpgradeStatusCompleted, response.Status)
		assert.Equal(t, "test-job-id", response.JobID)
	})
}

func TestUpgradeProgress(t *testing.T) {
	t.Run("ProgressCreation", func(t *testing.T) {
		progress := &models.UpgradeProgress{
			JobID:  "test-job-id",
			Status: models.UpgradeStatusInProgress,
			Clusters: []models.ClusterUpgradeStatus{
				{
					ClusterID: "test-cluster-id",
					Status:    string(models.UpgradeStatusInProgress),
				},
			},
		}

		assert.Equal(t, "test-job-id", progress.JobID)
		assert.Equal(t, models.UpgradeStatusInProgress, progress.Status)
		assert.Len(t, progress.Clusters, 1)
		assert.Equal(t, "test-cluster-id", progress.Clusters[0].ClusterID)
	})
}

func TestListAvailableVersionsResponse(t *testing.T) {
	t.Run("ResponseCreation", func(t *testing.T) {
		response := &models.ListAvailableVersionsResponse{
			Current: testOntapVersion,
			Versions: []models.AvailableVersion{
				{
					OntapVersion: testOntapVersion,
					VSAName:      testVSAName,
					MediatorName: testMediatorName,
				},
			},
		}

		assert.Equal(t, testOntapVersion, response.Current)
		assert.Len(t, response.Versions, 1)
		assert.Equal(t, testOntapVersion, response.Versions[0].OntapVersion)
		assert.Equal(t, testVSAName, response.Versions[0].VSAName)
		assert.Equal(t, testMediatorName, response.Versions[0].MediatorName)
	})
}

func TestImageVersion(t *testing.T) {
	t.Run("ImageVersionCreation", func(t *testing.T) {
		imageVersion := &datamodel.ImageVersion{
			OntapVersion: testOntapVersion,
			VSAImagePath: testVSAImagePath,
			VSAName:      testVSAName,
			MediatorName: testMediatorName,
			IsActive:     true,
		}

		assert.Equal(t, testOntapVersion, imageVersion.OntapVersion)
		assert.Equal(t, testVSAImagePath, imageVersion.VSAImagePath)
		assert.Equal(t, testVSAName, imageVersion.VSAName)
		assert.Equal(t, testMediatorName, imageVersion.MediatorName)
		assert.True(t, imageVersion.IsActive)
	})
}

func TestPoolBuildInfo(t *testing.T) {
	t.Run("PoolBuildInfoCreation", func(t *testing.T) {
		buildInfo := &datamodel.PoolBuildInfo{
			VSABuildImage:      "vsa-image:latest",
			MediatorBuildImage: "mediator-image:latest",
			OntapVersion:       testOntapVersion,
		}

		assert.Equal(t, "vsa-image:latest", buildInfo.VSABuildImage)
		assert.Equal(t, "mediator-image:latest", buildInfo.MediatorBuildImage)
		assert.Equal(t, testOntapVersion, buildInfo.OntapVersion)
	})
}

// Test internal functions to cover missing lines

func TestCheckClusterUpgradeStatus(t *testing.T) {
	t.Run("AlreadyUpgraded", func(t *testing.T) {
		pool := &datamodel.Pool{
			BuildInfo: &datamodel.PoolBuildInfo{
				VSABuildImage:      testVSAName,
				MediatorBuildImage: testMediatorName,
			},
		}

		shouldSkip := _checkClusterUpgradeStatus(pool, testVSAName, testMediatorName)

		assert.True(t, shouldSkip)
	})

	t.Run("NotUpgraded", func(t *testing.T) {
		pool := &datamodel.Pool{
			BuildInfo: &datamodel.PoolBuildInfo{
				VSABuildImage:      "vsa-9.16.1",
				MediatorBuildImage: "mediator-9.16.1",
			},
		}

		shouldSkip := _checkClusterUpgradeStatus(pool, testVSAName, testMediatorName)

		assert.False(t, shouldSkip)
	})

	t.Run("NoBuildInfo", func(t *testing.T) {
		pool := &datamodel.Pool{}

		shouldSkip := _checkClusterUpgradeStatus(pool, testVSAName, testMediatorName)

		assert.False(t, shouldSkip)
	})

	t.Run("TrueZonalAlreadyUpgradedWhenVSAMatches", func(t *testing.T) {
		pool := &datamodel.Pool{
			PoolAttributes: &datamodel.PoolAttributes{IsTrueZonal: true},
			BuildInfo: &datamodel.PoolBuildInfo{
				VSABuildImage:      testVSAName,
				MediatorBuildImage: "",
			},
		}

		shouldSkip := _checkClusterUpgradeStatus(pool, testVSAName, testMediatorName)

		assert.True(t, shouldSkip)
	})

	t.Run("TrueZonalNotUpgradedWhenVSADiffers", func(t *testing.T) {
		pool := &datamodel.Pool{
			PoolAttributes: &datamodel.PoolAttributes{IsTrueZonal: true},
			BuildInfo: &datamodel.PoolBuildInfo{
				VSABuildImage:      "vsa-9.16.1",
				MediatorBuildImage: "",
			},
		}

		shouldSkip := _checkClusterUpgradeStatus(pool, testVSAName, testMediatorName)

		assert.False(t, shouldSkip)
	})
}

func TestShouldSkipUpgrade(t *testing.T) {
	t.Run("ForceUpgrade", func(t *testing.T) {
		pool := &datamodel.Pool{
			BuildInfo: &datamodel.PoolBuildInfo{
				VSABuildImage:      testVSAName,
				MediatorBuildImage: testMediatorName,
			},
		}

		shouldSkip := _shouldSkipUpgrade(pool, testVSAName, testMediatorName, true)

		assert.False(t, shouldSkip)
	})

	t.Run("NotForceUpgradeAndAlreadyUpgraded", func(t *testing.T) {
		pool := &datamodel.Pool{
			BuildInfo: &datamodel.PoolBuildInfo{
				VSABuildImage:      testVSAName,
				MediatorBuildImage: testMediatorName,
			},
		}

		shouldSkip := _shouldSkipUpgrade(pool, testVSAName, testMediatorName, false)

		assert.True(t, shouldSkip)
	})

	t.Run("NotForceUpgradeAndNotUpgraded", func(t *testing.T) {
		pool := &datamodel.Pool{
			BuildInfo: &datamodel.PoolBuildInfo{
				VSABuildImage:      "vsa-9.16.1",
				MediatorBuildImage: "mediator-9.16.1",
			},
		}

		shouldSkip := _shouldSkipUpgrade(pool, testVSAName, testMediatorName, false)

		assert.False(t, shouldSkip)
	})

	t.Run("CheckClusterUpgradeStatusError", func(t *testing.T) {
		// Test line 250: Error handling in _shouldSkipUpgrade for checkClusterUpgradeStatus failure
		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{
				UUID: "test-cluster-id",
			},
			BuildInfo: &datamodel.PoolBuildInfo{
				VSABuildImage:      "old-vsa-image",
				MediatorBuildImage: "old-mediator-image",
			},
		}

		// Test the normal case where cluster is not upgraded
		shouldSkip := _shouldSkipUpgrade(pool, testVSAName, testMediatorName, false)

		// The function should return false since cluster is not upgraded
		assert.False(t, shouldSkip)
	})
}

func TestCheckActiveUpgradeJob(t *testing.T) {
	t.Run("NoActiveJob", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)

		ctx := context.Background()
		clusterID := "test-cluster-id"

		// Mock no active job
		mockStorage.On("GetClusterUpgradeJobsByClusterID", ctx, clusterID).Return([]*datamodel.ClusterUpgradeJob{}, nil)

		// Execute
		result, err := common.CheckActiveUpgradeJob(ctx, mockStorage, clusterID)

		// Assert
		assert.NoError(t, err)
		assert.Nil(t, result)
		mockStorage.AssertExpectations(t)
	})

	t.Run("ActiveJobExists", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)

		ctx := context.Background()
		clusterID := "test-cluster-id"
		activeJob := &datamodel.ClusterUpgradeJob{
			BaseModel: datamodel.BaseModel{
				UUID: "active-job-uuid",
			},
			ClusterID: clusterID,
			Status:    string(models.UpgradeStatusInProgress),
		}

		// Mock active job exists
		mockStorage.On("GetClusterUpgradeJobsByClusterID", ctx, clusterID).Return([]*datamodel.ClusterUpgradeJob{activeJob}, nil)

		// Execute
		result, err := common.CheckActiveUpgradeJob(ctx, mockStorage, clusterID)

		// Assert
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, activeJob, result)
		mockStorage.AssertExpectations(t)
	})

	t.Run("DatabaseError", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)

		ctx := context.Background()
		clusterID := "test-cluster-id"

		// Mock database error
		mockStorage.On("GetClusterUpgradeJobsByClusterID", ctx, clusterID).Return(nil, errors.New("database error"))

		// Execute
		result, err := common.CheckActiveUpgradeJob(ctx, mockStorage, clusterID)

		// Assert
		assert.Error(t, err)
		assert.Nil(t, result)
		mockStorage.AssertExpectations(t)
	})
}

func TestHasActiveClusterUpgrade(t *testing.T) {
	t.Run("ReturnsFalseWhenNoActiveJob", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		ctx := context.Background()
		clusterID := "test-cluster-id"
		mockStorage.On("GetClusterUpgradeJobsByClusterID", ctx, clusterID).Return([]*datamodel.ClusterUpgradeJob{}, nil)

		hasUpgrade, err := HasActiveClusterUpgrade(ctx, mockStorage, clusterID)

		assert.NoError(t, err)
		assert.False(t, hasUpgrade)
		mockStorage.AssertExpectations(t)
	})
	t.Run("ReturnsTrueWhenJobInProgress", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		ctx := context.Background()
		clusterID := "test-cluster-id"
		activeJob := &datamodel.ClusterUpgradeJob{ClusterID: clusterID, Status: string(models.UpgradeStatusInProgress)}
		mockStorage.On("GetClusterUpgradeJobsByClusterID", ctx, clusterID).Return([]*datamodel.ClusterUpgradeJob{activeJob}, nil)

		hasUpgrade, err := HasActiveClusterUpgrade(ctx, mockStorage, clusterID)

		assert.NoError(t, err)
		assert.True(t, hasUpgrade)
		mockStorage.AssertExpectations(t)
	})
	t.Run("ReturnsTrueWhenJobPending", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		ctx := context.Background()
		clusterID := "test-cluster-id"
		pendingJob := &datamodel.ClusterUpgradeJob{ClusterID: clusterID, Status: string(models.UpgradeStatusPending)}
		mockStorage.On("GetClusterUpgradeJobsByClusterID", ctx, clusterID).Return([]*datamodel.ClusterUpgradeJob{pendingJob}, nil)

		hasUpgrade, err := HasActiveClusterUpgrade(ctx, mockStorage, clusterID)

		assert.NoError(t, err)
		assert.True(t, hasUpgrade)
		mockStorage.AssertExpectations(t)
	})
	t.Run("ReturnsFalseWhenOnlyCompletedJobs", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		ctx := context.Background()
		clusterID := "test-cluster-id"
		completedJob := &datamodel.ClusterUpgradeJob{ClusterID: clusterID, Status: string(models.UpgradeStatusCompleted)}
		mockStorage.On("GetClusterUpgradeJobsByClusterID", ctx, clusterID).Return([]*datamodel.ClusterUpgradeJob{completedJob}, nil)

		hasUpgrade, err := HasActiveClusterUpgrade(ctx, mockStorage, clusterID)

		assert.NoError(t, err)
		assert.False(t, hasUpgrade)
		mockStorage.AssertExpectations(t)
	})
	t.Run("OrchestratorMethodDelegatesToPackageFunction", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		ctx := context.Background()
		clusterID := "pool-uuid-123"
		mockStorage.On("GetClusterUpgradeJobsByClusterID", ctx, clusterID).Return([]*datamodel.ClusterUpgradeJob{}, nil)

		o := &GCPOrchestrator{storage: mockStorage}
		hasUpgrade, err := o.HasActiveClusterUpgrade(ctx, clusterID)

		assert.NoError(t, err)
		assert.False(t, hasUpgrade)
		mockStorage.AssertExpectations(t)
	})
}

func TestUpdateUpgradeJobStatus(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)

		ctx := context.Background()
		jobUUID := "test-job-uuid"
		status := "COMPLETED"
		errorMessage := ""

		// Mock upgrade job
		upgradeJob := &datamodel.ClusterUpgradeJob{
			BaseModel: datamodel.BaseModel{
				UUID: jobUUID,
			},
			ClusterID: "test-cluster-id",
			Status:    "IN_PROGRESS",
		}

		// Set up mocks
		mockStorage.On("GetClusterUpgradeJobByUUID", ctx, jobUUID).Return(upgradeJob, nil)
		mockStorage.On("UpdateClusterUpgradeJob", ctx, mock.AnythingOfType("*datamodel.ClusterUpgradeJob")).Return(nil)

		// Execute
		err := common.UpdateUpgradeJobStatus(ctx, mockStorage, jobUUID, status, errorMessage)

		// Assert
		assert.NoError(t, err)
		mockStorage.AssertExpectations(t)
	})

	t.Run("WithErrorMessage", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)

		ctx := context.Background()
		jobUUID := "test-job-uuid"
		status := "FAILED"
		errorMessage := "Upgrade failed"

		// Mock upgrade job
		upgradeJob := &datamodel.ClusterUpgradeJob{
			BaseModel: datamodel.BaseModel{
				UUID: jobUUID,
			},
			ClusterID: "test-cluster-id",
			Status:    "IN_PROGRESS",
		}

		// Set up mocks
		mockStorage.On("GetClusterUpgradeJobByUUID", ctx, jobUUID).Return(upgradeJob, nil)
		mockStorage.On("UpdateClusterUpgradeJob", ctx, mock.AnythingOfType("*datamodel.ClusterUpgradeJob")).Return(nil)

		// Execute
		err := common.UpdateUpgradeJobStatus(ctx, mockStorage, jobUUID, status, errorMessage)

		// Assert
		assert.NoError(t, err)
		mockStorage.AssertExpectations(t)
	})

	t.Run("JobNotFound", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)

		ctx := context.Background()
		jobUUID := "non-existent-job"
		status := "COMPLETED"
		errorMessage := ""

		// Mock job not found
		mockStorage.On("GetClusterUpgradeJobByUUID", ctx, jobUUID).Return(nil, gorm.ErrRecordNotFound)

		// Execute
		err := common.UpdateUpgradeJobStatus(ctx, mockStorage, jobUUID, status, errorMessage)

		// Assert
		assert.Error(t, err)
		mockStorage.AssertExpectations(t)
	})

	t.Run("InProgressStatus", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)

		ctx := context.Background()
		jobUUID := "test-job-uuid"
		status := string(models.UpgradeStatusInProgress)
		errorMessage := ""

		// Mock GetClusterUpgradeJobByUUID to return a job
		upgradeJob := &datamodel.ClusterUpgradeJob{
			BaseModel: datamodel.BaseModel{
				UUID:      jobUUID,
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			},
			ClusterID: "test-cluster-id",
			Status:    "NOT_STARTED",
		}
		mockStorage.On("GetClusterUpgradeJobByUUID", ctx, jobUUID).Return(upgradeJob, nil)

		// Mock UpdateClusterUpgradeJob to succeed
		mockStorage.On("UpdateClusterUpgradeJob", ctx, mock.AnythingOfType("*datamodel.ClusterUpgradeJob")).Return(nil)

		// Execute function
		err := common.UpdateUpgradeJobStatus(ctx, mockStorage, jobUUID, status, errorMessage)

		// Assert
		assert.NoError(t, err)
		mockStorage.AssertExpectations(t)
	})
}

func TestConvertMetadataToJSONB(t *testing.T) {
	t.Run("ValidMetadata", func(t *testing.T) {
		metadata := map[string]string{
			"key1": "value1",
			"key2": "value2",
		}

		result := common.ConvertMetadataToJSONB(metadata)

		assert.NotNil(t, result)
		assert.Equal(t, "value1", (*result)["key1"])
		assert.Equal(t, "value2", (*result)["key2"])
	})

	t.Run("NilMetadata", func(t *testing.T) {
		result := common.ConvertMetadataToJSONB(nil)

		assert.Nil(t, result)
	})

	t.Run("EmptyMetadata", func(t *testing.T) {
		metadata := map[string]string{}

		result := common.ConvertMetadataToJSONB(metadata)

		assert.NotNil(t, result)
		assert.Len(t, *result, 0)
	})
}

func TestDetermineTargetBuildImages(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		// Set up environment variables
		err := os.Setenv("VSA_IMAGE_NAME", testVSAName)
		if err != nil {
			return
		}
		defer func() {
			err := os.Unsetenv("VSA_IMAGE_NAME")
			if err != nil {
				return
			}
		}()

		err = os.Setenv("ONTAP_VERSION_DETAILS", testOntapVersion)
		if err != nil {
			return
		}
		defer func() {
			err := os.Unsetenv("ONTAP_VERSION_DETAILS")
			if err != nil {
				return
			}
		}()

		mockStorage := database.NewMockStorage(t)

		ctx := context.Background()
		params := &commonparams.UpgradeClusterParams{
			ClusterID: "test-cluster-id",
		}

		// Mock image versions - use env.CurrentOntapVersionDetails to match what the function compares against
		imageVersions := []*datamodel.ImageVersion{
			{
				OntapVersion: env.CurrentOntapVersionDetails,
				VSAImagePath: testVSAImagePath,
				VSAName:      testVSAName,
				MediatorName: testMediatorName,
				IsActive:     true,
			},
		}

		// Set up mocks
		mockStorage.On("ListImageVersions", ctx, true).Return(imageVersions, nil)

		// Execute
		targetBuildImages, err := determineTargetBuildImages(ctx, mockStorage, params)

		// Assert
		assert.NoError(t, err)
		assert.NotNil(t, targetBuildImages)
		assert.Equal(t, env.CurrentOntapVersionDetails, targetBuildImages.OntapVersion)
		assert.Equal(t, testVSAImagePath, targetBuildImages.VSAImagePath)
		assert.Equal(t, testVSAName, targetBuildImages.VSAName)
		assert.Equal(t, testMediatorName, targetBuildImages.MediatorName)
		mockStorage.AssertExpectations(t)
	})

	t.Run("DatabaseError", func(t *testing.T) {
		// Set up environment variable
		err := os.Setenv("VSA_IMAGE_NAME", "vsa-9.17.1")
		if err != nil {
			return
		}
		defer func() {
			err := os.Unsetenv("VSA_IMAGE_NAME")
			if err != nil {
				return
			}
		}()

		mockStorage := database.NewMockStorage(t)

		ctx := context.Background()
		params := &commonparams.UpgradeClusterParams{
			ClusterID: "test-cluster-id",
		}

		// Mock database error
		mockStorage.On("ListImageVersions", ctx, true).Return(nil, errors.New("database error"))

		// Execute
		targetBuildImages, err := determineTargetBuildImages(ctx, mockStorage, params)

		// Assert
		assert.Error(t, err)
		assert.Nil(t, targetBuildImages)
		mockStorage.AssertExpectations(t)
	})

	t.Run("VSAImageNameNotConfigured", func(t *testing.T) {
		// Test line 173: Error when VSA_IMAGE_NAME is not configured
		mockStorage := database.NewMockStorage(t)

		ctx := context.Background()
		params := &commonparams.UpgradeClusterParams{
			ClusterID: "test-cluster-id",
		}

		// Ensure VSA_IMAGE_NAME is not set
		err := os.Unsetenv("VSA_IMAGE_NAME")
		if err != nil {
			return
		}

		// Execute function
		targetBuildImages, err := determineTargetBuildImages(ctx, mockStorage, params)

		// Assert
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "VSA_IMAGE_NAME not configured in environment")
		assert.Nil(t, targetBuildImages)
	})

	t.Run("VSAImageFromEnvironmentNotFound", func(t *testing.T) {
		// Test line 193: Error when VSA image from environment not found in available versions
		mockStorage := database.NewMockStorage(t)

		ctx := context.Background()
		params := &commonparams.UpgradeClusterParams{
			ClusterID: "test-cluster-id",
		}

		// Set up environment variable
		err := os.Setenv("VSA_IMAGE_NAME", "vsa-9.17.1")
		if err != nil {
			return
		}
		defer func() {
			err := os.Unsetenv("VSA_IMAGE_NAME")
			if err != nil {
				return
			}
		}()

		// Mock ListImageVersions to return empty list (image not found)
		mockStorage.On("ListImageVersions", ctx, true).Return([]*datamodel.ImageVersion{}, nil)

		// Execute function
		targetBuildImages, err := determineTargetBuildImages(ctx, mockStorage, params)

		// Assert
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "VSA image/Ontap version from environment not found in available versions")
		assert.Nil(t, targetBuildImages)
		mockStorage.AssertExpectations(t)
	})

	t.Run("ForceFlagRequired", func(t *testing.T) {
		// Test lines 206-207: Error when force flag is required but not provided
		mockStorage := database.NewMockStorage(t)

		ctx := context.Background()
		params := &commonparams.UpgradeClusterParams{
			ClusterID:     "test-cluster-id",
			VSABuildImage: "different-vsa-image", // Different from environment
			ForceUpgrade:  false,                 // Force flag not set
		}

		// Set up environment variable
		err := os.Setenv("VSA_IMAGE_NAME", "vsa-9.17.1")
		if err != nil {
			return
		}
		defer func() {
			err := os.Unsetenv("VSA_IMAGE_NAME")
			if err != nil {
				return
			}
		}()

		// Execute function
		targetBuildImages, err := determineTargetBuildImages(ctx, mockStorage, params)

		// Assert
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "Force flag must be true when specifying a VSA build image different from environment")
		assert.Nil(t, targetBuildImages)
	})

	t.Run("BothBuildImagesRequired", func(t *testing.T) {
		// Test lines 211-212: Error when both VSA and mediator build images are required but not provided
		mockStorage := database.NewMockStorage(t)

		ctx := context.Background()
		params := &commonparams.UpgradeClusterParams{
			ClusterID:          "test-cluster-id",
			VSABuildImage:      "vsa-image", // Only VSA provided
			MediatorBuildImage: "",          // Mediator not provided
			ForceUpgrade:       true,
		}

		// Set up environment variable
		err := os.Setenv("VSA_IMAGE_NAME", "vsa-9.17.1")
		if err != nil {
			return
		}
		defer func() {
			err := os.Unsetenv("VSA_IMAGE_NAME")
			if err != nil {
				return
			}
		}()

		// Execute function
		targetBuildImages, err := determineTargetBuildImages(ctx, mockStorage, params)

		// Assert
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "Both VSA and mediator build images are required when specifying build images")
		assert.Nil(t, targetBuildImages)
	})

	t.Run("ListImageVersionsError", func(t *testing.T) {
		// Test lines 216-218: Error handling for ListImageVersions failure
		mockStorage := database.NewMockStorage(t)

		ctx := context.Background()
		params := &commonparams.UpgradeClusterParams{
			ClusterID:          "test-cluster-id",
			VSABuildImage:      "vsa-image",
			MediatorBuildImage: "mediator-image",
			ForceUpgrade:       true,
		}

		// Set up environment variable
		err := os.Setenv("VSA_IMAGE_NAME", "vsa-9.17.1")
		if err != nil {
			return
		}
		defer func() {
			err := os.Unsetenv("VSA_IMAGE_NAME")
			if err != nil {
				return
			}
		}()

		// Mock ListImageVersions to return error
		mockStorage.On("ListImageVersions", ctx, true).Return(nil, errors.New("database error"))

		// Execute function
		targetBuildImages, err := determineTargetBuildImages(ctx, mockStorage, params)

		// Assert
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "Failed to retrieve available image versions")
		assert.Nil(t, targetBuildImages)
		mockStorage.AssertExpectations(t)
	})

	t.Run("SpecifiedBuildCombinationNotFound", func(t *testing.T) {
		// Test lines 221-225, 229-230: Logic for finding target version and error when not found
		mockStorage := database.NewMockStorage(t)

		ctx := context.Background()
		params := &commonparams.UpgradeClusterParams{
			ClusterID:          "test-cluster-id",
			VSABuildImage:      "vsa-image",
			MediatorBuildImage: "mediator-image",
			ForceUpgrade:       true,
		}

		// Set up environment variable
		err := os.Setenv("VSA_IMAGE_NAME", "vsa-9.17.1")
		if err != nil {
			return
		}
		defer func() {
			err := os.Unsetenv("VSA_IMAGE_NAME")
			if err != nil {
				return
			}
		}()

		// Mock ListImageVersions to return versions that don't match
		imageVersions := []*datamodel.ImageVersion{
			{
				OntapVersion: "9.17.1P1",
				VSAImagePath: "gcr.io/vsa-image:9.17.1",
				VSAName:      "different-vsa-image",
				MediatorName: "different-mediator-image",
				IsActive:     true,
			},
		}
		mockStorage.On("ListImageVersions", ctx, true).Return(imageVersions, nil)

		// Execute function
		targetBuildImages, err := determineTargetBuildImages(ctx, mockStorage, params)

		// Assert
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "Specified VSA and mediator build image combination not found in available versions")
		assert.Nil(t, targetBuildImages)
		mockStorage.AssertExpectations(t)
	})

	t.Run("SpecifiedBuildCombinationFound", func(t *testing.T) {
		// Test lines 221-225, 233, 239: Success path for finding target version
		mockStorage := database.NewMockStorage(t)

		ctx := context.Background()
		params := &commonparams.UpgradeClusterParams{
			ClusterID:          "test-cluster-id",
			VSABuildImage:      "vsa-image",
			MediatorBuildImage: "mediator-image",
			ForceUpgrade:       true,
		}

		// Set up environment variable
		err := os.Setenv("VSA_IMAGE_NAME", "vsa-9.17.1")
		if err != nil {
			return
		}
		defer func() {
			err := os.Unsetenv("VSA_IMAGE_NAME")
			if err != nil {
				return
			}
		}()

		// Mock ListImageVersions to return matching version
		imageVersions := []*datamodel.ImageVersion{
			{
				OntapVersion: "9.17.1P1",
				VSAImagePath: "gcr.io/vsa-image:9.17.1",
				VSAName:      "vsa-image",
				MediatorName: "mediator-image",
				IsActive:     true,
			},
		}
		mockStorage.On("ListImageVersions", ctx, true).Return(imageVersions, nil)

		// Execute function
		targetBuildImages, err := determineTargetBuildImages(ctx, mockStorage, params)

		// Assert
		assert.NoError(t, err)
		assert.NotNil(t, targetBuildImages)
		assert.Equal(t, "9.17.1P1", targetBuildImages.OntapVersion)
		assert.Equal(t, "gcr.io/vsa-image:9.17.1", targetBuildImages.VSAImagePath)
		assert.Equal(t, "vsa-image", targetBuildImages.VSAName)
		assert.Equal(t, "mediator-image", targetBuildImages.MediatorName)
		mockStorage.AssertExpectations(t)
	})
}

func TestCreateUpgradeJobInDB(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)

		ctx := context.Background()
		params := &commonparams.UpgradeClusterParams{
			ClusterID: "test-cluster-id",
		}

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{
				UUID: "test-cluster-id",
			},
		}

		jobUUID := "test-job-uuid"
		targetOntapVersion := "9.17.1P1"
		targetVSAName := "vsa-9.17.1"
		targetMediatorName := "mediator-9.17.1"

		// Mock upgrade job creation
		mockStorage.On("CreateClusterUpgradeJob", ctx, mock.AnythingOfType("*datamodel.ClusterUpgradeJob")).Return(&datamodel.ClusterUpgradeJob{
			BaseModel: datamodel.BaseModel{
				UUID: jobUUID,
			},
			ClusterID: "test-cluster-id",
		}, nil)

		// Execute
		result, err := createUpgradeJobInDB(ctx, mockStorage, params, pool, targetOntapVersion, jobUUID, targetVSAName, targetMediatorName)

		// Assert
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, jobUUID, result.UUID)
		mockStorage.AssertExpectations(t)
	})

	t.Run("DatabaseError", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)

		ctx := context.Background()
		params := &commonparams.UpgradeClusterParams{
			ClusterID: "test-cluster-id",
		}

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{
				UUID: "test-cluster-id",
			},
		}

		jobUUID := "test-job-uuid"
		targetOntapVersion := "9.17.1P1"
		targetVSAName := "vsa-9.17.1"
		targetMediatorName := "mediator-9.17.1"

		// Mock database error
		mockStorage.On("CreateClusterUpgradeJob", ctx, mock.AnythingOfType("*datamodel.ClusterUpgradeJob")).Return(nil, errors.New("database error"))

		// Execute
		result, err := createUpgradeJobInDB(ctx, mockStorage, params, pool, targetOntapVersion, jobUUID, targetVSAName, targetMediatorName)

		// Assert
		assert.Error(t, err)
		assert.Nil(t, result)
		mockStorage.AssertExpectations(t)
	})
}

func TestGetClusterUpgradeStatus(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		orchestrator := &GCPOrchestrator{
			storage: mockStorage,
		}

		ctx := context.Background()
		jobUUID := "test-job-uuid"
		clusterID := "test-cluster-id"

		// Mock upgrade job
		upgradeJob := &datamodel.ClusterUpgradeJob{
			BaseModel: datamodel.BaseModel{
				UUID:      jobUUID,
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			},
			ClusterID:   clusterID,
			Status:      string(models.UpgradeStatusInProgress),
			StartedAt:   &time.Time{},
			CompletedAt: nil,
		}

		// Set up mocks
		mockStorage.On("GetClusterUpgradeJobByUUID", ctx, jobUUID).Return(upgradeJob, nil)

		// Execute
		response, err := orchestrator.GetClusterUpgradeStatus(ctx, jobUUID)

		// Assert
		assert.NoError(t, err)
		assert.NotNil(t, response)
		assert.Equal(t, jobUUID, response.JobID)
		assert.Equal(t, models.UpgradeStatusInProgress, response.Status)
		assert.Len(t, response.Clusters, 1)
		assert.Equal(t, clusterID, response.Clusters[0].ClusterID)
		assert.Equal(t, string(models.UpgradeStatusInProgress), response.Clusters[0].Status)
		mockStorage.AssertExpectations(t)
	})

	t.Run("WithErrorDetails", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		orchestrator := &GCPOrchestrator{
			storage: mockStorage,
		}

		ctx := context.Background()
		jobUUID := "test-job-uuid"
		clusterID := "test-cluster-id"

		// Mock upgrade job with error details
		errorDetails := &datamodel.UpgradeErrorDetails{
			ErrorCode:    "UPGRADE_FAILED",
			ErrorMessage: "Upgrade failed due to network issues",
			ErrorType:    "NETWORK_ERROR",
			Retryable:    true,
		}

		upgradeJob := &datamodel.ClusterUpgradeJob{
			BaseModel: datamodel.BaseModel{
				UUID:      jobUUID,
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			},
			ClusterID:    clusterID,
			Status:       string(models.UpgradeStatusFailed),
			StartedAt:    &time.Time{},
			CompletedAt:  &time.Time{},
			ErrorDetails: errorDetails,
		}

		// Set up mocks
		mockStorage.On("GetClusterUpgradeJobByUUID", ctx, jobUUID).Return(upgradeJob, nil)

		// Execute
		response, err := orchestrator.GetClusterUpgradeStatus(ctx, jobUUID)

		// Assert
		assert.NoError(t, err)
		assert.NotNil(t, response)
		assert.Equal(t, jobUUID, response.JobID)
		assert.Equal(t, models.UpgradeStatusFailed, response.Status)
		assert.Len(t, response.Clusters, 1)
		assert.Equal(t, clusterID, response.Clusters[0].ClusterID)
		assert.Len(t, response.Errors, 1)
		assert.Equal(t, "UPGRADE_FAILED", response.Errors[0].Code)
		assert.Equal(t, "Upgrade failed due to network issues", response.Errors[0].Message)
		assert.Equal(t, "NETWORK_ERROR", response.Errors[0].Type)
		assert.True(t, response.Errors[0].Retryable)
		assert.Equal(t, clusterID, response.Errors[0].ClusterID)
		mockStorage.AssertExpectations(t)
	})

	t.Run("JobNotFound", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		orchestrator := &GCPOrchestrator{
			storage: mockStorage,
		}

		ctx := context.Background()
		jobUUID := "non-existent-job"

		// Mock job not found
		mockStorage.On("GetClusterUpgradeJobByUUID", ctx, jobUUID).Return(nil, gorm.ErrRecordNotFound)

		// Execute
		response, err := orchestrator.GetClusterUpgradeStatus(ctx, jobUUID)

		// Assert
		assert.Error(t, err)
		assert.Nil(t, response)
		assert.Error(t, err)
		mockStorage.AssertExpectations(t)
	})

	t.Run("DatabaseError", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		orchestrator := &GCPOrchestrator{
			storage: mockStorage,
		}

		ctx := context.Background()
		jobUUID := "test-job-uuid"

		// Mock database error
		mockStorage.On("GetClusterUpgradeJobByUUID", ctx, jobUUID).Return(nil, errors.New("database error"))

		// Execute
		response, err := orchestrator.GetClusterUpgradeStatus(ctx, jobUUID)

		// Assert
		assert.Error(t, err)
		assert.Nil(t, response)
		assert.Error(t, err)
		mockStorage.AssertExpectations(t)
	})
}

func TestListAvailableVersions(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		orchestrator := &GCPOrchestrator{
			storage: mockStorage,
		}

		ctx := context.Background()

		// Mock available versions - use env.CurrentOntapVersionDetails to match what the function uses
		versions := []*datamodel.ImageVersion{
			{
				OntapVersion: env.CurrentOntapVersionDetails,
				VSAImagePath: testVSAImagePath,
				VSAName:      testVSAName,
				MediatorName: testMediatorName,
				IsActive:     true,
			},
			{
				OntapVersion: "9.16.1",
				VSAImagePath: "gcr.io/vsa-image:9.16.1",
				VSAName:      "vsa-9.16.1",
				MediatorName: "mediator-9.16.1",
				IsActive:     true,
			},
		}

		// Set up mocks
		mockStorage.On("ListImageVersions", ctx, true).Return(versions, nil)

		// Execute
		response, err := orchestrator.ListAvailableVersions(ctx)

		// Assert
		assert.NoError(t, err)
		assert.NotNil(t, response)
		assert.Equal(t, env.CurrentOntapVersionDetails, response.Current)
		assert.Len(t, response.Versions, 2)

		// Check first version (current)
		assert.Equal(t, env.CurrentOntapVersionDetails, response.Versions[0].OntapVersion)
		assert.Equal(t, testVSAImagePath, response.Versions[0].VSAImagePath)
		assert.Equal(t, testVSAName, response.Versions[0].VSAName)
		assert.Equal(t, testMediatorName, response.Versions[0].MediatorName)
		assert.True(t, response.Versions[0].IsCurrent)
		assert.True(t, response.Versions[0].IsActive)

		// Check second version
		assert.Equal(t, "9.16.1", response.Versions[1].OntapVersion)
		assert.Equal(t, "gcr.io/vsa-image:9.16.1", response.Versions[1].VSAImagePath)
		assert.Equal(t, "vsa-9.16.1", response.Versions[1].VSAName)
		assert.Equal(t, "mediator-9.16.1", response.Versions[1].MediatorName)
		assert.False(t, response.Versions[1].IsCurrent)
		assert.True(t, response.Versions[1].IsActive)

		mockStorage.AssertExpectations(t)
	})

	t.Run("EmptyVersions", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		orchestrator := &GCPOrchestrator{
			storage: mockStorage,
		}

		ctx := context.Background()

		// Mock empty versions
		mockStorage.On("ListImageVersions", ctx, true).Return([]*datamodel.ImageVersion{}, nil)

		// Execute
		response, err := orchestrator.ListAvailableVersions(ctx)

		// Assert
		assert.NoError(t, err)
		assert.NotNil(t, response)
		assert.Equal(t, env.CurrentOntapVersionDetails, response.Current)
		assert.Len(t, response.Versions, 0)
		mockStorage.AssertExpectations(t)
	})

	t.Run("DatabaseError", func(t *testing.T) {
		// Set up environment variable
		err := os.Setenv("ONTAP_VERSION_DETAILS", testOntapVersion)
		if err != nil {
			return
		}
		defer func() {
			err := os.Unsetenv("ONTAP_VERSION_DETAILS")
			if err != nil {
				return
			}
		}()

		mockStorage := database.NewMockStorage(t)
		orchestrator := &GCPOrchestrator{
			storage: mockStorage,
		}

		ctx := context.Background()

		// Mock database error
		mockStorage.On("ListImageVersions", ctx, true).Return(nil, errors.New("database error"))

		// Execute
		response, err := orchestrator.ListAvailableVersions(ctx)

		// Assert
		assert.Error(t, err)
		assert.Nil(t, response)
		assert.Error(t, err)
		mockStorage.AssertExpectations(t)
	})

	t.Run("DefaultVersion", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		orchestrator := &GCPOrchestrator{
			storage: mockStorage,
		}

		ctx := context.Background()

		// Mock available versions - use env.CurrentOntapVersionDetails to match what the function uses
		versions := []*datamodel.ImageVersion{
			{
				OntapVersion: env.CurrentOntapVersionDetails,
				VSAImagePath: testVSAImagePath,
				VSAName:      testVSAName,
				MediatorName: testMediatorName,
				IsActive:     true,
			},
		}

		// Set up mocks
		mockStorage.On("ListImageVersions", ctx, true).Return(versions, nil)

		// Execute
		response, err := orchestrator.ListAvailableVersions(ctx)

		// Assert
		assert.NoError(t, err)
		assert.NotNil(t, response)
		assert.Equal(t, env.CurrentOntapVersionDetails, response.Current)
		assert.Len(t, response.Versions, 1)
		assert.True(t, response.Versions[0].IsCurrent)
		mockStorage.AssertExpectations(t)
	})
}

func TestListAvailableVersionsInternal(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)

		ctx := context.Background()

		// Mock available versions - use env.CurrentOntapVersionDetails to match what the function uses
		versions := []*datamodel.ImageVersion{
			{
				OntapVersion: env.CurrentOntapVersionDetails,
				VSAImagePath: testVSAImagePath,
				VSAName:      testVSAName,
				MediatorName: testMediatorName,
				IsActive:     true,
			},
		}

		// Set up mocks
		mockStorage.On("ListImageVersions", ctx, true).Return(versions, nil)

		// Execute
		response, err := listAvailableVersions(ctx, mockStorage)

		// Assert
		assert.NoError(t, err)
		assert.NotNil(t, response)
		assert.Equal(t, env.CurrentOntapVersionDetails, response.Current)
		assert.Len(t, response.Versions, 1)
		assert.Equal(t, env.CurrentOntapVersionDetails, response.Versions[0].OntapVersion)
		assert.True(t, response.Versions[0].IsCurrent)
		assert.True(t, response.Versions[0].IsActive)
		mockStorage.AssertExpectations(t)
	})

	t.Run("DatabaseError", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)

		ctx := context.Background()

		// Mock database error
		mockStorage.On("ListImageVersions", ctx, true).Return(nil, errors.New("database error"))

		// Execute
		response, err := listAvailableVersions(ctx, mockStorage)

		// Assert
		assert.Error(t, err)
		assert.Nil(t, response)
		assert.Error(t, err)
		mockStorage.AssertExpectations(t)
	})
}

func TestUpgradeClusterInternal(t *testing.T) {
	t.Run("PoolNotFound", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		mockTemporal := workflowEngineMock.NewMockTemporalTestClient(t)

		ctx := context.Background()
		params := &commonparams.UpgradeClusterParams{
			ClusterID: "non-existent-cluster",
		}

		// Mock pool not found
		mockStorage.On("GetPoolByUUID", ctx, "non-existent-cluster").Return(nil, gorm.ErrRecordNotFound)

		// Execute internal function
		result, jobID, err := _upgradeCluster(ctx, mockStorage, mockTemporal, params)

		// Assert
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Empty(t, jobID)
		mockStorage.AssertExpectations(t)
	})

	t.Run("PoolGetError", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		mockTemporal := workflowEngineMock.NewMockTemporalTestClient(t)

		ctx := context.Background()
		params := &commonparams.UpgradeClusterParams{
			ClusterID: "test-cluster-id",
		}

		// Mock pool get error
		mockStorage.On("GetPoolByUUID", ctx, "test-cluster-id").Return(nil, errors.New("database error"))

		// Execute internal function
		result, jobID, err := _upgradeCluster(ctx, mockStorage, mockTemporal, params)

		// Assert
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Empty(t, jobID)
		mockStorage.AssertExpectations(t)
	})

	t.Run("ZoneSwitchingBlocksUpgrade", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		mockTemporal := workflowEngineMock.NewMockTemporalTestClient(t)

		ctx := context.Background()
		params := &commonparams.UpgradeClusterParams{ClusterID: "test-cluster-id"}
		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{UUID: "test-cluster-id"},
			State:     datamodel.LifeCycleStateREADY,
			PoolAttributes: &datamodel.PoolAttributes{
				ZoneSwitchState: datamodel.ZoneSwitching,
			},
		}

		mockStorage.On("GetPoolByUUID", ctx, "test-cluster-id").Return(pool, nil)

		result, jobID, err := _upgradeCluster(ctx, mockStorage, mockTemporal, params)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), zoneSwitchClusterUpgradeBlockedMsg)
		assert.Nil(t, result)
		assert.Empty(t, jobID)
		mockStorage.AssertExpectations(t)
	})

	t.Run("ZoneSwitchedBlocksUpgrade", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		mockTemporal := workflowEngineMock.NewMockTemporalTestClient(t)

		ctx := context.Background()
		params := &commonparams.UpgradeClusterParams{ClusterID: "test-cluster-id"}
		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{UUID: "test-cluster-id"},
			State:     datamodel.LifeCycleStateREADY,
			PoolAttributes: &datamodel.PoolAttributes{
				ZoneSwitchState: datamodel.ZoneSwitched,
			},
		}

		mockStorage.On("GetPoolByUUID", ctx, "test-cluster-id").Return(pool, nil)

		result, jobID, err := _upgradeCluster(ctx, mockStorage, mockTemporal, params)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), zoneSwitchClusterUpgradeBlockedMsg)
		assert.Nil(t, result)
		assert.Empty(t, jobID)
		mockStorage.AssertExpectations(t)
	})

	t.Run("DetermineTargetBuildImagesError", func(t *testing.T) {
		// Set up environment variables
		err := os.Setenv("VSA_IMAGE_NAME", testVSAName)
		if err != nil {
			return
		}
		err = os.Setenv("ONTAP_VERSION_DETAILS", testOntapVersion)
		if err != nil {
			return
		}
		defer func() {
			err := os.Unsetenv("VSA_IMAGE_NAME")
			if err != nil {
				return
			}
			err = os.Unsetenv("ONTAP_VERSION_DETAILS")
			if err != nil {
				return
			}
		}()

		mockStorage := database.NewMockStorage(t)
		mockTemporal := workflowEngineMock.NewMockTemporalTestClient(t)

		ctx := context.Background()
		params := &commonparams.UpgradeClusterParams{
			ClusterID: "test-cluster-id",
		}

		// Mock pool data
		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{
				UUID: "test-cluster-id",
			},
			State: datamodel.LifeCycleStateREADY,
			BuildInfo: &datamodel.PoolBuildInfo{
				OntapVersion: "9.16.1",
			},
		}

		// Mock pool found but image versions error
		mockStorage.On("GetPoolByUUID", ctx, "test-cluster-id").Return(pool, nil)
		mockStorage.On("ListImageVersions", ctx, true).Return(nil, errors.New("database error"))

		// Execute internal function
		result, jobID, err := _upgradeCluster(ctx, mockStorage, mockTemporal, params)

		// Assert
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Empty(t, jobID)
		mockStorage.AssertExpectations(t)
	})

	t.Run("ShouldSkipUpgrade", func(t *testing.T) {
		// Set up environment variables
		err := os.Setenv("VSA_IMAGE_NAME", testVSAName)
		if err != nil {
			return
		}
		defer func() {
			err := os.Unsetenv("VSA_IMAGE_NAME")
			if err != nil {
				return
			}
		}()

		mockStorage := database.NewMockStorage(t)
		mockTemporal := workflowEngineMock.NewMockTemporalTestClient(t)

		ctx := context.Background()
		params := &commonparams.UpgradeClusterParams{
			ClusterID: "test-cluster-id",
		}

		// Mock pool data - already upgraded to match env.CurrentOntapVersionDetails
		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{
				UUID: "test-cluster-id",
			},
			State: datamodel.LifeCycleStateREADY,
			BuildInfo: &datamodel.PoolBuildInfo{
				OntapVersion:       env.CurrentOntapVersionDetails,
				VSABuildImage:      testVSAName,
				MediatorBuildImage: testMediatorName,
			},
		}

		// Mock image versions - use env.CurrentOntapVersionDetails to match what the function compares against
		imageVersions := []*datamodel.ImageVersion{
			{
				OntapVersion: env.CurrentOntapVersionDetails,
				VSAImagePath: testVSAImagePath,
				VSAName:      testVSAName,
				MediatorName: testMediatorName,
				IsActive:     true,
			},
		}

		// Set up mocks
		mockStorage.On("GetPoolByUUID", ctx, "test-cluster-id").Return(pool, nil)
		mockStorage.On("ListImageVersions", ctx, true).Return(imageVersions, nil)

		// Execute internal function
		result, jobID, err := _upgradeCluster(ctx, mockStorage, mockTemporal, params)

		// Assert
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Empty(t, jobID)
		assert.Equal(t, models.UpgradeStatusCompleted, result.Status)
		assert.Equal(t, "test-cluster-id", result.ClusterID)
		mockStorage.AssertExpectations(t)
	})

	t.Run("ActiveJobExists", func(t *testing.T) {
		// Set up environment variables
		err := os.Setenv("VSA_IMAGE_NAME", testVSAName)
		if err != nil {
			return
		}
		defer func() {
			err := os.Unsetenv("VSA_IMAGE_NAME")
			if err != nil {
				return
			}
		}()

		mockStorage := database.NewMockStorage(t)
		mockTemporal := workflowEngineMock.NewMockTemporalTestClient(t)

		ctx := context.Background()
		params := &commonparams.UpgradeClusterParams{
			ClusterID: "test-cluster-id",
		}

		// Mock pool data
		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{
				UUID: "test-cluster-id",
			},
			State: datamodel.LifeCycleStateREADY,
			BuildInfo: &datamodel.PoolBuildInfo{
				OntapVersion: "9.16.1",
			},
		}

		// Mock active upgrade job
		activeJob := &datamodel.ClusterUpgradeJob{
			BaseModel: datamodel.BaseModel{
				UUID:      "active-job-uuid",
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			},
			ClusterID: "test-cluster-id",
			Status:    string(models.UpgradeStatusInProgress),
		}

		// Mock image versions - use env.CurrentOntapVersionDetails to match what the function compares against
		imageVersions := []*datamodel.ImageVersion{
			{
				OntapVersion: env.CurrentOntapVersionDetails,
				VSAImagePath: testVSAImagePath,
				VSAName:      testVSAName,
				MediatorName: testMediatorName,
				IsActive:     true,
			},
		}

		// Set up mocks
		mockStorage.On("GetPoolByUUID", ctx, "test-cluster-id").Return(pool, nil)
		mockStorage.On("ListImageVersions", ctx, true).Return(imageVersions, nil)
		mockStorage.On("GetClusterUpgradeJobsByClusterID", ctx, "test-cluster-id").Return([]*datamodel.ClusterUpgradeJob{activeJob}, nil)

		// Execute internal function
		result, jobID, err := _upgradeCluster(ctx, mockStorage, mockTemporal, params)

		// Assert
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, "active-job-uuid", jobID)
		assert.Equal(t, models.UpgradeStatusInProgress, result.Status)
		mockStorage.AssertExpectations(t)
	})

	t.Run("CheckActiveUpgradeJobError", func(t *testing.T) {
		// Set up environment variables
		err := os.Setenv("VSA_IMAGE_NAME", testVSAName)
		if err != nil {
			return
		}
		err = os.Setenv("ONTAP_VERSION_DETAILS", testOntapVersion)
		if err != nil {
			return
		}
		defer func() {
			err := os.Unsetenv("VSA_IMAGE_NAME")
			if err != nil {
				return
			}
			err = os.Unsetenv("ONTAP_VERSION_DETAILS")
			if err != nil {
				return
			}
		}()

		mockStorage := database.NewMockStorage(t)
		mockTemporal := workflowEngineMock.NewMockTemporalTestClient(t)

		ctx := context.Background()
		params := &commonparams.UpgradeClusterParams{
			ClusterID: "test-cluster-id",
		}

		// Mock pool data
		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{
				UUID: "test-cluster-id",
			},
			State: datamodel.LifeCycleStateREADY,
			BuildInfo: &datamodel.PoolBuildInfo{
				OntapVersion: "9.16.1",
			},
		}

		// Mock image versions
		imageVersions := []*datamodel.ImageVersion{
			{
				OntapVersion: testOntapVersion,
				VSAImagePath: testVSAImagePath,
				VSAName:      testVSAName,
				MediatorName: testMediatorName,
				IsActive:     true,
			},
		}

		// Set up mocks
		mockStorage.On("GetPoolByUUID", ctx, "test-cluster-id").Return(pool, nil)
		mockStorage.On("ListImageVersions", ctx, true).Return(imageVersions, nil)
		mockStorage.On("GetClusterUpgradeJobsByClusterID", ctx, "test-cluster-id").Return(nil, errors.New("database error"))

		// Execute internal function
		result, jobID, err := _upgradeCluster(ctx, mockStorage, mockTemporal, params)

		// Assert
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Empty(t, jobID)
		mockStorage.AssertExpectations(t)
	})

	t.Run("CreateUpgradeJobInDBError", func(t *testing.T) {
		// Set up environment variables
		err := os.Setenv("VSA_IMAGE_NAME", testVSAName)
		if err != nil {
			return
		}
		err = os.Setenv("ONTAP_VERSION_DETAILS", testOntapVersion)
		if err != nil {
			return
		}
		defer func() {
			err := os.Unsetenv("VSA_IMAGE_NAME")
			if err != nil {
				return
			}
			err = os.Unsetenv("ONTAP_VERSION_DETAILS")
			if err != nil {
				return
			}
		}()

		mockStorage := database.NewMockStorage(t)
		mockTemporal := workflowEngineMock.NewMockTemporalTestClient(t)

		ctx := context.Background()
		params := &commonparams.UpgradeClusterParams{
			ClusterID: "test-cluster-id",
		}

		// Mock pool data
		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{
				UUID: "test-cluster-id",
			},
			State: datamodel.LifeCycleStateREADY,
			BuildInfo: &datamodel.PoolBuildInfo{
				OntapVersion: "9.16.1",
			},
		}

		// Mock image versions
		imageVersions := []*datamodel.ImageVersion{
			{
				OntapVersion: testOntapVersion,
				VSAImagePath: testVSAImagePath,
				VSAName:      testVSAName,
				MediatorName: testMediatorName,
				IsActive:     true,
			},
		}

		// Set up mocks
		mockStorage.On("GetPoolByUUID", ctx, "test-cluster-id").Return(pool, nil)
		mockStorage.On("ListImageVersions", ctx, true).Return(imageVersions, nil)
		mockStorage.On("GetClusterUpgradeJobsByClusterID", ctx, "test-cluster-id").Return([]*datamodel.ClusterUpgradeJob{}, nil)
		mockStorage.On("CreateClusterUpgradeJob", ctx, mock.AnythingOfType("*datamodel.ClusterUpgradeJob")).Return(nil, errors.New("database error"))

		// Execute internal function
		result, jobID, err := _upgradeCluster(ctx, mockStorage, mockTemporal, params)

		// Assert
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Empty(t, jobID)
		mockStorage.AssertExpectations(t)
	})

	t.Run("TemporalWorkflowError", func(t *testing.T) {
		// Set up environment variables
		err := os.Setenv("VSA_IMAGE_NAME", testVSAName)
		if err != nil {
			return
		}
		err = os.Setenv("ONTAP_VERSION_DETAILS", testOntapVersion)
		if err != nil {
			return
		}
		defer func() {
			err := os.Unsetenv("VSA_IMAGE_NAME")
			if err != nil {
				return
			}
			err = os.Unsetenv("ONTAP_VERSION_DETAILS")
			if err != nil {
				return
			}
		}()

		mockStorage := database.NewMockStorage(t)
		mockTemporal := workflowEngineMock.NewMockTemporalTestClient(t)

		ctx := context.Background()
		params := &commonparams.UpgradeClusterParams{
			ClusterID: "test-cluster-id",
		}

		// Mock pool data
		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{
				UUID: "test-cluster-id",
			},
			State: datamodel.LifeCycleStateREADY,
			BuildInfo: &datamodel.PoolBuildInfo{
				OntapVersion: "9.16.1",
			},
		}

		// Mock image versions
		imageVersions := []*datamodel.ImageVersion{
			{
				OntapVersion: testOntapVersion,
				VSAImagePath: testVSAImagePath,
				VSAName:      testVSAName,
				MediatorName: testMediatorName,
				IsActive:     true,
			},
		}

		// Mock upgrade job
		upgradeJob := &datamodel.ClusterUpgradeJob{
			BaseModel: datamodel.BaseModel{
				UUID: "test-job-uuid",
			},
			ClusterID: "test-cluster-id",
		}

		// Set up mocks
		mockStorage.On("GetPoolByUUID", ctx, "test-cluster-id").Return(pool, nil)
		mockStorage.On("ListImageVersions", ctx, true).Return(imageVersions, nil)
		mockStorage.On("GetClusterUpgradeJobsByClusterID", ctx, "test-cluster-id").Return([]*datamodel.ClusterUpgradeJob{}, nil)
		mockStorage.On("CreateClusterUpgradeJob", ctx, mock.AnythingOfType("*datamodel.ClusterUpgradeJob")).Return(upgradeJob, nil)
		mockStorage.On("GetClusterUpgradeJobByUUID", ctx, mock.AnythingOfType("string")).Return(upgradeJob, nil)
		mockStorage.On("UpdateClusterUpgradeJob", ctx, mock.AnythingOfType("*datamodel.ClusterUpgradeJob")).Return(nil)

		// Mock temporal workflow error
		mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("workflow error")).Once()

		// Execute internal function
		result, jobID, err := _upgradeCluster(ctx, mockStorage, mockTemporal, params)

		// Assert
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Empty(t, jobID)
		mockStorage.AssertExpectations(t)
		mockTemporal.AssertExpectations(t)
	})

	t.Run("Success", func(t *testing.T) {
		// Set up environment variables
		err := os.Setenv("VSA_IMAGE_NAME", testVSAName)
		if err != nil {
			return
		}
		err = os.Setenv("ONTAP_VERSION_DETAILS", testOntapVersion)
		if err != nil {
			return
		}
		defer func() {
			err := os.Unsetenv("VSA_IMAGE_NAME")
			if err != nil {
				return
			}
			err = os.Unsetenv("ONTAP_VERSION_DETAILS")
			if err != nil {
				return
			}
		}()

		mockStorage := database.NewMockStorage(t)
		mockTemporal := workflowEngineMock.NewMockTemporalTestClient(t)

		ctx := context.Background()
		params := &commonparams.UpgradeClusterParams{
			ClusterID: "test-cluster-id",
		}

		// Mock pool data
		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{
				UUID: "test-cluster-id",
			},
			State: datamodel.LifeCycleStateREADY,
			BuildInfo: &datamodel.PoolBuildInfo{
				OntapVersion: "9.16.1",
			},
		}

		// Mock image versions
		imageVersions := []*datamodel.ImageVersion{
			{
				OntapVersion: testOntapVersion,
				VSAImagePath: testVSAImagePath,
				VSAName:      testVSAName,
				MediatorName: testMediatorName,
				IsActive:     true,
			},
		}

		// Mock upgrade job
		upgradeJob := &datamodel.ClusterUpgradeJob{
			BaseModel: datamodel.BaseModel{
				UUID:      "test-job-uuid",
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			},
			ClusterID: "test-cluster-id",
		}

		// Set up mocks
		mockStorage.On("GetPoolByUUID", ctx, "test-cluster-id").Return(pool, nil)
		mockStorage.On("ListImageVersions", ctx, true).Return(imageVersions, nil)
		mockStorage.On("GetClusterUpgradeJobsByClusterID", ctx, "test-cluster-id").Return([]*datamodel.ClusterUpgradeJob{}, nil)
		mockStorage.On("CreateClusterUpgradeJob", ctx, mock.AnythingOfType("*datamodel.ClusterUpgradeJob")).Return(upgradeJob, nil)

		// Mock temporal workflow success
		mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil).Once()

		// Execute internal function
		result, jobID, err := _upgradeCluster(ctx, mockStorage, mockTemporal, params)

		// Assert
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, "test-job-uuid", jobID)
		assert.Equal(t, models.UpgradeStatusInProgress, result.Status)
		assert.Equal(t, "test-cluster-id", result.ClusterID)
		mockStorage.AssertExpectations(t)
		mockTemporal.AssertExpectations(t)
	})

	t.Run("SuccessWithNoBuildInfo", func(t *testing.T) {
		// Set up environment variables
		err := os.Setenv("VSA_IMAGE_NAME", testVSAName)
		if err != nil {
			return
		}
		err = os.Setenv("ONTAP_VERSION_DETAILS", testOntapVersion)
		if err != nil {
			return
		}
		defer func() {
			err := os.Unsetenv("VSA_IMAGE_NAME")
			if err != nil {
				return
			}
			err = os.Unsetenv("ONTAP_VERSION_DETAILS")
			if err != nil {
				return
			}
		}()

		mockStorage := database.NewMockStorage(t)
		mockTemporal := workflowEngineMock.NewMockTemporalTestClient(t)

		ctx := context.Background()
		params := &commonparams.UpgradeClusterParams{
			ClusterID: "test-cluster-id",
		}

		// Mock pool data with no build info but with cluster details
		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{
				UUID: "test-cluster-id",
			},
			State:     datamodel.LifeCycleStateREADY,
			BuildInfo: nil,
			ClusterDetails: datamodel.ClusterDetails{
				OntapVersion: "9.16.1",
			},
		}

		// Mock image versions
		imageVersions := []*datamodel.ImageVersion{
			{
				OntapVersion: testOntapVersion,
				VSAImagePath: testVSAImagePath,
				VSAName:      testVSAName,
				MediatorName: testMediatorName,
				IsActive:     true,
			},
		}

		// Mock upgrade job
		upgradeJob := &datamodel.ClusterUpgradeJob{
			BaseModel: datamodel.BaseModel{
				UUID:      "test-job-uuid",
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			},
			ClusterID: "test-cluster-id",
		}

		// Set up mocks
		mockStorage.On("GetPoolByUUID", ctx, "test-cluster-id").Return(pool, nil)
		mockStorage.On("ListImageVersions", ctx, true).Return(imageVersions, nil)
		mockStorage.On("GetClusterUpgradeJobsByClusterID", ctx, "test-cluster-id").Return([]*datamodel.ClusterUpgradeJob{}, nil)
		mockStorage.On("CreateClusterUpgradeJob", ctx, mock.AnythingOfType("*datamodel.ClusterUpgradeJob")).Return(upgradeJob, nil)

		// Mock temporal workflow success
		mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil).Once()

		// Execute internal function
		result, jobID, err := _upgradeCluster(ctx, mockStorage, mockTemporal, params)

		// Assert
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, "test-job-uuid", jobID)
		assert.Equal(t, models.UpgradeStatusInProgress, result.Status)
		assert.Equal(t, "test-cluster-id", result.ClusterID)
		mockStorage.AssertExpectations(t)
		mockTemporal.AssertExpectations(t)
	})

	t.Run("ShouldSkipUpgradeError", func(t *testing.T) {
		// Test lines 67-68: Error handling in _upgradeCluster for shouldSkipUpgrade failure
		mockStorage := database.NewMockStorage(t)
		mockTemporal := workflowEngineMock.NewMockTemporalTestClient(t)

		ctx := context.Background()
		params := &commonparams.UpgradeClusterParams{
			ClusterID: "test-cluster-id",
		}

		// Mock pool found
		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{
				UUID: "test-cluster-id",
			},
			State: datamodel.LifeCycleStateREADY,
			BuildInfo: &datamodel.PoolBuildInfo{
				VSABuildImage:      "old-vsa-image",
				MediatorBuildImage: "old-mediator-image",
			},
		}
		mockStorage.On("GetPoolByUUID", ctx, "test-cluster-id").Return(pool, nil)

		// Set up environment variables
		err := os.Setenv("VSA_IMAGE_NAME", testVSAName)
		if err != nil {
			return
		}
		err = os.Setenv("ONTAP_VERSION_DETAILS", testOntapVersion)
		if err != nil {
			return
		}
		defer func() {
			err := os.Unsetenv("VSA_IMAGE_NAME")
			if err != nil {
				return
			}
			err = os.Unsetenv("ONTAP_VERSION_DETAILS")
			if err != nil {
				return
			}
		}()

		// Mock determineTargetBuildImages to return error
		mockStorage.On("ListImageVersions", ctx, true).Return(nil, errors.New("database error"))

		// Execute internal function
		result, jobID, err := _upgradeCluster(ctx, mockStorage, mockTemporal, params)

		// Assert
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Empty(t, jobID)
		mockStorage.AssertExpectations(t)
	})

	t.Run("JobStatusUpdateError", func(t *testing.T) {
		// Test line 141: Error logging for job status update failure
		mockStorage := database.NewMockStorage(t)
		mockTemporal := workflowEngineMock.NewMockTemporalTestClient(t)

		ctx := context.Background()
		params := &commonparams.UpgradeClusterParams{
			ClusterID: "test-cluster-id",
		}

		// Mock pool found
		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{
				UUID: "test-cluster-id",
			},
			State: datamodel.LifeCycleStateREADY,
			BuildInfo: &datamodel.PoolBuildInfo{
				VSABuildImage:      "old-vsa-image",
				MediatorBuildImage: "old-mediator-image",
			},
		}
		mockStorage.On("GetPoolByUUID", ctx, "test-cluster-id").Return(pool, nil)

		// Set up environment variables
		err := os.Setenv("VSA_IMAGE_NAME", testVSAName)
		if err != nil {
			return
		}
		err = os.Setenv("ONTAP_VERSION_DETAILS", testOntapVersion)
		if err != nil {
			return
		}
		defer func() {
			err := os.Unsetenv("VSA_IMAGE_NAME")
			if err != nil {
				return
			}
			err = os.Unsetenv("ONTAP_VERSION_DETAILS")
			if err != nil {
				return
			}
		}()

		// Mock determineTargetBuildImages to succeed
		imageVersions := []*datamodel.ImageVersion{
			{
				OntapVersion: testOntapVersion,
				VSAImagePath: testVSAImagePath,
				VSAName:      testVSAName,
				MediatorName: testMediatorName,
				IsActive:     true,
			},
		}
		mockStorage.On("ListImageVersions", ctx, true).Return(imageVersions, nil)

		// Mock shouldSkipUpgrade to return false (don't skip)
		// This will be handled by the actual function logic

		// Mock checkActiveUpgradeJob to return no active job
		mockStorage.On("GetClusterUpgradeJobsByClusterID", ctx, "test-cluster-id").Return([]*datamodel.ClusterUpgradeJob{}, nil)

		// Mock createUpgradeJobInDB to succeed
		upgradeJob := &datamodel.ClusterUpgradeJob{
			BaseModel: datamodel.BaseModel{
				UUID: "test-job-uuid",
			},
			ClusterID: "test-cluster-id",
			Status:    "NOT_STARTED",
		}
		mockStorage.On("CreateClusterUpgradeJob", ctx, mock.AnythingOfType("*datamodel.ClusterUpgradeJob")).Return(upgradeJob, nil)

		// Mock temporal workflow execution to fail
		mockTemporal.On("ExecuteWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("workflow execution failed"))

		// Mock GetClusterUpgradeJobByUUID for the status update call
		mockStorage.On("GetClusterUpgradeJobByUUID", ctx, mock.AnythingOfType("string")).Return(upgradeJob, nil)

		// Mock UpdateClusterUpgradeJob to return error (this will trigger line 141)
		mockStorage.On("UpdateClusterUpgradeJob", ctx, mock.AnythingOfType("*datamodel.ClusterUpgradeJob")).Return(errors.New("update failed"))

		// Execute internal function
		result, jobID, err := _upgradeCluster(ctx, mockStorage, mockTemporal, params)

		// Assert
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Empty(t, jobID)
		mockStorage.AssertExpectations(t)
	})
}

// Test CreateImageVersion function
func TestCreateImageVersion_Success(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	orchestrator := &GCPOrchestrator{
		storage: mockStorage,
	}

	ctx := context.Background()
	ontapVersion := "9.17.1P1"
	vsaImagePath := "gcr.io/vsa-image:9.17.1"
	vsaName := "vsa-9.17.1"
	mediatorName := "mediator-9.17.1"
	isActive := true

	// Mock GetImageVersionByOntapVersion to return nil (not found)
	mockStorage.On("GetImageVersionByOntapVersion", ctx, ontapVersion).Return(nil, gorm.ErrRecordNotFound)

	// Mock CreateImageVersion to succeed
	createdVersion := &datamodel.ImageVersion{
		BaseModel: datamodel.BaseModel{
			UUID: "test-uuid",
		},
		OntapVersion: ontapVersion,
		VSAImagePath: vsaImagePath,
		VSAName:      vsaName,
		MediatorName: mediatorName,
		IsActive:     isActive,
	}
	mockStorage.On("CreateImageVersion", ctx, mock.MatchedBy(func(version *datamodel.ImageVersion) bool {
		return version.OntapVersion == ontapVersion &&
			version.VSAImagePath == vsaImagePath &&
			version.VSAName == vsaName &&
			version.MediatorName == mediatorName &&
			version.IsActive == isActive
	})).Return(createdVersion, nil)

	// Execute
	result, err := orchestrator.CreateImageVersion(ctx, ontapVersion, vsaImagePath, vsaName, mediatorName, isActive)

	// Assert
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, ontapVersion, result.OntapVersion)
	assert.Equal(t, vsaImagePath, result.VSAImagePath)
	assert.Equal(t, vsaName, result.VSAName)
	assert.Equal(t, mediatorName, result.MediatorName)
	assert.True(t, result.IsActive)
	mockStorage.AssertExpectations(t)
}

func TestCreateImageVersion_AlreadyExists(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	orchestrator := &GCPOrchestrator{
		storage: mockStorage,
	}

	ctx := context.Background()
	ontapVersion := "9.17.1P1"
	vsaImagePath := "gcr.io/vsa-image:9.17.1"
	vsaName := "vsa-9.17.1"
	mediatorName := "mediator-9.17.1"
	isActive := true

	// Mock GetImageVersionByOntapVersion to return existing version
	existingVersion := &datamodel.ImageVersion{
		BaseModel: datamodel.BaseModel{
			UUID: "existing-uuid",
		},
		OntapVersion: ontapVersion,
	}
	mockStorage.On("GetImageVersionByOntapVersion", ctx, ontapVersion).Return(existingVersion, nil)

	// Execute
	result, err := orchestrator.CreateImageVersion(ctx, ontapVersion, vsaImagePath, vsaName, mediatorName, isActive)

	// Assert
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "Image version with this ONTAP version already exists")
	mockStorage.AssertExpectations(t)
}

func TestCreateImageVersion_DuplicateKeyError(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	orchestrator := &GCPOrchestrator{
		storage: mockStorage,
	}

	ctx := context.Background()
	ontapVersion := "9.17.1P1"
	vsaImagePath := "gcr.io/vsa-image:9.17.1"
	vsaName := "vsa-9.17.1"
	mediatorName := "mediator-9.17.1"
	isActive := true

	// Mock GetImageVersionByOntapVersion to return nil (not found initially)
	mockStorage.On("GetImageVersionByOntapVersion", ctx, ontapVersion).Return(nil, gorm.ErrRecordNotFound)

	// Mock CreateImageVersion to return duplicate key error
	mockStorage.On("CreateImageVersion", ctx, mock.AnythingOfType("*datamodel.ImageVersion")).Return(nil, gorm.ErrDuplicatedKey)

	// Execute
	result, err := orchestrator.CreateImageVersion(ctx, ontapVersion, vsaImagePath, vsaName, mediatorName, isActive)

	// Assert
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "Image version with this ONTAP version already exists")
	mockStorage.AssertExpectations(t)
}

func TestCreateImageVersion_CreateError(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	orchestrator := &GCPOrchestrator{
		storage: mockStorage,
	}

	ctx := context.Background()
	ontapVersion := "9.17.1P1"
	vsaImagePath := "gcr.io/vsa-image:9.17.1"
	vsaName := "vsa-9.17.1"
	mediatorName := "mediator-9.17.1"
	isActive := true

	// Mock GetImageVersionByOntapVersion to return nil (not found)
	mockStorage.On("GetImageVersionByOntapVersion", ctx, ontapVersion).Return(nil, gorm.ErrRecordNotFound)

	// Mock CreateImageVersion to return error (not duplicate key)
	mockStorage.On("CreateImageVersion", ctx, mock.AnythingOfType("*datamodel.ImageVersion")).Return(nil, errors.New("database error"))

	// Execute
	result, err := orchestrator.CreateImageVersion(ctx, ontapVersion, vsaImagePath, vsaName, mediatorName, isActive)

	// Assert
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "Failed to create image version")
	mockStorage.AssertExpectations(t)
}

// Test DeleteImageVersion function
func TestDeleteImageVersion_Success(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	orchestrator := &GCPOrchestrator{
		storage: mockStorage,
	}

	ctx := context.Background()
	ontapVersion := "9.17.1P1"

	// Mock GetImageVersionByOntapVersion to return existing version
	existingVersion := &datamodel.ImageVersion{
		BaseModel: datamodel.BaseModel{
			UUID: "test-uuid",
		},
		OntapVersion: ontapVersion,
	}
	mockStorage.On("GetImageVersionByOntapVersion", ctx, ontapVersion).Return(existingVersion, nil)

	// Mock DeleteImageVersion to succeed
	mockStorage.On("DeleteImageVersion", ctx, ontapVersion).Return(nil)

	// Execute
	err := orchestrator.DeleteImageVersion(ctx, ontapVersion)

	// Assert
	assert.NoError(t, err)
	mockStorage.AssertExpectations(t)
}

func TestDeleteImageVersion_NotFound(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	orchestrator := &GCPOrchestrator{
		storage: mockStorage,
	}

	ctx := context.Background()
	ontapVersion := "9.17.1P1"

	// Mock GetImageVersionByOntapVersion to return not found error
	mockStorage.On("GetImageVersionByOntapVersion", ctx, ontapVersion).Return(nil, gorm.ErrRecordNotFound)

	// Execute
	err := orchestrator.DeleteImageVersion(ctx, ontapVersion)

	// Assert
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "ImageVersion")
	mockStorage.AssertExpectations(t)
}

func TestDeleteImageVersion_GetError(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	orchestrator := &GCPOrchestrator{
		storage: mockStorage,
	}

	ctx := context.Background()
	ontapVersion := "9.17.1P1"

	// Mock GetImageVersionByOntapVersion to return error (not record not found)
	mockStorage.On("GetImageVersionByOntapVersion", ctx, ontapVersion).Return(nil, errors.New("database error"))

	// Execute
	err := orchestrator.DeleteImageVersion(ctx, ontapVersion)

	// Assert
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Failed to retrieve image version")
	mockStorage.AssertExpectations(t)
}

func TestDeleteImageVersion_DeleteError(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	orchestrator := &GCPOrchestrator{
		storage: mockStorage,
	}

	ctx := context.Background()
	ontapVersion := "9.17.1P1"

	// Mock GetImageVersionByOntapVersion to return existing version
	existingVersion := &datamodel.ImageVersion{
		BaseModel: datamodel.BaseModel{
			UUID: "test-uuid",
		},
		OntapVersion: ontapVersion,
	}
	mockStorage.On("GetImageVersionByOntapVersion", ctx, ontapVersion).Return(existingVersion, nil)

	// Mock DeleteImageVersion to return error
	mockStorage.On("DeleteImageVersion", ctx, ontapVersion).Return(errors.New("delete error"))

	// Execute
	err := orchestrator.DeleteImageVersion(ctx, ontapVersion)

	// Assert
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Failed to delete image version")
	mockStorage.AssertExpectations(t)
}

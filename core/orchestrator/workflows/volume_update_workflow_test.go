package workflows

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/backgroundactivities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/flexcache_activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/scheduler"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

type VolumeUpdateTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite

	env *testsuite.TestWorkflowEnvironment
}

func (s *VolumeUpdateTestSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()
	s.env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
	mockHeader := &commonpb.Header{
		Fields: map[string]*commonpb.Payload{
			"logParam": encodedValue,
		},
	}
	s.env.SetHeader(mockHeader)

	// Register workflow
	s.env.RegisterWorkflow(UpdateVolumeWorkflow)

	// Register all activities that might be used across tests
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	backupActivity := activities.BackupActivity{SE: mockStorage}
	volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}

	// Register UpdateBackupMetadataIfExistsActivity - called by workflow at the end
	s.env.RegisterActivity(backupActivity.UpdateBackupMetadataIfExistsActivity)
	s.env.OnActivity(backupActivity.UpdateBackupMetadataIfExistsActivity, mock.Anything, mock.Anything).Return(nil).Maybe()

	// Register UpdateVolumeStateInDB - called by workflow in error handling
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	s.env.OnActivity(commonActivity.GetJob, mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
}

func (s *VolumeUpdateTestSuite) AfterTest() {
	s.env.AssertExpectations(s.T())
}

func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_Success() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateLun)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInDB)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 1000,
		Size:           1000,
		State:          "online",
	}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UpdateLun, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.LunResponse{ProviderResponse: vsa.ProviderResponse{Name: "/vol/vol1/lun1"}}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInDB, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Execute workflow
	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{
			Password:      "password",
			SecretID:      "",
			CertificateID: "",
		}},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		SizeInBytes: 1000,
		VolumeAttributes: &datamodel.VolumeAttributes{
			IsDataProtection: false,
		},
	}
	params := &common.UpdateVolumeParams{
		QuotaInBytes: 2000,
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	// Assert workflow completed successfully
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
	mockStorage.AssertNumberOfCalls(s.T(), "UpdateJob", 2)
}

func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_BackupVaultSwitching_DeleteSnapmirrorAndUpdateVaultID() {
	origFlag := utils.EnableBackupVaultSwitching
	defer utils.SetEnableBackupVaultSwitchingForTest(origFlag)
	utils.SetEnableBackupVaultSwitchingForTest(true)

	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}
	deleteActivity := activities.VolumeDeleteActivity{SE: mockStorage}
	backupActivity := activities.BackupActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetNode)
	s.env.RegisterActivity(commonActivity.GetOntapJob)
	s.env.RegisterActivity(updateActivity.GetVolumeFromONTAP)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateLun)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInDB)
	s.env.RegisterActivity(deleteActivity.DeleteSnapmirrorInONTAP)
	s.env.RegisterActivity(backupActivity.GetBackupVault)
	s.env.RegisterActivity(backupActivity.GetLatestBackupByVolumeAndVault)
	s.env.RegisterActivity(backupActivity.IsExpertModeVolume)
	s.env.RegisterActivity(backupActivity.RecalculateAndUpdateVolumeBackupChainBytesActivity)

	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(deleteActivity.DeleteSnapmirrorInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{JobUUID: "job-uuid"}, nil)
	s.env.OnActivity(backupActivity.GetBackupVault, mock.Anything, "old-vault-uuid").Return(&datamodel.BackupVault{BaseModel: datamodel.BaseModel{ID: 99}}, nil)
	s.env.OnActivity(backupActivity.GetLatestBackupByVolumeAndVault, mock.Anything, mock.Anything, int64(99)).Return((*datamodel.Backup)(nil), nil)
	s.env.OnActivity(backupActivity.IsExpertModeVolume, mock.Anything, mock.Anything).Return(false, nil)
	s.env.OnActivity(backupActivity.RecalculateAndUpdateVolumeBackupChainBytesActivity, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(commonActivity.GetOntapJob, mock.Anything, "job-uuid", mock.Anything).Return(&vsa.OntapJob{State: "success", UUID: "job-uuid"}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{ExternalUUID: "ext-uuid", Name: "vol"},
		AvailableSpace:   1000, Size: 1000, State: "online",
	}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UpdateLun, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.LunResponse{ProviderResponse: vsa.ProviderResponse{Name: "/vol/vol1/lun1"}}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInDB, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	emptyVaultID := ""
	volume := &datamodel.Volume{
		Pool:             &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{}},
		Account:          &datamodel.Account{Name: "test_account"},
		SizeInBytes:      1000,
		VolumeAttributes: &datamodel.VolumeAttributes{IsDataProtection: false},
		DataProtection:   &datamodel.DataProtection{BackupVaultID: "old-vault-uuid"},
	}
	params := &common.UpdateVolumeParams{
		QuotaInBytes:   2000,
		DataProtection: &models.UpdateDataProtection{BackupVaultID: &emptyVaultID},
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
}

func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_BackupVaultSwitching_DeletesLatestBackupSnapshot_NotExpertMode() {
	origFlag := utils.EnableBackupVaultSwitching
	defer utils.SetEnableBackupVaultSwitchingForTest(origFlag)
	utils.SetEnableBackupVaultSwitchingForTest(true)

	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}
	deleteActivity := activities.VolumeDeleteActivity{SE: mockStorage}
	backupActivity := activities.BackupActivity{SE: mockStorage}

	// Let real UpdateJobStatus run (do not OnActivity-mock it), so mockStorage.UpdateJob is exercised like other vault-switch tests.
	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	volumeUUID := "vol-switch-cleanup-1"
	snapshotExtID := "snapshot-ontap-uuid"
	volumeExtUUID := "volume-provider-ext-uuid"
	latestBackup := &datamodel.Backup{
		BaseModel: datamodel.BaseModel{UUID: "backup-latest-uuid"},
		Attributes: &datamodel.BackupAttributes{
			SnapshotID:          snapshotExtID,
			SnapshotName:        "vcp-backup-snap",
			UseExistingSnapshot: false,
		},
	}

	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetNode)
	s.env.RegisterActivity(commonActivity.GetOntapJob)
	s.env.RegisterActivity(updateActivity.GetVolumeFromONTAP)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateLun)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInDB)
	s.env.RegisterActivity(deleteActivity.DeleteSnapmirrorInONTAP)
	s.env.RegisterActivity(backupActivity.GetBackupVault)
	s.env.RegisterActivity(backupActivity.GetLatestBackupByVolumeAndVault)
	s.env.RegisterActivity(backupActivity.IsExpertModeVolume)
	s.env.RegisterActivity(backupActivity.DeleteSnapshotForBackup)
	s.env.RegisterActivity(backupActivity.DeleteBackupSnapshotFromDB)
	s.env.RegisterActivity(backupActivity.HydrateSnapshotDeletionToCCFEActivity)
	s.env.RegisterActivity(backupActivity.RecalculateAndUpdateVolumeBackupChainBytesActivity)

	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil).Times(1)
	s.env.OnActivity(deleteActivity.DeleteSnapmirrorInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{JobUUID: "job-uuid"}, nil).Times(1)
	s.env.OnActivity(backupActivity.GetBackupVault, mock.Anything, "old-vault-uuid").Return(&datamodel.BackupVault{BaseModel: datamodel.BaseModel{ID: 99}}, nil).Times(1)
	s.env.OnActivity(backupActivity.GetLatestBackupByVolumeAndVault, mock.Anything, volumeUUID, int64(99)).Return(latestBackup, nil).Times(1)
	s.env.OnActivity(backupActivity.IsExpertModeVolume, mock.Anything, volumeUUID).Return(false, nil).Times(2)
	s.env.OnActivity(backupActivity.DeleteSnapshotForBackup, mock.Anything, mock.Anything, snapshotExtID, volumeExtUUID, false).Return(nil).Times(1)
	s.env.OnActivity(backupActivity.DeleteBackupSnapshotFromDB, mock.Anything, latestBackup).Return(nil).Times(1)
	s.env.OnActivity(backupActivity.HydrateSnapshotDeletionToCCFEActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Times(1)
	s.env.OnActivity(backupActivity.RecalculateAndUpdateVolumeBackupChainBytesActivity, mock.Anything, volumeUUID).Return(nil).Times(1)
	s.env.OnActivity(commonActivity.GetOntapJob, mock.Anything, "job-uuid", mock.Anything).Return(&vsa.OntapJob{State: "success", UUID: "job-uuid"}, nil).Times(1)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{ExternalUUID: "ext-uuid", Name: "vol"},
		AvailableSpace:   1000, Size: 1000, State: "online",
	}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UpdateLun, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.LunResponse{ProviderResponse: vsa.ProviderResponse{Name: "/vol/vol1/lun1"}}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInDB, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	emptyVaultID := ""
	volume := &datamodel.Volume{
		BaseModel:        datamodel.BaseModel{UUID: volumeUUID},
		Pool:             &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{}},
		Account:          &datamodel.Account{Name: "test_account"},
		SizeInBytes:      1000,
		VolumeAttributes: &datamodel.VolumeAttributes{IsDataProtection: false, ExternalUUID: volumeExtUUID},
		DataProtection:   &datamodel.DataProtection{BackupVaultID: "old-vault-uuid"},
	}
	params := &common.UpdateVolumeParams{
		QuotaInBytes:   2000,
		DataProtection: &models.UpdateDataProtection{BackupVaultID: &emptyVaultID},
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
}

func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_BackupVaultSwitching_DeletesLatestBackupSnapshot_ExpertModeSkipsDBDeletion() {
	origFlag := utils.EnableBackupVaultSwitching
	defer utils.SetEnableBackupVaultSwitchingForTest(origFlag)
	utils.SetEnableBackupVaultSwitchingForTest(true)

	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}
	deleteActivity := activities.VolumeDeleteActivity{SE: mockStorage}
	backupActivity := activities.BackupActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	volumeUUID := "vol-switch-cleanup-expert"
	snapshotExtID := "snapshot-ontap-uuid-expert"
	volumeExtUUID := "volume-provider-ext-uuid-expert"
	latestBackup := &datamodel.Backup{
		BaseModel: datamodel.BaseModel{UUID: "backup-latest-uuid-expert"},
		Attributes: &datamodel.BackupAttributes{
			SnapshotID:          snapshotExtID,
			SnapshotName:        "vcp-backup-snap-expert",
			UseExistingSnapshot: false,
		},
	}

	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetNode)
	s.env.RegisterActivity(commonActivity.GetOntapJob)
	s.env.RegisterActivity(updateActivity.GetVolumeFromONTAP)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateLun)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInDB)
	s.env.RegisterActivity(deleteActivity.DeleteSnapmirrorInONTAP)
	s.env.RegisterActivity(backupActivity.GetBackupVault)
	s.env.RegisterActivity(backupActivity.GetLatestBackupByVolumeAndVault)
	s.env.RegisterActivity(backupActivity.IsExpertModeVolume)
	s.env.RegisterActivity(backupActivity.DeleteSnapshotForBackup)
	s.env.RegisterActivity(backupActivity.RecalculateAndUpdateVolumeBackupChainBytesActivity)
	// Deliberately omit DeleteBackupSnapshotFromDB — expert mode must not invoke it.

	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil).Times(1)
	s.env.OnActivity(deleteActivity.DeleteSnapmirrorInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{JobUUID: "job-uuid"}, nil).Times(1)
	s.env.OnActivity(backupActivity.GetBackupVault, mock.Anything, "old-vault-uuid").Return(&datamodel.BackupVault{BaseModel: datamodel.BaseModel{ID: 99}}, nil).Times(1)
	s.env.OnActivity(backupActivity.GetLatestBackupByVolumeAndVault, mock.Anything, volumeUUID, int64(99)).Return(latestBackup, nil).Times(1)
	s.env.OnActivity(backupActivity.IsExpertModeVolume, mock.Anything, volumeUUID).Return(true, nil).Times(2)
	s.env.OnActivity(backupActivity.DeleteSnapshotForBackup, mock.Anything, mock.Anything, snapshotExtID, volumeExtUUID, false).Return(nil).Times(1)
	s.env.OnActivity(backupActivity.RecalculateAndUpdateVolumeBackupChainBytesActivity, mock.Anything, volumeUUID).Return(nil).Times(1)
	s.env.OnActivity(commonActivity.GetOntapJob, mock.Anything, "job-uuid", mock.Anything).Return(&vsa.OntapJob{State: "success", UUID: "job-uuid"}, nil).Times(1)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{ExternalUUID: "ext-uuid", Name: "vol"},
		AvailableSpace:   1000, Size: 1000, State: "online",
	}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UpdateLun, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.LunResponse{ProviderResponse: vsa.ProviderResponse{Name: "/vol/vol1/lun1"}}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInDB, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	emptyVaultID := ""
	volume := &datamodel.Volume{
		BaseModel:        datamodel.BaseModel{UUID: volumeUUID},
		Pool:             &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{}},
		Account:          &datamodel.Account{Name: "test_account"},
		SizeInBytes:      1000,
		VolumeAttributes: &datamodel.VolumeAttributes{IsDataProtection: false, ExternalUUID: volumeExtUUID},
		DataProtection:   &datamodel.DataProtection{BackupVaultID: "old-vault-uuid"},
	}
	params := &common.UpdateVolumeParams{
		QuotaInBytes:   2000,
		DataProtection: &models.UpdateDataProtection{BackupVaultID: &emptyVaultID},
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
}

func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_BackupVaultSwitching_PreservesBackupChainBytesBeforeDBUpdate() {
	origFlag := utils.EnableBackupVaultSwitching
	defer utils.SetEnableBackupVaultSwitchingForTest(origFlag)
	utils.SetEnableBackupVaultSwitchingForTest(true)

	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}
	deleteActivity := activities.VolumeDeleteActivity{SE: mockStorage}
	backupActivity := activities.BackupActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetNode)
	s.env.RegisterActivity(commonActivity.GetOntapJob)
	s.env.RegisterActivity(updateActivity.GetVolumeFromONTAP)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateLun)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInDB)
	s.env.RegisterActivity(deleteActivity.DeleteSnapmirrorInONTAP)
	s.env.RegisterActivity(backupActivity.GetBackupVault)
	s.env.RegisterActivity(backupActivity.GetLatestBackupByVolumeAndVault)
	s.env.RegisterActivity(backupActivity.IsExpertModeVolume)
	s.env.RegisterActivity(backupActivity.RecalculateAndUpdateVolumeBackupChainBytesActivity)

	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(deleteActivity.DeleteSnapmirrorInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{JobUUID: "job-uuid"}, nil)
	s.env.OnActivity(backupActivity.GetBackupVault, mock.Anything, "old-vault-uuid").Return(&datamodel.BackupVault{BaseModel: datamodel.BaseModel{ID: 99}}, nil)
	s.env.OnActivity(backupActivity.GetLatestBackupByVolumeAndVault, mock.Anything, mock.Anything, int64(99)).Return((*datamodel.Backup)(nil), nil)
	s.env.OnActivity(backupActivity.IsExpertModeVolume, mock.Anything, mock.Anything).Return(false, nil)
	s.env.OnActivity(backupActivity.RecalculateAndUpdateVolumeBackupChainBytesActivity, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(commonActivity.GetOntapJob, mock.Anything, "job-uuid", mock.Anything).Return(&vsa.OntapJob{State: "success", UUID: "job-uuid"}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{ExternalUUID: "ext-uuid", Name: "vol"},
		AvailableSpace:   1000, Size: 1000, State: "online",
	}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UpdateLun, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.LunResponse{ProviderResponse: vsa.ProviderResponse{Name: "/vol/vol1/lun1"}}, nil)
	backupChainBytes := int64(4096)
	s.env.OnActivity(updateActivity.UpdateVolumeInDB, mock.Anything, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		var updatedVolume *datamodel.Volume
		switch v := args.Get(1).(type) {
		case *datamodel.Volume:
			updatedVolume = v
		case datamodel.Volume:
			updatedVolume = &v
		default:
			assert.Failf(s.T(), "unexpected volume argument type", "got %T", args.Get(1))
			return
		}

		if assert.NotNil(s.T(), updatedVolume.DataProtection) {
			assert.Equal(s.T(), "", updatedVolume.DataProtection.BackupVaultID)
			if assert.NotNil(s.T(), updatedVolume.DataProtection.BackupChainBytes) {
				assert.EqualValues(s.T(), backupChainBytes, *updatedVolume.DataProtection.BackupChainBytes)
			}
		}
	}).Return(nil)

	newVaultID := ""
	volume := &datamodel.Volume{
		Pool:             &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{}},
		Account:          &datamodel.Account{Name: "test_account"},
		SizeInBytes:      1000,
		VolumeAttributes: &datamodel.VolumeAttributes{IsDataProtection: false},
		DataProtection: &datamodel.DataProtection{
			BackupVaultID:    "old-vault-uuid",
			BackupChainBytes: &backupChainBytes,
		},
	}
	params := &common.UpdateVolumeParams{
		QuotaInBytes:   2000,
		DataProtection: &models.UpdateDataProtection{BackupVaultID: &newVaultID},
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
}

func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_BackupVaultSwitching_DeleteSnapmirrorInONTAPFails() {
	origFlag := utils.EnableBackupVaultSwitching
	defer utils.SetEnableBackupVaultSwitchingForTest(origFlag)
	utils.SetEnableBackupVaultSwitchingForTest(true)

	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	deleteActivity := activities.VolumeDeleteActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetNode)
	s.env.RegisterActivity(deleteActivity.DeleteSnapmirrorInONTAP)

	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(deleteActivity.DeleteSnapmirrorInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("delete snapmirror failed"))

	emptyVaultID := ""
	volume := &datamodel.Volume{
		Pool:             &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{}},
		Account:          &datamodel.Account{Name: "test_account"},
		SizeInBytes:      1000,
		VolumeAttributes: &datamodel.VolumeAttributes{IsDataProtection: false},
		DataProtection:   &datamodel.DataProtection{BackupVaultID: "old-vault-uuid"},
	}
	params := &common.UpdateVolumeParams{
		QuotaInBytes:   2000,
		DataProtection: &models.UpdateDataProtection{BackupVaultID: &emptyVaultID},
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
}

func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_BackupVaultSwitching_WaitForONTAPJobFails() {
	origFlag := utils.EnableBackupVaultSwitching
	defer utils.SetEnableBackupVaultSwitchingForTest(origFlag)
	utils.SetEnableBackupVaultSwitchingForTest(true)

	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}
	deleteActivity := activities.VolumeDeleteActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetNode)
	s.env.RegisterActivity(commonActivity.GetOntapJob)
	s.env.RegisterActivity(updateActivity.GetVolumeFromONTAP)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateLun)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInDB)
	s.env.RegisterActivity(deleteActivity.DeleteSnapmirrorInONTAP)

	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(deleteActivity.DeleteSnapmirrorInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{JobUUID: "job-uuid"}, nil)
	s.env.OnActivity(commonActivity.GetOntapJob, mock.Anything, "job-uuid", mock.Anything).Return(nil, errors.New("get ontap job failed"))
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{ExternalUUID: "ext-uuid", Name: "vol"},
		AvailableSpace:   1000, Size: 1000, State: "online",
	}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UpdateLun, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.LunResponse{ProviderResponse: vsa.ProviderResponse{Name: "/vol/vol1/lun1"}}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInDB, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	emptyVaultID := ""
	volume := &datamodel.Volume{
		Pool:             &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{}},
		Account:          &datamodel.Account{Name: "test_account"},
		SizeInBytes:      1000,
		VolumeAttributes: &datamodel.VolumeAttributes{IsDataProtection: false},
		DataProtection:   &datamodel.DataProtection{BackupVaultID: "old-vault-uuid"},
	}
	params := &common.UpdateVolumeParams{
		QuotaInBytes:   2000,
		DataProtection: &models.UpdateDataProtection{BackupVaultID: &emptyVaultID},
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
}

func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_UpdateFlexCacheVolumeInONTAP_Success() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}
	updateFlexCacheActivity := flexcache_activities.FlexCacheVolumeUpdateActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateFlexCacheActivity.UpdateFlexCacheVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateLun)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInDB)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 1000,
		Size:           1000,
		State:          "online",
	}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	s.env.OnActivity(updateFlexCacheActivity.UpdateFlexCacheVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return((*vsa.OntapAsyncResponse)(nil), nil)

	s.env.OnActivity(updateActivity.UpdateLun, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.LunResponse{ProviderResponse: vsa.ProviderResponse{Name: "/vol/vol1/lun1"}}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInDB, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	prevIsUpdateFlexCacheRequired := isUpdateFlexCacheRequired
	isUpdateFlexCacheRequired = func(existingVolume *datamodel.Volume, params *common.UpdateVolumeParams) bool {
		return true
	}
	defer func() { isUpdateFlexCacheRequired = prevIsUpdateFlexCacheRequired }()

	// Execute workflow
	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{
			Password:      "password",
			SecretID:      "",
			CertificateID: "",
		}},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		SizeInBytes: 1000,
		VolumeAttributes: &datamodel.VolumeAttributes{
			IsDataProtection: false,
		},
	}
	atimeScrubEnabled := true
	atimeScrubDays := int16(5)
	params := &common.UpdateVolumeParams{
		QuotaInBytes: 2000,
		CacheParameters: &models.CacheParameters{
			CacheConfig: &models.CacheConfig{
				AtimeScrubEnabled: &atimeScrubEnabled,
				AtimeScrubDays:    &atimeScrubDays,
			},
		},
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	// Assert workflow completed successfully
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
	mockStorage.AssertNumberOfCalls(s.T(), "UpdateJob", 2)
}

func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_UpdateFlexCacheVolumeInONTAP_Failure() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}
	updateFlexCacheActivity := flexcache_activities.FlexCacheVolumeUpdateActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateFlexCacheActivity.UpdateFlexCacheVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateLun)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInDB)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 1000,
		Size:           1000,
		State:          "online",
	}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateFlexCacheActivity.UpdateFlexCacheVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("error updating flexcache volume in ontap"))
	prevIsUpdateFlexCacheRequired := isUpdateFlexCacheRequired
	isUpdateFlexCacheRequired = func(existingVolume *datamodel.Volume, params *common.UpdateVolumeParams) bool {
		return true
	}
	defer func() { isUpdateFlexCacheRequired = prevIsUpdateFlexCacheRequired }()

	// Execute workflow
	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{
			Password:      "password",
			SecretID:      "",
			CertificateID: "",
		}},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		SizeInBytes: 1000,
		VolumeAttributes: &datamodel.VolumeAttributes{
			IsDataProtection: false,
		},
	}
	atimeScrubEnabled := true
	atimeScrubDays := int16(5)
	params := &common.UpdateVolumeParams{
		QuotaInBytes: 2000,
		CacheParameters: &models.CacheParameters{
			CacheConfig: &models.CacheConfig{
				AtimeScrubEnabled: &atimeScrubEnabled,
				AtimeScrubDays:    &atimeScrubDays,
			},
		},
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
}

func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_No_UpdateFlexCacheVolumeInONTAP() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}
	updateFlexCacheActivity := flexcache_activities.FlexCacheVolumeUpdateActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	mockStorage.On("UpdateVolumeFields", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateFlexCacheActivity.UpdateFlexCacheVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateLun)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInDB)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 1000,
		Size:           1000,
		State:          "online",
	}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateFlexCacheActivity.UpdateFlexCacheVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("error updating flexcache volume in ontap"))
	prevIsUpdateFlexCacheRequired := isUpdateFlexCacheRequired
	// no new parameters provided for flexcache update
	isUpdateFlexCacheRequired = func(existingVolume *datamodel.Volume, params *common.UpdateVolumeParams) bool {
		return false
	}
	defer func() { isUpdateFlexCacheRequired = prevIsUpdateFlexCacheRequired }()

	// Execute workflow
	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{
			Password:      "password",
			SecretID:      "",
			CertificateID: "",
		}},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		SizeInBytes: 1000,
		VolumeAttributes: &datamodel.VolumeAttributes{
			IsDataProtection: false,
		},
	}
	atimeScrubEnabled := true
	atimeScrubDays := int16(5)
	params := &common.UpdateVolumeParams{
		QuotaInBytes: 2000,
		CacheParameters: &models.CacheParameters{
			CacheConfig: &models.CacheConfig{
				AtimeScrubEnabled: &atimeScrubEnabled,
				AtimeScrubDays:    &atimeScrubDays,
			},
		},
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
	mockStorage.AssertNumberOfCalls(s.T(), "UpdateJob", 2)
}

func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_Success_WithDataProtectionTrue() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInDB)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 1000,
		Size:           1000,
		State:          "online",
	}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInDB, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Execute workflow
	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{
			Password:      "password",
			SecretID:      "",
			CertificateID: "",
		}},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		SizeInBytes: 1000,
		VolumeAttributes: &datamodel.VolumeAttributes{
			IsDataProtection: true,
		},
	}
	params := &common.UpdateVolumeParams{
		QuotaInBytes: 2000,
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	// Assert workflow completed successfully
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
	mockStorage.AssertNumberOfCalls(s.T(), "UpdateJob", 2)
}

func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_Success_WithSnapshotPolicy_NoExistingWithNew() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}
	createActivity := activities.VolumeCreateActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateLun)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInDB)
	s.env.RegisterActivity(createActivity.CreateSnapshotPolicyInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateSnapshotPolicyInOntap)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 1000,
		Size:           1000,
		State:          "online",
	}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UpdateLun, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.LunResponse{ProviderResponse: vsa.ProviderResponse{Name: "/vol/vol1/lun1"}}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInDB, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(createActivity.CreateSnapshotPolicyInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UpdateSnapshotPolicyInOntap, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Case : No existing snapshot policy, new policy provided
	volume1 := &datamodel.Volume{
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			}},
		Account:        &datamodel.Account{Name: "test_account"},
		SizeInBytes:    100,
		SnapshotPolicy: nil,
		VolumeAttributes: &datamodel.VolumeAttributes{
			IsDataProtection: false,
		},
	}
	params1 := &common.UpdateVolumeParams{
		QuotaInBytes: 200,
		SnapshotPolicy: &models.SnapshotPolicy{
			Name:      "policy1",
			IsEnabled: true,
			Schedules: []*models.SnapshotPolicySchedule{
				{
					Schedule: &models.Schedule{
						DaysOfMonth: []int{1, 2},
						DaysOfWeek:  []int{1},
						Hours:       []int{0},
						Minutes:     []int{0},
					},
					Count:           3,
					SnapmirrorLabel: "label1",
				},
			},
		},
		AutoTieringPolicy: &common.AutoTieringPolicy{
			AutoTieringEnabled: false,
		},
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params1, volume1)
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
}

func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_Success_WithSnapshotPolicy_NoExistingWithNew_Error() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}
	createActivity := activities.VolumeCreateActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateLun)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInDB)
	s.env.RegisterActivity(createActivity.CreateSnapshotPolicyInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateSnapshotPolicyInOntap)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return(&datamodel.Node{EndpointAddress: "127.0.0.1"}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 1000,
		Size:           1000,
		State:          "online",
	}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UpdateLun, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.LunResponse{ProviderResponse: vsa.ProviderResponse{Name: "/vol/vol1/lun1"}}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInDB, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(createActivity.CreateSnapshotPolicyInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("error"))
	s.env.OnActivity(updateActivity.UpdateSnapshotPolicyInOntap, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Case : No existing snapshot policy, new policy provided
	volume1 := &datamodel.Volume{
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			}},
		Account:        &datamodel.Account{Name: "test_account"},
		SizeInBytes:    100,
		SnapshotPolicy: nil,
		VolumeAttributes: &datamodel.VolumeAttributes{
			IsDataProtection: false,
		},
	}
	params1 := &common.UpdateVolumeParams{
		QuotaInBytes: 200,
		SnapshotPolicy: &models.SnapshotPolicy{
			Name:      "policy1",
			IsEnabled: true,
			Schedules: []*models.SnapshotPolicySchedule{
				{
					Schedule: &models.Schedule{
						DaysOfMonth: []int{1, 2},
						DaysOfWeek:  []int{1},
						Hours:       []int{0},
						Minutes:     []int{0},
					},
					Count:           3,
					SnapmirrorLabel: "label1",
				},
			},
		},
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params1, volume1)
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
}

func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_Success_WithSnapshotPolicy_ExistingWithNew() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}
	createActivity := activities.VolumeCreateActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateLun)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInDB)
	s.env.RegisterActivity(createActivity.CreateSnapshotPolicyInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateSnapshotPolicyInOntap)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 1000,
		Size:           1000,
		State:          "online",
	}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UpdateLun, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.LunResponse{ProviderResponse: vsa.ProviderResponse{Name: "/vol/vol1/lun1"}}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInDB, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(createActivity.CreateSnapshotPolicyInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UpdateSnapshotPolicyInOntap, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Case : Existing snapshot policy, new policy provided (update)
	volume2 := &datamodel.Volume{
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{
			Password:      "password",
			SecretID:      "",
			CertificateID: "",
		}},
		Account:     &datamodel.Account{Name: "test_account"},
		SizeInBytes: 100,
		SnapshotPolicy: &datamodel.SnapshotPolicy{
			Name:      "policy1",
			IsEnabled: true,
			Schedules: []*datamodel.SnapshotPolicySchedule{},
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			IsDataProtection: false,
		},
	}
	params2 := &common.UpdateVolumeParams{
		QuotaInBytes: 200,
		SnapshotPolicy: &models.SnapshotPolicy{
			Name:      "policy2",
			IsEnabled: false,
			Schedules: []*models.SnapshotPolicySchedule{
				{
					Schedule: &models.Schedule{
						DaysOfMonth: []int{3, 4},
						DaysOfWeek:  []int{2},
						Hours:       []int{1},
						Minutes:     []int{30},
					},
					Count:           2,
					SnapmirrorLabel: "label2",
				},
			},
		},
		AutoTieringPolicy: &common.AutoTieringPolicy{
			AutoTieringEnabled: false,
		},
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params2, volume2)
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
}

func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_Success_WithSnapshotPolicy_ExistingWithNew_Error() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}
	createActivity := activities.VolumeCreateActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateLun)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInDB)
	s.env.RegisterActivity(createActivity.CreateSnapshotPolicyInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateSnapshotPolicyInOntap)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return(&datamodel.Node{EndpointAddress: "127.0.0.1"}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 1000,
		Size:           1000,
		State:          "online",
	}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UpdateLun, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.LunResponse{ProviderResponse: vsa.ProviderResponse{Name: "/vol/vol1/lun1"}}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInDB, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(createActivity.CreateSnapshotPolicyInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UpdateSnapshotPolicyInOntap, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("error"))

	// Case : Existing snapshot policy, new policy provided (update)
	volume2 := &datamodel.Volume{
		Pool:        &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}},
		Account:     &datamodel.Account{Name: "test_account"},
		SizeInBytes: 100,
		SnapshotPolicy: &datamodel.SnapshotPolicy{
			Name:      "policy1",
			IsEnabled: true,
			Schedules: []*datamodel.SnapshotPolicySchedule{},
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			IsDataProtection: false,
		},
	}
	params2 := &common.UpdateVolumeParams{
		QuotaInBytes: 200,
		SnapshotPolicy: &models.SnapshotPolicy{
			Name:      "policy2",
			IsEnabled: false,
			Schedules: []*models.SnapshotPolicySchedule{
				{
					Schedule: &models.Schedule{
						DaysOfMonth: []int{3, 4},
						DaysOfWeek:  []int{2},
						Hours:       []int{1},
						Minutes:     []int{30},
					},
					Count:           2,
					SnapmirrorLabel: "label2",
				},
			},
		},
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params2, volume2)
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
}

func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_Success_WithSnapshotPolicy_NoExisting() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}
	createActivity := activities.VolumeCreateActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateLun)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInDB)
	s.env.RegisterActivity(createActivity.CreateSnapshotPolicyInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateSnapshotPolicyInOntap)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 1000,
		Size:           1000,
		State:          "online",
	}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UpdateLun, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.LunResponse{ProviderResponse: vsa.ProviderResponse{Name: "/vol/vol1/lun1"}}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInDB, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(createActivity.CreateSnapshotPolicyInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UpdateSnapshotPolicyInOntap, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Case : No snapshot policy provided in params (should skip snapshot policy logic)
	volume3 := &datamodel.Volume{
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			}},
		Account:     &datamodel.Account{Name: "test_account"},
		SizeInBytes: 100,
		VolumeAttributes: &datamodel.VolumeAttributes{
			IsDataProtection: false,
		},
	}
	params3 := &common.UpdateVolumeParams{
		QuotaInBytes:   200,
		SnapshotPolicy: nil,
		AutoTieringPolicy: &common.AutoTieringPolicy{
			AutoTieringEnabled: false,
		},
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params3, volume3)
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
}

func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_SnapshotPolicy_OnlyEnableDisable() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}
	createActivity := activities.VolumeCreateActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateLun)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInDB)
	s.env.RegisterActivity(createActivity.CreateSnapshotPolicyInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateSnapshotPolicyInOntap)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 1000,
		Size:           1000,
		State:          "online",
	}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UpdateLun, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.LunResponse{ProviderResponse: vsa.ProviderResponse{Name: "/vol/vol1/lun1"}}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInDB, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(createActivity.CreateSnapshotPolicyInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UpdateSnapshotPolicyInOntap, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			}},
		Account:     &datamodel.Account{Name: "test_account"},
		SizeInBytes: 100,
		SnapshotPolicy: &datamodel.SnapshotPolicy{
			Name:      "policy1",
			IsEnabled: true,
			Schedules: []*datamodel.SnapshotPolicySchedule{
				// Existing schedules
			},
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			IsDataProtection: false,
		},
	}
	params := &common.UpdateVolumeParams{
		QuotaInBytes: 200,
		SnapshotPolicy: &models.SnapshotPolicy{
			Name:      "policy1",
			IsEnabled: true,
			Schedules: []*models.SnapshotPolicySchedule{}, // Empty schedules
		},
		AutoTieringPolicy: &common.AutoTieringPolicy{
			AutoTieringEnabled: false,
		},
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
}

func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_DataProtectionVolume_SnapshotPolicy_Skip() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateLun)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInDB)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 1000,
		Size:           1000,
		State:          "online",
	}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UpdateLun, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.LunResponse{ProviderResponse: vsa.ProviderResponse{Name: "/vol/vol1/lun1"}}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInDB, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Case : No existing snapshot policy, new policy provided
	volume1 := &datamodel.Volume{
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			}},
		Account:        &datamodel.Account{Name: "test_account"},
		SizeInBytes:    100,
		SnapshotPolicy: nil,
		VolumeAttributes: &datamodel.VolumeAttributes{
			IsDataProtection: true,
		},
	}
	params1 := &common.UpdateVolumeParams{
		QuotaInBytes: 200,
		SnapshotPolicy: &models.SnapshotPolicy{
			Name:      "policy1",
			IsEnabled: true,
			Schedules: []*models.SnapshotPolicySchedule{
				{
					Schedule: &models.Schedule{
						DaysOfMonth: []int{1, 2},
						DaysOfWeek:  []int{1},
						Hours:       []int{0},
						Minutes:     []int{0},
					},
					Count:           3,
					SnapmirrorLabel: "label1",
				},
			},
		},
		AutoTieringPolicy: &common.AutoTieringPolicy{
			AutoTieringEnabled: false,
		},
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params1, volume1)
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
}

func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_NoSizeChange() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, "PROCESSING", mock.Anything, mock.Anything).Return(nil)
	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, "DONE", mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateLun)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInDB)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 100,
		Size:           200,
		State:          "online",
	}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInDB, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Execute workflow with no size change
	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			}},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		SizeInBytes: 100,
	}
	params := &common.UpdateVolumeParams{
		QuotaInBytes: 100,
		AutoTieringPolicy: &common.AutoTieringPolicy{
			AutoTieringEnabled: false,
		},
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	// Assert workflow completed successfully
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
	mockStorage.AssertNumberOfCalls(s.T(), "UpdateJob", 2)
}

func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_Failure() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}
	createActivity := activities.VolumeCreateActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, "PROCESSING", mock.Anything, mock.Anything).Return(nil)
	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, "ERROR", mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetNode)
	s.env.RegisterActivity(updateActivity.GetVolumeFromONTAP)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateLun)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInDB)
	s.env.RegisterActivity(createActivity.UpdateVolumeStateInDB)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("ONTAP error"))
	s.env.OnActivity(updateActivity.UpdateVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return()
	s.env.OnActivity(updateActivity.UpdateLun, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.LunResponse{ProviderResponse: vsa.ProviderResponse{Name: "/vol/vol1/lun1"}}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInDB, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(createActivity.UpdateVolumeStateInDB, mock.Anything, "test-volume-uuid", datamodel.LifeCycleStateREADY, datamodel.LifeCycleStateAvailableDetails).Return(nil)

	// Execute workflow
	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{ID: int64(2), UUID: "test-volume-uuid"},
		Pool: &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: int64(1), UUID: "test-pool-uuid"},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			},
		},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		SizeInBytes: 100,
	}
	params := &common.UpdateVolumeParams{
		QuotaInBytes: 200,
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	_, err := s.env.QueryWorkflowByID("default-test-workflow-id", "status")
	assert.Nil(s.T(), err)

	// Assert workflow failed
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NotNil(s.T(), s.env.GetWorkflowError())
	assert.Contains(s.T(), s.env.GetWorkflowError().Error(), "workflow execution error")
	mockStorage.AssertNumberOfCalls(s.T(), "UpdateJob", 2)
}

func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_JobUpdateFailure() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}
	createActivity := activities.VolumeCreateActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Times(2)
	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("failed updating job"))

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetNode)
	s.env.RegisterActivity(updateActivity.GetVolumeFromONTAP)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateLun)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInDB)
	s.env.RegisterActivity(createActivity.UpdateVolumeStateInDB)

	// Mock activities
	s.env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).Return("test-token", nil)
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 100,
		Size:           100,
		State:          "online",
	}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("ONTAP error"))
	s.env.OnActivity(updateActivity.UpdateLun, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.LunResponse{ProviderResponse: vsa.ProviderResponse{Name: "/vol/vol1/lun1"}}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInDB, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(createActivity.UpdateVolumeStateInDB, mock.Anything, "test-volume-uuid", datamodel.LifeCycleStateREADY, datamodel.LifeCycleStateAvailableDetails).Return(nil)

	// Execute workflow
	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{ID: int64(2), UUID: "test-volume-uuid"},
		Pool: &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: int64(1), UUID: "test-pool-uuid"},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			},
		},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		SizeInBytes: 100,
	}
	params := &common.UpdateVolumeParams{
		QuotaInBytes: 200,
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	_, err := s.env.QueryWorkflowByID("default-test-workflow-id", "status")
	assert.Nil(s.T(), err)

	// Assert workflow failed
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NotNil(s.T(), s.env.GetWorkflowError())
	assert.Contains(s.T(), s.env.GetWorkflowError().Error(), "workflow execution error")
	mockStorage.AssertNumberOfCalls(s.T(), "UpdateJob", 2)
}

func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_SizeChanged() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateLun)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInDB)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 1000,
		Size:           1000,
		State:          "online",
	}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UpdateLun, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.LunResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "/vol/vol1/lun1",
		},
		SerialNumber: "6c573830325d596f4f373437",
		Size:         88424124416,
		OSType:       "LINUX",
	}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInDB, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Execute workflow
	volume := &datamodel.Volume{
		Name: "test_volume",
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{
			Password:      "password",
			SecretID:      "",
			CertificateID: "",
		}},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		SizeInBytes: 1000,
		VolumeAttributes: &datamodel.VolumeAttributes{
			BlockDevices: &[]datamodel.BlockDevice{
				{
					Name: "lun_test_volume",
					HostGroupDetails: []datamodel.HostGroupDetail{
						{
							HostGroupUUID: "hg-uuid-1",
							HostQNs:       []string{"iqn.1998-01.com.vmware:host1"},
						},
					},
					OSType: "LINUX",
				},
			},
		},
	}
	params := &common.UpdateVolumeParams{
		QuotaInBytes: 2000,
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	// Assert workflow completed successfully
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
	mockStorage.AssertNumberOfCalls(s.T(), "UpdateJob", 2)
}

func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_WithBlockDevices_HostGroupsChanged() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateLun)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInDB)
	s.env.RegisterActivity(updateActivity.EnsureHostGroupsExistsAndMapDisk)
	s.env.RegisterActivity(updateActivity.UnmapHostGroupFromDisk)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 1000,
		Size:           1000,
		State:          "online",
	}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UpdateLun, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.LunResponse{ProviderResponse: vsa.ProviderResponse{Name: "/vol/vol1/lun1"}}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInDB, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.EnsureHostGroupsExistsAndMapDisk, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UnmapHostGroupFromDisk, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Execute workflow
	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{
			Password:      "password",
			SecretID:      "",
			CertificateID: "",
		}},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		SizeInBytes: 1000,
		VolumeAttributes: &datamodel.VolumeAttributes{
			BlockDevices: &[]datamodel.BlockDevice{
				{
					Name: "test-lun",
					HostGroupDetails: []datamodel.HostGroupDetail{
						{
							HostGroupUUID: "hg-uuid-1",
							HostQNs:       []string{"iqn.1998-01.com.vmware:host1"},
						},
					},
				},
			},
		},
	}
	params := &common.UpdateVolumeParams{
		QuotaInBytes: 2000,
		BlockDevices: []*common.BlockDevice{
			{
				Name:       "test-lun",
				HostGroups: []string{"hg-uuid-2", "hg-uuid-3"}, // Different host groups
			},
		},
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	// Assert workflow completed successfully
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
	mockStorage.AssertNumberOfCalls(s.T(), "UpdateJob", 2)
}

func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_WithBlockDevices_HostGroupsUnchanged() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateLun)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInDB)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 1000,
		Size:           1000,
		State:          "online",
	}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UpdateLun, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.LunResponse{ProviderResponse: vsa.ProviderResponse{Name: "/vol/vol1/lun1"}}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInDB, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Execute workflow
	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{
			Password:      "password",
			SecretID:      "",
			CertificateID: "",
		}},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		SizeInBytes: 1000,
		VolumeAttributes: &datamodel.VolumeAttributes{
			BlockDevices: &[]datamodel.BlockDevice{
				{
					Name: "test-lun",
					HostGroupDetails: []datamodel.HostGroupDetail{
						{
							HostGroupUUID: "hg-uuid-1",
							HostQNs:       []string{"iqn.1998-01.com.vmware:host1"},
						},
					},
				},
			},
		},
	}
	params := &common.UpdateVolumeParams{
		QuotaInBytes: 2000,
		BlockDevices: []*common.BlockDevice{
			{
				Name:       "test-lun",
				HostGroups: []string{"hg-uuid-1"}, // Same host groups
			},
		},
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	// Assert workflow completed successfully
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
	mockStorage.AssertNumberOfCalls(s.T(), "UpdateJob", 2)
}

func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_WithBlockDevices_NoExistingBlockDevices() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateLun)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInDB)
	s.env.RegisterActivity(updateActivity.EnsureHostGroupsExistsAndMapDisk)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 1000,
		Size:           1000,
		State:          "online",
	}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UpdateLun, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.LunResponse{ProviderResponse: vsa.ProviderResponse{Name: "/vol/vol1/lun1"}}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInDB, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.EnsureHostGroupsExistsAndMapDisk, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Execute workflow
	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{
			Password:      "password",
			SecretID:      "",
			CertificateID: "",
		}},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		SizeInBytes: 1000,
		VolumeAttributes: &datamodel.VolumeAttributes{
			BlockDevices: nil, // No existing BlockDevices
		},
	}
	params := &common.UpdateVolumeParams{
		QuotaInBytes: 2000,
		BlockDevices: []*common.BlockDevice{
			{
				Name:       "test-lun",
				HostGroups: []string{"hg-uuid-1", "hg-uuid-2"},
			},
		},
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	// Assert workflow completed successfully
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
	mockStorage.AssertNumberOfCalls(s.T(), "UpdateJob", 2)
}

func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_WithBlockProperties_Fallback() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateLun)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInDB)
	s.env.RegisterActivity(updateActivity.EnsureHostGroupsExistsAndMapDisk)
	s.env.RegisterActivity(updateActivity.UnmapHostGroupFromDisk)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 1000,
		Size:           1000,
		State:          "online",
	}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UpdateLun, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.LunResponse{ProviderResponse: vsa.ProviderResponse{Name: "/vol/vol1/lun1"}}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInDB, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.EnsureHostGroupsExistsAndMapDisk, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UnmapHostGroupFromDisk, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Execute workflow
	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{
			Password:      "password",
			SecretID:      "",
			CertificateID: "",
		}},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		SizeInBytes: 1000,
		VolumeAttributes: &datamodel.VolumeAttributes{
			BlockDevices: &[]datamodel.BlockDevice{}, // Empty BlockDevices
			BlockProperties: &datamodel.BlockProperties{
				HostGroupDetails: []datamodel.HostGroupDetail{
					{
						HostGroupUUID: "hg-uuid-1",
						HostQNs:       []string{"iqn.1998-01.com.vmware:host1"},
					},
				},
			},
		},
	}
	params := &common.UpdateVolumeParams{
		QuotaInBytes: 2000,
		BlockDevices: []*common.BlockDevice{}, // Empty BlockDevices
		BlockProperties: &common.BlockPropertiesRequest{
			HostGroupUUIDs: []string{"hg-uuid-2", "hg-uuid-3"}, // Different host groups
		},
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	// Assert workflow completed successfully
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
	mockStorage.AssertNumberOfCalls(s.T(), "UpdateJob", 2)
}

func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_BPSuccess() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateLun)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInDB)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 100,
		Size:           200,
		State:          "online",
	}, nil)
	s.env.OnActivity(updateActivity.EnsureHostGroupsExistsAndMapDisk, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UnmapHostGroupFromDisk, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInDB, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Execute workflow with no size change
	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			},
		},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		SizeInBytes: 100,
		VolumeAttributes: &datamodel.VolumeAttributes{
			BlockProperties: &datamodel.BlockProperties{
				HostGroupDetails: []datamodel.HostGroupDetail{
					{
						HostGroupUUID: "hg-uuid-1",
					},
					{
						HostGroupUUID: "hg-uuid-2",
					},
				},
			},
		},
	}
	params := &common.UpdateVolumeParams{
		QuotaInBytes: 100,
		BlockProperties: &common.BlockPropertiesRequest{
			HostGroupUUIDs: []string{"hg-uuid-3"},
		},
		AutoTieringPolicy: &common.AutoTieringPolicy{
			AutoTieringEnabled: false,
		},
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	// Assert workflow completed successfully
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
	mockStorage.AssertNumberOfCalls(s.T(), "UpdateJob", 2)
}

func TestVolumeUpdateTestSuite(t *testing.T) {
	suite.Run(t, new(VolumeUpdateTestSuite))
}

func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_FindTenancyDetailsFailure() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateLun)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInDB)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 1000,
		Size:           1000,
		State:          "online",
	}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UpdateLun, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.LunResponse{ProviderResponse: vsa.ProviderResponse{Name: "/vol/vol1/lun1"}}, nil)
	s.env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).Return("test-token", nil)
	s.env.OnActivity(updateActivity.FindTenancyDetails, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("failed to find tenancy details"))
	// s.env.OnActivity(updateActivity.UpdateVolumeInDB, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Execute workflow
	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			}},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		SizeInBytes: 100,
		DataProtection: &datamodel.DataProtection{
			BackupVaultID: "test-backup-vault",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			VendorSubnetID: "test-subnet-id",
		},
	}
	params := &common.UpdateVolumeParams{
		QuotaInBytes: 200,
		Region:       "us-west-1",
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	// Assert workflow completed successfully
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NotNil(s.T(), s.env.GetWorkflowError())
	mockStorage.AssertNumberOfCalls(s.T(), "UpdateJob", 2)
}

func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_TokenError() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}
	createActivity := activities.VolumeCreateActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetNode)
	s.env.RegisterActivity(updateActivity.GetVolumeFromONTAP)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateLun)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInDB)
	s.env.RegisterActivity(commonActivity.GetAuthJWTToken)
	s.env.RegisterActivity(createActivity.UpdateVolumeStateInDB)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 1000,
		Size:           1000,
		State:          "online",
	}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UpdateLun, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.LunResponse{ProviderResponse: vsa.ProviderResponse{Name: "/vol/vol1/lun1"}}, nil)
	s.env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).Return(nil, errors.New("failed to get auth JWT token"))
	s.env.OnActivity(createActivity.UpdateVolumeStateInDB, mock.Anything, "test-volume-uuid", datamodel.LifeCycleStateREADY, datamodel.LifeCycleStateAvailableDetails).Return(nil)

	// Execute workflow
	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
		Pool: &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			},
		},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		SizeInBytes: 100,
		DataProtection: &datamodel.DataProtection{
			BackupVaultID: "test-backup-vault",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			VendorSubnetID: "test-subnet-id",
		},
	}
	params := &common.UpdateVolumeParams{
		QuotaInBytes: 200,
		Region:       "us-west-1",
		AccountName:  "test_account",
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	// Assert workflow completed successfully
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NotNil(s.T(), s.env.GetWorkflowError())
	mockStorage.AssertNumberOfCalls(s.T(), "UpdateJob", 2)
}

func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_CheckBackupVaultExistInVCPFailure() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateLun)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInDB)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 1000,
		Size:           1000,
		State:          "online",
	}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UpdateLun, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.LunResponse{ProviderResponse: vsa.ProviderResponse{Name: "/vol/vol1/lun1"}}, nil)
	s.env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).Return("test-token", nil)
	s.env.OnActivity(updateActivity.FindTenancyDetails, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.TenancyInfo{RegionalTenantProject: "tenant-project"}, nil)
	s.env.OnActivity(updateActivity.CheckBackupVaultExistInVCP, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("failed to check backup vault exists in VCP"))

	// Execute workflow
	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			}},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		SizeInBytes: 100,
		DataProtection: &datamodel.DataProtection{
			BackupVaultID: "test-backup-vault",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			VendorSubnetID: "test-subnet-id",
		},
	}
	params := &common.UpdateVolumeParams{
		QuotaInBytes: 200,
		Region:       "us-west-1",
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	// Assert workflow completed successfully
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NotNil(s.T(), s.env.GetWorkflowError())
	mockStorage.AssertNumberOfCalls(s.T(), "UpdateJob", 2)
}

func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_CheckBucketResourceNameFailure() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateLun)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInDB)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 1000,
		Size:           1000,
		State:          "online",
	}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UpdateLun, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.LunResponse{ProviderResponse: vsa.ProviderResponse{Name: "/vol/vol1/lun1"}}, nil)
	s.env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).Return("test-token", nil)
	s.env.OnActivity(updateActivity.FindTenancyDetails, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.TenancyInfo{RegionalTenantProject: "tenant-project"}, nil)
	s.env.OnActivity(updateActivity.CheckBackupVaultExistInVCP, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.CheckBucketResourceName, mock.Anything, mock.Anything).Return(nil, errors.New("failed to check for bucket resource name"))
	// s.env.OnActivity(updateActivity.UpdateVolumeInDB, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Execute workflow
	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			}},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		SizeInBytes: 100,
		DataProtection: &datamodel.DataProtection{
			BackupVaultID: "test-backup-vault",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			VendorSubnetID: "test-subnet-id",
		},
	}
	params := &common.UpdateVolumeParams{
		QuotaInBytes: 200,
		Region:       "us-west-1",
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	// Assert workflow completed successfully
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NotNil(s.T(), s.env.GetWorkflowError())
	mockStorage.AssertNumberOfCalls(s.T(), "UpdateJob", 2)
}

func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_GenerateResourceNamesForBackupVaultFailure() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateLun)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInDB)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 1000,
		Size:           1000,
		State:          "online",
	}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UpdateLun, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.LunResponse{ProviderResponse: vsa.ProviderResponse{Name: "/vol/vol1/lun1"}}, nil)
	s.env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).Return("test-token", nil)
	s.env.OnActivity(updateActivity.FindTenancyDetails, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.TenancyInfo{RegionalTenantProject: "tenant-project"}, nil)
	s.env.OnActivity(updateActivity.CheckBackupVaultExistInVCP, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.CheckBucketResourceName, mock.Anything, mock.Anything).Return(&common.BucketDetails{}, nil)
	s.env.OnActivity(updateActivity.GenerateResourceNamesForBackupVault, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("failed to generate resource names"))

	// s.env.OnActivity(updateActivity.UpdateVolumeInDB, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Execute workflow
	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			}},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		SizeInBytes: 100,
		DataProtection: &datamodel.DataProtection{
			BackupVaultID: "test-backup-vault",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			VendorSubnetID: "test-subnet-id",
		},
	}
	params := &common.UpdateVolumeParams{
		QuotaInBytes: 200,
		Region:       "us-west-1",
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	// Assert workflow completed successfully
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NotNil(s.T(), s.env.GetWorkflowError())
	mockStorage.AssertNumberOfCalls(s.T(), "UpdateJob", 2)
}

func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_CreateBucketForBackupVaultFailure() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateLun)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInDB)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 1000,
		Size:           1000,
		State:          "online",
	}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UpdateLun, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.LunResponse{ProviderResponse: vsa.ProviderResponse{Name: "/vol/vol1/lun1"}}, nil)
	s.env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).Return("test-token", nil)
	s.env.OnActivity(updateActivity.FindTenancyDetails, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.TenancyInfo{RegionalTenantProject: "tenant-project"}, nil)
	s.env.OnActivity(updateActivity.CheckBackupVaultExistInVCP, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.CheckBucketResourceName, mock.Anything, mock.Anything).Return(&common.BucketDetails{}, nil)
	s.env.OnActivity(updateActivity.GenerateResourceNamesForBackupVault, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.ResourceNames{}, nil)
	s.env.OnActivity(updateActivity.CreateBucketForBackupVault, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.AnythingOfType("*string")).Return(nil, errors.New("failed to create bucket for backup vault"))

	// s.env.OnActivity(updateActivity.UpdateVolumeInDB, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Execute workflow
	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			}},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		SizeInBytes: 100,
		DataProtection: &datamodel.DataProtection{
			BackupVaultID: "test-backup-vault",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			VendorSubnetID: "test-subnet-id",
		},
	}
	params := &common.UpdateVolumeParams{
		QuotaInBytes: 200,
		Region:       "us-west-1",
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	// Assert workflow completed successfully
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NotNil(s.T(), s.env.GetWorkflowError())
	mockStorage.AssertNumberOfCalls(s.T(), "UpdateJob", 2)
}

func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_UpdateBucketDetailsOfBackupVaultFailure() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}
	volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}
	syncBackupZiZsActivity := backgroundactivities.SyncBackupZiZsActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateLun)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInDB)
	s.env.RegisterActivity(syncBackupZiZsActivity.SyncBucketDetails)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 1000,
		Size:           1000,
		State:          "online",
	}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UpdateLun, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.LunResponse{ProviderResponse: vsa.ProviderResponse{Name: "/vol/vol1/lun1"}}, nil)
	s.env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).Return("test-token", nil)
	s.env.OnActivity(updateActivity.FindTenancyDetails, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.TenancyInfo{RegionalTenantProject: "tenant-project"}, nil)
	s.env.OnActivity(updateActivity.CheckBackupVaultExistInVCP, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.CheckBucketResourceName, mock.Anything, mock.Anything).Return(&common.BucketDetails{}, nil)
	s.env.OnActivity(updateActivity.GenerateResourceNamesForBackupVault, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.ResourceNames{}, nil)
	s.env.OnActivity(updateActivity.CreateBucketForBackupVault, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.AnythingOfType("*string")).Return(&common.BucketDetails{
		BucketName:          "test-bucket",
		ServiceAccountName:  "test-service-account",
		VendorSubnetID:      "test-subnet-id",
		TenantProjectNumber: "test-project-number",
	}, nil)
	s.env.OnActivity(syncBackupZiZsActivity.SyncBucketDetails, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{
		BucketName:          "test-bucket",
		ServiceAccountName:  "test-service-account",
		VendorSubnetID:      "test-subnet-id",
		TenantProjectNumber: "test-project-number",
		SatisfiesPzi:        true,
		SatisfiesPzs:        false,
	}, nil)
	s.env.OnActivity(updateActivity.UpdateBucketDetailsOfBackupVault, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("failed to update bucket details of backup vault"))
	// Mock SetupCrossRegionBackupPermissionsActivity - not called for non-cross-region backup vault
	s.env.OnActivity(volumeCreateActivity.SetupCrossRegionBackupPermissionsActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	// Execute workflow
	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			}},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		SizeInBytes: 100,
		DataProtection: &datamodel.DataProtection{
			BackupVaultID: "test-backup-vault",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			VendorSubnetID: "test-subnet-id",
		},
	}
	params := &common.UpdateVolumeParams{
		QuotaInBytes: 200,
		Region:       "us-west-1",
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	// Assert workflow completed successfully
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NotNil(s.T(), s.env.GetWorkflowError())
	mockStorage.AssertNumberOfCalls(s.T(), "UpdateJob", 2)
}

func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_SyncBucketDetailsError_WorkflowContinues() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}
	volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}
	backupActivity := activities.BackupActivity{SE: mockStorage}
	syncBackupZiZsActivity := backgroundactivities.SyncBackupZiZsActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetNode)
	s.env.RegisterActivity(commonActivity.GetAuthJWTToken)
	s.env.RegisterActivity(updateActivity.GetVolumeFromONTAP)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateLun)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInDB)
	s.env.RegisterActivity(updateActivity.FindTenancyDetails)
	s.env.RegisterActivity(updateActivity.CheckBackupVaultExistInVCP)
	s.env.RegisterActivity(updateActivity.CheckBucketResourceName)
	s.env.RegisterActivity(updateActivity.GenerateResourceNamesForBackupVault)
	s.env.RegisterActivity(updateActivity.CreateBucketForBackupVault)
	s.env.RegisterActivity(updateActivity.UpdateBucketDetailsOfBackupVault)
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)
	s.env.RegisterActivity(backupActivity.UpdateBackupMetadataIfExistsActivity)
	s.env.RegisterActivity(syncBackupZiZsActivity.SyncBucketDetails)
	s.env.RegisterActivity(volumeCreateActivity.CheckOrCreateRemoteBackupVaultInVCP)
	s.env.RegisterActivity(volumeCreateActivity.UpdateRemoteBackupVaultWithBucketDetails)

	// Mock activities - all succeed except SyncBucketDetails
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 1000,
		Size:           1000,
		State:          "online",
	}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UpdateLun, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.LunResponse{ProviderResponse: vsa.ProviderResponse{Name: "/vol/vol1/lun1"}}, nil)
	s.env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).Return("test-token", nil)
	s.env.OnActivity(updateActivity.FindTenancyDetails, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.TenancyInfo{RegionalTenantProject: "tenant-project"}, nil)
	s.env.OnActivity(updateActivity.CheckBackupVaultExistInVCP, mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.BackupVault{
		BaseModel: datamodel.BaseModel{UUID: "test-backup-vault"},
		Name:      "test-backup-vault",
	}, nil)
	s.env.OnActivity(updateActivity.CheckBucketResourceName, mock.Anything, mock.Anything).Return(&common.BucketDetails{}, nil)
	s.env.OnActivity(updateActivity.GenerateResourceNamesForBackupVault, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.ResourceNames{}, nil)
	s.env.OnActivity(updateActivity.CreateBucketForBackupVault, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.AnythingOfType("*string")).Return(&common.BucketDetails{
		BucketName:          "test-bucket",
		ServiceAccountName:  "test-service-account",
		VendorSubnetID:      "test-subnet-id",
		TenantProjectNumber: "test-project-number",
	}, nil)
	s.env.OnActivity(syncBackupZiZsActivity.SyncBucketDetails, mock.Anything, mock.Anything).Return(nil, errors.New("failed to sync bucket details"))
	s.env.OnActivity(updateActivity.UpdateBucketDetailsOfBackupVault, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(volumeCreateActivity.CheckOrCreateRemoteBackupVaultInVCP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.BackupVault{
		BaseModel: datamodel.BaseModel{UUID: "test-remote-backup-vault"},
		Name:      "test-remote-backup-vault",
	}, nil)
	s.env.OnActivity(volumeCreateActivity.UpdateRemoteBackupVaultWithBucketDetails, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	// Mock SetupCrossRegionBackupPermissionsActivity - not called for non-cross-region backup vault
	s.env.OnActivity(volumeCreateActivity.SetupCrossRegionBackupPermissionsActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(updateActivity.UpdateVolumeInDB, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupActivity.UpdateBackupMetadataIfExistsActivity, mock.Anything, mock.Anything).Return(nil)

	// Execute workflow
	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			}},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		SizeInBytes: 100,
		DataProtection: &datamodel.DataProtection{
			BackupVaultID: "test-backup-vault",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			VendorSubnetID: "test-subnet-id",
		},
	}
	params := &common.UpdateVolumeParams{
		QuotaInBytes: 200,
		Region:       "us-west-1",
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	// Assert workflow completed successfully despite SyncBucketDetails error
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
	mockStorage.AssertNumberOfCalls(s.T(), "UpdateJob", 2)
}

func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_AttachBackupPolicyTokenError() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}
	volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateLun)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInDB)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 1000,
		Size:           1000,
		State:          "online",
	}, nil)
	s.env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).Return("", errors.New("failed to get auth JWT token"))
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Execute workflow
	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{
			Password:      "password",
			SecretID:      "",
			CertificateID: "",
		}},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		DataProtection: &datamodel.DataProtection{},
		VolumeAttributes: &datamodel.VolumeAttributes{
			VendorSubnetID: "test-subnet-id",
		},
	}
	backupPolicyId := "test-backup-policy-id"
	params := &common.UpdateVolumeParams{
		Region: "us-west-1",
		DataProtection: &models.UpdateDataProtection{
			BackupPolicyId: &backupPolicyId,
		},
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	// Assert workflow completed successfully
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NotNil(s.T(), s.env.GetWorkflowError())
	mockStorage.AssertNumberOfCalls(s.T(), "UpdateJob", 2)
}

func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_VerifyIfBackupPolicyExistsInVCPError() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}
	volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateLun)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInDB)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 1000,
		Size:           1000,
		State:          "online",
	}, nil)
	s.env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).Return("", nil)
	s.env.OnActivity(updateActivity.VerifyIfBackupPolicyExistsInVCP, mock.Anything, mock.Anything, mock.Anything).Return(false, errors.New("failed to verify if backup policy exists in VCP"))
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Execute workflow
	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{
			Password:      "password",
			SecretID:      "",
			CertificateID: "",
		}},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		DataProtection: &datamodel.DataProtection{},
		VolumeAttributes: &datamodel.VolumeAttributes{
			VendorSubnetID: "test-subnet-id",
		},
	}
	backupPolicyId := "test-backup-policy-id"
	params := &common.UpdateVolumeParams{
		Region: "us-west-1",
		DataProtection: &models.UpdateDataProtection{
			BackupPolicyId: &backupPolicyId,
		},
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	// Assert workflow completed successfully
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NotNil(s.T(), s.env.GetWorkflowError())
	mockStorage.AssertNumberOfCalls(s.T(), "UpdateJob", 2)
}

func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_FetchAndCreateBackupPolicyFromSDEError() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}
	volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateLun)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInDB)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 1000,
		Size:           1000,
		State:          "online",
	}, nil)
	s.env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).Return("", nil)
	s.env.OnActivity(updateActivity.VerifyIfBackupPolicyExistsInVCP, mock.Anything, mock.Anything, mock.Anything).Return(false, nil)
	s.env.OnActivity(updateActivity.FetchAndCreateBackupPolicyFromSDE, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("failed to fetch and create backup policy from SDE"))
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Execute workflow
	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{
			Password:      "password",
			SecretID:      "",
			CertificateID: "",
		}},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		DataProtection: &datamodel.DataProtection{},
		VolumeAttributes: &datamodel.VolumeAttributes{
			VendorSubnetID: "test-subnet-id",
		},
	}
	backupPolicyId := "test-backup-policy-id"
	params := &common.UpdateVolumeParams{
		Region: "us-west-1",
		DataProtection: &models.UpdateDataProtection{
			BackupPolicyId: &backupPolicyId,
		},
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	// Assert workflow completed successfully
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NotNil(s.T(), s.env.GetWorkflowError())
	mockStorage.AssertNumberOfCalls(s.T(), "UpdateJob", 2)
}

func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_CreateScheduleForBackupPolicyError() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}
	volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateLun)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInDB)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 1000,
		Size:           1000,
		State:          "online",
	}, nil)
	s.env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).Return("", nil)
	s.env.OnActivity(updateActivity.VerifyIfBackupPolicyExistsInVCP, mock.Anything, mock.Anything, mock.Anything).Return(false, nil)
	s.env.OnActivity(updateActivity.FetchAndCreateBackupPolicyFromSDE, mock.Anything, mock.Anything, mock.Anything).
		Return(&datamodel.BackupPolicy{BaseModel: datamodel.BaseModel{UUID: "test-backup-policy-uuid"}}, nil)
	s.env.OnActivity(updateActivity.CreateScheduleForBackupPolicy, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("failed to create schedule"))
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Execute workflow
	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{
			Password:      "password",
			SecretID:      "",
			CertificateID: "",
		}},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		DataProtection: &datamodel.DataProtection{},
		VolumeAttributes: &datamodel.VolumeAttributes{
			VendorSubnetID: "test-subnet-id",
		},
	}
	backupPolicyId := "test-backup-policy-id"
	params := &common.UpdateVolumeParams{
		Region: "us-west-1",
		DataProtection: &models.UpdateDataProtection{
			BackupPolicyId: &backupPolicyId,
		},
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	// Assert workflow completed successfully
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NotNil(s.T(), s.env.GetWorkflowError())
	mockStorage.AssertNumberOfCalls(s.T(), "UpdateJob", 2)
}

func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_AttachBackupPolicySuccess() {
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()

	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateLun)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInDB)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 1000,
		Size:           1000,
		State:          "online",
	}, nil)
	s.env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).Return("", nil)
	s.env.OnActivity(updateActivity.VerifyIfBackupPolicyExistsInVCP, mock.Anything, mock.Anything, mock.Anything).Return(false, nil)
	s.env.OnActivity(updateActivity.FetchAndCreateBackupPolicyFromSDE, mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.BackupPolicy{PolicyEnabled: true}, nil)
	s.env.OnActivity(updateActivity.CreateScheduleForBackupPolicy, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInDB, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Execute workflow
	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{
			Password:      "password",
			SecretID:      "",
			CertificateID: "",
		}},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		DataProtection: &datamodel.DataProtection{},
		VolumeAttributes: &datamodel.VolumeAttributes{
			VendorSubnetID: "test-subnet-id",
		},
	}
	backupPolicyId := "test-backup-policy-id"
	params := &common.UpdateVolumeParams{
		Region: "us-west-1",
		DataProtection: &models.UpdateDataProtection{
			BackupPolicyId: &backupPolicyId,
		},
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	// Assert workflow completed successfully
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
	mockStorage.AssertNumberOfCalls(s.T(), "UpdateJob", 2)
}

func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_UseVCPRegionEnabled_BackupPolicyNotFound() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage, Scheduler: &scheduler.TemporalScheduler{}}
	volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage, Scheduler: &scheduler.TemporalScheduler{}}

	originalUseVCPRegion := cvp.CVP_HOST
	defer func() {
		cvp.CVP_HOST = originalUseVCPRegion
	}()
	cvp.CVP_HOST = ""

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateLun)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInDB)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 1000,
		Size:           1000,
		State:          "online",
	}, nil)
	s.env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).Return("", nil)
	s.env.OnActivity(updateActivity.VerifyIfBackupPolicyExistsInVCP, mock.Anything, mock.Anything, mock.Anything).Return(false, nil)
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Execute workflow
	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{
			Password:      "password",
			SecretID:      "",
			CertificateID: "",
		}},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		DataProtection: &datamodel.DataProtection{},
		VolumeAttributes: &datamodel.VolumeAttributes{
			VendorSubnetID: "test-subnet-id",
		},
	}
	backupPolicyId := "test-backup-policy-id"
	params := &common.UpdateVolumeParams{
		Region: "us-west-1",
		DataProtection: &models.UpdateDataProtection{
			BackupPolicyId: &backupPolicyId,
		},
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	// Assert workflow failed with NotFoundErr
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NotNil(s.T(), s.env.GetWorkflowError())
	workflowErr := s.env.GetWorkflowError()
	assert.Contains(s.T(), workflowErr.Error(), "Backup policy test-backup-policy-id not found")
}

func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_UseVCPRegionEnabled_BackupPolicyExists_ScheduleDoesNotExist() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage, Scheduler: &scheduler.TemporalScheduler{}}

	backupPolicy := &datamodel.BackupPolicy{
		BaseModel: datamodel.BaseModel{
			UUID: "backup-policy-uuid",
		},
		Name:          "backup-policy-name",
		PolicyEnabled: true,
	}

	backupPolicyActivity := activities.BackupPolicyActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateLun)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInDB)
	s.env.RegisterActivity(backupPolicyActivity.GetBackupPolicyByUUIDAndAccountID)
	s.env.RegisterActivity(backupPolicyActivity.CheckIfBackupPolicyScheduleExists)
	s.env.RegisterActivity(updateActivity.CreateScheduleForBackupPolicy)
	s.env.RegisterActivity(backupPolicyActivity.UnpauseBackupPolicySchedule)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 1000,
		Size:           1000,
		State:          "online",
	}, nil)
	s.env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).Return("", nil)
	s.env.OnActivity(updateActivity.VerifyIfBackupPolicyExistsInVCP, mock.Anything, mock.Anything, mock.Anything).Return(true, nil)
	s.env.OnActivity(backupPolicyActivity.GetBackupPolicyByUUIDAndAccountID, mock.Anything, mock.Anything, mock.Anything).Return(backupPolicy, nil)
	s.env.OnActivity(backupPolicyActivity.CheckIfBackupPolicyScheduleExists, mock.Anything, mock.Anything).Return(false, nil)
	s.env.OnActivity(updateActivity.CreateScheduleForBackupPolicy, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupPolicyActivity.UnpauseBackupPolicySchedule, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInDB, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Execute workflow
	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{
			Password:      "password",
			SecretID:      "",
			CertificateID: "",
		}},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		DataProtection: &datamodel.DataProtection{},
		VolumeAttributes: &datamodel.VolumeAttributes{
			VendorSubnetID: "test-subnet-id",
		},
	}
	backupPolicyId := "test-backup-policy-id"
	params := &common.UpdateVolumeParams{
		Region: "us-west-1",
		DataProtection: &models.UpdateDataProtection{
			BackupPolicyId: &backupPolicyId,
		},
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	// Assert workflow completed successfully
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
}

func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_UseVCPRegionEnabled_BackupPolicyExists_ScheduleExists() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage, Scheduler: &scheduler.TemporalScheduler{}}

	backupPolicy := &datamodel.BackupPolicy{
		BaseModel: datamodel.BaseModel{
			UUID: "backup-policy-uuid",
		},
		Name:          "backup-policy-name",
		PolicyEnabled: true,
	}

	backupPolicyActivity := activities.BackupPolicyActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateLun)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInDB)
	s.env.RegisterActivity(backupPolicyActivity.GetBackupPolicyByUUIDAndAccountID)
	s.env.RegisterActivity(backupPolicyActivity.CheckIfBackupPolicyScheduleExists)
	s.env.RegisterActivity(backupPolicyActivity.UnpauseBackupPolicySchedule)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 1000,
		Size:           1000,
		State:          "online",
	}, nil)
	s.env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).Return("", nil)
	s.env.OnActivity(updateActivity.VerifyIfBackupPolicyExistsInVCP, mock.Anything, mock.Anything, mock.Anything).Return(true, nil)
	s.env.OnActivity(backupPolicyActivity.GetBackupPolicyByUUIDAndAccountID, mock.Anything, mock.Anything, mock.Anything).Return(backupPolicy, nil)
	s.env.OnActivity(backupPolicyActivity.CheckIfBackupPolicyScheduleExists, mock.Anything, mock.Anything).Return(true, nil)
	s.env.OnActivity(backupPolicyActivity.UnpauseBackupPolicySchedule, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInDB, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Execute workflow
	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{
			Password:      "password",
			SecretID:      "",
			CertificateID: "",
		}},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		DataProtection: &datamodel.DataProtection{},
		VolumeAttributes: &datamodel.VolumeAttributes{
			VendorSubnetID: "test-subnet-id",
		},
	}
	backupPolicyId := "test-backup-policy-id"
	params := &common.UpdateVolumeParams{
		Region: "us-west-1",
		DataProtection: &models.UpdateDataProtection{
			BackupPolicyId: &backupPolicyId,
		},
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	// Assert workflow completed successfully (schedule creation should be skipped)
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
	// Verify CreateScheduleForBackupPolicy was NOT called
	s.env.AssertNotCalled(s.T(), "CreateScheduleForBackupPolicy", mock.Anything, mock.Anything, mock.Anything)
}

func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_UseVCPRegionEnabled_GetBackupPolicyByUUIDFails() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage, Scheduler: &scheduler.TemporalScheduler{}}
	backupPolicyActivity := activities.BackupPolicyActivity{SE: mockStorage, Scheduler: &scheduler.TemporalScheduler{}}
	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateLun)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInDB)
	s.env.RegisterActivity(backupPolicyActivity.GetBackupPolicyByUUIDAndAccountID)
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 1000,
		Size:           1000,
		State:          "online",
	}, nil)
	s.env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).Return("", nil)
	s.env.OnActivity(updateActivity.VerifyIfBackupPolicyExistsInVCP, mock.Anything, mock.Anything, mock.Anything).Return(true, nil)
	s.env.OnActivity(backupPolicyActivity.GetBackupPolicyByUUIDAndAccountID, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("failed to get backup policy"))
	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{
			Password:      "password",
			SecretID:      "",
			CertificateID: "",
		}},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		DataProtection: &datamodel.DataProtection{},
		VolumeAttributes: &datamodel.VolumeAttributes{
			VendorSubnetID: "test-subnet-id",
		},
	}
	backupPolicyId := "test-backup-policy-id"
	params := &common.UpdateVolumeParams{
		Region: "us-west-1",
		DataProtection: &models.UpdateDataProtection{
			BackupPolicyId: &backupPolicyId,
		},
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NotNil(s.T(), s.env.GetWorkflowError())
}

func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_UseVCPRegionEnabled_CheckScheduleExistsFails() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage, Scheduler: &scheduler.TemporalScheduler{}}
	backupPolicy := &datamodel.BackupPolicy{
		BaseModel: datamodel.BaseModel{
			UUID: "backup-policy-uuid",
		},
		Name:          "backup-policy-name",
		PolicyEnabled: true,
	}
	backupPolicyActivity := activities.BackupPolicyActivity{SE: mockStorage}
	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateLun)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInDB)
	s.env.RegisterActivity(backupPolicyActivity.GetBackupPolicyByUUIDAndAccountID)
	s.env.RegisterActivity(backupPolicyActivity.CheckIfBackupPolicyScheduleExists)
	s.env.RegisterActivity(backupPolicyActivity.UnpauseBackupPolicySchedule)
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 1000,
		Size:           1000,
		State:          "online",
	}, nil)
	s.env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).Return("", nil)
	s.env.OnActivity(updateActivity.VerifyIfBackupPolicyExistsInVCP, mock.Anything, mock.Anything, mock.Anything).Return(true, nil)
	s.env.OnActivity(backupPolicyActivity.GetBackupPolicyByUUIDAndAccountID, mock.Anything, mock.Anything, mock.Anything).Return(backupPolicy, nil)
	s.env.OnActivity(backupPolicyActivity.CheckIfBackupPolicyScheduleExists, mock.Anything, mock.Anything).Return(false, errors.New("failed to check schedule"))
	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{
			Password:      "password",
			SecretID:      "",
			CertificateID: "",
		}},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		DataProtection: &datamodel.DataProtection{},
		VolumeAttributes: &datamodel.VolumeAttributes{
			VendorSubnetID: "test-subnet-id",
		},
	}
	backupPolicyId := "test-backup-policy-id"
	params := &common.UpdateVolumeParams{
		Region: "us-west-1",
		DataProtection: &models.UpdateDataProtection{
			BackupPolicyId: &backupPolicyId,
		},
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NotNil(s.T(), s.env.GetWorkflowError())
}

func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_UseVCPRegionEnabled_CreateScheduleFails() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage, Scheduler: &scheduler.TemporalScheduler{}}
	backupPolicy := &datamodel.BackupPolicy{
		BaseModel: datamodel.BaseModel{
			UUID: "backup-policy-uuid",
		},
		Name:          "backup-policy-name",
		PolicyEnabled: true,
	}
	backupPolicyActivity := activities.BackupPolicyActivity{SE: mockStorage}
	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateLun)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInDB)
	s.env.RegisterActivity(backupPolicyActivity.GetBackupPolicyByUUIDAndAccountID)
	s.env.RegisterActivity(backupPolicyActivity.CheckIfBackupPolicyScheduleExists)
	s.env.RegisterActivity(updateActivity.CreateScheduleForBackupPolicy)
	s.env.RegisterActivity(backupPolicyActivity.UnpauseBackupPolicySchedule)
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 1000,
		Size:           1000,
		State:          "online",
	}, nil)
	s.env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).Return("", nil)
	s.env.OnActivity(updateActivity.VerifyIfBackupPolicyExistsInVCP, mock.Anything, mock.Anything, mock.Anything).Return(true, nil)
	s.env.OnActivity(backupPolicyActivity.GetBackupPolicyByUUIDAndAccountID, mock.Anything, mock.Anything, mock.Anything).Return(backupPolicy, nil)
	s.env.OnActivity(backupPolicyActivity.CheckIfBackupPolicyScheduleExists, mock.Anything, mock.Anything).Return(false, nil)
	s.env.OnActivity(updateActivity.CreateScheduleForBackupPolicy, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("failed to create schedule"))
	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{
			Password:      "password",
			SecretID:      "",
			CertificateID: "",
		}},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		DataProtection: &datamodel.DataProtection{},
		VolumeAttributes: &datamodel.VolumeAttributes{
			VendorSubnetID: "test-subnet-id",
		},
	}
	backupPolicyId := "test-backup-policy-id"
	params := &common.UpdateVolumeParams{
		Region: "us-west-1",
		DataProtection: &models.UpdateDataProtection{
			BackupPolicyId: &backupPolicyId,
		},
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NotNil(s.T(), s.env.GetWorkflowError())
}

func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_UseVCPRegionEnabled_UnpauseScheduleFails() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage, Scheduler: &scheduler.TemporalScheduler{}}
	backupPolicy := &datamodel.BackupPolicy{
		BaseModel: datamodel.BaseModel{
			UUID: "backup-policy-uuid",
		},
		Name:          "backup-policy-name",
		PolicyEnabled: true,
	}
	backupPolicyActivity := activities.BackupPolicyActivity{SE: mockStorage}
	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateLun)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInDB)
	s.env.RegisterActivity(backupPolicyActivity.GetBackupPolicyByUUIDAndAccountID)
	s.env.RegisterActivity(backupPolicyActivity.CheckIfBackupPolicyScheduleExists)
	s.env.RegisterActivity(backupPolicyActivity.UnpauseBackupPolicySchedule)
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 1000,
		Size:           1000,
		State:          "online",
	}, nil)
	s.env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).Return("", nil)
	s.env.OnActivity(updateActivity.VerifyIfBackupPolicyExistsInVCP, mock.Anything, mock.Anything, mock.Anything).Return(true, nil)
	s.env.OnActivity(backupPolicyActivity.GetBackupPolicyByUUIDAndAccountID, mock.Anything, mock.Anything, mock.Anything).Return(backupPolicy, nil)
	s.env.OnActivity(backupPolicyActivity.CheckIfBackupPolicyScheduleExists, mock.Anything, mock.Anything).Return(true, nil)
	s.env.OnActivity(backupPolicyActivity.UnpauseBackupPolicySchedule, mock.Anything, mock.Anything).Return(errors.New("failed to unpause schedule"))
	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{
			Password:      "password",
			SecretID:      "",
			CertificateID: "",
		}},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		DataProtection: &datamodel.DataProtection{},
		VolumeAttributes: &datamodel.VolumeAttributes{
			VendorSubnetID: "test-subnet-id",
		},
	}
	backupPolicyId := "test-backup-policy-id"
	params := &common.UpdateVolumeParams{
		Region: "us-west-1",
		DataProtection: &models.UpdateDataProtection{
			BackupPolicyId: &backupPolicyId,
		},
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NotNil(s.T(), s.env.GetWorkflowError())
}

func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_BackupPolicyExists_PolicyDisabled_ScheduleDoesNotExist() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage, Scheduler: &scheduler.TemporalScheduler{}}
	backupPolicyActivity := activities.BackupPolicyActivity{SE: mockStorage}

	disabledBackupPolicy := &datamodel.BackupPolicy{
		BaseModel: datamodel.BaseModel{
			UUID: "backup-policy-uuid",
		},
		Name:          "backup-policy-name",
		PolicyEnabled: false,
	}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateLun)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInDB)
	s.env.RegisterActivity(backupPolicyActivity.GetBackupPolicyByUUIDAndAccountID)
	s.env.RegisterActivity(backupPolicyActivity.CheckIfBackupPolicyScheduleExists)
	s.env.RegisterActivity(updateActivity.CreateScheduleForBackupPolicy)
	s.env.RegisterActivity(backupPolicyActivity.PauseBackupPolicySchedule)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 1000,
		Size:           1000,
		State:          "online",
	}, nil)
	s.env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).Return("", nil)
	s.env.OnActivity(updateActivity.VerifyIfBackupPolicyExistsInVCP, mock.Anything, mock.Anything, mock.Anything).Return(true, nil)
	s.env.OnActivity(backupPolicyActivity.GetBackupPolicyByUUIDAndAccountID, mock.Anything, mock.Anything, mock.Anything).Return(disabledBackupPolicy, nil)
	s.env.OnActivity(backupPolicyActivity.CheckIfBackupPolicyScheduleExists, mock.Anything, mock.Anything).Return(false, nil)
	s.env.OnActivity(updateActivity.CreateScheduleForBackupPolicy, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupPolicyActivity.PauseBackupPolicySchedule, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInDB, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Execute workflow
	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{
			Password:      "password",
			SecretID:      "",
			CertificateID: "",
		}},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		DataProtection: &datamodel.DataProtection{},
		VolumeAttributes: &datamodel.VolumeAttributes{
			VendorSubnetID: "test-subnet-id",
		},
	}
	backupPolicyId := "test-backup-policy-id"
	params := &common.UpdateVolumeParams{
		Region: "us-west-1",
		DataProtection: &models.UpdateDataProtection{
			BackupPolicyId: &backupPolicyId,
		},
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	// Assert workflow completed successfully and schedule was paused
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
	s.env.AssertExpectations(s.T())
}

func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_BackupPolicyExists_PolicyDisabled_ScheduleExists() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage, Scheduler: &scheduler.TemporalScheduler{}}
	backupPolicyActivity := activities.BackupPolicyActivity{SE: mockStorage}

	disabledBackupPolicy := &datamodel.BackupPolicy{
		BaseModel: datamodel.BaseModel{
			UUID: "backup-policy-uuid",
		},
		Name:          "backup-policy-name",
		PolicyEnabled: false,
	}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateLun)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInDB)
	s.env.RegisterActivity(backupPolicyActivity.GetBackupPolicyByUUIDAndAccountID)
	s.env.RegisterActivity(backupPolicyActivity.CheckIfBackupPolicyScheduleExists)
	s.env.RegisterActivity(backupPolicyActivity.PauseBackupPolicySchedule)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 1000,
		Size:           1000,
		State:          "online",
	}, nil)
	s.env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).Return("", nil)
	s.env.OnActivity(updateActivity.VerifyIfBackupPolicyExistsInVCP, mock.Anything, mock.Anything, mock.Anything).Return(true, nil)
	s.env.OnActivity(backupPolicyActivity.GetBackupPolicyByUUIDAndAccountID, mock.Anything, mock.Anything, mock.Anything).Return(disabledBackupPolicy, nil)
	s.env.OnActivity(backupPolicyActivity.CheckIfBackupPolicyScheduleExists, mock.Anything, mock.Anything).Return(true, nil)
	s.env.OnActivity(backupPolicyActivity.PauseBackupPolicySchedule, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInDB, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Execute workflow
	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{
			Password:      "password",
			SecretID:      "",
			CertificateID: "",
		}},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		DataProtection: &datamodel.DataProtection{},
		VolumeAttributes: &datamodel.VolumeAttributes{
			VendorSubnetID: "test-subnet-id",
		},
	}
	backupPolicyId := "test-backup-policy-id"
	params := &common.UpdateVolumeParams{
		Region: "us-west-1",
		DataProtection: &models.UpdateDataProtection{
			BackupPolicyId: &backupPolicyId,
		},
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	// Assert workflow completed and CreateScheduleForBackupPolicy was NOT called
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
	s.env.AssertNotCalled(s.T(), "CreateScheduleForBackupPolicy", mock.Anything, mock.Anything, mock.Anything)
	s.env.AssertExpectations(s.T())
}

func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_BackupPolicyExists_PolicyDisabled_PauseScheduleError() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage, Scheduler: &scheduler.TemporalScheduler{}}
	backupPolicyActivity := activities.BackupPolicyActivity{SE: mockStorage}

	disabledBackupPolicy := &datamodel.BackupPolicy{
		BaseModel: datamodel.BaseModel{
			UUID: "backup-policy-uuid",
		},
		Name:          "backup-policy-name",
		PolicyEnabled: false,
	}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateLun)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInDB)
	s.env.RegisterActivity(backupPolicyActivity.GetBackupPolicyByUUIDAndAccountID)
	s.env.RegisterActivity(backupPolicyActivity.CheckIfBackupPolicyScheduleExists)
	s.env.RegisterActivity(backupPolicyActivity.PauseBackupPolicySchedule)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 1000,
		Size:           1000,
		State:          "online",
	}, nil)
	s.env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).Return("", nil)
	s.env.OnActivity(updateActivity.VerifyIfBackupPolicyExistsInVCP, mock.Anything, mock.Anything, mock.Anything).Return(true, nil)
	s.env.OnActivity(backupPolicyActivity.GetBackupPolicyByUUIDAndAccountID, mock.Anything, mock.Anything, mock.Anything).Return(disabledBackupPolicy, nil)
	s.env.OnActivity(backupPolicyActivity.CheckIfBackupPolicyScheduleExists, mock.Anything, mock.Anything).Return(true, nil)
	s.env.OnActivity(backupPolicyActivity.PauseBackupPolicySchedule, mock.Anything, mock.Anything).Return(errors.New("failed to pause backup policy schedule"))

	// Execute workflow
	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{
			Password:      "password",
			SecretID:      "",
			CertificateID: "",
		}},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		DataProtection: &datamodel.DataProtection{},
		VolumeAttributes: &datamodel.VolumeAttributes{
			VendorSubnetID: "test-subnet-id",
		},
	}
	backupPolicyId := "test-backup-policy-id"
	params := &common.UpdateVolumeParams{
		Region: "us-west-1",
		DataProtection: &models.UpdateDataProtection{
			BackupPolicyId: &backupPolicyId,
		},
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	// Assert workflow failed due to PauseBackupPolicySchedule error
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
	assert.Contains(s.T(), s.env.GetWorkflowError().Error(), "failed to pause backup policy schedule")
	s.env.AssertExpectations(s.T())
}

func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_PauseBackupPolicyWhenDisabled() {
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()

	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}
	backupPolicyActivity := activities.BackupPolicyActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateLun)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInDB)
	s.env.RegisterActivity(backupPolicyActivity.PauseBackupPolicySchedule)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 1000,
		Size:           1000,
		State:          "online",
	}, nil)
	s.env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).Return("", nil)
	s.env.OnActivity(updateActivity.VerifyIfBackupPolicyExistsInVCP, mock.Anything, mock.Anything, mock.Anything).Return(false, nil)

	// Mock a backup policy with PolicyEnabled = false
	disabledBackupPolicy := &datamodel.BackupPolicy{
		BaseModel: datamodel.BaseModel{
			UUID: "test-backup-policy-uuid",
		},
		Name:          "test-backup-policy",
		PolicyEnabled: false, // This is the key condition for the test
	}

	s.env.OnActivity(updateActivity.FetchAndCreateBackupPolicyFromSDE, mock.Anything, mock.Anything, mock.Anything).Return(disabledBackupPolicy, nil)
	s.env.OnActivity(updateActivity.CreateScheduleForBackupPolicy, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupPolicyActivity.PauseBackupPolicySchedule, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInDB, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Execute workflow
	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{
			Password:      "password",
			SecretID:      "",
			CertificateID: "",
		}},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		DataProtection: &datamodel.DataProtection{},
		VolumeAttributes: &datamodel.VolumeAttributes{
			VendorSubnetID: "test-subnet-id",
		},
	}
	backupPolicyId := "test-backup-policy-id"
	params := &common.UpdateVolumeParams{
		Region: "us-west-1",
		DataProtection: &models.UpdateDataProtection{
			BackupPolicyId: &backupPolicyId,
		},
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	// Assert workflow completed successfully
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
	mockStorage.AssertNumberOfCalls(s.T(), "UpdateJob", 2)

	// Verify that PauseBackupPolicySchedule was called with the disabled backup policy
	s.env.AssertExpectations(s.T())
}

func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_PauseBackupPolicyScheduleError() {
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()

	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}
	backupPolicyActivity := activities.BackupPolicyActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateLun)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInDB)
	s.env.RegisterActivity(backupPolicyActivity.PauseBackupPolicySchedule)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 1000,
		Size:           1000,
		State:          "online",
	}, nil)
	s.env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).Return("", nil)
	s.env.OnActivity(updateActivity.VerifyIfBackupPolicyExistsInVCP, mock.Anything, mock.Anything, mock.Anything).Return(false, nil)

	// Mock a backup policy with PolicyEnabled = false
	disabledBackupPolicy := &datamodel.BackupPolicy{
		BaseModel: datamodel.BaseModel{
			UUID: "test-backup-policy-uuid",
		},
		Name:          "test-backup-policy",
		PolicyEnabled: false, // This is the key condition for the test
	}

	s.env.OnActivity(updateActivity.FetchAndCreateBackupPolicyFromSDE, mock.Anything, mock.Anything, mock.Anything).Return(disabledBackupPolicy, nil)
	s.env.OnActivity(updateActivity.CreateScheduleForBackupPolicy, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupPolicyActivity.PauseBackupPolicySchedule, mock.Anything, mock.Anything).Return(errors.New("failed to pause backup policy schedule"))

	// Execute workflow
	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{
			Password:      "password",
			SecretID:      "",
			CertificateID: "",
		}},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		DataProtection: &datamodel.DataProtection{},
		VolumeAttributes: &datamodel.VolumeAttributes{
			VendorSubnetID: "test-subnet-id",
		},
	}
	backupPolicyId := "test-backup-policy-id"
	params := &common.UpdateVolumeParams{
		Region: "us-west-1",
		DataProtection: &models.UpdateDataProtection{
			BackupPolicyId: &backupPolicyId,
		},
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	// Assert workflow failed due to PauseBackupPolicySchedule error
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
	assert.Contains(s.T(), s.env.GetWorkflowError().Error(), "failed to pause backup policy schedule")

	// Verify that PauseBackupPolicySchedule was called with the disabled backup policy
	s.env.AssertExpectations(s.T())
}

func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_AutoTier() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateLun)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInDB)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 100,
		Size:           200,
		State:          "online",
	}, nil)
	s.env.OnActivity(updateActivity.EnsureHostGroupsExistsAndMapDisk, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UnmapHostGroupFromDisk, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInDB, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Execute workflow with no size change
	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			}},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		SizeInBytes: 100,
		VolumeAttributes: &datamodel.VolumeAttributes{
			BlockProperties: &datamodel.BlockProperties{
				HostGroupDetails: []datamodel.HostGroupDetail{
					{
						HostGroupUUID: "hg-uuid-1",
					},
					{
						HostGroupUUID: "hg-uuid-2",
					},
				},
			},
		},
		AutoTieringEnabled: true,
		AutoTieringPolicy: &datamodel.AutoTieringPolicy{
			CoolingThresholdDays: 5,
		},
	}
	params := &common.UpdateVolumeParams{
		QuotaInBytes: 100,
		BlockProperties: &common.BlockPropertiesRequest{
			HostGroupUUIDs: []string{"hg-uuid-3"},
		},
		AutoTieringPolicy: &common.AutoTieringPolicy{
			AutoTieringEnabled:   true,
			CoolingThresholdDays: 10,
		},
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	// Assert workflow completed successfully
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())

	// Run the workflow again with different params
	s.env = s.NewTestWorkflowEnvironment()
	s.env.RegisterWorkflow(UpdateVolumeWorkflow)
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.OnActivity(commonActivity.GetJob, mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	s.env.OnActivity(updateActivity.UpdateLun, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.LunResponse{ProviderResponse: vsa.ProviderResponse{Name: "/vol/vol1/lun1"}}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInDB, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 100,
		Size:           200,
		State:          "online",
	}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInDB, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// New params for second run
	params2 := &common.UpdateVolumeParams{
		QuotaInBytes: 100,
		AutoTieringPolicy: &common.AutoTieringPolicy{
			AutoTieringEnabled: false,
		},
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params2, volume)
	assert.True(s.T(), s.env.IsWorkflowCompleted())

	assert.Nil(s.T(), s.env.GetWorkflowError())
	mockStorage.AssertNumberOfCalls(s.T(), "UpdateJob", 4)
}

func TestIsUpdateRequired(t *testing.T) {
	tests := []struct {
		name           string
		response       *vsa.VolumeResponse
		params         *common.UpdateVolumeParams
		existingVolume *datamodel.Volume
		want           bool
	}{
		{
			name: "Size increased - update required",
			response: &vsa.VolumeResponse{
				Size: 100,
			},
			params: &common.UpdateVolumeParams{
				QuotaInBytes: 200,
				AutoTieringPolicy: &common.AutoTieringPolicy{
					AutoTieringEnabled: false,
				},
			},
			existingVolume: &datamodel.Volume{
				AutoTieringEnabled: false,
			},
			want: true,
		},
		{
			name: "Size same - no update required",
			response: &vsa.VolumeResponse{
				Size: 200,
			},
			params: &common.UpdateVolumeParams{
				QuotaInBytes: 200,
				AutoTieringPolicy: &common.AutoTieringPolicy{
					AutoTieringEnabled: false,
				},
			},
			existingVolume: &datamodel.Volume{
				AutoTieringEnabled: false,
			},
			want: false,
		},
		{
			name: "SnapReserve changed - update required",
			response: &vsa.VolumeResponse{
				Size:        150,
				SnapReserve: 10,
			},
			params: &common.UpdateVolumeParams{
				QuotaInBytes: 150,
				SnapReserve:  &[]int64{20}[0],
				AutoTieringPolicy: &common.AutoTieringPolicy{
					AutoTieringEnabled: false,
				},
			},
			existingVolume: &datamodel.Volume{
				AutoTieringEnabled: false,
			},
			want: true,
		},
		{
			name: "SnapReserve same - no update required",
			response: &vsa.VolumeResponse{
				Size:        150,
				SnapReserve: 10,
			},
			params: &common.UpdateVolumeParams{
				QuotaInBytes: 150,
				SnapReserve:  &[]int64{10}[0],
				AutoTieringPolicy: &common.AutoTieringPolicy{
					AutoTieringEnabled: false,
				},
			},
			existingVolume: &datamodel.Volume{
				AutoTieringEnabled: false,
			},
			want: false,
		},
		{
			name: "SnapshotPolicy name changed - update required",
			response: &vsa.VolumeResponse{
				Size:               150,
				SnapshotPolicyName: "old-policy",
			},
			params: &common.UpdateVolumeParams{
				QuotaInBytes:   150,
				SnapshotPolicy: &models.SnapshotPolicy{Name: "new-policy"},
				AutoTieringPolicy: &common.AutoTieringPolicy{
					AutoTieringEnabled: false,
				},
			},
			existingVolume: &datamodel.Volume{
				AutoTieringEnabled: false,
			},
			want: true,
		},
		{
			name: "SnapshotPolicy name same - no update required",
			response: &vsa.VolumeResponse{
				Size:               150,
				SnapshotPolicyName: "policy1",
			},
			params: &common.UpdateVolumeParams{
				QuotaInBytes:   150,
				SnapshotPolicy: &models.SnapshotPolicy{Name: "policy1"},
				AutoTieringPolicy: &common.AutoTieringPolicy{
					AutoTieringEnabled: false,
				},
			},
			existingVolume: &datamodel.Volume{
				AutoTieringEnabled: false,
			},
			want: false,
		},
		{
			name: "AutoTieringEnabled changed from false to true - update required",
			response: &vsa.VolumeResponse{
				Size: 150,
			},
			params: &common.UpdateVolumeParams{
				QuotaInBytes: 150,
				AutoTieringPolicy: &common.AutoTieringPolicy{
					AutoTieringEnabled:   true,
					CoolingThresholdDays: 10,
				},
			},
			existingVolume: &datamodel.Volume{
				AutoTieringEnabled: false,
				AutoTieringPolicy: &datamodel.AutoTieringPolicy{
					CoolingThresholdDays: 5,
				},
			},
			want: true,
		},
		{
			name: "AutoTieringEnabled changed from true to false - update required",
			response: &vsa.VolumeResponse{
				Size: 150,
			},
			params: &common.UpdateVolumeParams{
				AutoTieringPolicy: &common.AutoTieringPolicy{
					AutoTieringEnabled: false,
				},
			},
			existingVolume: &datamodel.Volume{
				AutoTieringEnabled: true,
				AutoTieringPolicy: &datamodel.AutoTieringPolicy{
					CoolingThresholdDays: 10,
				},
			},
			want: true,
		},
		{
			name: "CoolnessPeriod changed when AutoTieringEnabled is true - update required",
			response: &vsa.VolumeResponse{
				Size: 150,
			},
			params: &common.UpdateVolumeParams{
				AutoTieringPolicy: &common.AutoTieringPolicy{
					AutoTieringEnabled:   true,
					CoolingThresholdDays: 15,
				},
			},
			existingVolume: &datamodel.Volume{
				AutoTieringEnabled: true,
				AutoTieringPolicy:  &datamodel.AutoTieringPolicy{CoolingThresholdDays: 10},
			},
			want: true,
		},
		{
			name: "CoolnessPeriod changed when AutoTieringEnabled is false - no update required",
			response: &vsa.VolumeResponse{
				Size: 150,
			},
			params: &common.UpdateVolumeParams{
				AutoTieringPolicy: &common.AutoTieringPolicy{
					AutoTieringEnabled:   false,
					CoolingThresholdDays: 15,
				},
			},
			existingVolume: &datamodel.Volume{
				AutoTieringEnabled: false,
				AutoTieringPolicy:  &datamodel.AutoTieringPolicy{CoolingThresholdDays: 10},
			},
			want: false,
		},
		{
			name: "No changes - no update required",
			response: &vsa.VolumeResponse{
				Size:               150,
				SnapReserve:        10,
				SnapshotPolicyName: "policy1",
			},
			params: &common.UpdateVolumeParams{
				QuotaInBytes:   150,
				SnapReserve:    &[]int64{10}[0],
				SnapshotPolicy: &models.SnapshotPolicy{Name: "policy1"},
				AutoTieringPolicy: &common.AutoTieringPolicy{
					AutoTieringEnabled:   true,
					CoolingThresholdDays: 10,
				},
			},
			existingVolume: &datamodel.Volume{
				AutoTieringEnabled: true,
				AutoTieringPolicy:  &datamodel.AutoTieringPolicy{CoolingThresholdDays: 10},
			},
			want: false,
		},
		{
			name: "Multiple changes - update required",
			response: &vsa.VolumeResponse{
				Size:               100,
				SnapReserve:        5,
				SnapshotPolicyName: "old-policy",
			},
			params: &common.UpdateVolumeParams{
				QuotaInBytes:   200,
				SnapReserve:    &[]int64{15}[0],
				SnapshotPolicy: &models.SnapshotPolicy{Name: "new-policy"},
				AutoTieringPolicy: &common.AutoTieringPolicy{
					AutoTieringEnabled:   true,
					CoolingThresholdDays: 20,
				},
			},
			existingVolume: &datamodel.Volume{
				AutoTieringEnabled: false,
				AutoTieringPolicy:  &datamodel.AutoTieringPolicy{CoolingThresholdDays: 10},
			},
			want: true,
		},
		{
			name: "No SnapReserve or SnapshotPolicy in params - no update required",
			response: &vsa.VolumeResponse{
				Size:               150,
				SnapReserve:        5,
				SnapshotPolicyName: "policy1",
			},
			params: &common.UpdateVolumeParams{
				QuotaInBytes: 150,
				AutoTieringPolicy: &common.AutoTieringPolicy{
					AutoTieringEnabled: false,
				},
			},
			existingVolume: &datamodel.Volume{
				AutoTieringEnabled: false,
			},
			want: false,
		},
		{
			name: "Size decreased - update required (size reduction support)",
			response: &vsa.VolumeResponse{
				Size: 200,
			},
			params: &common.UpdateVolumeParams{
				QuotaInBytes: 100,
				AutoTieringPolicy: &common.AutoTieringPolicy{
					AutoTieringEnabled: false,
				},
			},
			existingVolume: &datamodel.Volume{
				AutoTieringEnabled: false,
			},
			want: true, // Changed from false - now detects size reductions
		},
		{
			name: "Size is same and no tiering policy passed - no update required",
			response: &vsa.VolumeResponse{
				Size: 200,
			},
			params: &common.UpdateVolumeParams{
				QuotaInBytes: 200,
			},
			existingVolume: &datamodel.Volume{},
			want:           false,
		},
		{
			name: "QuotaInBytes is 0 - no size update requested",
			response: &vsa.VolumeResponse{
				Size: 200,
			},
			params: &common.UpdateVolumeParams{
				QuotaInBytes: 0, // Not changing size
			},
			existingVolume: &datamodel.Volume{},
			want:           false,
		},
		{
			name: "Unix permissions change requires update",
			response: &vsa.VolumeResponse{
				Size: 200,
			},
			params: &common.UpdateVolumeParams{
				QuotaInBytes: 200,
				FileProperties: &models.FileProperties{
					UnixPermissions: "0770",
				},
			},
			existingVolume: &datamodel.Volume{
				VolumeAttributes: &datamodel.VolumeAttributes{
					FileProperties: &datamodel.FileProperties{
						UnixPermissions: "0755",
					},
				},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isUpdateRequired(tt.response, tt.params, tt.existingVolume)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestGetUpdateParamsForRollback(t *testing.T) {
	tests := []struct {
		name           string
		volResponse    *vsa.VolumeResponse
		existingVolume *datamodel.Volume
		expected       *common.UpdateVolumeParams
	}{
		{
			name: "Volume with AutoTieringPolicy - all fields populated",
			volResponse: &vsa.VolumeResponse{
				Size: 1024 * 1024 * 1024, // 1GB
			},
			existingVolume: &datamodel.Volume{
				AutoTieringEnabled: true,
				AutoTieringPolicy: &datamodel.AutoTieringPolicy{
					CoolingThresholdDays: 30,
					TieringPolicy:        "auto",
					RetrievalPolicy:      "default",
				},
			},
			expected: &common.UpdateVolumeParams{
				QuotaInBytes: 1024 * 1024 * 1024,
				AutoTieringPolicy: &common.AutoTieringPolicy{
					AutoTieringEnabled:   true,
					CoolingThresholdDays: 30,
					TieringPolicy:        "auto",
					RetrievalPolicy:      "default",
				},
			},
		},
		{
			name: "Volume with AutoTieringPolicy - AutoTieringEnabled false",
			volResponse: &vsa.VolumeResponse{
				Size: 2048 * 1024 * 1024, // 2GB
			},
			existingVolume: &datamodel.Volume{
				AutoTieringEnabled: false,
				AutoTieringPolicy: &datamodel.AutoTieringPolicy{
					CoolingThresholdDays: 15,
					TieringPolicy:        "none",
					RetrievalPolicy:      "never",
				},
			},
			expected: &common.UpdateVolumeParams{
				QuotaInBytes: 2048 * 1024 * 1024,
				AutoTieringPolicy: &common.AutoTieringPolicy{
					AutoTieringEnabled:   false,
					CoolingThresholdDays: 15,
					TieringPolicy:        "none",
					RetrievalPolicy:      "never",
				},
			},
		},
		{
			name: "Volume with AutoTieringPolicy - zero cooling threshold",
			volResponse: &vsa.VolumeResponse{
				Size: 512 * 1024 * 1024, // 512MB
			},
			existingVolume: &datamodel.Volume{
				AutoTieringEnabled: true,
				AutoTieringPolicy: &datamodel.AutoTieringPolicy{
					CoolingThresholdDays: 0,
					TieringPolicy:        "snapshot-only",
					RetrievalPolicy:      "on-read",
				},
			},
			expected: &common.UpdateVolumeParams{
				QuotaInBytes: 512 * 1024 * 1024,
				AutoTieringPolicy: &common.AutoTieringPolicy{
					AutoTieringEnabled:   true,
					CoolingThresholdDays: 0,
					TieringPolicy:        "snapshot-only",
					RetrievalPolicy:      "on-read",
				},
			},
		},
		{
			name: "Volume with AutoTieringPolicy - empty string policies",
			volResponse: &vsa.VolumeResponse{
				Size: 4096 * 1024 * 1024, // 4GB
			},
			existingVolume: &datamodel.Volume{
				AutoTieringEnabled: true,
				AutoTieringPolicy: &datamodel.AutoTieringPolicy{
					CoolingThresholdDays: 90,
					TieringPolicy:        "",
					RetrievalPolicy:      "",
				},
			},
			expected: &common.UpdateVolumeParams{
				QuotaInBytes: 4096 * 1024 * 1024,
				AutoTieringPolicy: &common.AutoTieringPolicy{
					AutoTieringEnabled:   true,
					CoolingThresholdDays: 90,
					TieringPolicy:        "",
					RetrievalPolicy:      "",
				},
			},
		},
		{
			name: "Volume without AutoTieringPolicy - nil policy",
			volResponse: &vsa.VolumeResponse{
				Size: 1024 * 1024 * 1024, // 1GB
			},
			existingVolume: &datamodel.Volume{
				AutoTieringEnabled: false,
				AutoTieringPolicy:  nil,
			},
			expected: &common.UpdateVolumeParams{
				QuotaInBytes: 1024 * 1024 * 1024,
				// AutoTieringPolicy should be nil
			},
		},
		{
			name: "Volume with AutoTieringEnabled but nil AutoTieringPolicy",
			volResponse: &vsa.VolumeResponse{
				Size: 2048 * 1024 * 1024, // 2GB
			},
			existingVolume: &datamodel.Volume{
				AutoTieringEnabled: true,
				AutoTieringPolicy:  nil,
			},
			expected: &common.UpdateVolumeParams{
				QuotaInBytes: 2048 * 1024 * 1024,
				// AutoTieringPolicy should be nil even though AutoTieringEnabled is true
			},
		},
		{
			name: "Volume with cooling threshold days",
			volResponse: &vsa.VolumeResponse{
				Size: 8192 * 1024 * 1024, // 8GB
			},
			existingVolume: &datamodel.Volume{
				AutoTieringEnabled: true,
				AutoTieringPolicy: &datamodel.AutoTieringPolicy{
					CoolingThresholdDays: 183, // Maximum allowed days
					TieringPolicy:        "auto",
					RetrievalPolicy:      "default",
				},
			},
			expected: &common.UpdateVolumeParams{
				QuotaInBytes: 8192 * 1024 * 1024,
				AutoTieringPolicy: &common.AutoTieringPolicy{
					AutoTieringEnabled:   true,
					CoolingThresholdDays: 183,
					TieringPolicy:        "auto",
					RetrievalPolicy:      "default",
				},
			},
		},
		{
			name: "Volume with cache parameters",
			volResponse: &vsa.VolumeResponse{
				Size: 8192 * 1024 * 1024, // 8GB
			},
			existingVolume: &datamodel.Volume{
				CacheParameters: &datamodel.CacheParameters{
					CacheConfig: &datamodel.CacheConfig{
						WritebackEnabled: func(b bool) *bool { return &b }(true),
					},
				},
			},
			expected: &common.UpdateVolumeParams{
				QuotaInBytes: 8192 * 1024 * 1024,
				CacheParameters: &models.CacheParameters{
					CacheConfig: &models.CacheConfig{
						WritebackEnabled: func(b bool) *bool { return &b }(true),
					},
				},
			},
		},
		{
			name: "Volume with nil cache parameters",
			volResponse: &vsa.VolumeResponse{
				Size: 8192 * 1024 * 1024, // 8GB
			},
			existingVolume: &datamodel.Volume{},
			expected: &common.UpdateVolumeParams{
				QuotaInBytes: 8192 * 1024 * 1024,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			convertCacheParameters = func(src *datamodel.CacheParameters) *models.CacheParameters {
				return &models.CacheParameters{
					CacheConfig: &models.CacheConfig{
						WritebackEnabled: src.CacheConfig.WritebackEnabled,
					},
				}
			}
			result := getUpdateParamsForRollback(tt.volResponse, tt.existingVolume)

			// Assert basic fields
			assert.Equal(t, tt.expected.QuotaInBytes, result.QuotaInBytes)

			// Assert AutoTieringPolicy
			if tt.expected.AutoTieringPolicy == nil {
				assert.Nil(t, result.AutoTieringPolicy)
			} else {
				assert.NotNil(t, result.AutoTieringPolicy)
				assert.Equal(t, tt.expected.AutoTieringPolicy.AutoTieringEnabled, result.AutoTieringPolicy.AutoTieringEnabled)
				assert.Equal(t, tt.expected.AutoTieringPolicy.CoolingThresholdDays, result.AutoTieringPolicy.CoolingThresholdDays)
				assert.Equal(t, tt.expected.AutoTieringPolicy.TieringPolicy, result.AutoTieringPolicy.TieringPolicy)
				assert.Equal(t, tt.expected.AutoTieringPolicy.RetrievalPolicy, result.AutoTieringPolicy.RetrievalPolicy)
			}

			if tt.expected.CacheParameters == nil {
				assert.Nil(t, result.CacheParameters)
			} else {
				assert.NotNil(t, result.CacheParameters)
				assert.Equal(t, tt.expected.CacheParameters.CacheConfig.WritebackEnabled, result.CacheParameters.CacheConfig.WritebackEnabled)
			}
		})
	}
}

func TestCloneCacheParameters(t *testing.T) {
	original := &datamodel.CacheParameters{
		PeerSvmName:     "peer-svm",
		PeerVolumeName:  "peer-volume",
		PeerClusterName: "peer-cluster",
		CacheConfig: &datamodel.CacheConfig{
			WritebackEnabled: func(b bool) *bool { return &b }(true),
			CachePrePopulate: &datamodel.CachePrePopulate{
				Recursion: func(b bool) *bool { return &b }(true),
			},
		},
	}

	cloned := convertCacheParameters(original)

	// Verify that the cloned object is equal to the original
	assert.Equal(t, original.CacheConfig.WritebackEnabled, cloned.CacheConfig.WritebackEnabled)
}

func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_UpdateJobStatusProcessingError() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)

	// Mock UpdateJobStatus to return error for PROCESSING state
	expectedError := errors.New("failed to update job status to PROCESSING")
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.MatchedBy(func(job *datamodel.Job) bool {
		return job.State == string(datamodel.JobsStatePROCESSING)
	})).Return(expectedError)

	// Execute workflow
	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{
			Password:      "password",
			SecretID:      "",
			CertificateID: "",
		}},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		SizeInBytes: 1000,
	}
	params := &common.UpdateVolumeParams{
		QuotaInBytes: 2000,
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	// Assert workflow failed
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
	assert.Contains(s.T(), s.env.GetWorkflowError().Error(), expectedError.Error())
}

func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_UpdateJobStatusDoneError() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateLun)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInDB)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 1000,
		Size:           1000,
		State:          "online",
	}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UpdateLun, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.LunResponse{ProviderResponse: vsa.ProviderResponse{Name: "/vol/vol1/lun1"}}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInDB, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Mock UpdateJobStatus to succeed for PROCESSING state
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.MatchedBy(func(job *datamodel.Job) bool {
		return job.State == string(datamodel.JobsStatePROCESSING)
	})).Return(nil)

	// Mock UpdateJobStatus to fail for DONE state (successful completion)
	expectedError := errors.New("failed to update job status to DONE")
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.MatchedBy(func(job *datamodel.Job) bool {
		return job.State == string(datamodel.JobsStateDONE) && job.ErrorDetails == ""
	})).Return(expectedError)

	// Execute workflow
	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{
			Password:      "password",
			SecretID:      "",
			CertificateID: "",
		}},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		SizeInBytes: 1000,
		VolumeAttributes: &datamodel.VolumeAttributes{
			IsDataProtection: false,
		},
	}
	params := &common.UpdateVolumeParams{
		QuotaInBytes: 2000,
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	// Assert workflow failed
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
	assert.Contains(s.T(), s.env.GetWorkflowError().Error(), expectedError.Error())
}

func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_UpdateJobStatusErrorDetailsError() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)

	// Mock activities
	updateVolError := errors.New("failed to get hosts from ONTAP")
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 1000,
		Size:           1000,
		State:          "online",
	}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(updateVolError)

	// Mock UpdateJobStatus to succeed for PROCESSING state
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.MatchedBy(func(job *datamodel.Job) bool {
		return job.State == string(datamodel.JobsStatePROCESSING)
	})).Return(nil)

	// Mock UpdateJobStatus to fail for DONE state with error details
	errorDetailsUpdateError := errors.New("failed to update job status with error details")
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.MatchedBy(func(job *datamodel.Job) bool {
		return job.State == string(datamodel.JobsStateERROR) && job.ErrorDetails != ""
	})).Return(errorDetailsUpdateError)

	// Execute workflow
	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{
			Password:      "password",
			SecretID:      "",
			CertificateID: "",
		}},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		SizeInBytes: 1000,
	}
	params := &common.UpdateVolumeParams{
		QuotaInBytes: 2000,
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	// Assert workflow failed
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
	assert.Contains(s.T(), s.env.GetWorkflowError().Error(), errorDetailsUpdateError.Error())
}

func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_LunUpdateWithSnapReserve() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateLun)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInDB)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)

	// First call to GetVolumeFromONTAP (before volume update)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 1000,
		Size:           1000,
		State:          "online",
	}, nil).Once()

	// Second call to GetVolumeFromONTAP (before LUN update) - this should return a larger size
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 2000,
		Size:           2000, // Updated volume size
		State:          "online",
	}, nil).Once()

	s.env.OnActivity(updateActivity.UpdateVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Mock UpdateLun activity - this should be called with the calculated LUN size
	// Expected calculation: 2000 - (2000 * 20 / 100) = 2000 - 400 = 1600
	expectedLunSize := int64(1600)
	s.env.OnActivity(updateActivity.UpdateLun, mock.Anything, mock.Anything, expectedLunSize, mock.Anything).Return(&vsa.LunResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "lun-uuid",
			Name:         "/vol/vol1/lun1",
		},
		Size: expectedLunSize,
	}, nil)

	s.env.OnActivity(updateActivity.UpdateVolumeInDB, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Execute workflow with both QuotaInBytes and SnapReserve
	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{
			Password: "test_pass",
		}},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		SizeInBytes: 1000,
		VolumeAttributes: &datamodel.VolumeAttributes{
			SnapReserve: 10, // Current snap reserve
		},
	}

	snapReserve := int64(20) // New snap reserve
	params := &common.UpdateVolumeParams{
		QuotaInBytes: 2000,         // Increase volume size
		SnapReserve:  &snapReserve, // Change snap reserve
	}

	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	// Assert workflow completed successfully
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
	mockStorage.AssertNumberOfCalls(s.T(), "UpdateJob", 2)
}

func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_UpdateLunReturnedError() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateLun)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInDB)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 1000,
		Size:           1000,
		State:          "online",
	}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UpdateLun, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("some error"))
	s.env.OnActivity(updateActivity.UpdateVolumeInDB, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Execute workflow
	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{
			Password:      "password",
			SecretID:      "",
			CertificateID: "",
		}},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		SizeInBytes: 1000,
		VolumeAttributes: &datamodel.VolumeAttributes{
			IsDataProtection: false,
			Protocols:        []string{"ISCSI"},
		},
	}
	params := &common.UpdateVolumeParams{
		QuotaInBytes: 2000,
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	// Assert workflow completed successfully
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NotNil(s.T(), s.env.GetWorkflowError())
	mockStorage.AssertNumberOfCalls(s.T(), "UpdateJob", 2)
}

// Unit tests for standalone functions

func TestIsExportPolicyRulesUpdateRequired(t *testing.T) {
	t.Run("WhenCurrentPolicyNilAndUpdatePolicyNotNil_ShouldReturnTrue", func(t *testing.T) {
		updatePolicy := &models.ExportPolicy{
			ExportPolicyName: "test-policy",
			ExportRules:      []*models.ExportRule{},
		}
		result := isExportPolicyRulesUpdateRequired(nil, updatePolicy)
		assert.True(t, result)
	})

	t.Run("WhenCurrentPolicyNotNilWithZeroRuleAndUpdatePolicyNil_ShouldReturnTrue", func(t *testing.T) {
		currentPolicy := &datamodel.ExportPolicy{
			ExportPolicyName: "test-policy",
			ExportRules:      nil,
		}
		result := isExportPolicyRulesUpdateRequired(currentPolicy, nil)
		assert.False(t, result)
	})

	t.Run("WhenCurrentPolicyNotNilAndUpdatePolicyNil_ShouldReturnTrue", func(t *testing.T) {
		currentPolicy := &datamodel.ExportPolicy{
			ExportPolicyName: "test-policy",
			ExportRules:      []*datamodel.ExportRule{{UnixReadOnly: true}},
		}
		result := isExportPolicyRulesUpdateRequired(currentPolicy, nil)
		assert.True(t, result)
	})

	t.Run("WhenBothPoliciesNil_ShouldReturnFalse", func(t *testing.T) {
		result := isExportPolicyRulesUpdateRequired(nil, nil)
		assert.False(t, result)
	})

	t.Run("WhenPolicyNamesAreDifferentAndUpdatePolicyNameIsNotEmpty_ShouldReturnFalse", func(t *testing.T) {
		currentPolicy := &datamodel.ExportPolicy{
			ExportPolicyName: "current-policy",
			ExportRules:      []*datamodel.ExportRule{},
		}
		updatePolicy := &models.ExportPolicy{
			ExportPolicyName: "different-policy",
			ExportRules:      []*models.ExportRule{},
		}
		result := isExportPolicyRulesUpdateRequired(currentPolicy, updatePolicy)
		assert.False(t, result)
	})

	t.Run("WhenUpdatePolicyNameIsEmpty_ShouldProceedWithRuleComparison", func(t *testing.T) {
		currentPolicy := &datamodel.ExportPolicy{
			ExportPolicyName: "current-policy",
			ExportRules:      []*datamodel.ExportRule{},
		}
		updatePolicy := &models.ExportPolicy{
			ExportPolicyName: "",
			ExportRules:      []*models.ExportRule{},
		}
		result := isExportPolicyRulesUpdateRequired(currentPolicy, updatePolicy)
		assert.False(t, result)
	})

	t.Run("WhenRuleCountsDiffer_ShouldReturnTrue", func(t *testing.T) {
		currentPolicy := &datamodel.ExportPolicy{
			ExportPolicyName: "test-policy",
			ExportRules: []*datamodel.ExportRule{
				{AllowedClients: "192.168.1.0/24"},
			},
		}
		updatePolicy := &models.ExportPolicy{
			ExportPolicyName: "test-policy",
			ExportRules:      []*models.ExportRule{},
		}
		result := isExportPolicyRulesUpdateRequired(currentPolicy, updatePolicy)
		assert.True(t, result)
	})

	t.Run("WhenRulesAreIdentical_ShouldReturnFalse", func(t *testing.T) {
		currentPolicy := &datamodel.ExportPolicy{
			ExportPolicyName: "test-policy",
			ExportRules: []*datamodel.ExportRule{
				{
					AllowedClients:      "192.168.1.0/24",
					AnonymousUser:       "nobody",
					AccessType:          "rw",
					CIFS:                false,
					NFSv3:               true,
					NFSv4:               false,
					S3:                  false,
					UnixReadOnly:        false,
					UnixReadWrite:       true,
					Kerberos5ReadOnly:   false,
					Kerberos5ReadWrite:  false,
					Kerberos5iReadOnly:  false,
					Kerberos5iReadWrite: false,
					Kerberos5pReadOnly:  false,
					Kerberos5pReadWrite: false,
					Superuser:           true,
				},
			},
		}
		updatePolicy := &models.ExportPolicy{
			ExportPolicyName: "test-policy",
			ExportRules: []*models.ExportRule{
				{
					AllowedClients:      "192.168.1.0/24",
					AnonymousUser:       "nobody",
					AccessType:          "rw",
					CIFS:                false,
					NFSv3:               true,
					NFSv4:               false,
					S3:                  false,
					UnixReadOnly:        false,
					UnixReadWrite:       true,
					Kerberos5ReadOnly:   false,
					Kerberos5ReadWrite:  false,
					Kerberos5iReadOnly:  false,
					Kerberos5iReadWrite: false,
					Kerberos5pReadOnly:  false,
					Kerberos5pReadWrite: false,
					Superuser:           true,
				},
			},
		}
		result := isExportPolicyRulesUpdateRequired(currentPolicy, updatePolicy)
		assert.False(t, result)
	})

	t.Run("WhenUpdateRuleNotFoundInCurrent_ShouldReturnTrue", func(t *testing.T) {
		currentPolicy := &datamodel.ExportPolicy{
			ExportPolicyName: "test-policy",
			ExportRules: []*datamodel.ExportRule{
				{AllowedClients: "192.168.1.0/24", NFSv3: true},
			},
		}
		updatePolicy := &models.ExportPolicy{
			ExportPolicyName: "test-policy",
			ExportRules: []*models.ExportRule{
				{AllowedClients: "192.168.2.0/24", NFSv3: true}, // Different client
			},
		}
		result := isExportPolicyRulesUpdateRequired(currentPolicy, updatePolicy)
		assert.True(t, result)
	})

	t.Run("WhenCurrentRuleNotFoundInUpdate_ShouldReturnTrue", func(t *testing.T) {
		currentPolicy := &datamodel.ExportPolicy{
			ExportPolicyName: "test-policy",
			ExportRules: []*datamodel.ExportRule{
				{AllowedClients: "192.168.1.0/24", NFSv3: true},
				{AllowedClients: "192.168.2.0/24", NFSv3: true},
			},
		}
		updatePolicy := &models.ExportPolicy{
			ExportPolicyName: "test-policy",
			ExportRules: []*models.ExportRule{
				{AllowedClients: "192.168.1.0/24", NFSv3: true}, // Missing second rule
			},
		}
		result := isExportPolicyRulesUpdateRequired(currentPolicy, updatePolicy)
		assert.True(t, result)
	})

	t.Run("WhenOnlyAllSquashOrAnonUidDiffers_ShouldReturnTrue", func(t *testing.T) {
		// Current: hasRootAccess (Superuser true), no allSquash/anonUid
		currentPolicy := &datamodel.ExportPolicy{
			ExportPolicyName: "test-policy",
			ExportRules: []*datamodel.ExportRule{
				{AllowedClients: "0.0.0.0/0", AccessType: "rw", NFSv3: true, Superuser: true},
			},
		}
		// Update: user switches to allSquash true, anonUid 0 (root squash)
		allSquashTrue := true
		anonUid0 := int64(0)
		updatePolicy := &models.ExportPolicy{
			ExportPolicyName: "test-policy",
			ExportRules: []*models.ExportRule{
				{AllowedClients: "0.0.0.0/0", AccessType: "rw", NFSv3: true, Superuser: false, AllSquash: &allSquashTrue, AnonUid: &anonUid0},
			},
		}
		result := isExportPolicyRulesUpdateRequired(currentPolicy, updatePolicy)
		assert.True(t, result, "update should be required when only AllSquash/AnonUid change so ONTAP update is not skipped")
	})
}

func TestRulesEqual(t *testing.T) {
	t.Run("WhenAllFieldsAreEqual_ShouldReturnTrue", func(t *testing.T) {
		currentRule := &datamodel.ExportRule{
			AllowedClients:      "192.168.1.0/24",
			AnonymousUser:       "nobody",
			AccessType:          "rw",
			CIFS:                false,
			NFSv3:               true,
			NFSv4:               false,
			S3:                  false,
			UnixReadOnly:        false,
			UnixReadWrite:       true,
			Kerberos5ReadOnly:   false,
			Kerberos5ReadWrite:  false,
			Kerberos5iReadOnly:  false,
			Kerberos5iReadWrite: false,
			Kerberos5pReadOnly:  false,
			Kerberos5pReadWrite: false,
			Superuser:           true,
		}
		updateRule := &models.ExportRule{
			AllowedClients:      "192.168.1.0/24",
			AnonymousUser:       "nobody",
			AccessType:          "rw",
			CIFS:                false,
			NFSv3:               true,
			NFSv4:               false,
			S3:                  false,
			UnixReadOnly:        false,
			UnixReadWrite:       true,
			Kerberos5ReadOnly:   false,
			Kerberos5ReadWrite:  false,
			Kerberos5iReadOnly:  false,
			Kerberos5iReadWrite: false,
			Kerberos5pReadOnly:  false,
			Kerberos5pReadWrite: false,
			Superuser:           true,
		}
		result := rulesEqual(currentRule, updateRule)
		assert.True(t, result)
	})

	t.Run("WhenAllowedClientsDiffer_ShouldReturnFalse", func(t *testing.T) {
		currentRule := &datamodel.ExportRule{AllowedClients: "192.168.1.0/24"}
		updateRule := &models.ExportRule{AllowedClients: "192.168.2.0/24"}
		result := rulesEqual(currentRule, updateRule)
		assert.False(t, result)
	})

	t.Run("WhenAnonymousUserDiffers_ShouldReturnFalse", func(t *testing.T) {
		currentRule := &datamodel.ExportRule{AnonymousUser: "nobody"}
		updateRule := &models.ExportRule{AnonymousUser: "root"}
		result := rulesEqual(currentRule, updateRule)
		assert.False(t, result)
	})

	t.Run("WhenAccessTypeDiffers_ShouldReturnFalse", func(t *testing.T) {
		currentRule := &datamodel.ExportRule{AccessType: "rw"}
		updateRule := &models.ExportRule{AccessType: "ro"}
		result := rulesEqual(currentRule, updateRule)
		assert.False(t, result)
	})

	t.Run("WhenCIFSFlagDiffers_ShouldReturnFalse", func(t *testing.T) {
		currentRule := &datamodel.ExportRule{CIFS: true}
		updateRule := &models.ExportRule{CIFS: false}
		result := rulesEqual(currentRule, updateRule)
		assert.False(t, result)
	})

	t.Run("WhenNFSv3FlagDiffers_ShouldReturnFalse", func(t *testing.T) {
		currentRule := &datamodel.ExportRule{NFSv3: true}
		updateRule := &models.ExportRule{NFSv3: false}
		result := rulesEqual(currentRule, updateRule)
		assert.False(t, result)
	})

	t.Run("WhenNFSv4FlagDiffers_ShouldReturnFalse", func(t *testing.T) {
		currentRule := &datamodel.ExportRule{NFSv4: true}
		updateRule := &models.ExportRule{NFSv4: false}
		result := rulesEqual(currentRule, updateRule)
		assert.False(t, result)
	})

	t.Run("WhenS3FlagDiffers_ShouldReturnFalse", func(t *testing.T) {
		currentRule := &datamodel.ExportRule{S3: true}
		updateRule := &models.ExportRule{S3: false}
		result := rulesEqual(currentRule, updateRule)
		assert.False(t, result)
	})

	t.Run("WhenUnixReadOnlyFlagDiffers_ShouldReturnFalse", func(t *testing.T) {
		currentRule := &datamodel.ExportRule{UnixReadOnly: true}
		updateRule := &models.ExportRule{UnixReadOnly: false}
		result := rulesEqual(currentRule, updateRule)
		assert.False(t, result)
	})

	t.Run("WhenUnixReadWriteFlagDiffers_ShouldReturnFalse", func(t *testing.T) {
		currentRule := &datamodel.ExportRule{UnixReadWrite: true}
		updateRule := &models.ExportRule{UnixReadWrite: false}
		result := rulesEqual(currentRule, updateRule)
		assert.False(t, result)
	})

	t.Run("WhenKerberos5ReadOnlyFlagDiffers_ShouldReturnFalse", func(t *testing.T) {
		currentRule := &datamodel.ExportRule{Kerberos5ReadOnly: true}
		updateRule := &models.ExportRule{Kerberos5ReadOnly: false}
		result := rulesEqual(currentRule, updateRule)
		assert.False(t, result)
	})

	t.Run("WhenKerberos5ReadWriteFlagDiffers_ShouldReturnFalse", func(t *testing.T) {
		currentRule := &datamodel.ExportRule{Kerberos5ReadWrite: true}
		updateRule := &models.ExportRule{Kerberos5ReadWrite: false}
		result := rulesEqual(currentRule, updateRule)
		assert.False(t, result)
	})

	t.Run("WhenKerberos5iReadOnlyFlagDiffers_ShouldReturnFalse", func(t *testing.T) {
		currentRule := &datamodel.ExportRule{Kerberos5iReadOnly: true}
		updateRule := &models.ExportRule{Kerberos5iReadOnly: false}
		result := rulesEqual(currentRule, updateRule)
		assert.False(t, result)
	})

	t.Run("WhenKerberos5iReadWriteFlagDiffers_ShouldReturnFalse", func(t *testing.T) {
		currentRule := &datamodel.ExportRule{Kerberos5iReadWrite: true}
		updateRule := &models.ExportRule{Kerberos5iReadWrite: false}
		result := rulesEqual(currentRule, updateRule)
		assert.False(t, result)
	})

	t.Run("WhenKerberos5pReadOnlyFlagDiffers_ShouldReturnFalse", func(t *testing.T) {
		currentRule := &datamodel.ExportRule{Kerberos5pReadOnly: true}
		updateRule := &models.ExportRule{Kerberos5pReadOnly: false}
		result := rulesEqual(currentRule, updateRule)
		assert.False(t, result)
	})

	t.Run("WhenKerberos5pReadWriteFlagDiffers_ShouldReturnFalse", func(t *testing.T) {
		currentRule := &datamodel.ExportRule{Kerberos5pReadWrite: true}
		updateRule := &models.ExportRule{Kerberos5pReadWrite: false}
		result := rulesEqual(currentRule, updateRule)
		assert.False(t, result)
	})

	t.Run("WhenSuperuserFlagDiffers_ShouldReturnFalse", func(t *testing.T) {
		currentRule := &datamodel.ExportRule{Superuser: true}
		updateRule := &models.ExportRule{Superuser: false}
		result := rulesEqual(currentRule, updateRule)
		assert.False(t, result)
	})

	t.Run("WhenAllSquashDiffers_ShouldReturnFalse", func(t *testing.T) {
		allSquashTrue := true
		allSquashFalse := false
		currentRule := &datamodel.ExportRule{AllowedClients: "0.0.0.0/0", AllSquash: &allSquashTrue}
		updateRule := &models.ExportRule{AllowedClients: "0.0.0.0/0", AllSquash: &allSquashFalse}
		result := rulesEqual(currentRule, updateRule)
		assert.False(t, result)
	})

	t.Run("WhenAllSquashNilVsSet_ShouldReturnFalse", func(t *testing.T) {
		allSquashTrue := true
		currentRule := &datamodel.ExportRule{AllowedClients: "0.0.0.0/0"}
		updateRule := &models.ExportRule{AllowedClients: "0.0.0.0/0", AllSquash: &allSquashTrue}
		result := rulesEqual(currentRule, updateRule)
		assert.False(t, result)
	})

	t.Run("WhenAnonUidDiffers_ShouldReturnFalse", func(t *testing.T) {
		anonUid0 := int64(0)
		anonUid65534 := int64(65534)
		currentRule := &datamodel.ExportRule{AllowedClients: "0.0.0.0/0", AnonUid: &anonUid0}
		updateRule := &models.ExportRule{AllowedClients: "0.0.0.0/0", AnonUid: &anonUid65534}
		result := rulesEqual(currentRule, updateRule)
		assert.False(t, result)
	})

	t.Run("WhenAnonUidNilVsSet_ShouldReturnFalse", func(t *testing.T) {
		anonUid0 := int64(0)
		currentRule := &datamodel.ExportRule{AllowedClients: "0.0.0.0/0"}
		updateRule := &models.ExportRule{AllowedClients: "0.0.0.0/0", AnonUid: &anonUid0}
		result := rulesEqual(currentRule, updateRule)
		assert.False(t, result)
	})

	t.Run("WhenAllSquashAndAnonUidMatch_ShouldReturnTrue", func(t *testing.T) {
		allSquashTrue := true
		anonUid0 := int64(0)
		currentRule := &datamodel.ExportRule{AllowedClients: "0.0.0.0/0", AllSquash: &allSquashTrue, AnonUid: &anonUid0}
		updateRule := &models.ExportRule{AllowedClients: "0.0.0.0/0", AllSquash: &allSquashTrue, AnonUid: &anonUid0}
		result := rulesEqual(currentRule, updateRule)
		assert.True(t, result)
	})

	t.Run("WhenAllSquashSetVsNilInUpdate_ShouldReturnFalse", func(t *testing.T) {
		allSquashFalse := false
		currentRule := &datamodel.ExportRule{AllowedClients: "0.0.0.0/0", AllSquash: &allSquashFalse}
		updateRule := &models.ExportRule{AllowedClients: "0.0.0.0/0"} // request omitted AllSquash
		result := rulesEqual(currentRule, updateRule)
		assert.False(t, result, "nil vs set is different full state; update required")
	})

	t.Run("WhenAnonUidSetVsNilInUpdate_ShouldReturnFalse", func(t *testing.T) {
		anonUid0 := int64(0)
		currentRule := &datamodel.ExportRule{AllowedClients: "0.0.0.0/0", AnonUid: &anonUid0}
		updateRule := &models.ExportRule{AllowedClients: "0.0.0.0/0"} // request omitted AnonUid
		result := rulesEqual(currentRule, updateRule)
		assert.False(t, result, "nil vs set is different full state; update required")
	})
}

func TestGetUpdatedExportPolicy(t *testing.T) {
	t.Run("WhenUpdatePolicyIsNil_ShouldReturnNil", func(t *testing.T) {
		result := getUpdatedExportPolicy(nil)
		assert.Nil(t, result)
	})

	t.Run("WhenUpdatePolicyIsEmpty_ShouldReturnEmptyDatamodelPolicy", func(t *testing.T) {
		updatePolicy := &models.ExportPolicy{
			ExportPolicyName: "test-policy",
			ExportRules:      []*models.ExportRule{},
		}
		result := getUpdatedExportPolicy(updatePolicy)

		assert.NotNil(t, result)
		assert.Equal(t, "test-policy", result.ExportPolicyName)
		assert.Equal(t, 0, len(result.ExportRules))
	})

	t.Run("WhenUpdatePolicyHasSingleRule_ShouldConvertCorrectly", func(t *testing.T) {
		updatePolicy := &models.ExportPolicy{
			ExportPolicyName: "test-policy",
			ExportRules: []*models.ExportRule{
				{
					AllowedClients:      "192.168.1.0/24",
					AnonymousUser:       "nobody",
					Index:               1,
					ChownMode:           "restricted",
					AccessType:          "rw",
					CIFS:                false,
					NFSv3:               true,
					NFSv4:               false,
					S3:                  false,
					UnixReadOnly:        false,
					UnixReadWrite:       true,
					Kerberos5ReadOnly:   false,
					Kerberos5ReadWrite:  false,
					Kerberos5iReadOnly:  false,
					Kerberos5iReadWrite: false,
					Kerberos5pReadOnly:  false,
					Kerberos5pReadWrite: false,
					Superuser:           true,
				},
			},
		}
		result := getUpdatedExportPolicy(updatePolicy)

		assert.NotNil(t, result)
		assert.Equal(t, "test-policy", result.ExportPolicyName)
		assert.Equal(t, 1, len(result.ExportRules))

		rule := result.ExportRules[0]
		assert.Equal(t, "192.168.1.0/24", rule.AllowedClients)
		assert.Equal(t, "nobody", rule.AnonymousUser)
		assert.Equal(t, 1, rule.Index)
		assert.Equal(t, "restricted", rule.ChownMode)
		assert.Equal(t, "rw", rule.AccessType)
		assert.False(t, rule.CIFS)
		assert.True(t, rule.NFSv3)
		assert.False(t, rule.NFSv4)
		assert.False(t, rule.S3)
		assert.False(t, rule.UnixReadOnly)
		assert.True(t, rule.UnixReadWrite)
		assert.False(t, rule.Kerberos5ReadOnly)
		assert.False(t, rule.Kerberos5ReadWrite)
		assert.False(t, rule.Kerberos5iReadOnly)
		assert.False(t, rule.Kerberos5iReadWrite)
		assert.False(t, rule.Kerberos5pReadOnly)
		assert.False(t, rule.Kerberos5pReadWrite)
		assert.True(t, rule.Superuser)
	})

	t.Run("WhenUpdatePolicyHasMultipleRules_ShouldConvertAllRules", func(t *testing.T) {
		updatePolicy := &models.ExportPolicy{
			ExportPolicyName: "multi-rule-policy",
			ExportRules: []*models.ExportRule{
				{
					AllowedClients: "192.168.1.0/24",
					NFSv3:          true,
					AccessType:     "rw",
					Index:          1,
				},
				{
					AllowedClients: "10.0.0.0/8",
					NFSv4:          true,
					AccessType:     "ro",
					Index:          2,
				},
			},
		}
		result := getUpdatedExportPolicy(updatePolicy)

		assert.NotNil(t, result)
		assert.Equal(t, "multi-rule-policy", result.ExportPolicyName)
		assert.Equal(t, 2, len(result.ExportRules))

		rule1 := result.ExportRules[0]
		assert.Equal(t, "192.168.1.0/24", rule1.AllowedClients)
		assert.True(t, rule1.NFSv3)
		assert.Equal(t, "rw", rule1.AccessType)
		assert.Equal(t, 1, rule1.Index)

		rule2 := result.ExportRules[1]
		assert.Equal(t, "10.0.0.0/8", rule2.AllowedClients)
		assert.True(t, rule2.NFSv4)
		assert.Equal(t, "ro", rule2.AccessType)
		assert.Equal(t, 2, rule2.Index)
	})

	t.Run("WhenUpdatePolicyHasAllProtocolFlags_ShouldConvertCorrectly", func(t *testing.T) {
		updatePolicy := &models.ExportPolicy{
			ExportPolicyName: "all-protocols-policy",
			ExportRules: []*models.ExportRule{
				{
					AllowedClients:      "0.0.0.0/0",
					CIFS:                true,
					NFSv3:               true,
					NFSv4:               true,
					S3:                  true,
					UnixReadOnly:        true,
					UnixReadWrite:       true,
					Kerberos5ReadOnly:   true,
					Kerberos5ReadWrite:  true,
					Kerberos5iReadOnly:  true,
					Kerberos5iReadWrite: true,
					Kerberos5pReadOnly:  true,
					Kerberos5pReadWrite: true,
					Superuser:           true,
				},
			},
		}
		result := getUpdatedExportPolicy(updatePolicy)

		assert.NotNil(t, result)
		assert.Equal(t, 1, len(result.ExportRules))

		rule := result.ExportRules[0]
		assert.True(t, rule.CIFS)
		assert.True(t, rule.NFSv3)
		assert.True(t, rule.NFSv4)
		assert.True(t, rule.S3)
		assert.True(t, rule.UnixReadOnly)
		assert.True(t, rule.UnixReadWrite)
		assert.True(t, rule.Kerberos5ReadOnly)
		assert.True(t, rule.Kerberos5ReadWrite)
		assert.True(t, rule.Kerberos5iReadOnly)
		assert.True(t, rule.Kerberos5iReadWrite)
		assert.True(t, rule.Kerberos5pReadOnly)
		assert.True(t, rule.Kerberos5pReadWrite)
		assert.True(t, rule.Superuser)
	})

	t.Run("WhenUpdatePolicyHasAllSquashAndAnonUid_ShouldPreserveThem", func(t *testing.T) {
		allSquashVal := true
		anonUidVal := int64(0)
		updatePolicy := &models.ExportPolicy{
			ExportPolicyName: "test-policy",
			ExportRules: []*models.ExportRule{
				{
					AllowedClients: "0.0.0.0/0",
					AccessType:     "rw",
					NFSv3:          true,
					Index:          1,
					AllSquash:      &allSquashVal,
					AnonUid:        &anonUidVal,
				},
			},
		}
		result := getUpdatedExportPolicy(updatePolicy)

		assert.NotNil(t, result)
		assert.Equal(t, 1, len(result.ExportRules))
		rule := result.ExportRules[0]
		assert.NotNil(t, rule.AllSquash, "AllSquash should be preserved")
		assert.True(t, *rule.AllSquash)
		assert.NotNil(t, rule.AnonUid, "AnonUid should be preserved")
		assert.Equal(t, int64(0), *rule.AnonUid)
	})
}

func TestIsUpdateFlexCacheRequired(t *testing.T) {
	boolPtr := func(b bool) *bool { return &b }
	int16Ptr := func(i int16) *int16 { return &i }

	t.Run("NoParams", func(t *testing.T) {
		assert.False(t, isUpdateFlexCacheRequired(&datamodel.Volume{}, nil))
	})

	t.Run("NoCacheParamsInRequest", func(t *testing.T) {
		assert.False(t, isUpdateFlexCacheRequired(&datamodel.Volume{}, &common.UpdateVolumeParams{}))
	})

	t.Run("NoCacheConfigInRequest", func(t *testing.T) {
		assert.False(t, isUpdateFlexCacheRequired(&datamodel.Volume{}, &common.UpdateVolumeParams{
			CacheParameters: &models.CacheParameters{},
		}))
	})

	t.Run("ExistingVolumeNotFlexCache", func(t *testing.T) {
		v := &datamodel.Volume{} // no CacheParameters
		params := &common.UpdateVolumeParams{
			CacheParameters: &models.CacheParameters{
				CacheConfig: &models.CacheConfig{WritebackEnabled: boolPtr(true)},
			},
		}
		assert.False(t, isUpdateFlexCacheRequired(v, params))
	})

	t.Run("AddCacheConfigToExistingFlexCacheParams", func(t *testing.T) {
		v := &datamodel.Volume{
			CacheParameters: &datamodel.CacheParameters{},
		}
		params := &common.UpdateVolumeParams{
			CacheParameters: &models.CacheParameters{
				CacheConfig: &models.CacheConfig{WritebackEnabled: boolPtr(true)},
			},
		}
		assert.True(t, isUpdateFlexCacheRequired(v, params))
	})

	baseExisting := &datamodel.Volume{
		CacheParameters: &datamodel.CacheParameters{
			CacheConfig: &datamodel.CacheConfig{
				WritebackEnabled:        boolPtr(false),
				AtimeScrubEnabled:       boolPtr(true),
				AtimeScrubDays:          int16Ptr(7),
				CifsChangeNotifyEnabled: boolPtr(false),
				CachePrePopulate: &datamodel.CachePrePopulate{
					PathList:        []string{"a", "b"},
					ExcludePathList: []string{"x"},
					Recursion:       boolPtr(false),
				},
			},
		},
	}

	t.Run("NoChangeReturnsFalse", func(t *testing.T) {
		params := &common.UpdateVolumeParams{
			CacheParameters: &models.CacheParameters{
				CacheConfig: &models.CacheConfig{
					// nil pointers mean no intent to change
				},
			},
		}
		assert.False(t, isUpdateFlexCacheRequired(baseExisting, params))
	})

	t.Run("ChangeWritebackEnabled", func(t *testing.T) {
		params := &common.UpdateVolumeParams{
			CacheParameters: &models.CacheParameters{
				CacheConfig: &models.CacheConfig{
					WritebackEnabled: boolPtr(true), // existing false
				},
			},
		}
		assert.True(t, isUpdateFlexCacheRequired(baseExisting, params))
	})

	t.Run("ChangeAtimeScrubDays", func(t *testing.T) {
		params := &common.UpdateVolumeParams{
			CacheParameters: &models.CacheParameters{
				CacheConfig: &models.CacheConfig{
					AtimeScrubDays: int16Ptr(10), // existing 7
				},
			},
		}
		assert.True(t, isUpdateFlexCacheRequired(baseExisting, params))
	})

	t.Run("PrePopulateNil_NoChange", func(t *testing.T) {
		params := &common.UpdateVolumeParams{
			CacheParameters: &models.CacheParameters{
				CacheConfig: &models.CacheConfig{},
			},
		}
		assert.False(t, isUpdateFlexCacheRequired(baseExisting, params))
	})

	t.Run("PrePopulatePathListOrderIgnored_NoChange", func(t *testing.T) {
		params := &common.UpdateVolumeParams{
			CacheParameters: &models.CacheParameters{
				CacheConfig: &models.CacheConfig{
					CachePrePopulate: &models.CachePrePopulate{
						PathList: []string{"b", "a"}, // same set
					},
				},
			},
		}
		assert.False(t, isUpdateFlexCacheRequired(baseExisting, params))
	})

	t.Run("PrePopulateDuplicatesIgnored_NoChange", func(t *testing.T) {
		// Duplicates collapse; should appear unchanged
		params := &common.UpdateVolumeParams{
			CacheParameters: &models.CacheParameters{
				CacheConfig: &models.CacheConfig{
					CachePrePopulate: &models.CachePrePopulate{
						PathList: []string{"a", "b", "a", "b"},
					},
				},
			},
		}
		assert.False(t, isUpdateFlexCacheRequired(baseExisting, params))
	})
}

func TestSanitizeUpdateParamsForFlexCache(t *testing.T) {
	t.Run("SkipForNormalVolume", func(t *testing.T) {
		snapshotDirectoryAccess := true
		snapReserve := int64(25)
		params := &common.UpdateVolumeParams{
			SnapshotDirectoryAccess: &snapshotDirectoryAccess,
			SnapReserve:             &snapReserve,
		}
		volume := &datamodel.Volume{}

		sanitizeUpdateParamsForFlexCache(params, volume)

		assert.NotNil(t, params.SnapshotDirectoryAccess)
		assert.NotNil(t, params.SnapReserve)
		assert.True(t, *params.SnapshotDirectoryAccess)
		assert.Equal(t, int64(25), *params.SnapReserve)
	})

	t.Run("ApplyForFlexCacheVolume", func(t *testing.T) {
		snapshotDirectoryAccess := true
		snapReserve := int64(25)
		params := &common.UpdateVolumeParams{
			SnapshotDirectoryAccess: &snapshotDirectoryAccess,
			SnapReserve:             &snapReserve,
		}
		volume := &datamodel.Volume{
			CacheParameters: &datamodel.CacheParameters{},
		}

		sanitizeUpdateParamsForFlexCache(params, volume)

		assert.Nil(t, params.SnapshotDirectoryAccess)
		assert.Nil(t, params.SnapReserve)
	})
}

func Test_isUpdateFlexCachePrepopulateRequired(t *testing.T) {
	tests := []struct {
		name           string
		existingVolume *datamodel.Volume
		params         *common.UpdateVolumeParams
		want           bool
	}{
		{
			name:           "flexCache disabled globally",
			existingVolume: &datamodel.Volume{},
			params:         &common.UpdateVolumeParams{},
			want:           false,
		},
		{
			name:           "nil params",
			existingVolume: &datamodel.Volume{},
			params:         nil,
			want:           false,
		},
		{
			name:           "nil CacheParameters in params",
			existingVolume: &datamodel.Volume{},
			params: &common.UpdateVolumeParams{
				CacheParameters: nil,
			},
			want: false,
		},
		{
			name:           "nil CacheConfig in params",
			existingVolume: &datamodel.Volume{},
			params: &common.UpdateVolumeParams{
				CacheParameters: &models.CacheParameters{
					CacheConfig: nil,
				},
			},
			want: false,
		},
		{
			name:           "nil existingVolume",
			existingVolume: nil,
			params: &common.UpdateVolumeParams{
				CacheParameters: &models.CacheParameters{
					CacheConfig: &models.CacheConfig{},
				},
			},
			want: false,
		},
		{
			name: "nil CacheParameters in existingVolume",
			existingVolume: &datamodel.Volume{
				CacheParameters: nil,
			},
			params: &common.UpdateVolumeParams{
				CacheParameters: &models.CacheParameters{
					CacheConfig: &models.CacheConfig{},
				},
			},
			want: false,
		},
		{
			name: "adding CachePrePopulate when existing CacheConfig is nil",
			existingVolume: &datamodel.Volume{
				CacheParameters: &datamodel.CacheParameters{
					CacheConfig: nil,
				},
			},
			params: &common.UpdateVolumeParams{
				CacheParameters: &models.CacheParameters{
					CacheConfig: &models.CacheConfig{
						CachePrePopulate: &models.CachePrePopulate{
							PathList: []string{"/path1"},
						},
					},
				},
			},
			want: true,
		},
		{
			name: "adding CachePrePopulate when existing PrePopulate is nil",
			existingVolume: &datamodel.Volume{
				CacheParameters: &datamodel.CacheParameters{
					CacheConfig: &datamodel.CacheConfig{
						CachePrePopulate: nil,
					},
				},
			},
			params: &common.UpdateVolumeParams{
				CacheParameters: &models.CacheParameters{
					CacheConfig: &models.CacheConfig{
						CachePrePopulate: &models.CachePrePopulate{
							PathList: []string{"/path1"},
						},
					},
				},
			},
			want: true,
		},
		{
			name: "nil CachePrePopulate in params - no change required",
			existingVolume: &datamodel.Volume{
				CacheParameters: &datamodel.CacheParameters{
					CacheConfig: &datamodel.CacheConfig{
						CachePrePopulate: &datamodel.CachePrePopulate{
							PathList: []string{"/path1"},
						},
					},
				},
			},
			params: &common.UpdateVolumeParams{
				CacheParameters: &models.CacheParameters{
					CacheConfig: &models.CacheConfig{
						CachePrePopulate: nil,
					},
				},
			},
			want: false,
		},
		{
			name: "PathList changed - different paths",
			existingVolume: &datamodel.Volume{
				CacheParameters: &datamodel.CacheParameters{
					CacheConfig: &datamodel.CacheConfig{
						CachePrePopulate: &datamodel.CachePrePopulate{
							PathList: []string{"/path1", "/path2"},
						},
					},
				},
			},
			params: &common.UpdateVolumeParams{
				CacheParameters: &models.CacheParameters{
					CacheConfig: &models.CacheConfig{
						CachePrePopulate: &models.CachePrePopulate{
							PathList: []string{"/path1", "/path3"},
						},
					},
				},
			},
			want: true,
		},
		{
			name: "PathList changed - different count",
			existingVolume: &datamodel.Volume{
				CacheParameters: &datamodel.CacheParameters{
					CacheConfig: &datamodel.CacheConfig{
						CachePrePopulate: &datamodel.CachePrePopulate{
							PathList: []string{"/path1"},
						},
					},
				},
			},
			params: &common.UpdateVolumeParams{
				CacheParameters: &models.CacheParameters{
					CacheConfig: &models.CacheConfig{
						CachePrePopulate: &models.CachePrePopulate{
							PathList: []string{"/path1", "/path2"},
						},
					},
				},
			},
			want: true,
		},
		{
			name: "PathList unchanged - same paths, different order",
			existingVolume: &datamodel.Volume{
				CacheParameters: &datamodel.CacheParameters{
					CacheConfig: &datamodel.CacheConfig{
						CachePrePopulate: &datamodel.CachePrePopulate{
							PathList: []string{"/path1", "/path2"},
						},
					},
				},
			},
			params: &common.UpdateVolumeParams{
				CacheParameters: &models.CacheParameters{
					CacheConfig: &models.CacheConfig{
						CachePrePopulate: &models.CachePrePopulate{
							PathList: []string{"/path2", "/path1"},
						},
					},
				},
			},
			want: false,
		},
		{
			name: "PathList nil in params - no change",
			existingVolume: &datamodel.Volume{
				CacheParameters: &datamodel.CacheParameters{
					CacheConfig: &datamodel.CacheConfig{
						CachePrePopulate: &datamodel.CachePrePopulate{
							PathList: []string{"/path1"},
						},
					},
				},
			},
			params: &common.UpdateVolumeParams{
				CacheParameters: &models.CacheParameters{
					CacheConfig: &models.CacheConfig{
						CachePrePopulate: &models.CachePrePopulate{
							PathList: nil,
						},
					},
				},
			},
			want: false,
		},
		{
			name: "PathList empty in params, existing has paths - change required",
			existingVolume: &datamodel.Volume{
				CacheParameters: &datamodel.CacheParameters{
					CacheConfig: &datamodel.CacheConfig{
						CachePrePopulate: &datamodel.CachePrePopulate{
							PathList: []string{"/path1"},
						},
					},
				},
			},
			params: &common.UpdateVolumeParams{
				CacheParameters: &models.CacheParameters{
					CacheConfig: &models.CacheConfig{
						CachePrePopulate: &models.CachePrePopulate{
							PathList: []string{},
						},
					},
				},
			},
			want: true,
		},
		{
			name: "ExcludePathList changed",
			existingVolume: &datamodel.Volume{
				CacheParameters: &datamodel.CacheParameters{
					CacheConfig: &datamodel.CacheConfig{
						CachePrePopulate: &datamodel.CachePrePopulate{
							ExcludePathList: []string{"/exclude1"},
						},
					},
				},
			},
			params: &common.UpdateVolumeParams{
				CacheParameters: &models.CacheParameters{
					CacheConfig: &models.CacheConfig{
						CachePrePopulate: &models.CachePrePopulate{
							ExcludePathList: []string{"/exclude2"},
						},
					},
				},
			},
			want: true,
		},
		{
			name: "ExcludePathList unchanged",
			existingVolume: &datamodel.Volume{
				CacheParameters: &datamodel.CacheParameters{
					CacheConfig: &datamodel.CacheConfig{
						CachePrePopulate: &datamodel.CachePrePopulate{
							ExcludePathList: []string{"/exclude1", "/exclude2"},
						},
					},
				},
			},
			params: &common.UpdateVolumeParams{
				CacheParameters: &models.CacheParameters{
					CacheConfig: &models.CacheConfig{
						CachePrePopulate: &models.CachePrePopulate{
							ExcludePathList: []string{"/exclude2", "/exclude1"},
						},
					},
				},
			},
			want: false,
		},
		{
			name: "Recursion changed from false to true",
			existingVolume: &datamodel.Volume{
				CacheParameters: &datamodel.CacheParameters{
					CacheConfig: &datamodel.CacheConfig{
						CachePrePopulate: &datamodel.CachePrePopulate{
							Recursion: boolPtr(false),
						},
					},
				},
			},
			params: &common.UpdateVolumeParams{
				CacheParameters: &models.CacheParameters{
					CacheConfig: &models.CacheConfig{
						CachePrePopulate: &models.CachePrePopulate{
							Recursion: boolPtr(true),
						},
					},
				},
			},
			want: true,
		},
		{
			name: "Recursion unchanged",
			existingVolume: &datamodel.Volume{
				CacheParameters: &datamodel.CacheParameters{
					CacheConfig: &datamodel.CacheConfig{
						CachePrePopulate: &datamodel.CachePrePopulate{
							Recursion: boolPtr(true),
						},
					},
				},
			},
			params: &common.UpdateVolumeParams{
				CacheParameters: &models.CacheParameters{
					CacheConfig: &models.CacheConfig{
						CachePrePopulate: &models.CachePrePopulate{
							Recursion: boolPtr(true),
						},
					},
				},
			},
			want: false,
		},
		{
			name: "Recursion nil in params - no change",
			existingVolume: &datamodel.Volume{
				CacheParameters: &datamodel.CacheParameters{
					CacheConfig: &datamodel.CacheConfig{
						CachePrePopulate: &datamodel.CachePrePopulate{
							Recursion: boolPtr(true),
						},
					},
				},
			},
			params: &common.UpdateVolumeParams{
				CacheParameters: &models.CacheParameters{
					CacheConfig: &models.CacheConfig{
						CachePrePopulate: &models.CachePrePopulate{
							Recursion: nil,
						},
					},
				},
			},
			want: false,
		},
		{
			name: "Multiple fields changed",
			existingVolume: &datamodel.Volume{
				CacheParameters: &datamodel.CacheParameters{
					CacheConfig: &datamodel.CacheConfig{
						CachePrePopulate: &datamodel.CachePrePopulate{
							PathList:        []string{"/path1"},
							ExcludePathList: []string{"/exclude1"},
							Recursion:       boolPtr(false),
						},
					},
				},
			},
			params: &common.UpdateVolumeParams{
				CacheParameters: &models.CacheParameters{
					CacheConfig: &models.CacheConfig{
						CachePrePopulate: &models.CachePrePopulate{
							PathList:        []string{"/path2"},
							ExcludePathList: []string{"/exclude2"},
							Recursion:       boolPtr(true),
						},
					},
				},
			},
			want: true,
		},
		{
			name: "All fields unchanged",
			existingVolume: &datamodel.Volume{
				CacheParameters: &datamodel.CacheParameters{
					CacheConfig: &datamodel.CacheConfig{
						CachePrePopulate: &datamodel.CachePrePopulate{
							PathList:        []string{"/path1", "/path2"},
							ExcludePathList: []string{"/exclude1"},
							Recursion:       boolPtr(true),
						},
					},
				},
			},
			params: &common.UpdateVolumeParams{
				CacheParameters: &models.CacheParameters{
					CacheConfig: &models.CacheConfig{
						CachePrePopulate: &models.CachePrePopulate{
							PathList:        []string{"/path2", "/path1"},
							ExcludePathList: []string{"/exclude1"},
							Recursion:       boolPtr(true),
						},
					},
				},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := _isUpdateFlexCachePrepopulateRequired(tt.existingVolume, tt.params)
			if got != tt.want {
				t.Errorf("_isUpdateFlexCachePrepopulateRequired() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_updateOrAddBlockDevice(t *testing.T) {
	t.Run("UpdateExistingBlockDevice", func(t *testing.T) {
		existingDevice := &common.BlockDevice{
			Name:            "lun1",
			SizeInBytes:     536870912,
			OSType:          "linux",
			LunSerialNumber: "serial123",
			LunUUID:         "uuid123",
			HostGroups:      []string{"hg1"},
		}

		updatedDevice := &common.BlockDevice{
			Name:            "lun1",
			SizeInBytes:     1073741824, // Updated size
			OSType:          "linux",
			LunSerialNumber: "serial123",
			LunUUID:         "uuid123",
			HostGroups:      []string{"hg1", "hg2"}, // Updated host groups
		}

		params := &common.UpdateVolumeParams{
			BlockDevices: []*common.BlockDevice{updatedDevice},
		}
		updateOrAddBlockDevice(params, existingDevice)

		assert.Len(t, params.BlockDevices, 1)
		assert.Equal(t, "lun1", params.BlockDevices[0].Name)
		assert.Equal(t, int64(536870912), params.BlockDevices[0].SizeInBytes)
		assert.Equal(t, []string{"hg1", "hg2"}, params.BlockDevices[0].HostGroups)
	})
	t.Run("UpdateNonExistingBlockDevice", func(t *testing.T) {
		existingDevice := &common.BlockDevice{
			Name:            "lun1",
			SizeInBytes:     536870912,
			OSType:          "linux",
			LunSerialNumber: "serial123",
			LunUUID:         "uuid123",
			HostGroups:      []string{"hg1"},
		}

		updatedDevice := &common.BlockDevice{
			Name:            "lun2",
			SizeInBytes:     1073741824, // Updated size
			OSType:          "linux",
			LunSerialNumber: "serial123",
			LunUUID:         "uuid123",
			HostGroups:      []string{"hg1", "hg2"}, // Updated host groups
		}

		params := &common.UpdateVolumeParams{
			BlockDevices: []*common.BlockDevice{updatedDevice},
		}
		updateOrAddBlockDevice(params, existingDevice)

		assert.Len(t, params.BlockDevices, 2)
		assert.Equal(t, "lun2", params.BlockDevices[0].Name)
		assert.Equal(t, int64(1073741824), params.BlockDevices[0].SizeInBytes)
		assert.Equal(t, []string{"hg1", "hg2"}, params.BlockDevices[0].HostGroups)
	})
}

func TestUpdateSMBShareSettings_WorkflowIntegration(t *testing.T) {
	originalEnableSmb := enableSmb
	enableSmb = true
	defer func() {
		enableSmb = originalEnableSmb
	}()
	t.Run("Calls UpdateSMBShareSettings activity for SMB volumes", func(tt *testing.T) {
		// This test verifies that the volume update workflow correctly calls
		// the UpdateSMBShareSettings activity when SMB share settings are provided

		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()

		mockProvider := new(vsa.MockProvider)
		originalGetProviderByNode := vsa.GetProviderByNode
		defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()

		vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "vol-uuid-123"},
			Name:      "test-smb-volume",
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{"CIFS"},
				FileProperties: &datamodel.FileProperties{
					JunctionPath:     "/test_share",
					SMBShareSettings: []string{"browsable", "encrypt_data"},
				},
			},
			Svm: &datamodel.Svm{
				SvmDetails: &datamodel.SvmDetails{
					ExternalUUID: "test-svm-uuid",
				},
			},
		}

		params := &common.UpdateVolumeParams{
			SMBShareSettings: []string{"browsable", "encrypt_data", "oplocks"},
		}

		node := &models.Node{}

		// Mock CifsShareCollectionGet to return existing properties without oplocks
		mockProvider.On("CifsShareCollectionGet", "test-svm-uuid", "test_share", []string{"continuously_available"}).
			Return([]string{"browsable", "encrypt_data"}, nil)

		// Mock UpdateCIFSServer - this should be called with the new settings
		mockProvider.On("UpdateCIFSServer", "test-svm-uuid", "test_share", []string{"browsable", "encrypt_data", "oplocks"}).
			Return(nil)

		// Call the UpdateSMBShareSettings activity using Temporal test environment
		activity := activities.VolumeUpdateActivity{}
		env.RegisterActivity(activity.UpdateSMBShareSettings)

		_, err := env.ExecuteActivity(activity.UpdateSMBShareSettings, volume, params, node)
		assert.NoError(tt, err)
		mockProvider.AssertExpectations(tt)
	})

	t.Run("Returns error for volumes without FileProperties", func(tt *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "vol-uuid-123"},
			Name:      "test-volume-without-file-properties",
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{"CIFS"},
				// No FileProperties
			},
		}

		params := &common.UpdateVolumeParams{
			SMBShareSettings: []string{"browsable"},
		}

		node := &models.Node{}

		// Call the UpdateSMBShareSettings activity - should return error for missing FileProperties
		activity := activities.VolumeUpdateActivity{}
		env.RegisterActivity(activity.UpdateSMBShareSettings)

		_, err := env.ExecuteActivity(activity.UpdateSMBShareSettings, volume, params, node)
		assert.Error(tt, err)

		// The error is wrapped by Temporal (ActivityError -> ApplicationError -> NotFoundErr)
		// but the error message "share not found" is preserved in the chain
		// Check the error message contains "not found" which works even with Temporal wrapping
		assert.Contains(tt, err.Error(), "not found", "Expected error to contain 'not found', got: %v", err)
	})
}

func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_UpdateSMBShareSettings_WithFlagEnabled() {
	// Save original value and enable flag
	originalEnableSmb := enableSmb
	defer func() { enableSmb = originalEnableSmb }()
	enableSmb = true

	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}
	backupActivity := activities.BackupActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(updateActivity.GetVolumeFromONTAP)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInDB)
	s.env.RegisterActivity(updateActivity.UpdateSMBShareSettings)
	s.env.RegisterActivity(backupActivity.UpdateBackupMetadataIfExistsActivity)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 1000,
		Size:           1000,
		State:          "online",
	}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UpdateSMBShareSettings, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInDB, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupActivity.UpdateBackupMetadataIfExistsActivity, mock.Anything, mock.Anything).Return(nil)

	// Execute workflow
	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{
			Password:      "password",
			SecretID:      "",
			CertificateID: "",
		}},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		SizeInBytes: 1000,
		VolumeAttributes: &datamodel.VolumeAttributes{
			Protocols: []string{"SMB"},
			FileProperties: &datamodel.FileProperties{
				JunctionPath: "/test_share",
			},
		},
	}
	params := &common.UpdateVolumeParams{
		SMBShareSettings: []string{"browsable", "encrypt_data"},
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	// Assert workflow completed successfully
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
	// Verify UpdateSMBShareSettings was called with volume, params, and node (3 arguments after context)
	s.env.AssertCalled(s.T(), "UpdateSMBShareSettings", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	mockStorage.AssertNumberOfCalls(s.T(), "UpdateJob", 2)
}

func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_FlexCachePrepopulate_Success_SynchronousCompletion() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}
	flexCacheUpdateActivity := flexcache_activities.FlexCacheVolumeUpdateActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInDB)
	s.env.RegisterActivity(flexCacheUpdateActivity.UpdatePrepopulateState)
	s.env.RegisterActivity(flexCacheUpdateActivity.StartFlexCachePrepopulate)

	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 1000,
		Size:           1000,
		State:          "online",
	}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInDB, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	s.env.OnActivity(flexCacheUpdateActivity.UpdatePrepopulateState, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(flexCacheUpdateActivity.StartFlexCachePrepopulate, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return("", nil) // Empty string = synchronous

	prevIsUpdateFlexCachePrepopulateRequired := isUpdateFlexCachePrepopulateRequired
	isUpdateFlexCachePrepopulateRequired = func(existingVolume *datamodel.Volume, params *common.UpdateVolumeParams) bool {
		return true
	}
	defer func() { isUpdateFlexCachePrepopulateRequired = prevIsUpdateFlexCachePrepopulateRequired }()

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "volume-uuid-123"},
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{
			Password:      "password",
			SecretID:      "",
			CertificateID: "",
		}},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		SizeInBytes: 1000,
		VolumeAttributes: &datamodel.VolumeAttributes{
			IsDataProtection: false,
		},
		CacheParameters: &datamodel.CacheParameters{
			CacheConfig: &datamodel.CacheConfig{},
		},
	}

	params := &common.UpdateVolumeParams{
		QuotaInBytes: 2000,
		CacheParameters: &models.CacheParameters{
			CacheConfig: &models.CacheConfig{
				CachePrePopulate: &models.CachePrePopulate{
					PathList:  []string{"/data1"},
					Recursion: boolPtr(true),
				},
			},
		},
	}

	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
	mockStorage.AssertNumberOfCalls(s.T(), "UpdateJob", 2)
}

func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_FlexCachePrepopulate_Success_AsyncWithJobCreation() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}
	flexCacheUpdateActivity := flexcache_activities.FlexCacheVolumeUpdateActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInDB)
	s.env.RegisterActivity(flexCacheUpdateActivity.UpdatePrepopulateState)
	s.env.RegisterActivity(flexCacheUpdateActivity.StartFlexCachePrepopulate)
	s.env.RegisterActivity(flexCacheUpdateActivity.CreatePrepopulateJob)

	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 1000,
		Size:           1000,
		State:          "online",
	}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInDB, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	ontapJobUUID := "ontap-job-uuid-789"
	createdJobUUID := "created-job-uuid-456"
	s.env.OnActivity(flexCacheUpdateActivity.UpdatePrepopulateState, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(flexCacheUpdateActivity.StartFlexCachePrepopulate, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(ontapJobUUID, nil)
	s.env.OnActivity(flexCacheUpdateActivity.CreatePrepopulateJob, mock.Anything, mock.Anything, ontapJobUUID).Return(createdJobUUID, nil)

	prevIsUpdateFlexCachePrepopulateRequired := isUpdateFlexCachePrepopulateRequired
	isUpdateFlexCachePrepopulateRequired = func(existingVolume *datamodel.Volume, params *common.UpdateVolumeParams) bool {
		return true
	}
	defer func() { isUpdateFlexCachePrepopulateRequired = prevIsUpdateFlexCachePrepopulateRequired }()

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "volume-uuid-123"},
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{
			Password: "password",
		}},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		SizeInBytes: 1000,
		VolumeAttributes: &datamodel.VolumeAttributes{
			IsDataProtection: false,
		},
		CacheParameters: &datamodel.CacheParameters{
			CacheConfig: &datamodel.CacheConfig{},
		},
	}

	params := &common.UpdateVolumeParams{
		QuotaInBytes: 2000,
		CacheParameters: &models.CacheParameters{
			CacheConfig: &models.CacheConfig{
				CachePrePopulate: &models.CachePrePopulate{
					PathList:  []string{"/data1", "/data2"},
					Recursion: boolPtr(true),
				},
			},
		},
	}

	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
	mockStorage.AssertNumberOfCalls(s.T(), "UpdateJob", 2)
}

func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_UpdateFlexCacheVolumeInONTAP_WithAsyncJob_Success() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}
	updateFlexCacheActivity := flexcache_activities.FlexCacheVolumeUpdateActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetNode)
	s.env.RegisterActivity(updateActivity.GetVolumeFromONTAP)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateFlexCacheActivity.UpdateFlexCacheVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateLun)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInDB)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 1000,
		Size:           1000,
		State:          "online",
	}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	asyncResponse := &vsa.OntapAsyncResponse{JobUUID: "test-job-uuid"}
	s.env.OnActivity(updateFlexCacheActivity.UpdateFlexCacheVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(asyncResponse, nil)

	s.env.OnActivity(commonActivity.GetOntapJob, mock.Anything, "test-job-uuid", mock.Anything).Return(&vsa.OntapJob{
		State: "success",
		UUID:  "test-job-uuid",
	}, nil)

	s.env.OnActivity(updateActivity.UpdateLun, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.LunResponse{ProviderResponse: vsa.ProviderResponse{Name: "/vol/vol1/lun1"}}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInDB, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	prevIsUpdateFlexCacheRequired := isUpdateFlexCacheRequired
	isUpdateFlexCacheRequired = func(existingVolume *datamodel.Volume, params *common.UpdateVolumeParams) bool {
		return true
	}
	defer func() { isUpdateFlexCacheRequired = prevIsUpdateFlexCacheRequired }()

	// Execute workflow
	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{
			Password:      "password",
			SecretID:      "",
			CertificateID: "",
		}},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		SizeInBytes: 1000,
		VolumeAttributes: &datamodel.VolumeAttributes{
			IsDataProtection: false,
		},
	}
	atimeScrubEnabled := true
	atimeScrubDays := int16(5)
	params := &common.UpdateVolumeParams{
		QuotaInBytes: 2000,
		CacheParameters: &models.CacheParameters{
			CacheConfig: &models.CacheConfig{
				AtimeScrubEnabled: &atimeScrubEnabled,
				AtimeScrubDays:    &atimeScrubDays,
			},
		},
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	// Assert workflow completed successfully
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
}

func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_FlexCachePrepopulate_StartPrepopulateFails() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}
	flexCacheUpdateActivity := flexcache_activities.FlexCacheVolumeUpdateActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInDB)
	s.env.RegisterActivity(flexCacheUpdateActivity.UpdatePrepopulateState)
	s.env.RegisterActivity(flexCacheUpdateActivity.StartFlexCachePrepopulate)

	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 1000,
		Size:           1000,
		State:          "online",
	}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInDB, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	s.env.OnActivity(flexCacheUpdateActivity.UpdatePrepopulateState, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(flexCacheUpdateActivity.StartFlexCachePrepopulate, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return("", errors.New("ONTAP error starting prepopulate"))

	prevIsUpdateFlexCachePrepopulateRequired := isUpdateFlexCachePrepopulateRequired
	isUpdateFlexCachePrepopulateRequired = func(existingVolume *datamodel.Volume, params *common.UpdateVolumeParams) bool {
		return true
	}
	defer func() { isUpdateFlexCachePrepopulateRequired = prevIsUpdateFlexCachePrepopulateRequired }()

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "volume-uuid-123"},
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{
			Password: "password",
		}},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		SizeInBytes: 1000,
		VolumeAttributes: &datamodel.VolumeAttributes{
			IsDataProtection: false,
		},
		CacheParameters: &datamodel.CacheParameters{
			CacheConfig: &datamodel.CacheConfig{},
		},
	}

	params := &common.UpdateVolumeParams{
		QuotaInBytes: 2000,
		CacheParameters: &models.CacheParameters{
			CacheConfig: &models.CacheConfig{
				CachePrePopulate: &models.CachePrePopulate{
					PathList: []string{"/data1"},
				},
			},
		},
	}

	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
	mockStorage.AssertNumberOfCalls(s.T(), "UpdateJob", 2)
}

func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_FlexCachePrepopulate_CreatePrepopulateJobFails() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}
	flexCacheUpdateActivity := flexcache_activities.FlexCacheVolumeUpdateActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInDB)
	s.env.RegisterActivity(flexCacheUpdateActivity.UpdatePrepopulateState)
	s.env.RegisterActivity(flexCacheUpdateActivity.StartFlexCachePrepopulate)
	s.env.RegisterActivity(flexCacheUpdateActivity.CreatePrepopulateJob)

	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 1000,
		Size:           1000,
		State:          "online",
	}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInDB, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	ontapJobUUID := "ontap-job-uuid-789"
	s.env.OnActivity(flexCacheUpdateActivity.UpdatePrepopulateState, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(flexCacheUpdateActivity.StartFlexCachePrepopulate, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(ontapJobUUID, nil)
	s.env.OnActivity(flexCacheUpdateActivity.CreatePrepopulateJob, mock.Anything, mock.Anything, ontapJobUUID).Return("", errors.New("failed to create job record"))

	prevIsUpdateFlexCachePrepopulateRequired := isUpdateFlexCachePrepopulateRequired
	isUpdateFlexCachePrepopulateRequired = func(existingVolume *datamodel.Volume, params *common.UpdateVolumeParams) bool {
		return true
	}
	defer func() { isUpdateFlexCachePrepopulateRequired = prevIsUpdateFlexCachePrepopulateRequired }()

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "volume-uuid-123"},
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{
			Password: "password",
		}},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		SizeInBytes: 1000,
		VolumeAttributes: &datamodel.VolumeAttributes{
			IsDataProtection: false,
		},
		CacheParameters: &datamodel.CacheParameters{
			CacheConfig: &datamodel.CacheConfig{},
		},
	}

	params := &common.UpdateVolumeParams{
		QuotaInBytes: 2000,
		CacheParameters: &models.CacheParameters{
			CacheConfig: &models.CacheConfig{
				CachePrePopulate: &models.CachePrePopulate{
					PathList: []string{"/data1"},
				},
			},
		},
	}

	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
	mockStorage.AssertNumberOfCalls(s.T(), "UpdateJob", 2)
}

func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_WithKmsGrant() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}
	volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}
	backupActivity := activities.BackupActivity{SE: mockStorage}
	syncBackupZiZsActivity := backgroundactivities.SyncBackupZiZsActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInDB)
	s.env.RegisterActivity(updateActivity.FindTenancyDetails)
	s.env.RegisterActivity(updateActivity.CheckBackupVaultExistInVCP)
	s.env.RegisterActivity(updateActivity.CheckBucketResourceName)
	s.env.RegisterActivity(updateActivity.GenerateResourceNamesForBackupVault)
	s.env.RegisterActivity(updateActivity.CreateBucketForBackupVault)
	s.env.RegisterActivity(updateActivity.UpdateBucketDetailsOfBackupVault)
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)
	s.env.RegisterActivity(backupActivity.UpdateBackupMetadataIfExistsActivity)
	s.env.RegisterActivity(syncBackupZiZsActivity.SyncBucketDetails)
	s.env.RegisterActivity(volumeCreateActivity.CheckOrCreateRemoteBackupVaultInVCP)
	s.env.RegisterActivity(volumeCreateActivity.UpdateRemoteBackupVaultWithBucketDetails)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 1000,
		Size:           1000,
		State:          "online",
	}, nil)
	s.env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).Return("", nil)
	s.env.OnActivity(updateActivity.CheckBackupVaultExistInVCP, mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.BackupVault{
		BaseModel:       datamodel.BaseModel{UUID: "test-bv-uuid"},
		Name:            "test-backup-vault",
		BackupVaultType: "LOCAL",
	}, nil)
	s.env.OnActivity(updateActivity.FindTenancyDetails, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.TenancyInfo{
		RegionalTenantProject: "test-project",
	}, nil)
	s.env.OnActivity(updateActivity.CheckBucketResourceName, mock.Anything, mock.Anything).Return(&common.BucketDetails{}, nil)
	s.env.OnActivity(updateActivity.GenerateResourceNamesForBackupVault, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.ResourceNames{
		BucketName:       "test-bucket",
		ServiceAccountId: "test-sa",
		Email:            "test@example.com",
	}, nil)

	// Verify kmsGrant is passed to CreateBucketForBackupVault
	kmsGrant := "projects/test-project/locations/us-west1/keyRings/test-keyring/cryptoKeys/test-key"
	var capturedKmsGrant *string
	s.env.OnActivity(updateActivity.CreateBucketForBackupVault, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.MatchedBy(func(grant *string) bool {
		capturedKmsGrant = grant
		return true
	})).Return(&common.BucketDetails{
		BucketName:          "test-bucket",
		ServiceAccountName:  "test-sa",
		TenantProjectNumber: "123456789",
	}, nil)
	s.env.OnActivity(syncBackupZiZsActivity.SyncBucketDetails, mock.Anything, mock.Anything).Return(nil, nil)
	s.env.OnActivity(updateActivity.UpdateBucketDetailsOfBackupVault, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(volumeCreateActivity.CheckOrCreateRemoteBackupVaultInVCP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.BackupVault{
		BaseModel: datamodel.BaseModel{UUID: "test-remote-bv-uuid"},
		Name:      "test-remote-backup-vault",
	}, nil)
	s.env.OnActivity(volumeCreateActivity.UpdateRemoteBackupVaultWithBucketDetails, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInDB, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupActivity.UpdateBackupMetadataIfExistsActivity, mock.Anything, mock.Anything).Return(nil)

	// Execute workflow
	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{
			Password:      "password",
			SecretID:      "",
			CertificateID: "",
		}},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		DataProtection: &datamodel.DataProtection{
			BackupVaultID: "test-bv-uuid",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			VendorSubnetID: "test-subnet-id",
		},
	}
	params := &common.UpdateVolumeParams{
		Region: "us-west-1",
		DataProtection: &models.UpdateDataProtection{
			KmsGrant: &kmsGrant,
		},
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	// Assert workflow completed successfully
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
	// Verify kmsGrant was passed to CreateBucketForBackupVault
	if assert.NotNil(s.T(), capturedKmsGrant, "kmsGrant should have been captured") {
		assert.Equal(s.T(), kmsGrant, *capturedKmsGrant)
	}
	mockStorage.AssertNumberOfCalls(s.T(), "UpdateJob", 2)
}

// Test_UpdateVolumeWorkflow_CrossRegionBackup_WithPermissionsSetup tests cross-region backup
// permissions setup during volume update
func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_CrossRegionBackup_WithPermissionsSetup() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}
	volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}
	backupActivity := activities.BackupActivity{SE: mockStorage}

	backupRegionName := "us-east1"

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetNode)
	s.env.RegisterActivity(commonActivity.GetAuthJWTToken)
	s.env.RegisterActivity(updateActivity.GetVolumeFromONTAP)
	s.env.RegisterActivity(updateActivity.CheckBackupVaultExistInVCP)
	s.env.RegisterActivity(updateActivity.FindTenancyDetails)
	s.env.RegisterActivity(updateActivity.CheckBucketResourceName)
	s.env.RegisterActivity(volumeCreateActivity.SetupCrossRegionBackupPermissionsActivity)
	s.env.RegisterActivity(volumeCreateActivity.CheckOrCreateRemoteBackupVaultInVCP)
	s.env.RegisterActivity(volumeCreateActivity.UpdateRemoteBackupVaultWithBucketDetails)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInDB)
	s.env.RegisterActivity(backupActivity.UpdateBackupMetadataIfExistsActivity)

	// Mock activities
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 1000,
		Size:           1000,
		State:          "online",
	}, nil)
	s.env.OnActivity(updateActivity.CheckBackupVaultExistInVCP, mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.BackupVault{
		BaseModel:        datamodel.BaseModel{UUID: "backup-vault-uuid"},
		Name:             "test-backup-vault",
		BackupVaultType:  activities.CrossRegionBackupType,
		BackupRegionName: &backupRegionName,
	}, nil)
	s.env.OnActivity(volumeCreateActivity.CheckOrCreateRemoteBackupVaultInVCP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.BackupVault{
		BaseModel: datamodel.BaseModel{UUID: "remote-backup-vault-uuid"},
		Name:      "test-remote-backup-vault",
	}, nil)
	s.env.OnActivity(updateActivity.FindTenancyDetails, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.TenancyInfo{
		RegionalTenantProject: "tenant-project",
	}, nil)
	s.env.OnActivity(updateActivity.CheckBucketResourceName, mock.Anything, mock.Anything).Return(&common.BucketDetails{
		BucketName:          "test-bucket",
		ServiceAccountName:  "test-sa",
		TenantProjectNumber: "12345",
	}, nil)
	s.env.OnActivity(volumeCreateActivity.UpdateRemoteBackupVaultWithBucketDetails, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).Return("test-token", nil)
	s.env.OnActivity(volumeCreateActivity.SetupCrossRegionBackupPermissionsActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInDB, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupActivity.UpdateBackupMetadataIfExistsActivity, mock.Anything, mock.Anything).Return(nil)

	// Execute workflow
	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			},
		},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		DataProtection: &datamodel.DataProtection{
			BackupVaultID: "test-bv-uuid",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			VendorSubnetID: "test-subnet-id",
		},
	}
	params := &common.UpdateVolumeParams{
		QuotaInBytes: 1000,
		Region:       "us-west-1",
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	// Assert workflow completed successfully
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
}

// Test_UpdateVolumeWorkflow_CrossRegionBackup_SetupPermissionsError tests error handling
// during cross-region backup permissions setup
func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_CrossRegionBackup_SetupPermissionsError() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}
	volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}

	backupRegionName := "us-east1"

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetNode)
	s.env.RegisterActivity(commonActivity.GetAuthJWTToken)
	s.env.RegisterActivity(updateActivity.GetVolumeFromONTAP)
	s.env.RegisterActivity(updateActivity.CheckBackupVaultExistInVCP)
	s.env.RegisterActivity(updateActivity.FindTenancyDetails)
	s.env.RegisterActivity(updateActivity.CheckBucketResourceName)
	s.env.RegisterActivity(volumeCreateActivity.SetupCrossRegionBackupPermissionsActivity)
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)

	// Mock activities
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 1000,
		Size:           1000,
		State:          "online",
	}, nil)
	s.env.OnActivity(updateActivity.CheckBackupVaultExistInVCP, mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.BackupVault{
		BaseModel:        datamodel.BaseModel{UUID: "backup-vault-uuid"},
		Name:             "test-backup-vault",
		BackupVaultType:  activities.CrossRegionBackupType,
		BackupRegionName: &backupRegionName,
	}, nil)
	s.env.OnActivity(updateActivity.FindTenancyDetails, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.TenancyInfo{
		RegionalTenantProject: "tenant-project",
	}, nil)
	s.env.OnActivity(updateActivity.CheckBucketResourceName, mock.Anything, mock.Anything).Return(&common.BucketDetails{
		BucketName:          "test-bucket",
		ServiceAccountName:  "test-sa",
		TenantProjectNumber: "12345",
	}, nil)
	s.env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).Return("test-token", nil)
	s.env.OnActivity(volumeCreateActivity.SetupCrossRegionBackupPermissionsActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("failed to setup cross-region permissions"))
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Execute workflow
	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			},
		},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		DataProtection: &datamodel.DataProtection{
			BackupVaultID: "test-bv-uuid",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			VendorSubnetID: "test-subnet-id",
		},
	}
	params := &common.UpdateVolumeParams{
		QuotaInBytes: 1000,
		Region:       "us-west-1",
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	// Assert workflow completed with error
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NotNil(s.T(), s.env.GetWorkflowError())
}

// Test_UpdateVolumeWorkflow_CrossRegionBackup_SleepError tests error handling
// when workflow.Sleep fails after SetupCrossRegionBackupPermissionsActivity
func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_CrossRegionBackup_SleepError() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}
	volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}
	backupActivity := activities.BackupActivity{SE: mockStorage}

	backupRegionName := "us-east1"

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetNode)
	s.env.RegisterActivity(commonActivity.GetAuthJWTToken)
	s.env.RegisterActivity(updateActivity.GetVolumeFromONTAP)
	s.env.RegisterActivity(updateActivity.CheckBackupVaultExistInVCP)
	s.env.RegisterActivity(updateActivity.FindTenancyDetails)
	s.env.RegisterActivity(updateActivity.CheckBucketResourceName)
	s.env.RegisterActivity(volumeCreateActivity.SetupCrossRegionBackupPermissionsActivity)
	s.env.RegisterActivity(volumeCreateActivity.CheckOrCreateRemoteBackupVaultInVCP)
	s.env.RegisterActivity(volumeCreateActivity.UpdateRemoteBackupVaultWithBucketDetails)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInDB)
	s.env.RegisterActivity(backupActivity.UpdateBackupMetadataIfExistsActivity)

	// Register SyncBucketDetails activity instance
	syncBackupZiZsActivity := backgroundactivities.SyncBackupZiZsActivity{SE: mockStorage}
	s.env.RegisterActivity(syncBackupZiZsActivity.SyncBucketDetails)

	// Mock activities
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 1000,
		Size:           1000,
		State:          "online",
	}, nil)
	s.env.OnActivity(updateActivity.CheckBackupVaultExistInVCP, mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.BackupVault{
		BaseModel:        datamodel.BaseModel{UUID: "backup-vault-uuid"},
		Name:             "test-backup-vault",
		BackupVaultType:  activities.CrossRegionBackupType,
		BackupRegionName: &backupRegionName,
	}, nil)
	s.env.OnActivity(volumeCreateActivity.CheckOrCreateRemoteBackupVaultInVCP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.BackupVault{
		BaseModel: datamodel.BaseModel{UUID: "remote-backup-vault-uuid"},
		Name:      "test-remote-backup-vault",
	}, nil)
	s.env.OnActivity(updateActivity.FindTenancyDetails, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.TenancyInfo{
		RegionalTenantProject: "tenant-project",
	}, nil)
	s.env.OnActivity(updateActivity.CheckBucketResourceName, mock.Anything, mock.Anything).Return(&common.BucketDetails{
		BucketName:          "test-bucket",
		ServiceAccountName:  "test-sa",
		TenantProjectNumber: "12345",
	}, nil)
	s.env.OnActivity(volumeCreateActivity.UpdateRemoteBackupVaultWithBucketDetails, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).Return("test-token", nil)

	// Mock SyncBucketDetails activity (called by syncBucketDetailsWithGCP)
	s.env.OnActivity(syncBackupZiZsActivity.SyncBucketDetails, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{
		BucketName:          "test-bucket",
		TenantProjectNumber: "12345",
	}, nil)

	// Mock SetupCrossRegionBackupPermissionsActivity to succeed
	s.env.OnActivity(volumeCreateActivity.SetupCrossRegionBackupPermissionsActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Override workflowSleep to return error - this tests the error handling at line 462-464
	origWorkflowSleep := workflowSleep
	workflowSleep = func(ctx workflow.Context, d time.Duration) error {
		return errors.New("failed to sleep after cross-region backup permissions are created")
	}
	defer func() { workflowSleep = origWorkflowSleep }()

	s.env.OnActivity(updateActivity.UpdateVolumeInDB, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupActivity.UpdateBackupMetadataIfExistsActivity, mock.Anything, mock.Anything).Return(nil)

	// Execute workflow
	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
		Name:      "test_volume",
		Pool: &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			},
		},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		DataProtection: &datamodel.DataProtection{
			BackupVaultID: "test-bv-uuid",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			VendorSubnetID: "test-subnet-id",
		},
	}
	params := &common.UpdateVolumeParams{
		QuotaInBytes: 1000,
		Region:       "us-west-1",
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	// Assert workflow completed successfully despite sleep error
	// The error is logged but doesn't stop workflow execution
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
}

// ============================================================================
// QoS Update Workflow Tests - Autogenerated VPG Update Path
// ============================================================================

func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_AutogeneratedVPG_UpdateQoS_Success() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetNode)
	s.env.RegisterActivity(updateActivity.GetVolumeFromONTAP)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateLun)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInDB)
	s.env.RegisterActivity(updateActivity.UpdateQoSPolicyGroupForVolume)
	s.env.RegisterActivity(updateActivity.UpdateVolumePerformanceGroupInDB)
	backupActivity := activities.BackupActivity{SE: mockStorage}
	s.env.RegisterActivity(backupActivity.UpdateBackupMetadataIfExistsActivity)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 1000,
		Size:           1000,
		State:          "online",
	}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UpdateLun, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.LunResponse{ProviderResponse: vsa.ProviderResponse{Name: "/vol/vol1/lun1"}}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInDB, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupActivity.UpdateBackupMetadataIfExistsActivity, mock.Anything, mock.Anything).Return(nil)

	// Mock QoS update activities
	newThroughput := int64(200)
	newIops := int64(2000)
	s.env.OnActivity(updateActivity.UpdateQoSPolicyGroupForVolume, mock.Anything, mock.Anything, newThroughput, newIops, mock.Anything).Return(nil)
	// Mock rollback call with original values
	s.env.OnActivity(updateActivity.UpdateQoSPolicyGroupForVolume, mock.Anything, mock.Anything, int64(100), int64(1000), mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(updateActivity.UpdateVolumePerformanceGroupInDB, mock.Anything, mock.Anything).Return(nil)

	// Execute workflow
	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
		Name:      "test_volume",
		Pool: &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			},
		},
		Account:                  &datamodel.Account{Name: "test_account"},
		SizeInBytes:              1000,
		VolumeAttributes:         &datamodel.VolumeAttributes{IsDataProtection: false},
		VolumePerformanceGroupID: sql.NullInt64{Int64: 1, Valid: true},
		VolumePerformanceGroup: &datamodel.VolumePerformanceGroup{
			BaseModel:        datamodel.BaseModel{ID: 1, UUID: "vpg-uuid"},
			IsAutoGen:        true,
			OntapQosPolicyID: "550e8400-e29b-41d4-a716-446655440000",
			ThroughputMibps:  100,
			Iops:             1000,
		},
	}
	params := &common.UpdateVolumeParams{
		ThroughputMibps: &newThroughput,
		Iops:            &newIops,
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
	mockStorage.AssertNumberOfCalls(s.T(), "UpdateJob", 2)
}

func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_AutogeneratedVPG_UpdateQoS_OnlyThroughput() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetNode)
	s.env.RegisterActivity(updateActivity.GetVolumeFromONTAP)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateLun)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInDB)
	s.env.RegisterActivity(updateActivity.UpdateQoSPolicyGroupForVolume)
	s.env.RegisterActivity(updateActivity.UpdateVolumePerformanceGroupInDB)
	backupActivity := activities.BackupActivity{SE: mockStorage}
	s.env.RegisterActivity(backupActivity.UpdateBackupMetadataIfExistsActivity)

	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 1000,
		Size:           1000,
		State:          "online",
	}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UpdateLun, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.LunResponse{ProviderResponse: vsa.ProviderResponse{Name: "/vol/vol1/lun1"}}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInDB, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupActivity.UpdateBackupMetadataIfExistsActivity, mock.Anything, mock.Anything).Return(nil)

	// Mock QoS update - only throughput provided, IOPS uses current (1000)
	newThroughput := int64(200)
	currentIops := int64(1000)
	s.env.OnActivity(updateActivity.UpdateQoSPolicyGroupForVolume, mock.Anything, mock.Anything, newThroughput, currentIops, mock.Anything).Return(nil)

	s.env.OnActivity(updateActivity.UpdateQoSPolicyGroupForVolume, mock.Anything, mock.Anything, int64(100), int64(1000), mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(updateActivity.UpdateVolumePerformanceGroupInDB, mock.Anything, mock.Anything).Return(nil)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
		Name:      "test_volume",
		Pool: &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			},
		},
		Account:                  &datamodel.Account{Name: "test_account"},
		SizeInBytes:              1000,
		VolumeAttributes:         &datamodel.VolumeAttributes{IsDataProtection: false},
		VolumePerformanceGroupID: sql.NullInt64{Int64: 1, Valid: true},
		VolumePerformanceGroup: &datamodel.VolumePerformanceGroup{
			BaseModel:        datamodel.BaseModel{ID: 1, UUID: "vpg-uuid"},
			IsAutoGen:        true,
			OntapQosPolicyID: "550e8400-e29b-41d4-a716-446655440000",
			ThroughputMibps:  100,
			Iops:             1000,
		},
	}
	params := &common.UpdateVolumeParams{
		ThroughputMibps: &newThroughput,
		// Iops is nil, should use current value
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
	mockStorage.AssertNumberOfCalls(s.T(), "UpdateJob", 2)
}

func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_AutogeneratedVPG_UpdateQoS_MissingOntapQosPolicyID() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetNode)
	s.env.RegisterActivity(updateActivity.GetVolumeFromONTAP)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateLun)
	backupActivity := activities.BackupActivity{SE: mockStorage}
	s.env.RegisterActivity(backupActivity.UpdateBackupMetadataIfExistsActivity)

	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 1000,
		Size:           1000,
		State:          "online",
	}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UpdateLun, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.LunResponse{ProviderResponse: vsa.ProviderResponse{Name: "/vol/vol1/lun1"}}, nil)
	s.env.OnActivity(backupActivity.UpdateBackupMetadataIfExistsActivity, mock.Anything, mock.Anything).Return(nil)

	// Execute workflow - volume has autogenerated VPG but missing OntapQosPolicyID
	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
		Pool: &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			},
		},
		Account:                  &datamodel.Account{Name: "test_account"},
		SizeInBytes:              1000,
		VolumeAttributes:         &datamodel.VolumeAttributes{IsDataProtection: false},
		VolumePerformanceGroupID: sql.NullInt64{Int64: 1, Valid: true},
		VolumePerformanceGroup: &datamodel.VolumePerformanceGroup{
			BaseModel:        datamodel.BaseModel{ID: 1, UUID: "vpg-uuid"},
			IsAutoGen:        true,
			OntapQosPolicyID: "",
			Iops:             1000,
		},
	}
	newThroughput := int64(200)
	newIops := int64(2000)
	params := &common.UpdateVolumeParams{
		ThroughputMibps: &newThroughput,
		Iops:            &newIops,
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
	assert.Contains(s.T(), s.env.GetWorkflowError().Error(), "missing OntapQosPolicyID")
}

func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_AutogeneratedVPG_UpdateQoS_UpdateQoSPolicyGroupFailure() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetNode)
	s.env.RegisterActivity(updateActivity.GetVolumeFromONTAP)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateLun)
	s.env.RegisterActivity(updateActivity.UpdateQoSPolicyGroupForVolume)
	backupActivity := activities.BackupActivity{SE: mockStorage}
	s.env.RegisterActivity(backupActivity.UpdateBackupMetadataIfExistsActivity)
	volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)

	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 1000,
		Size:           1000,
		State:          "online",
	}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UpdateLun, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.LunResponse{ProviderResponse: vsa.ProviderResponse{Name: "/vol/vol1/lun1"}}, nil)
	s.env.OnActivity(backupActivity.UpdateBackupMetadataIfExistsActivity, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Mock QoS update failure
	newThroughput := int64(200)
	newIops := int64(2000)
	s.env.OnActivity(updateActivity.UpdateQoSPolicyGroupForVolume, mock.Anything, mock.Anything, newThroughput, newIops, mock.Anything).Return(errors.New("failed to update QoS policy"))
	// Allow rollback attempt with current values
	s.env.OnActivity(updateActivity.UpdateQoSPolicyGroupForVolume, mock.Anything, mock.Anything, int64(100), int64(1000), mock.Anything).Return(nil).Maybe()

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
		Name:      "test_volume",
		Pool: &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			},
		},
		Account:                  &datamodel.Account{Name: "test_account"},
		SizeInBytes:              1000,
		VolumeAttributes:         &datamodel.VolumeAttributes{IsDataProtection: false},
		VolumePerformanceGroupID: sql.NullInt64{Int64: 1, Valid: true},
		VolumePerformanceGroup: &datamodel.VolumePerformanceGroup{
			BaseModel:        datamodel.BaseModel{ID: 1, UUID: "vpg-uuid"},
			IsAutoGen:        true,
			OntapQosPolicyID: "550e8400-e29b-41d4-a716-446655440000",
			ThroughputMibps:  100,
			Iops:             1000,
		},
	}
	params := &common.UpdateVolumeParams{
		ThroughputMibps: &newThroughput,
		Iops:            &newIops,
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
	assert.Contains(s.T(), s.env.GetWorkflowError().Error(), "failed to update QoS policy")
}

func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_AutogeneratedVPG_UpdateQoS_UpdateVolumePerformanceGroupInDBFailure() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetNode)
	s.env.RegisterActivity(updateActivity.GetVolumeFromONTAP)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateLun)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInDB)
	s.env.RegisterActivity(updateActivity.UpdateQoSPolicyGroupForVolume)
	s.env.RegisterActivity(updateActivity.UpdateVolumePerformanceGroupInDB)
	backupActivity := activities.BackupActivity{SE: mockStorage}
	s.env.RegisterActivity(backupActivity.UpdateBackupMetadataIfExistsActivity)

	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 1000,
		Size:           1000,
		State:          "online",
	}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UpdateLun, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.LunResponse{ProviderResponse: vsa.ProviderResponse{Name: "/vol/vol1/lun1"}}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInDB, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupActivity.UpdateBackupMetadataIfExistsActivity, mock.Anything, mock.Anything).Return(nil)

	// Mock QoS update success
	newThroughput := int64(200)
	newIops := int64(2000)
	currentThroughput := int64(100)
	currentIops := int64(1000)
	s.env.OnActivity(updateActivity.UpdateQoSPolicyGroupForVolume, mock.Anything, mock.Anything, newThroughput, newIops, mock.Anything).Return(nil)
	// Mock UpdateVolumePerformanceGroupInDB failure (triggers rollback)
	s.env.OnActivity(updateActivity.UpdateVolumePerformanceGroupInDB, mock.Anything, mock.Anything).Return(errors.New("failed to update VPG in database"))
	// Mock rollback call with original values
	s.env.OnActivity(updateActivity.UpdateQoSPolicyGroupForVolume, mock.Anything, mock.Anything, currentThroughput, currentIops, mock.Anything).Return(nil).Maybe()

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
		Name:      "test_volume",
		Pool: &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			},
		},
		Account:                  &datamodel.Account{Name: "test_account"},
		SizeInBytes:              1000,
		VolumeAttributes:         &datamodel.VolumeAttributes{IsDataProtection: false},
		VolumePerformanceGroupID: sql.NullInt64{Int64: 1, Valid: true},
		VolumePerformanceGroup: &datamodel.VolumePerformanceGroup{
			BaseModel:        datamodel.BaseModel{ID: 1, UUID: "vpg-uuid"},
			IsAutoGen:        true,
			OntapQosPolicyID: "550e8400-e29b-41d4-a716-446655440000",
			ThroughputMibps:  100,
			Iops:             1000,
		},
	}
	params := &common.UpdateVolumeParams{
		ThroughputMibps: &newThroughput,
		Iops:            &newIops,
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	// Assert workflow failed and rollback was executed
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
	assert.Contains(s.T(), s.env.GetWorkflowError().Error(), "failed to update VPG in database")
	mockStorage.AssertNumberOfCalls(s.T(), "UpdateJob", 2)
}

func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_AutogeneratedVPG_UpdateQoS_RollbackExecution() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetNode)
	s.env.RegisterActivity(updateActivity.GetVolumeFromONTAP)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateLun)
	s.env.RegisterActivity(updateActivity.UpdateQoSPolicyGroupForVolume)
	s.env.RegisterActivity(updateActivity.UpdateVolumePerformanceGroupInDB)
	backupActivity := activities.BackupActivity{SE: mockStorage}
	s.env.RegisterActivity(backupActivity.UpdateBackupMetadataIfExistsActivity)
	volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)

	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 1000,
		Size:           1000,
		State:          "online",
	}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(updateActivity.UpdateLun, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.LunResponse{ProviderResponse: vsa.ProviderResponse{Name: "/vol/vol1/lun1"}}, nil).Maybe()
	s.env.OnActivity(backupActivity.UpdateBackupMetadataIfExistsActivity, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	// Mock QoS update success, but then UpdateVolumePerformanceGroupInDB fails (triggering rollback)
	newThroughput := int64(200)
	newIops := int64(2000)
	currentThroughput := int64(100)
	currentIops := int64(1000)

	// First call: UpdateQoSPolicyGroupForVolume succeeds (updates QoS in ONTAP)
	s.env.OnActivity(updateActivity.UpdateQoSPolicyGroupForVolume, mock.Anything, mock.Anything, newThroughput, newIops, mock.Anything).Return(nil).Once()

	// UpdateVolumePerformanceGroupInDB fails (may be retried, so allow multiple calls but always return error)
	s.env.OnActivity(updateActivity.UpdateVolumePerformanceGroupInDB, mock.Anything, mock.Anything).Return(errors.New("failed to update VPG in database"))

	// The workflow passes pointers to rollback manager, but we match the dereferenced values
	s.env.OnActivity(updateActivity.UpdateQoSPolicyGroupForVolume, mock.Anything, mock.Anything, currentThroughput, currentIops, mock.Anything).Return(nil).Once()

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
		Name:      "test_volume",
		Pool: &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			},
		},
		Account:                  &datamodel.Account{Name: "test_account"},
		SizeInBytes:              1000,
		VolumeAttributes:         &datamodel.VolumeAttributes{IsDataProtection: false},
		VolumePerformanceGroupID: sql.NullInt64{Int64: 1, Valid: true},
		VolumePerformanceGroup: &datamodel.VolumePerformanceGroup{
			BaseModel:        datamodel.BaseModel{ID: 1, UUID: "vpg-uuid"},
			IsAutoGen:        true,
			OntapQosPolicyID: "550e8400-e29b-41d4-a716-446655440000",
			ThroughputMibps:  currentThroughput,
			Iops:             currentIops,
		},
	}
	params := &common.UpdateVolumeParams{
		ThroughputMibps: &newThroughput,
		Iops:            &newIops,
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	// Assert workflow failed and rollback was executed
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
	assert.Contains(s.T(), s.env.GetWorkflowError().Error(), "failed to update VPG in database")

	// Verify that the rollback activity was called to restore original QoS values
	// The rollback call should have the original throughput and iops values
	s.env.AssertExpectations(s.T())
}

// ============================================================================
// QoS Update Workflow Tests - Non-Autogen to Autogen Conversion
// ============================================================================

func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_NonAutogenToAutogen_Success() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}
	vpgActivity := activities.VolumePerformanceGroupActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetNode)
	s.env.RegisterActivity(updateActivity.GetVolumeFromONTAP)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateLun)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInDB)
	s.env.RegisterActivity(updateActivity.UnassignQoSPolicyFromVolume)
	s.env.RegisterActivity(updateActivity.CreateAutoGeneratedQoSPolicyGroupForVolume)
	s.env.RegisterActivity(updateActivity.AssignQoSPolicyToVolume)
	s.env.RegisterActivity(vpgActivity.CreateVPGInDB)
	s.env.RegisterActivity(updateActivity.UpdateVolumePerformanceGroupInDBForVolume)
	s.env.RegisterActivity(vpgActivity.HardDeleteVPGInDB)
	backupActivity := activities.BackupActivity{SE: mockStorage}
	s.env.RegisterActivity(backupActivity.UpdateBackupMetadataIfExistsActivity)

	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 1000,
		Size:           1000,
		State:          "online",
	}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UpdateLun, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.LunResponse{ProviderResponse: vsa.ProviderResponse{Name: "/vol/vol1/lun1"}}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInDB, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupActivity.UpdateBackupMetadataIfExistsActivity, mock.Anything, mock.Anything).Return(nil)

	s.env.OnActivity(updateActivity.UnassignQoSPolicyFromVolume, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	newPolicyUUID := "660e8400-e29b-41d4-a716-446655440000"
	newQosPolicy := &vsa.QoSGroupPolicyResponse{
		Name:          "autoGenerated-test_volume-660e8400-e29b-41d4-a716-446655440000",
		UUID:          newPolicyUUID,
		SvmName:       "test-svm",
		MaxThroughput: 200,
		MaxIOPS:       2000,
		IsShared:      false,
	}
	s.env.OnActivity(updateActivity.CreateAutoGeneratedQoSPolicyGroupForVolume, mock.Anything, mock.Anything, int64(200), int64(2000), mock.Anything).Return(newQosPolicy, nil)
	s.env.OnActivity(updateActivity.AssignQoSPolicyToVolume, mock.Anything, mock.Anything, newQosPolicy.Name, mock.Anything).Return(nil)

	createdVPG := &datamodel.VolumePerformanceGroup{
		BaseModel:        datamodel.BaseModel{ID: 207, UUID: "new-autogen-vpg-uuid"},
		Name:             newQosPolicy.Name,
		PoolID:           1,
		AllocationType:   models.AllocationTypePerVolume,
		IsAutoGen:        true,
		ThroughputMibps:  200,
		Iops:             2000,
		OntapQosPolicyID: newPolicyUUID,
	}
	s.env.OnActivity(vpgActivity.CreateVPGInDB, mock.Anything, mock.Anything).Return(createdVPG, nil)
	s.env.OnActivity(updateActivity.UpdateVolumePerformanceGroupInDBForVolume, mock.Anything, "test-volume-uuid", mock.Anything).Return(nil)

	// Rollback activities (registered as Maybe since they only run on failure)
	s.env.OnActivity(updateActivity.AssignQoSPolicyToVolume, mock.Anything, mock.Anything, "old-vpg-qos-policy", mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(vpgActivity.HardDeleteVPGInDB, mock.Anything, mock.Anything).Return(nil).Maybe()

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
		Pool: &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			},
		},
		Account:     &datamodel.Account{Name: "test_account"},
		SizeInBytes: 1000,
		VolumeAttributes: &datamodel.VolumeAttributes{
			IsDataProtection: false,
			ExternalUUID:     "test-external-uuid",
		},
		Svm:                      &datamodel.Svm{Name: "test-svm"},
		VolumePerformanceGroupID: sql.NullInt64{Int64: 1, Valid: true},
		VolumePerformanceGroup: &datamodel.VolumePerformanceGroup{
			BaseModel:        datamodel.BaseModel{ID: 1, UUID: "vpg-uuid"},
			Name:             "old-vpg-qos-policy",
			IsAutoGen:        false,
			OntapQosPolicyID: "old-policy-uuid",
			ThroughputMibps:  100,
			Iops:             1000,
		},
	}
	newThroughput := int64(200)
	newIops := int64(2000)
	params := &common.UpdateVolumeParams{
		ThroughputMibps: &newThroughput,
		Iops:            &newIops,
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
	mockStorage.AssertNumberOfCalls(s.T(), "UpdateJob", 2)
}

func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_NonAutogenToAutogen_UnassignFailure() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetNode)
	s.env.RegisterActivity(updateActivity.GetVolumeFromONTAP)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateLun)
	s.env.RegisterActivity(updateActivity.UnassignQoSPolicyFromVolume)
	backupActivity := activities.BackupActivity{SE: mockStorage}
	s.env.RegisterActivity(backupActivity.UpdateBackupMetadataIfExistsActivity)
	volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)

	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 1000,
		Size:           1000,
		State:          "online",
	}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UpdateLun, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.LunResponse{ProviderResponse: vsa.ProviderResponse{Name: "/vol/vol1/lun1"}}, nil)
	s.env.OnActivity(backupActivity.UpdateBackupMetadataIfExistsActivity, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Mock UnassignQoSPolicyFromVolume failure
	s.env.OnActivity(updateActivity.UnassignQoSPolicyFromVolume, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("failed to unassign QoS policy"))

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
		Pool: &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			},
		},
		Account:     &datamodel.Account{Name: "test_account"},
		SizeInBytes: 1000,
		VolumeAttributes: &datamodel.VolumeAttributes{
			IsDataProtection: false,
			ExternalUUID:     "test-external-uuid",
		},
		Svm:                      &datamodel.Svm{Name: "test-svm"},
		VolumePerformanceGroupID: sql.NullInt64{Int64: 1, Valid: true},
		VolumePerformanceGroup: &datamodel.VolumePerformanceGroup{
			BaseModel:        datamodel.BaseModel{ID: 1, UUID: "vpg-uuid"},
			Name:             "old-vpg-qos-policy",
			IsAutoGen:        false,
			OntapQosPolicyID: "old-policy-uuid",
			ThroughputMibps:  100,
			Iops:             1000,
		},
	}
	newThroughput := int64(200)
	newIops := int64(2000)
	params := &common.UpdateVolumeParams{
		ThroughputMibps: &newThroughput,
		Iops:            &newIops,
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
	assert.Contains(s.T(), s.env.GetWorkflowError().Error(), "failed to unassign QoS policy")
}

// ============================================================================
// QoS Update Workflow Tests - VPG Reassignment Path
// ============================================================================

func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_VPGReassignment_Success() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}
	vpgActivity := activities.VolumePerformanceGroupActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetNode)
	s.env.RegisterActivity(updateActivity.GetVolumeFromONTAP)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateLun)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInDB)
	s.env.RegisterActivity(updateActivity.UnassignQoSPolicyFromVolume)
	s.env.RegisterActivity(vpgActivity.GetVolumePerformanceGroupByUUID)
	s.env.RegisterActivity(updateActivity.FindQoSGroupPolicyForVolume)
	s.env.RegisterActivity(updateActivity.AssignQoSPolicyToVolume)
	s.env.RegisterActivity(updateActivity.UpdateVolumePerformanceGroupInDBForVolume)
	backupActivity := activities.BackupActivity{SE: mockStorage}
	s.env.RegisterActivity(backupActivity.UpdateBackupMetadataIfExistsActivity)

	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 1000,
		Size:           1000,
		State:          "online",
	}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UpdateLun, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.LunResponse{ProviderResponse: vsa.ProviderResponse{Name: "/vol/vol1/lun1"}}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInDB, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupActivity.UpdateBackupMetadataIfExistsActivity, mock.Anything, mock.Anything).Return(nil)

	s.env.OnActivity(updateActivity.UnassignQoSPolicyFromVolume, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	newVPG := &datamodel.VolumePerformanceGroup{
		BaseModel:        datamodel.BaseModel{ID: 2, UUID: "new-vpg-uuid"},
		Name:             "new-vpg-qos-policy",
		PoolID:           1,
		OntapQosPolicyID: "new-policy-uuid",
		IsAutoGen:        false,
	}
	s.env.OnActivity(vpgActivity.GetVolumePerformanceGroupByUUID, mock.Anything, "new-vpg-uuid").Return(newVPG, nil)
	newQosPolicy := &vsa.QoSGroupPolicyResponse{
		Name:    "new-policy-name",
		UUID:    "new-policy-uuid",
		SvmName: "test-svm",
	}
	s.env.OnActivity(updateActivity.FindQoSGroupPolicyForVolume, mock.Anything, "new-vpg-qos-policy", "test-svm", mock.Anything).Return(newQosPolicy, nil)
	s.env.OnActivity(updateActivity.AssignQoSPolicyToVolume, mock.Anything, mock.Anything, newQosPolicy.Name, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UpdateVolumePerformanceGroupInDBForVolume, mock.Anything, mock.Anything, newVPG).Return(nil)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
		Pool: &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			},
		},
		PoolID:      1,
		Account:     &datamodel.Account{Name: "test_account"},
		SizeInBytes: 1000,
		VolumeAttributes: &datamodel.VolumeAttributes{
			IsDataProtection: false,
			ExternalUUID:     "test-external-uuid",
		},
		Svm:                      &datamodel.Svm{Name: "test-svm"},
		VolumePerformanceGroupID: sql.NullInt64{Int64: 1, Valid: true},
		VolumePerformanceGroup: &datamodel.VolumePerformanceGroup{
			BaseModel:        datamodel.BaseModel{ID: 1, UUID: "old-vpg-uuid"},
			Name:             "old-vpg-qos-policy",
			PoolID:           1,
			OntapQosPolicyID: "old-policy-uuid",
			IsAutoGen:        false,
		},
	}
	newVPGUUID := "new-vpg-uuid"
	params := &common.UpdateVolumeParams{
		VolumePerformanceGroupId: &newVPGUUID,
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
	mockStorage.AssertNumberOfCalls(s.T(), "UpdateJob", 2)
}

func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_VPGReassignment_DifferentPool() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}
	vpgActivity := activities.VolumePerformanceGroupActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetNode)
	s.env.RegisterActivity(updateActivity.GetVolumeFromONTAP)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateLun)
	s.env.RegisterActivity(updateActivity.UnassignQoSPolicyFromVolume)
	s.env.RegisterActivity(vpgActivity.GetVolumePerformanceGroupByUUID)
	backupActivity := activities.BackupActivity{SE: mockStorage}
	s.env.RegisterActivity(backupActivity.UpdateBackupMetadataIfExistsActivity)
	volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 1000,
		Size:           1000,
		State:          "online",
	}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UpdateLun, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.LunResponse{ProviderResponse: vsa.ProviderResponse{Name: "/vol/vol1/lun1"}}, nil)
	s.env.OnActivity(backupActivity.UpdateBackupMetadataIfExistsActivity, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	s.env.OnActivity(updateActivity.UnassignQoSPolicyFromVolume, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	newVPG := &datamodel.VolumePerformanceGroup{
		BaseModel:        datamodel.BaseModel{ID: 2, UUID: "new-vpg-uuid"},
		PoolID:           2, // Different pool than volume (which is pool 1)
		OntapQosPolicyID: "new-policy-uuid",
		IsAutoGen:        false,
	}
	s.env.OnActivity(vpgActivity.GetVolumePerformanceGroupByUUID, mock.Anything, "new-vpg-uuid").Return(newVPG, nil)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
		Pool: &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			},
		},
		PoolID:      1,
		Account:     &datamodel.Account{Name: "test_account"},
		SizeInBytes: 1000,
		VolumeAttributes: &datamodel.VolumeAttributes{
			IsDataProtection: false,
			ExternalUUID:     "test-external-uuid",
		},
		Svm:                      &datamodel.Svm{Name: "test-svm"},
		VolumePerformanceGroupID: sql.NullInt64{Int64: 1, Valid: true},
		VolumePerformanceGroup: &datamodel.VolumePerformanceGroup{
			BaseModel:        datamodel.BaseModel{ID: 1, UUID: "old-vpg-uuid"},
			Name:             "old-vpg-qos-policy",
			PoolID:           1,
			OntapQosPolicyID: "old-policy-uuid",
			IsAutoGen:        false,
		},
	}
	newVPGUUID := "new-vpg-uuid"
	params := &common.UpdateVolumeParams{
		VolumePerformanceGroupId: &newVPGUUID,
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
	assert.Contains(s.T(), s.env.GetWorkflowError().Error(), "does not belong to the same pool")
}

func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_VPGReassignment_VPGNotFound() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}
	vpgActivity := activities.VolumePerformanceGroupActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetNode)
	s.env.RegisterActivity(updateActivity.GetVolumeFromONTAP)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateLun)
	s.env.RegisterActivity(updateActivity.UnassignQoSPolicyFromVolume)
	s.env.RegisterActivity(vpgActivity.GetVolumePerformanceGroupByUUID)
	backupActivity := activities.BackupActivity{SE: mockStorage}
	s.env.RegisterActivity(backupActivity.UpdateBackupMetadataIfExistsActivity)
	volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)

	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 1000,
		Size:           1000,
		State:          "online",
	}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UpdateLun, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.LunResponse{ProviderResponse: vsa.ProviderResponse{Name: "/vol/vol1/lun1"}}, nil)
	s.env.OnActivity(backupActivity.UpdateBackupMetadataIfExistsActivity, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Mock VPG reassignment activities
	s.env.OnActivity(updateActivity.UnassignQoSPolicyFromVolume, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(vpgActivity.GetVolumePerformanceGroupByUUID, mock.Anything, "non-existent-vpg-uuid").Return(nil, errors.New("VPG not found"))

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
		Pool: &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			},
		},
		PoolID:      1,
		Account:     &datamodel.Account{Name: "test_account"},
		SizeInBytes: 1000,
		VolumeAttributes: &datamodel.VolumeAttributes{
			IsDataProtection: false,
			ExternalUUID:     "test-external-uuid",
		},
		Svm:                      &datamodel.Svm{Name: "test-svm"},
		VolumePerformanceGroupID: sql.NullInt64{Int64: 1, Valid: true},
		VolumePerformanceGroup: &datamodel.VolumePerformanceGroup{
			BaseModel:        datamodel.BaseModel{ID: 1, UUID: "old-vpg-uuid"},
			Name:             "old-vpg-qos-policy",
			PoolID:           1,
			OntapQosPolicyID: "old-policy-uuid",
			IsAutoGen:        false,
		},
	}
	nonExistentVPGUUID := "non-existent-vpg-uuid"
	params := &common.UpdateVolumeParams{
		VolumePerformanceGroupId: &nonExistentVPGUUID,
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
	assert.Contains(s.T(), s.env.GetWorkflowError().Error(), "VPG not found")
}

func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_VPGReassignment_MissingOntapQosPolicyID() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}
	vpgActivity := activities.VolumePerformanceGroupActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetNode)
	s.env.RegisterActivity(updateActivity.GetVolumeFromONTAP)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateLun)
	s.env.RegisterActivity(updateActivity.UnassignQoSPolicyFromVolume)
	s.env.RegisterActivity(vpgActivity.GetVolumePerformanceGroupByUUID)
	backupActivity := activities.BackupActivity{SE: mockStorage}
	s.env.RegisterActivity(backupActivity.UpdateBackupMetadataIfExistsActivity)
	volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)

	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 1000,
		Size:           1000,
		State:          "online",
	}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UpdateLun, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.LunResponse{ProviderResponse: vsa.ProviderResponse{Name: "/vol/vol1/lun1"}}, nil)
	s.env.OnActivity(backupActivity.UpdateBackupMetadataIfExistsActivity, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Mock VPG reassignment activities
	s.env.OnActivity(updateActivity.UnassignQoSPolicyFromVolume, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	newVPG := &datamodel.VolumePerformanceGroup{
		BaseModel:        datamodel.BaseModel{ID: 2, UUID: "new-vpg-uuid"},
		PoolID:           1,
		OntapQosPolicyID: "", // Missing OntapQosPolicyID
		IsAutoGen:        false,
	}
	s.env.OnActivity(vpgActivity.GetVolumePerformanceGroupByUUID, mock.Anything, "new-vpg-uuid").Return(newVPG, nil)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
		Pool: &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			},
		},
		PoolID:      1,
		Account:     &datamodel.Account{Name: "test_account"},
		SizeInBytes: 1000,
		VolumeAttributes: &datamodel.VolumeAttributes{
			IsDataProtection: false,
			ExternalUUID:     "test-external-uuid",
		},
		Svm:                      &datamodel.Svm{Name: "test-svm"},
		VolumePerformanceGroupID: sql.NullInt64{Int64: 1, Valid: true},
		VolumePerformanceGroup: &datamodel.VolumePerformanceGroup{
			BaseModel:        datamodel.BaseModel{ID: 1, UUID: "old-vpg-uuid"},
			Name:             "old-vpg-qos-policy",
			PoolID:           1,
			OntapQosPolicyID: "old-policy-uuid",
			IsAutoGen:        false,
		},
	}
	newVPGUUID := "new-vpg-uuid"
	params := &common.UpdateVolumeParams{
		VolumePerformanceGroupId: &newVPGUUID,
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
	assert.Contains(s.T(), s.env.GetWorkflowError().Error(), "has no OntapQosPolicyID")
}

func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_AutogeneratedVPG_UpdateQoS_OnlyIOPS() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetNode)
	s.env.RegisterActivity(updateActivity.GetVolumeFromONTAP)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateLun)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInDB)
	s.env.RegisterActivity(updateActivity.UpdateQoSPolicyGroupForVolume)
	s.env.RegisterActivity(updateActivity.UpdateVolumePerformanceGroupInDB)
	backupActivity := activities.BackupActivity{SE: mockStorage}
	s.env.RegisterActivity(backupActivity.UpdateBackupMetadataIfExistsActivity)

	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 1000,
		Size:           1000,
		State:          "online",
	}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UpdateLun, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.LunResponse{ProviderResponse: vsa.ProviderResponse{Name: "/vol/vol1/lun1"}}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInDB, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupActivity.UpdateBackupMetadataIfExistsActivity, mock.Anything, mock.Anything).Return(nil)

	// Mock QoS update - only IOPS provided, throughput uses current (100)
	currentThroughput := int64(100)
	newIops := int64(2000)
	s.env.OnActivity(updateActivity.UpdateQoSPolicyGroupForVolume, mock.Anything, mock.Anything, currentThroughput, newIops, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UpdateQoSPolicyGroupForVolume, mock.Anything, mock.Anything, int64(100), int64(1000), mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(updateActivity.UpdateVolumePerformanceGroupInDB, mock.Anything, mock.Anything).Return(nil)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
		Name:      "test_volume",
		Pool: &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			},
		},
		Account:                  &datamodel.Account{Name: "test_account"},
		SizeInBytes:              1000,
		VolumeAttributes:         &datamodel.VolumeAttributes{IsDataProtection: false},
		VolumePerformanceGroupID: sql.NullInt64{Int64: 1, Valid: true},
		VolumePerformanceGroup: &datamodel.VolumePerformanceGroup{
			BaseModel:        datamodel.BaseModel{ID: 1, UUID: "vpg-uuid"},
			IsAutoGen:        true,
			OntapQosPolicyID: "550e8400-e29b-41d4-a716-446655440000",
			ThroughputMibps:  100,
			Iops:             1000,
		},
	}
	params := &common.UpdateVolumeParams{
		// ThroughputMibps is nil, should use current value
		Iops: &newIops,
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
	mockStorage.AssertNumberOfCalls(s.T(), "UpdateJob", 2)
}

func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_AutogeneratedVPG_UpdateQoS_ValuesUnchanged() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetNode)
	s.env.RegisterActivity(updateActivity.GetVolumeFromONTAP)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateLun)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInDB)
	s.env.RegisterActivity(updateActivity.UpdateQoSPolicyGroupForVolume)
	backupActivity := activities.BackupActivity{SE: mockStorage}
	s.env.RegisterActivity(backupActivity.UpdateBackupMetadataIfExistsActivity)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 1000,
		Size:           1000,
		State:          "online",
	}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UpdateLun, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.LunResponse{ProviderResponse: vsa.ProviderResponse{Name: "/vol/vol1/lun1"}}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInDB, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupActivity.UpdateBackupMetadataIfExistsActivity, mock.Anything, mock.Anything).Return(nil)

	// Mock QoS update - values unchanged, UpdateQoSPolicyGroupForVolume should detect no update needed
	currentThroughput := int64(100)
	currentIops := int64(1000)
	s.env.OnActivity(updateActivity.UpdateQoSPolicyGroupForVolume, mock.Anything, mock.Anything, currentThroughput, currentIops, mock.Anything).Return(nil)
	// Mock rollback call (same values, so rollback would use same values)
	s.env.OnActivity(updateActivity.UpdateQoSPolicyGroupForVolume, mock.Anything, mock.Anything, currentThroughput, currentIops, mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(updateActivity.UpdateVolumePerformanceGroupInDB, mock.Anything, mock.Anything).Return(nil).Maybe()

	// Execute workflow
	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
		Name:      "test_volume",
		Pool: &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			},
		},
		Account:                  &datamodel.Account{Name: "test_account"},
		SizeInBytes:              1000,
		VolumeAttributes:         &datamodel.VolumeAttributes{IsDataProtection: false},
		VolumePerformanceGroupID: sql.NullInt64{Int64: 1, Valid: true},
		VolumePerformanceGroup: &datamodel.VolumePerformanceGroup{
			BaseModel:        datamodel.BaseModel{ID: 1, UUID: "vpg-uuid"},
			IsAutoGen:        true,
			OntapQosPolicyID: "550e8400-e29b-41d4-a716-446655440000",
			ThroughputMibps:  100,
			Iops:             1000,
		},
	}
	params := &common.UpdateVolumeParams{
		ThroughputMibps: &currentThroughput,
		Iops:            &currentIops,
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	// Assert workflow completed successfully (idempotent - no update needed)
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
	mockStorage.AssertNumberOfCalls(s.T(), "UpdateJob", 2)
}

func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_NonAutogenToAutogen_CreateQoSPolicyFailure() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetNode)
	s.env.RegisterActivity(updateActivity.GetVolumeFromONTAP)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateLun)
	s.env.RegisterActivity(updateActivity.UnassignQoSPolicyFromVolume)
	s.env.RegisterActivity(updateActivity.CreateAutoGeneratedQoSPolicyGroupForVolume)
	backupActivity := activities.BackupActivity{SE: mockStorage}
	s.env.RegisterActivity(backupActivity.UpdateBackupMetadataIfExistsActivity)
	volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 1000,
		Size:           1000,
		State:          "online",
	}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UpdateLun, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.LunResponse{ProviderResponse: vsa.ProviderResponse{Name: "/vol/vol1/lun1"}}, nil)
	s.env.OnActivity(backupActivity.UpdateBackupMetadataIfExistsActivity, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Mock QoS conversion activities
	s.env.OnActivity(updateActivity.UnassignQoSPolicyFromVolume, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.CreateAutoGeneratedQoSPolicyGroupForVolume, mock.Anything, mock.Anything, int64(200), int64(2000), mock.Anything).Return(nil, errors.New("failed to create QoS policy"))

	// Execute workflow
	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
		Pool: &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			},
		},
		Account:     &datamodel.Account{Name: "test_account"},
		SizeInBytes: 1000,
		VolumeAttributes: &datamodel.VolumeAttributes{
			IsDataProtection: false,
			ExternalUUID:     "test-external-uuid",
		},
		Svm:                      &datamodel.Svm{Name: "test-svm"},
		VolumePerformanceGroupID: sql.NullInt64{Int64: 1, Valid: true},
		VolumePerformanceGroup: &datamodel.VolumePerformanceGroup{
			BaseModel:        datamodel.BaseModel{ID: 1, UUID: "vpg-uuid"},
			Name:             "old-vpg-qos-policy",
			IsAutoGen:        false,
			OntapQosPolicyID: "old-policy-uuid",
			ThroughputMibps:  100,
			Iops:             1000,
		},
	}
	newThroughput := int64(200)
	newIops := int64(2000)
	params := &common.UpdateVolumeParams{
		ThroughputMibps: &newThroughput,
		Iops:            &newIops,
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	// Assert workflow failed
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
	assert.Contains(s.T(), s.env.GetWorkflowError().Error(), "failed to create QoS policy")
}

func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_NonAutogenToAutogen_AssignQoSPolicyFailure() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetNode)
	s.env.RegisterActivity(updateActivity.GetVolumeFromONTAP)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateLun)
	s.env.RegisterActivity(updateActivity.UnassignQoSPolicyFromVolume)
	s.env.RegisterActivity(updateActivity.CreateAutoGeneratedQoSPolicyGroupForVolume)
	s.env.RegisterActivity(updateActivity.AssignQoSPolicyToVolume)
	backupActivity := activities.BackupActivity{SE: mockStorage}
	s.env.RegisterActivity(backupActivity.UpdateBackupMetadataIfExistsActivity)
	volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 1000,
		Size:           1000,
		State:          "online",
	}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UpdateLun, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.LunResponse{ProviderResponse: vsa.ProviderResponse{Name: "/vol/vol1/lun1"}}, nil)
	s.env.OnActivity(backupActivity.UpdateBackupMetadataIfExistsActivity, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Mock QoS conversion activities
	s.env.OnActivity(updateActivity.UnassignQoSPolicyFromVolume, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	// QoS policy name follows pattern: autoGenerated-{volumeName}-{uuid}
	// The UUID should match the OntapQosPolicyID that will be set in the VPG
	newPolicyUUID := "770e8400-e29b-41d4-a716-446655440000"
	newQosPolicy := &vsa.QoSGroupPolicyResponse{
		Name:          "autoGenerated-test_volume-770e8400-e29b-41d4-a716-446655440000",
		UUID:          newPolicyUUID,
		SvmName:       "test-svm",
		MaxThroughput: 200,
		MaxIOPS:       2000,
		IsShared:      false,
	}
	s.env.OnActivity(updateActivity.CreateAutoGeneratedQoSPolicyGroupForVolume, mock.Anything, mock.Anything, int64(200), int64(2000), mock.Anything).Return(newQosPolicy, nil)
	s.env.OnActivity(updateActivity.AssignQoSPolicyToVolume, mock.Anything, mock.Anything, newQosPolicy.Name, mock.Anything).Return(errors.New("failed to assign QoS policy"))

	// Execute workflow
	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
		Pool: &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			},
		},
		Account:     &datamodel.Account{Name: "test_account"},
		SizeInBytes: 1000,
		VolumeAttributes: &datamodel.VolumeAttributes{
			IsDataProtection: false,
			ExternalUUID:     "test-external-uuid",
		},
		Svm:                      &datamodel.Svm{Name: "test-svm"},
		VolumePerformanceGroupID: sql.NullInt64{Int64: 1, Valid: true},
		VolumePerformanceGroup: &datamodel.VolumePerformanceGroup{
			BaseModel:        datamodel.BaseModel{ID: 1, UUID: "vpg-uuid"},
			IsAutoGen:        false,
			OntapQosPolicyID: "old-policy-uuid",
			ThroughputMibps:  100,
			Iops:             1000,
		},
	}
	newThroughput := int64(200)
	newIops := int64(2000)
	params := &common.UpdateVolumeParams{
		ThroughputMibps: &newThroughput,
		Iops:            &newIops,
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	// Assert workflow failed
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
	assert.Contains(s.T(), s.env.GetWorkflowError().Error(), "failed to assign QoS policy")
}

func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_NonAutogenToAutogen_CreateVPGInDBFailure() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}
	vpgActivity := activities.VolumePerformanceGroupActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetNode)
	s.env.RegisterActivity(updateActivity.GetVolumeFromONTAP)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateLun)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInDB)
	s.env.RegisterActivity(updateActivity.UnassignQoSPolicyFromVolume)
	s.env.RegisterActivity(updateActivity.CreateAutoGeneratedQoSPolicyGroupForVolume)
	s.env.RegisterActivity(updateActivity.AssignQoSPolicyToVolume)
	s.env.RegisterActivity(vpgActivity.CreateVPGInDB)
	s.env.RegisterActivity(updateActivity.UpdateVolumePerformanceGroupInDBForVolume)
	s.env.RegisterActivity(vpgActivity.HardDeleteVPGInDB)
	backupActivity := activities.BackupActivity{SE: mockStorage}
	s.env.RegisterActivity(backupActivity.UpdateBackupMetadataIfExistsActivity)
	volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)

	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 1000,
		Size:           1000,
		State:          "online",
	}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UpdateLun, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.LunResponse{ProviderResponse: vsa.ProviderResponse{Name: "/vol/vol1/lun1"}}, nil)
	s.env.OnActivity(backupActivity.UpdateBackupMetadataIfExistsActivity, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	s.env.OnActivity(updateActivity.UnassignQoSPolicyFromVolume, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	newPolicyUUID := "880e8400-e29b-41d4-a716-446655440000"
	newQosPolicy := &vsa.QoSGroupPolicyResponse{
		Name:          "autoGenerated-test_volume-880e8400-e29b-41d4-a716-446655440000",
		UUID:          newPolicyUUID,
		SvmName:       "test-svm",
		MaxThroughput: 200,
		MaxIOPS:       2000,
		IsShared:      false,
	}
	s.env.OnActivity(updateActivity.CreateAutoGeneratedQoSPolicyGroupForVolume, mock.Anything, mock.Anything, int64(200), int64(2000), mock.Anything).Return(newQosPolicy, nil)
	s.env.OnActivity(updateActivity.AssignQoSPolicyToVolume, mock.Anything, mock.Anything, newQosPolicy.Name, mock.Anything).Return(nil)

	// CreateVPGInDB fails — should cause workflow to fail
	s.env.OnActivity(vpgActivity.CreateVPGInDB, mock.Anything, mock.Anything).Return(nil, errors.New("failed to create VPG in database"))

	// Rollback activities
	s.env.OnActivity(updateActivity.AssignQoSPolicyToVolume, mock.Anything, mock.Anything, "old-vpg-qos-policy", mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(updateActivity.UpdateVolumePerformanceGroupInDBForVolume, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(vpgActivity.HardDeleteVPGInDB, mock.Anything, mock.Anything).Return(nil).Maybe()

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
		Pool: &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			},
		},
		Account:     &datamodel.Account{Name: "test_account"},
		SizeInBytes: 1000,
		VolumeAttributes: &datamodel.VolumeAttributes{
			IsDataProtection: false,
			ExternalUUID:     "test-external-uuid",
		},
		Svm:                      &datamodel.Svm{Name: "test-svm"},
		VolumePerformanceGroupID: sql.NullInt64{Int64: 1, Valid: true},
		VolumePerformanceGroup: &datamodel.VolumePerformanceGroup{
			BaseModel:        datamodel.BaseModel{ID: 1, UUID: "vpg-uuid"},
			IsAutoGen:        false,
			OntapQosPolicyID: "old-policy-uuid",
			ThroughputMibps:  100,
			Iops:             1000,
		},
	}
	newThroughput := int64(200)
	newIops := int64(2000)
	params := &common.UpdateVolumeParams{
		ThroughputMibps: &newThroughput,
		Iops:            &newIops,
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
	assert.Contains(s.T(), s.env.GetWorkflowError().Error(), "failed to create VPG in database")
}

func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_NonAutogenToAutogen_RollbackExecution() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}
	vpgActivity := activities.VolumePerformanceGroupActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetNode)
	s.env.RegisterActivity(updateActivity.GetVolumeFromONTAP)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateLun)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInDB)
	s.env.RegisterActivity(updateActivity.UnassignQoSPolicyFromVolume)
	s.env.RegisterActivity(updateActivity.CreateAutoGeneratedQoSPolicyGroupForVolume)
	s.env.RegisterActivity(updateActivity.AssignQoSPolicyToVolume)
	s.env.RegisterActivity(vpgActivity.CreateVPGInDB)
	s.env.RegisterActivity(updateActivity.UpdateVolumePerformanceGroupInDBForVolume)
	s.env.RegisterActivity(vpgActivity.HardDeleteVPGInDB)
	backupActivity := activities.BackupActivity{SE: mockStorage}
	s.env.RegisterActivity(backupActivity.UpdateBackupMetadataIfExistsActivity)
	volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)

	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 1000,
		Size:           1000,
		State:          "online",
	}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(updateActivity.UpdateLun, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.LunResponse{ProviderResponse: vsa.ProviderResponse{Name: "/vol/vol1/lun1"}}, nil).Maybe()
	s.env.OnActivity(backupActivity.UpdateBackupMetadataIfExistsActivity, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	s.env.OnActivity(updateActivity.UnassignQoSPolicyFromVolume, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	newPolicyUUID := "990e8400-e29b-41d4-a716-446655440000"
	newQosPolicy := &vsa.QoSGroupPolicyResponse{
		Name:          "autoGenerated-test_volume-990e8400-e29b-41d4-a716-446655440000",
		UUID:          newPolicyUUID,
		SvmName:       "test-svm",
		MaxThroughput: 200,
		MaxIOPS:       2000,
		IsShared:      false,
	}
	s.env.OnActivity(updateActivity.CreateAutoGeneratedQoSPolicyGroupForVolume, mock.Anything, mock.Anything, int64(200), int64(2000), mock.Anything).Return(newQosPolicy, nil)
	s.env.OnActivity(updateActivity.AssignQoSPolicyToVolume, mock.Anything, mock.Anything, newQosPolicy.Name, mock.Anything).Return(nil)

	createdVPG := &datamodel.VolumePerformanceGroup{
		BaseModel:        datamodel.BaseModel{ID: 207, UUID: "new-autogen-vpg-uuid"},
		Name:             newQosPolicy.Name,
		PoolID:           1,
		AllocationType:   models.AllocationTypePerVolume,
		IsAutoGen:        true,
		ThroughputMibps:  200,
		Iops:             2000,
		OntapQosPolicyID: newPolicyUUID,
	}
	s.env.OnActivity(vpgActivity.CreateVPGInDB, mock.Anything, mock.Anything).Return(createdVPG, nil)
	s.env.OnActivity(updateActivity.UpdateVolumePerformanceGroupInDBForVolume, mock.Anything, "test-volume-uuid", mock.Anything).Return(nil)

	// UpdateVolumeInDB fails, triggering rollback
	s.env.OnActivity(updateActivity.UpdateVolumeInDB, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("failed to update volume in DB"))

	// Rollback activities
	s.env.OnActivity(updateActivity.UpdateVolumePerformanceGroupInDBForVolume, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(updateActivity.AssignQoSPolicyToVolume, mock.Anything, mock.Anything, "old-vpg-qos-policy", mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(vpgActivity.HardDeleteVPGInDB, mock.Anything, mock.Anything).Return(nil).Maybe()

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
		Pool: &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			},
		},
		Account:     &datamodel.Account{Name: "test_account"},
		SizeInBytes: 1000,
		VolumeAttributes: &datamodel.VolumeAttributes{
			IsDataProtection: false,
			ExternalUUID:     "test-external-uuid",
		},
		Svm:                      &datamodel.Svm{Name: "test-svm"},
		VolumePerformanceGroupID: sql.NullInt64{Int64: 1, Valid: true},
		VolumePerformanceGroup: &datamodel.VolumePerformanceGroup{
			BaseModel:        datamodel.BaseModel{ID: 1, UUID: "vpg-uuid"},
			Name:             "old-vpg-qos-policy",
			IsAutoGen:        false,
			OntapQosPolicyID: "old-policy-uuid",
			ThroughputMibps:  100,
			Iops:             1000,
		},
	}
	newThroughput := int64(200)
	newIops := int64(2000)
	params := &common.UpdateVolumeParams{
		ThroughputMibps: &newThroughput,
		Iops:            &newIops,
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
	assert.Contains(s.T(), s.env.GetWorkflowError().Error(), "failed to update volume in DB")

	s.env.AssertExpectations(s.T())
}

func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_NonAutogenToAutogen_RollbackExecution_EmptyOntapQosPolicyID() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}
	vpgActivity := activities.VolumePerformanceGroupActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetNode)
	s.env.RegisterActivity(updateActivity.GetVolumeFromONTAP)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateLun)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInDB)
	s.env.RegisterActivity(updateActivity.UnassignQoSPolicyFromVolume)
	s.env.RegisterActivity(updateActivity.CreateAutoGeneratedQoSPolicyGroupForVolume)
	s.env.RegisterActivity(updateActivity.AssignQoSPolicyToVolume)
	s.env.RegisterActivity(vpgActivity.CreateVPGInDB)
	s.env.RegisterActivity(updateActivity.UpdateVolumePerformanceGroupInDBForVolume)
	s.env.RegisterActivity(vpgActivity.HardDeleteVPGInDB)
	backupActivity := activities.BackupActivity{SE: mockStorage}
	s.env.RegisterActivity(backupActivity.UpdateBackupMetadataIfExistsActivity)
	volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)

	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 1000,
		Size:           1000,
		State:          "online",
	}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(updateActivity.UpdateLun, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.LunResponse{ProviderResponse: vsa.ProviderResponse{Name: "/vol/vol1/lun1"}}, nil).Maybe()
	s.env.OnActivity(backupActivity.UpdateBackupMetadataIfExistsActivity, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	s.env.OnActivity(updateActivity.UnassignQoSPolicyFromVolume, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	newPolicyUUID := "990e8400-e29b-41d4-a716-446655440000"
	newQosPolicy := &vsa.QoSGroupPolicyResponse{
		Name:          "autoGenerated-test_volume-990e8400-e29b-41d4-a716-446655440000",
		UUID:          newPolicyUUID,
		SvmName:       "test-svm",
		MaxThroughput: 200,
		MaxIOPS:       2000,
		IsShared:      false,
	}
	s.env.OnActivity(updateActivity.CreateAutoGeneratedQoSPolicyGroupForVolume, mock.Anything, mock.Anything, int64(200), int64(2000), mock.Anything).Return(newQosPolicy, nil)
	s.env.OnActivity(updateActivity.AssignQoSPolicyToVolume, mock.Anything, mock.Anything, newQosPolicy.Name, mock.Anything).Return(nil)

	createdVPG := &datamodel.VolumePerformanceGroup{
		BaseModel:        datamodel.BaseModel{ID: 207, UUID: "new-autogen-vpg-uuid"},
		Name:             newQosPolicy.Name,
		PoolID:           1,
		AllocationType:   models.AllocationTypePerVolume,
		IsAutoGen:        true,
		ThroughputMibps:  200,
		Iops:             2000,
		OntapQosPolicyID: newPolicyUUID,
	}
	s.env.OnActivity(vpgActivity.CreateVPGInDB, mock.Anything, mock.Anything).Return(createdVPG, nil)
	s.env.OnActivity(updateActivity.UpdateVolumePerformanceGroupInDBForVolume, mock.Anything, "test-volume-uuid", mock.Anything).Return(nil)

	// UpdateVolumeInDB fails, triggering rollback
	s.env.OnActivity(updateActivity.UpdateVolumeInDB, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("failed to update volume in DB"))

	// Rollback activities — old VPG has empty OntapQosPolicyID so AssignQoSPolicyToVolume is skipped
	s.env.OnActivity(updateActivity.UpdateVolumePerformanceGroupInDBForVolume, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(vpgActivity.HardDeleteVPGInDB, mock.Anything, mock.Anything).Return(nil).Maybe()

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
		Pool: &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			},
		},
		Account:     &datamodel.Account{Name: "test_account"},
		SizeInBytes: 1000,
		VolumeAttributes: &datamodel.VolumeAttributes{
			IsDataProtection: false,
			ExternalUUID:     "test-external-uuid",
		},
		Svm:                      &datamodel.Svm{Name: "test-svm"},
		VolumePerformanceGroupID: sql.NullInt64{Int64: 1, Valid: true},
		VolumePerformanceGroup: &datamodel.VolumePerformanceGroup{
			BaseModel:        datamodel.BaseModel{ID: 1, UUID: "vpg-uuid"},
			IsAutoGen:        false,
			OntapQosPolicyID: "",
			ThroughputMibps:  100,
			Iops:             1000,
		},
	}
	newThroughput := int64(200)
	newIops := int64(2000)
	params := &common.UpdateVolumeParams{
		ThroughputMibps: &newThroughput,
		Iops:            &newIops,
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
	assert.Contains(s.T(), s.env.GetWorkflowError().Error(), "failed to update volume in DB")

	s.env.AssertExpectations(s.T())
}

func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_VPGReassignment_UnassignQoSPolicyFailure() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetNode)
	s.env.RegisterActivity(updateActivity.GetVolumeFromONTAP)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateLun)
	s.env.RegisterActivity(updateActivity.UnassignQoSPolicyFromVolume)
	backupActivity := activities.BackupActivity{SE: mockStorage}
	s.env.RegisterActivity(backupActivity.UpdateBackupMetadataIfExistsActivity)
	volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)

	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 1000,
		Size:           1000,
		State:          "online",
	}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UpdateLun, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.LunResponse{ProviderResponse: vsa.ProviderResponse{Name: "/vol/vol1/lun1"}}, nil)
	s.env.OnActivity(backupActivity.UpdateBackupMetadataIfExistsActivity, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Mock UnassignQoSPolicyFromVolume failure
	s.env.OnActivity(updateActivity.UnassignQoSPolicyFromVolume, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("failed to unassign QoS policy"))

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
		Pool: &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			},
		},
		PoolID:      1,
		Account:     &datamodel.Account{Name: "test_account"},
		SizeInBytes: 1000,
		VolumeAttributes: &datamodel.VolumeAttributes{
			IsDataProtection: false,
			ExternalUUID:     "test-external-uuid",
		},
		Svm:                      &datamodel.Svm{Name: "test-svm"},
		VolumePerformanceGroupID: sql.NullInt64{Int64: 1, Valid: true},
		VolumePerformanceGroup: &datamodel.VolumePerformanceGroup{
			BaseModel:        datamodel.BaseModel{ID: 1, UUID: "old-vpg-uuid"},
			Name:             "old-vpg-qos-policy",
			PoolID:           1,
			OntapQosPolicyID: "old-policy-uuid",
			IsAutoGen:        false,
		},
	}
	newVPGUUID := "new-vpg-uuid"
	params := &common.UpdateVolumeParams{
		VolumePerformanceGroupId: &newVPGUUID,
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	// Assert workflow failed
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
	assert.Contains(s.T(), s.env.GetWorkflowError().Error(), "failed to unassign QoS policy")
}

func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_VPGReassignment_FindQoSGroupPolicyFailure() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}
	vpgActivity := activities.VolumePerformanceGroupActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetNode)
	s.env.RegisterActivity(updateActivity.GetVolumeFromONTAP)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateLun)
	s.env.RegisterActivity(updateActivity.UnassignQoSPolicyFromVolume)
	s.env.RegisterActivity(vpgActivity.GetVolumePerformanceGroupByUUID)
	s.env.RegisterActivity(updateActivity.FindQoSGroupPolicyForVolume)
	backupActivity := activities.BackupActivity{SE: mockStorage}
	s.env.RegisterActivity(backupActivity.UpdateBackupMetadataIfExistsActivity)
	volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 1000,
		Size:           1000,
		State:          "online",
	}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UpdateLun, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.LunResponse{ProviderResponse: vsa.ProviderResponse{Name: "/vol/vol1/lun1"}}, nil)
	s.env.OnActivity(backupActivity.UpdateBackupMetadataIfExistsActivity, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Mock VPG reassignment activities
	s.env.OnActivity(updateActivity.UnassignQoSPolicyFromVolume, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	newVPG := &datamodel.VolumePerformanceGroup{
		BaseModel:        datamodel.BaseModel{ID: 2, UUID: "new-vpg-uuid"},
		Name:             "new-vpg-qos-policy",
		PoolID:           1,
		OntapQosPolicyID: "new-policy-uuid",
		IsAutoGen:        false,
	}
	s.env.OnActivity(vpgActivity.GetVolumePerformanceGroupByUUID, mock.Anything, "new-vpg-uuid").Return(newVPG, nil)
	s.env.OnActivity(updateActivity.FindQoSGroupPolicyForVolume, mock.Anything, "new-vpg-qos-policy", "test-svm", mock.Anything).Return(nil, errors.New("failed to find QoS policy"))

	// Execute workflow
	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
		Pool: &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			},
		},
		PoolID:      1,
		Account:     &datamodel.Account{Name: "test_account"},
		SizeInBytes: 1000,
		VolumeAttributes: &datamodel.VolumeAttributes{
			IsDataProtection: false,
			ExternalUUID:     "test-external-uuid",
		},
		Svm:                      &datamodel.Svm{Name: "test-svm"},
		VolumePerformanceGroupID: sql.NullInt64{Int64: 1, Valid: true},
		VolumePerformanceGroup: &datamodel.VolumePerformanceGroup{
			BaseModel:        datamodel.BaseModel{ID: 1, UUID: "old-vpg-uuid"},
			Name:             "old-vpg-qos-policy",
			PoolID:           1,
			OntapQosPolicyID: "old-policy-uuid",
			IsAutoGen:        false,
		},
	}
	newVPGUUID := "new-vpg-uuid"
	params := &common.UpdateVolumeParams{
		VolumePerformanceGroupId: &newVPGUUID,
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	// Assert workflow failed
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
	assert.Contains(s.T(), s.env.GetWorkflowError().Error(), "failed to find QoS policy")
}

func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_VPGReassignment_AssignQoSPolicyFailure() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}
	vpgActivity := activities.VolumePerformanceGroupActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetNode)
	s.env.RegisterActivity(updateActivity.GetVolumeFromONTAP)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateLun)
	s.env.RegisterActivity(updateActivity.UnassignQoSPolicyFromVolume)
	s.env.RegisterActivity(vpgActivity.GetVolumePerformanceGroupByUUID)
	s.env.RegisterActivity(updateActivity.FindQoSGroupPolicyForVolume)
	s.env.RegisterActivity(updateActivity.AssignQoSPolicyToVolume)
	backupActivity := activities.BackupActivity{SE: mockStorage}
	s.env.RegisterActivity(backupActivity.UpdateBackupMetadataIfExistsActivity)
	volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 1000,
		Size:           1000,
		State:          "online",
	}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UpdateLun, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.LunResponse{ProviderResponse: vsa.ProviderResponse{Name: "/vol/vol1/lun1"}}, nil)
	s.env.OnActivity(backupActivity.UpdateBackupMetadataIfExistsActivity, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Mock VPG reassignment activities
	s.env.OnActivity(updateActivity.UnassignQoSPolicyFromVolume, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	newVPG := &datamodel.VolumePerformanceGroup{
		BaseModel:        datamodel.BaseModel{ID: 2, UUID: "new-vpg-uuid"},
		Name:             "new-vpg-qos-policy",
		PoolID:           1,
		OntapQosPolicyID: "new-policy-uuid",
		IsAutoGen:        false,
	}
	s.env.OnActivity(vpgActivity.GetVolumePerformanceGroupByUUID, mock.Anything, "new-vpg-uuid").Return(newVPG, nil)
	newQosPolicy := &vsa.QoSGroupPolicyResponse{
		Name:    "new-policy-name",
		UUID:    "new-policy-uuid",
		SvmName: "test-svm",
	}
	s.env.OnActivity(updateActivity.FindQoSGroupPolicyForVolume, mock.Anything, "new-vpg-qos-policy", "test-svm", mock.Anything).Return(newQosPolicy, nil)
	s.env.OnActivity(updateActivity.AssignQoSPolicyToVolume, mock.Anything, mock.Anything, newQosPolicy.Name, mock.Anything).Return(errors.New("failed to assign QoS policy"))

	// Execute workflow
	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
		Pool: &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			},
		},
		PoolID:      1,
		Account:     &datamodel.Account{Name: "test_account"},
		SizeInBytes: 1000,
		VolumeAttributes: &datamodel.VolumeAttributes{
			IsDataProtection: false,
			ExternalUUID:     "test-external-uuid",
		},
		Svm:                      &datamodel.Svm{Name: "test-svm"},
		VolumePerformanceGroupID: sql.NullInt64{Int64: 1, Valid: true},
		VolumePerformanceGroup: &datamodel.VolumePerformanceGroup{
			BaseModel:        datamodel.BaseModel{ID: 1, UUID: "old-vpg-uuid"},
			Name:             "old-vpg-qos-policy",
			PoolID:           1,
			OntapQosPolicyID: "old-policy-uuid",
			IsAutoGen:        false,
		},
	}
	newVPGUUID := "new-vpg-uuid"
	params := &common.UpdateVolumeParams{
		VolumePerformanceGroupId: &newVPGUUID,
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	// Assert workflow failed
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
	assert.Contains(s.T(), s.env.GetWorkflowError().Error(), "failed to assign QoS policy")
}

func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_VPGReassignment_UpdateVolumePerformanceGroupInDBForVolumeFailure() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}
	vpgActivity := activities.VolumePerformanceGroupActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetNode)
	s.env.RegisterActivity(updateActivity.GetVolumeFromONTAP)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateLun)
	s.env.RegisterActivity(updateActivity.UnassignQoSPolicyFromVolume)
	s.env.RegisterActivity(vpgActivity.GetVolumePerformanceGroupByUUID)
	s.env.RegisterActivity(updateActivity.FindQoSGroupPolicyForVolume)
	s.env.RegisterActivity(updateActivity.AssignQoSPolicyToVolume)
	s.env.RegisterActivity(updateActivity.UpdateVolumePerformanceGroupInDBForVolume)
	backupActivity := activities.BackupActivity{SE: mockStorage}
	s.env.RegisterActivity(backupActivity.UpdateBackupMetadataIfExistsActivity)
	volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)

	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 1000,
		Size:           1000,
		State:          "online",
	}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(updateActivity.UpdateLun, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.LunResponse{ProviderResponse: vsa.ProviderResponse{Name: "/vol/vol1/lun1"}}, nil).Maybe()
	s.env.OnActivity(backupActivity.UpdateBackupMetadataIfExistsActivity, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	// Mock VPG reassignment activities - all succeed up to UpdateVolumePerformanceGroupInDBForVolume
	s.env.OnActivity(updateActivity.UnassignQoSPolicyFromVolume, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	newVPG := &datamodel.VolumePerformanceGroup{
		BaseModel:        datamodel.BaseModel{ID: 2, UUID: "new-vpg-uuid"},
		Name:             "new-vpg-qos-policy",
		PoolID:           1,
		OntapQosPolicyID: "new-policy-uuid",
		IsAutoGen:        false,
	}
	s.env.OnActivity(vpgActivity.GetVolumePerformanceGroupByUUID, mock.Anything, "new-vpg-uuid").Return(newVPG, nil)
	newQosPolicy := &vsa.QoSGroupPolicyResponse{
		Name:    "new-policy-name",
		UUID:    "new-policy-uuid",
		SvmName: "test-svm",
	}
	s.env.OnActivity(updateActivity.FindQoSGroupPolicyForVolume, mock.Anything, "new-vpg-qos-policy", "test-svm", mock.Anything).Return(newQosPolicy, nil)
	s.env.OnActivity(updateActivity.AssignQoSPolicyToVolume, mock.Anything, mock.Anything, newQosPolicy.Name, mock.Anything).Return(nil)
	// UpdateVolumePerformanceGroupInDBForVolume fails (may be retried)
	s.env.OnActivity(updateActivity.UpdateVolumePerformanceGroupInDBForVolume, mock.Anything, mock.Anything, newVPG).Return(errors.New("failed to update VPG in database for volume"))

	oldVPG := &datamodel.VolumePerformanceGroup{
		BaseModel:        datamodel.BaseModel{ID: 1, UUID: "old-vpg-uuid"},
		Name:             "old-vpg-qos-policy",
		PoolID:           1,
		OntapQosPolicyID: "old-policy-uuid",
		IsAutoGen:        false,
	}
	s.env.OnActivity(updateActivity.UpdateVolumePerformanceGroupInDBForVolume, mock.Anything, mock.Anything, oldVPG).Return(nil)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
		Pool: &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			},
		},
		PoolID:      1,
		Account:     &datamodel.Account{Name: "test_account"},
		SizeInBytes: 1000,
		VolumeAttributes: &datamodel.VolumeAttributes{
			IsDataProtection: false,
			ExternalUUID:     "test-external-uuid",
		},
		Svm:                      &datamodel.Svm{Name: "test-svm"},
		VolumePerformanceGroupID: sql.NullInt64{Int64: 1, Valid: true},
		VolumePerformanceGroup: &datamodel.VolumePerformanceGroup{
			BaseModel:        datamodel.BaseModel{ID: 1, UUID: "old-vpg-uuid"},
			Name:             "old-vpg-qos-policy",
			PoolID:           1,
			OntapQosPolicyID: "old-policy-uuid",
			IsAutoGen:        false,
		},
	}
	newVPGUUID := "new-vpg-uuid"
	params := &common.UpdateVolumeParams{
		VolumePerformanceGroupId: &newVPGUUID,
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	// Assert workflow failed
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
	assert.Contains(s.T(), s.env.GetWorkflowError().Error(), "failed to update VPG in database for volume")
}

func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_VPGReassignment_NoCurrentVPG() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}
	vpgActivity := activities.VolumePerformanceGroupActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetNode)
	s.env.RegisterActivity(updateActivity.GetVolumeFromONTAP)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateLun)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInDB)
	s.env.RegisterActivity(vpgActivity.GetVolumePerformanceGroupByUUID)
	s.env.RegisterActivity(updateActivity.FindQoSGroupPolicyForVolume)
	s.env.RegisterActivity(updateActivity.AssignQoSPolicyToVolume)
	s.env.RegisterActivity(updateActivity.UpdateVolumePerformanceGroupInDBForVolume)
	backupActivity := activities.BackupActivity{SE: mockStorage}
	s.env.RegisterActivity(backupActivity.UpdateBackupMetadataIfExistsActivity)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 1000,
		Size:           1000,
		State:          "online",
	}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UpdateLun, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.LunResponse{ProviderResponse: vsa.ProviderResponse{Name: "/vol/vol1/lun1"}}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInDB, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupActivity.UpdateBackupMetadataIfExistsActivity, mock.Anything, mock.Anything).Return(nil)

	// Mock VPG reassignment activities - volume has no current VPG
	newVPG := &datamodel.VolumePerformanceGroup{
		BaseModel:        datamodel.BaseModel{ID: 2, UUID: "new-vpg-uuid"},
		Name:             "new-vpg-qos-policy",
		PoolID:           1,
		OntapQosPolicyID: "new-policy-uuid",
		IsAutoGen:        false,
	}
	s.env.OnActivity(vpgActivity.GetVolumePerformanceGroupByUUID, mock.Anything, "new-vpg-uuid").Return(newVPG, nil)
	newQosPolicy := &vsa.QoSGroupPolicyResponse{
		Name:    "new-policy-name",
		UUID:    "new-policy-uuid",
		SvmName: "test-svm",
	}
	s.env.OnActivity(updateActivity.FindQoSGroupPolicyForVolume, mock.Anything, "new-vpg-qos-policy", "test-svm", mock.Anything).Return(newQosPolicy, nil)
	s.env.OnActivity(updateActivity.AssignQoSPolicyToVolume, mock.Anything, mock.Anything, newQosPolicy.Name, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UpdateVolumePerformanceGroupInDBForVolume, mock.Anything, mock.Anything, newVPG).Return(nil)

	// Execute workflow - volume has no current VPG (VolumePerformanceGroupID not valid)
	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
		Pool: &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			},
		},
		PoolID:      1,
		Account:     &datamodel.Account{Name: "test_account"},
		SizeInBytes: 1000,
		VolumeAttributes: &datamodel.VolumeAttributes{
			IsDataProtection: false,
			ExternalUUID:     "test-external-uuid",
		},
		Svm:                      &datamodel.Svm{Name: "test-svm"},
		VolumePerformanceGroupID: sql.NullInt64{Valid: false}, // No current VPG
		VolumePerformanceGroup:   nil,
	}
	newVPGUUID := "new-vpg-uuid"
	params := &common.UpdateVolumeParams{
		VolumePerformanceGroupId: &newVPGUUID,
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	// Assert workflow completed successfully (assigning VPG to volume with no previous assignment)
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
	mockStorage.AssertNumberOfCalls(s.T(), "UpdateJob", 2)
}

func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_VPGReassignment_RollbackExecution() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}
	vpgActivity := activities.VolumePerformanceGroupActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetNode)
	s.env.RegisterActivity(updateActivity.GetVolumeFromONTAP)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateLun)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInDB)
	s.env.RegisterActivity(updateActivity.UnassignQoSPolicyFromVolume)
	s.env.RegisterActivity(vpgActivity.GetVolumePerformanceGroupByUUID)
	s.env.RegisterActivity(updateActivity.FindQoSGroupPolicyForVolume)
	s.env.RegisterActivity(updateActivity.AssignQoSPolicyToVolume)
	s.env.RegisterActivity(updateActivity.UpdateVolumePerformanceGroupInDBForVolume)
	backupActivity := activities.BackupActivity{SE: mockStorage}
	s.env.RegisterActivity(backupActivity.UpdateBackupMetadataIfExistsActivity)
	volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)

	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 1000,
		Size:           1000,
		State:          "online",
	}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(updateActivity.UpdateLun, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.LunResponse{ProviderResponse: vsa.ProviderResponse{Name: "/vol/vol1/lun1"}}, nil).Maybe()
	s.env.OnActivity(backupActivity.UpdateBackupMetadataIfExistsActivity, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	oldVPG := &datamodel.VolumePerformanceGroup{
		BaseModel:        datamodel.BaseModel{ID: 1, UUID: "old-vpg-uuid"},
		Name:             "old-vpg-qos-policy",
		PoolID:           1,
		OntapQosPolicyID: "old-policy-uuid",
		IsAutoGen:        false,
	}
	oldQosPolicyID := "old-vpg-qos-policy"
	s.env.OnActivity(updateActivity.UnassignQoSPolicyFromVolume, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	newVPG := &datamodel.VolumePerformanceGroup{
		BaseModel:        datamodel.BaseModel{ID: 2, UUID: "new-vpg-uuid"},
		Name:             "new-vpg-qos-policy",
		PoolID:           1,
		OntapQosPolicyID: "new-policy-uuid",
		IsAutoGen:        false,
	}
	s.env.OnActivity(vpgActivity.GetVolumePerformanceGroupByUUID, mock.Anything, "new-vpg-uuid").Return(newVPG, nil)
	newQosPolicy := &vsa.QoSGroupPolicyResponse{
		Name:    "new-policy-name",
		UUID:    "new-policy-uuid",
		SvmName: "test-svm",
	}
	s.env.OnActivity(updateActivity.FindQoSGroupPolicyForVolume, mock.Anything, "new-vpg-qos-policy", "test-svm", mock.Anything).Return(newQosPolicy, nil)
	s.env.OnActivity(updateActivity.AssignQoSPolicyToVolume, mock.Anything, mock.Anything, newQosPolicy.Name, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UpdateVolumePerformanceGroupInDBForVolume, mock.Anything, mock.Anything, newVPG).Return(nil)

	// UpdateVolumeInDB fails, triggering rollback (may be retried)
	s.env.OnActivity(updateActivity.UpdateVolumeInDB, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("failed to update volume in DB"))

	s.env.OnActivity(updateActivity.AssignQoSPolicyToVolume, mock.Anything, mock.Anything, oldQosPolicyID, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UpdateVolumePerformanceGroupInDBForVolume, mock.Anything, mock.Anything, oldVPG).Return(nil)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
		Pool: &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			},
		},
		PoolID:      1,
		Account:     &datamodel.Account{Name: "test_account"},
		SizeInBytes: 1000,
		VolumeAttributes: &datamodel.VolumeAttributes{
			IsDataProtection: false,
			ExternalUUID:     "test-external-uuid",
		},
		Svm:                      &datamodel.Svm{Name: "test-svm"},
		VolumePerformanceGroupID: sql.NullInt64{Int64: 1, Valid: true},
		VolumePerformanceGroup:   oldVPG,
	}
	newVPGUUID := "new-vpg-uuid"
	params := &common.UpdateVolumeParams{
		VolumePerformanceGroupId: &newVPGUUID,
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
	assert.Contains(s.T(), s.env.GetWorkflowError().Error(), "failed to update volume in DB")

	// Verify that the rollback activities were called to restore old VPG assignment
	s.env.AssertExpectations(s.T())
}

func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_VPGReassignment_RollbackExecution_EmptyOldQoSPolicy() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}
	vpgActivity := activities.VolumePerformanceGroupActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetNode)
	s.env.RegisterActivity(updateActivity.GetVolumeFromONTAP)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateLun)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInDB)
	s.env.RegisterActivity(updateActivity.UnassignQoSPolicyFromVolume)
	s.env.RegisterActivity(vpgActivity.GetVolumePerformanceGroupByUUID)
	s.env.RegisterActivity(updateActivity.FindQoSGroupPolicyForVolume)
	s.env.RegisterActivity(updateActivity.AssignQoSPolicyToVolume)
	s.env.RegisterActivity(updateActivity.UpdateVolumePerformanceGroupInDBForVolume)
	backupActivity := activities.BackupActivity{SE: mockStorage}
	s.env.RegisterActivity(backupActivity.UpdateBackupMetadataIfExistsActivity)
	volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)

	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 1000,
		Size:           1000,
		State:          "online",
	}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(updateActivity.UpdateLun, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.LunResponse{ProviderResponse: vsa.ProviderResponse{Name: "/vol/vol1/lun1"}}, nil).Maybe()
	s.env.OnActivity(backupActivity.UpdateBackupMetadataIfExistsActivity, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	// Old VPG has empty OntapQosPolicyID (rollback unassign scenario)
	oldVPG := &datamodel.VolumePerformanceGroup{
		BaseModel:        datamodel.BaseModel{ID: 1, UUID: "old-vpg-uuid"},
		PoolID:           1,
		OntapQosPolicyID: "", // Empty OntapQosPolicyID - this is the key scenario
		IsAutoGen:        false,
	}
	oldQosPolicyID := "" // Empty QoS policy ID

	s.env.OnActivity(updateActivity.UnassignQoSPolicyFromVolume, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	newVPG := &datamodel.VolumePerformanceGroup{
		BaseModel:        datamodel.BaseModel{ID: 2, UUID: "new-vpg-uuid"},
		Name:             "new-vpg-qos-policy",
		PoolID:           1,
		OntapQosPolicyID: "new-policy-uuid",
		IsAutoGen:        false,
	}
	s.env.OnActivity(vpgActivity.GetVolumePerformanceGroupByUUID, mock.Anything, "new-vpg-uuid").Return(newVPG, nil).Once()
	newQosPolicy := &vsa.QoSGroupPolicyResponse{
		Name:    "new-policy-name",
		UUID:    "new-policy-uuid",
		SvmName: "test-svm",
	}
	s.env.OnActivity(updateActivity.FindQoSGroupPolicyForVolume, mock.Anything, "new-vpg-qos-policy", "test-svm", mock.Anything).Return(newQosPolicy, nil).Once()
	s.env.OnActivity(updateActivity.AssignQoSPolicyToVolume, mock.Anything, mock.Anything, newQosPolicy.Name, mock.Anything).Return(nil).Once()
	s.env.OnActivity(updateActivity.UpdateVolumePerformanceGroupInDBForVolume, mock.Anything, mock.Anything, newVPG).Return(nil).Once()

	// UpdateVolumeInDB fails, triggering rollback
	// Allow retries (default is 3 attempts), so mock should handle up to 3 calls
	s.env.OnActivity(updateActivity.UpdateVolumeInDB, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("failed to update volume in DB")).Times(3)

	s.env.OnActivity(updateActivity.AssignQoSPolicyToVolume, mock.Anything, mock.Anything, oldQosPolicyID, mock.Anything).Return(errors.New("cannot assign empty QoS policy")).Maybe()
	s.env.OnActivity(updateActivity.UpdateVolumePerformanceGroupInDBForVolume, mock.Anything, mock.Anything, oldVPG).Return(nil).Once()

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
		Pool: &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			},
		},
		PoolID:      1,
		Account:     &datamodel.Account{Name: "test_account"},
		SizeInBytes: 1000,
		VolumeAttributes: &datamodel.VolumeAttributes{
			IsDataProtection: false,
			ExternalUUID:     "test-external-uuid",
		},
		Svm:                      &datamodel.Svm{Name: "test-svm"},
		VolumePerformanceGroupID: sql.NullInt64{Int64: 1, Valid: true},
		VolumePerformanceGroup:   oldVPG,
	}
	newVPGUUID := "new-vpg-uuid"
	params := &common.UpdateVolumeParams{
		VolumePerformanceGroupId: &newVPGUUID,
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	// Assert workflow failed
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
	assert.Contains(s.T(), s.env.GetWorkflowError().Error(), "failed to update volume in DB")

	s.env.AssertExpectations(s.T())
}

// Test_UpdateVolumeWorkflow_SizeReduction_Success tests successful volume size reduction
// when new size is larger than used bytes
func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_SizeReduction_Success() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInDB)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)

	// Volume currently: Size=100GB, UsedBytes=30GB
	// Attempting to reduce to 50GB (should succeed because 50GB > 30GB used)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		Size:           107374182400, // 100GB
		UsedBytes:      32212254720,  // 30GB
		AvailableSpace: 75161927680,  // 70GB
		State:          "online",
	}, nil)

	s.env.OnActivity(updateActivity.UpdateVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInDB, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Execute workflow
	volume := &datamodel.Volume{
		Name: "test_volume",
		Pool: &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			},
		},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		SizeInBytes: 107374182400, // 100GB in DB (not used for validation)
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID: "test-external-uuid",
		},
	}
	params := &common.UpdateVolumeParams{
		QuotaInBytes: 53687091200, // 50GB - larger than 30GB used, should succeed
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	// Assert workflow completed successfully
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
	mockStorage.AssertNumberOfCalls(s.T(), "UpdateJob", 2)
}

// Test_UpdateVolumeWorkflow_SizeReduction_FailsUsedBytesExceeded tests that volume size reduction
// is rejected when new size is smaller than current used bytes
func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_SizeReduction_FailsUsedBytesExceeded() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)

	// Volume currently: Size=100GB, UsedBytes=70GB
	// Attempting to reduce to 50GB (should fail because 50GB < 70GB used)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		Size:           107374182400, // 100GB
		UsedBytes:      75161927680,  // 70GB
		AvailableSpace: 32212254720,  // 30GB
		State:          "online",
	}, nil)

	// Execute workflow
	volume := &datamodel.Volume{
		Name: "test_volume",
		Pool: &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			},
		},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		SizeInBytes: 107374182400, // 100GB in DB (not used for validation)
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID: "test-external-uuid",
		},
	}
	params := &common.UpdateVolumeParams{
		QuotaInBytes: 53687091200, // 50GB - smaller than 70GB used, should fail
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	// Assert workflow failed with validation error
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
	assert.Contains(s.T(), s.env.GetWorkflowError().Error(), "cannot reduce volume size")
	assert.Contains(s.T(), s.env.GetWorkflowError().Error(), "currently using")

	// Verify UpdateVolumeInONTAP was NOT called (validation failed before update)
	s.env.AssertNotCalled(s.T(), "UpdateVolumeInONTAP")
}

// Test_UpdateVolumeWorkflow_SizeReduction_ExactlyEqualUsedBytes tests edge case
// where new size exactly equals used bytes (should succeed - technically valid)
func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_SizeReduction_ExactlyEqualUsedBytes() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInDB)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)

	// Volume currently: Size=100GB, UsedBytes=50GB
	// Attempting to reduce to exactly 50GB (should succeed because 50GB >= 50GB used, though risky in practice)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		Size:           107374182400, // 100GB
		UsedBytes:      53687091200,  // 50GB
		AvailableSpace: 53687091200,  // 50GB
		State:          "online",
	}, nil)

	s.env.OnActivity(updateActivity.UpdateVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInDB, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Execute workflow
	volume := &datamodel.Volume{
		Name: "test_volume",
		Pool: &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			},
		},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		SizeInBytes: 107374182400, // 100GB in DB
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID: "test-external-uuid",
		},
	}
	params := &common.UpdateVolumeParams{
		QuotaInBytes: 53687091200, // 50GB - equal to used bytes, should succeed (edge case)
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	// Assert workflow completed successfully (validation allows equal)
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
	mockStorage.AssertNumberOfCalls(s.T(), "UpdateJob", 2)
}

// Test_UpdateVolumeWorkflow_SizeIncrease_BypassesValidation tests that volume size increases
// skip the used bytes validation (validation only applies to size reductions)
func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_SizeIncrease_BypassesValidation() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInDB)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)

	// Volume currently: Size=100GB, UsedBytes=30GB
	// Attempting to increase to 200GB (should succeed and bypass validation)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		Size:           107374182400, // 100GB
		UsedBytes:      32212254720,  // 30GB
		AvailableSpace: 75161927680,  // 70GB
		State:          "online",
	}, nil)

	s.env.OnActivity(updateActivity.UpdateVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInDB, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Execute workflow
	volume := &datamodel.Volume{
		Name: "test_volume",
		Pool: &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			},
		},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		SizeInBytes: 107374182400, // 100GB in DB
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID: "test-external-uuid",
		},
	}
	params := &common.UpdateVolumeParams{
		QuotaInBytes: 214748364800, // 200GB - increase, bypasses validation
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	// Assert workflow completed successfully
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
	mockStorage.AssertNumberOfCalls(s.T(), "UpdateJob", 2)
}

// Test_UpdateVolumeWorkflow_SizeReduction_ZeroUsedBytes tests size reduction
// when volume has zero used bytes (empty volume)
func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_SizeReduction_ZeroUsedBytes() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInDB)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)

	// Volume currently: Size=100GB, UsedBytes=0 (empty volume)
	// Attempting to reduce to 10GB (should succeed)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		Size:           107374182400, // 100GB
		UsedBytes:      0,            // Empty
		AvailableSpace: 107374182400, // 100GB
		State:          "online",
	}, nil)

	s.env.OnActivity(updateActivity.UpdateVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInDB, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Execute workflow
	volume := &datamodel.Volume{
		Name: "test_volume",
		Pool: &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			},
		},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		SizeInBytes: 107374182400, // 100GB in DB
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID: "test-external-uuid",
		},
	}
	params := &common.UpdateVolumeParams{
		QuotaInBytes: 10737418240, // 10GB - much smaller than current size, but no data, should succeed
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	// Assert workflow completed successfully
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
	mockStorage.AssertNumberOfCalls(s.T(), "UpdateJob", 2)
}

// Test_UpdateVolumeWorkflow_NoSizeChange_SkipsValidation tests that when
// size is not being changed, validation is skipped
func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_NoSizeChange_SkipsValidation() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInDB)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)

	// Volume currently: Size=100GB, UsedBytes=30GB
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		Size:           107374182400, // 100GB
		UsedBytes:      32212254720,  // 30GB
		AvailableSpace: 75161927680,  // 70GB
		State:          "online",
	}, nil)

	s.env.OnActivity(updateActivity.UpdateVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInDB, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Execute workflow
	volume := &datamodel.Volume{
		Name: "test_volume",
		Pool: &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			},
		},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		SizeInBytes: 107374182400, // 100GB in DB
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID:     "test-external-uuid",
			IsDataProtection: false,
		},
	}
	snapReserve := int64(10)
	params := &common.UpdateVolumeParams{
		QuotaInBytes: 0, // No size change requested
		SnapReserve:  &snapReserve,
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	// Assert workflow completed successfully (no size validation ran)
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
	mockStorage.AssertNumberOfCalls(s.T(), "UpdateJob", 2)
}

// Test_UpdateVolumeWorkflow_SizeReduction_GetVolumeFromONTAPFails tests that
// if we can't fetch volume from ONTAP, the workflow fails before attempting update
func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_SizeReduction_GetVolumeFromONTAPFails() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)

	// GetVolumeFromONTAP fails
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("failed to fetch volume from ONTAP"))

	// Execute workflow
	volume := &datamodel.Volume{
		Name: "test_volume",
		Pool: &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			},
		},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		SizeInBytes: 107374182400, // 100GB
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID: "test-external-uuid",
		},
	}
	params := &common.UpdateVolumeParams{
		QuotaInBytes: 53687091200, // 50GB
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	// Assert workflow failed
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
	assert.Contains(s.T(), s.env.GetWorkflowError().Error(), "failed to fetch volume from ONTAP")
}

// ===== GCBDR Backup Vault Tests for Volume Update =====

// Test_UpdateVolumeWorkflow_GCBDR_BucketAlreadyExists tests that when a GCBDR vault is
// attached and the bucket already exists (skips bucket creation), SetupCrossProjectBackupPermissions
// is still called unconditionally to ensure every pool receives the IAM grant.
func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_GCBDR_BucketAlreadyExists() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}
	volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}
	backupActivity := activities.BackupActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetNode)
	s.env.RegisterActivity(commonActivity.GetAuthJWTToken)
	s.env.RegisterActivity(updateActivity.GetVolumeFromONTAP)
	s.env.RegisterActivity(updateActivity.CheckBackupVaultExistInVCP)
	s.env.RegisterActivity(updateActivity.FindTenancyDetails)
	s.env.RegisterActivity(updateActivity.CheckBucketResourceName)
	s.env.RegisterActivity(volumeCreateActivity.SetupCrossProjectBackupPermissions)
	s.env.RegisterActivity(volumeCreateActivity.CheckOrCreateRemoteBackupVaultInVCP)
	s.env.RegisterActivity(volumeCreateActivity.UpdateRemoteBackupVaultWithBucketDetails)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInDB)
	s.env.RegisterActivity(backupActivity.UpdateBackupMetadataIfExistsActivity)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 1000,
		Size:           1000,
		State:          "online",
	}, nil)
	s.env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).Return("test-token", nil)

	// CheckBackupVaultExistInVCP returns a GCBDR vault (ServiceType = "GCBDR")
	s.env.OnActivity(updateActivity.CheckBackupVaultExistInVCP, mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.BackupVault{
		BaseModel:   datamodel.BaseModel{UUID: "gcbdr-vault-uuid"},
		Name:        "gcbdr-vault",
		ServiceType: activities.GCBDRServiceType,
		BucketDetails: datamodel.BucketDetailsArray{
			{BucketName: "gcbdr-bucket", TenantProjectNumber: "tenant-project"},
		},
	}, nil)
	s.env.OnActivity(volumeCreateActivity.CheckOrCreateRemoteBackupVaultInVCP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
	// CheckBucketResourceName returns existing bucket — no bucket creation needed
	s.env.OnActivity(updateActivity.CheckBucketResourceName, mock.Anything, mock.Anything).Return(&common.BucketDetails{
		BucketName:          "gcbdr-bucket",
		ServiceAccountName:  "gcbdr-sa",
		TenantProjectNumber: "12345",
	}, nil)
	s.env.OnActivity(volumeCreateActivity.UpdateRemoteBackupVaultWithBucketDetails, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	// SetupCrossProjectBackupPermissions is called unconditionally — even when bucket already exists
	s.env.OnActivity(volumeCreateActivity.SetupCrossProjectBackupPermissions, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInDB, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupActivity.UpdateBackupMetadataIfExistsActivity, mock.Anything, mock.Anything).Return(nil)

	// Execute workflow
	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			},
		},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		DataProtection: &datamodel.DataProtection{
			BackupVaultID: "gcbdr-vault-uuid",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			VendorSubnetID: "test-subnet-id",
		},
	}
	params := &common.UpdateVolumeParams{
		QuotaInBytes: 1000,
		Region:       "us-west-1",
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	// Assert workflow completed successfully — permissions granted even when bucket pre-existed
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
}

// Test_UpdateVolumeWorkflow_GCBDR_NewBucketCreation tests the full bucket creation flow
// when a GCBDR vault is attached and no bucket exists yet.
func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_GCBDR_NewBucketCreation() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}
	volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}
	backupActivity := activities.BackupActivity{SE: mockStorage}
	syncBackupZiZsActivity := backgroundactivities.SyncBackupZiZsActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetNode)
	s.env.RegisterActivity(commonActivity.GetAuthJWTToken)
	s.env.RegisterActivity(updateActivity.GetVolumeFromONTAP)
	s.env.RegisterActivity(updateActivity.CheckBackupVaultExistInVCP)
	s.env.RegisterActivity(updateActivity.FindTenancyDetails)
	s.env.RegisterActivity(updateActivity.CheckBucketResourceName)
	s.env.RegisterActivity(updateActivity.GenerateResourceNamesForBackupVault)
	s.env.RegisterActivity(updateActivity.CreateBucketForBackupVault)
	s.env.RegisterActivity(updateActivity.UpdateBucketDetailsOfBackupVault)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInDB)
	s.env.RegisterActivity(syncBackupZiZsActivity.SyncBucketDetails)
	s.env.RegisterActivity(volumeCreateActivity.CheckOrCreateRemoteBackupVaultInVCP)
	s.env.RegisterActivity(volumeCreateActivity.UpdateRemoteBackupVaultWithBucketDetails)
	s.env.RegisterActivity(volumeCreateActivity.SetupCrossProjectBackupPermissions)
	s.env.RegisterActivity(backupActivity.UpdateBackupMetadataIfExistsActivity)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 1000,
		Size:           1000,
		State:          "online",
	}, nil)
	s.env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).Return("test-token", nil)

	// CheckBackupVaultExistInVCP returns a GCBDR vault
	s.env.OnActivity(updateActivity.CheckBackupVaultExistInVCP, mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.BackupVault{
		BaseModel:   datamodel.BaseModel{UUID: "gcbdr-vault-uuid"},
		Name:        "gcbdr-vault",
		ServiceType: activities.GCBDRServiceType,
		BucketDetails: datamodel.BucketDetailsArray{
			{BucketName: "gcbdr-existing-bucket", TenantProjectNumber: "tenant-project"},
		},
	}, nil)
	// CheckBucketResourceName returns empty — triggers bucket creation
	s.env.OnActivity(updateActivity.CheckBucketResourceName, mock.Anything, mock.Anything).Return(&common.BucketDetails{}, nil)
	s.env.OnActivity(updateActivity.GenerateResourceNamesForBackupVault, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.ResourceNames{}, nil)
	s.env.OnActivity(updateActivity.CreateBucketForBackupVault, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.AnythingOfType("*string")).Return(&common.BucketDetails{
		BucketName:          "gcbdr-new-bucket",
		TenantProjectNumber: "12345",
	}, nil)
	s.env.OnActivity(syncBackupZiZsActivity.SyncBucketDetails, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{
		BucketName:          "gcbdr-new-bucket",
		TenantProjectNumber: "12345",
		SatisfiesPzi:        true,
		SatisfiesPzs:        false,
	}, nil)
	s.env.OnActivity(updateActivity.UpdateBucketDetailsOfBackupVault, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(volumeCreateActivity.SetupCrossProjectBackupPermissions, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	// GCBDR is not CrossRegion, so CheckOrCreateRemoteBackupVaultInVCP returns nil
	s.env.OnActivity(volumeCreateActivity.CheckOrCreateRemoteBackupVaultInVCP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
	s.env.OnActivity(volumeCreateActivity.UpdateRemoteBackupVaultWithBucketDetails, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInDB, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupActivity.UpdateBackupMetadataIfExistsActivity, mock.Anything, mock.Anything).Return(nil)

	// Execute workflow
	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{
			BaseModel:        datamodel.BaseModel{ID: int64(1)},
			ServiceAccountId: "pool-sa@project.iam.gserviceaccount.com",
			ClusterDetails:   datamodel.ClusterDetails{RegionalTenantProject: "pool-tenant"},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			},
		},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		DataProtection: &datamodel.DataProtection{
			BackupVaultID: "gcbdr-vault-uuid",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			VendorSubnetID: "test-subnet-id",
		},
	}
	params := &common.UpdateVolumeParams{
		QuotaInBytes: 1000,
		Region:       "us-west-1",
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	// Assert workflow completed successfully — full GCBDR bucket creation path
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
}

// Test_UpdateVolumeWorkflow_GCBDR_CreateBucketFailure tests that the workflow fails
// when bucket creation fails for a GCBDR vault.
func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_GCBDR_CreateBucketFailure() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateLun)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInDB)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 1000,
		Size:           1000,
		State:          "online",
	}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UpdateLun, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.LunResponse{ProviderResponse: vsa.ProviderResponse{Name: "/vol/vol1/lun1"}}, nil)
	s.env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).Return("test-token", nil)

	// CheckBackupVaultExistInVCP returns a GCBDR vault
	s.env.OnActivity(updateActivity.CheckBackupVaultExistInVCP, mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.BackupVault{
		BaseModel:   datamodel.BaseModel{UUID: "gcbdr-vault-uuid"},
		Name:        "gcbdr-vault",
		ServiceType: activities.GCBDRServiceType,
		BucketDetails: datamodel.BucketDetailsArray{
			{BucketName: "gcbdr-existing-bucket", TenantProjectNumber: "tenant-project"},
		},
	}, nil)
	// CheckBucketResourceName returns empty — triggers bucket creation
	s.env.OnActivity(updateActivity.CheckBucketResourceName, mock.Anything, mock.Anything).Return(&common.BucketDetails{}, nil)
	s.env.OnActivity(updateActivity.GenerateResourceNamesForBackupVault, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.ResourceNames{}, nil)
	// CreateBucketForBackupVault fails
	s.env.OnActivity(updateActivity.CreateBucketForBackupVault, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.AnythingOfType("*string")).Return(nil, errors.New("failed to create bucket for GCBDR vault"))

	// Execute workflow
	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			},
		},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		SizeInBytes: 100,
		DataProtection: &datamodel.DataProtection{
			BackupVaultID: "gcbdr-vault-uuid",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			VendorSubnetID: "test-subnet-id",
		},
	}
	params := &common.UpdateVolumeParams{
		QuotaInBytes: 200,
		Region:       "us-west-1",
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	// Assert workflow failed due to bucket creation error
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NotNil(s.T(), s.env.GetWorkflowError())
	mockStorage.AssertNumberOfCalls(s.T(), "UpdateJob", 2)
}

// Test_UpdateVolumeWorkflow_GCBDR_UpdateBucketDetailsFailure tests that the workflow fails
// when updating bucket details fails for a GCBDR vault after successful bucket creation.
func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_GCBDR_UpdateBucketDetailsFailure() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}
	syncBackupZiZsActivity := backgroundactivities.SyncBackupZiZsActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateLun)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInDB)
	s.env.RegisterActivity(syncBackupZiZsActivity.SyncBucketDetails)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 1000,
		Size:           1000,
		State:          "online",
	}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UpdateLun, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.LunResponse{ProviderResponse: vsa.ProviderResponse{Name: "/vol/vol1/lun1"}}, nil)
	s.env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).Return("test-token", nil)

	// CheckBackupVaultExistInVCP returns a GCBDR vault
	s.env.OnActivity(updateActivity.CheckBackupVaultExistInVCP, mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.BackupVault{
		BaseModel:   datamodel.BaseModel{UUID: "gcbdr-vault-uuid"},
		Name:        "gcbdr-vault",
		ServiceType: activities.GCBDRServiceType,
		BucketDetails: datamodel.BucketDetailsArray{
			{BucketName: "gcbdr-existing-bucket", TenantProjectNumber: "tenant-project"},
		},
	}, nil)
	s.env.OnActivity(updateActivity.CheckBucketResourceName, mock.Anything, mock.Anything).Return(&common.BucketDetails{}, nil)
	s.env.OnActivity(updateActivity.GenerateResourceNamesForBackupVault, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.ResourceNames{}, nil)
	s.env.OnActivity(updateActivity.CreateBucketForBackupVault, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.AnythingOfType("*string")).Return(&common.BucketDetails{
		BucketName:          "gcbdr-new-bucket",
		TenantProjectNumber: "12345",
	}, nil)
	s.env.OnActivity(syncBackupZiZsActivity.SyncBucketDetails, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{
		BucketName:          "gcbdr-new-bucket",
		TenantProjectNumber: "12345",
		SatisfiesPzi:        true,
		SatisfiesPzs:        false,
	}, nil)
	// UpdateBucketDetailsOfBackupVault fails
	s.env.OnActivity(updateActivity.UpdateBucketDetailsOfBackupVault, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("failed to update GCBDR vault bucket details"))

	// Execute workflow
	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			},
		},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		SizeInBytes: 100,
		DataProtection: &datamodel.DataProtection{
			BackupVaultID: "gcbdr-vault-uuid",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			VendorSubnetID: "test-subnet-id",
		},
	}
	params := &common.UpdateVolumeParams{
		QuotaInBytes: 200,
		Region:       "us-west-1",
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	// Assert workflow failed
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NotNil(s.T(), s.env.GetWorkflowError())
	mockStorage.AssertNumberOfCalls(s.T(), "UpdateJob", 2)
}

// Test_UpdateVolumeWorkflow_GCBDR_NilPool_Error tests that the workflow fails when the volume
// has no pool and a GCBDR vault is attached (bucket already exists path).
func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_GCBDR_NilPool_Error() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(updateActivity.CheckBackupVaultExistInVCP)
	s.env.RegisterActivity(updateActivity.FindTenancyDetails)
	s.env.RegisterActivity(updateActivity.CheckBucketResourceName)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{ExternalUUID: "ext-uuid", Name: "test_volume"},
		AvailableSpace:   1000,
		Size:             1000,
		State:            "online",
	}, nil)
	s.env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).Return("test-token", nil)
	s.env.OnActivity(updateActivity.CheckBackupVaultExistInVCP, mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.BackupVault{
		BaseModel:   datamodel.BaseModel{UUID: "gcbdr-vault-uuid"},
		Name:        "gcbdr-vault",
		ServiceType: activities.GCBDRServiceType,
		BucketDetails: datamodel.BucketDetailsArray{
			{BucketName: "gcbdr-bucket", TenantProjectNumber: "tenant-project"},
		},
	}, nil)
	// Bucket already exists — skip creation block, go straight to unconditional permissions check
	s.env.OnActivity(updateActivity.CheckBucketResourceName, mock.Anything, mock.Anything).Return(&common.BucketDetails{
		BucketName: "gcbdr-bucket", ServiceAccountName: "gcbdr-sa", TenantProjectNumber: "12345",
	}, nil)

	// Execute workflow with nil Pool — triggers "pool details required" error
	volume := &datamodel.Volume{
		Pool: nil,
		Account: &datamodel.Account{
			Name: "test_account",
		},
		DataProtection: &datamodel.DataProtection{
			BackupVaultID: "gcbdr-vault-uuid",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			VendorSubnetID: "test-subnet-id",
		},
	}
	params := &common.UpdateVolumeParams{
		QuotaInBytes: 1000,
		Region:       "us-west-1",
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NotNil(s.T(), s.env.GetWorkflowError())
}

// Test_UpdateVolumeWorkflow_GCBDR_SetupCrossProjectPermissions_Error tests that the workflow
// fails when SetupCrossProjectBackupPermissions returns an error (bucket already exists path).
func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_GCBDR_SetupCrossProjectPermissions_Error() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}
	volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(updateActivity.CheckBackupVaultExistInVCP)
	s.env.RegisterActivity(updateActivity.FindTenancyDetails)
	s.env.RegisterActivity(updateActivity.CheckBucketResourceName)
	s.env.RegisterActivity(volumeCreateActivity.SetupCrossProjectBackupPermissions)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{ExternalUUID: "ext-uuid", Name: "test_volume"},
		AvailableSpace:   1000,
		Size:             1000,
		State:            "online",
	}, nil)
	s.env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).Return("test-token", nil)
	s.env.OnActivity(updateActivity.CheckBackupVaultExistInVCP, mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.BackupVault{
		BaseModel:   datamodel.BaseModel{UUID: "gcbdr-vault-uuid"},
		Name:        "gcbdr-vault",
		ServiceType: activities.GCBDRServiceType,
		BucketDetails: datamodel.BucketDetailsArray{
			{BucketName: "gcbdr-bucket", TenantProjectNumber: "tenant-project"},
		},
	}, nil)
	// Bucket already exists — skip creation block, go straight to unconditional permissions check
	s.env.OnActivity(updateActivity.CheckBucketResourceName, mock.Anything, mock.Anything).Return(&common.BucketDetails{
		BucketName: "gcbdr-bucket", ServiceAccountName: "gcbdr-sa", TenantProjectNumber: "12345",
	}, nil)
	// SetupCrossProjectBackupPermissions fails
	s.env.OnActivity(volumeCreateActivity.SetupCrossProjectBackupPermissions, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("iam grant failed"))

	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password: "password",
			},
		},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		DataProtection: &datamodel.DataProtection{
			BackupVaultID: "gcbdr-vault-uuid",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			VendorSubnetID: "test-subnet-id",
		},
	}
	params := &common.UpdateVolumeParams{
		QuotaInBytes: 1000,
		Region:       "us-west-1",
	}
	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NotNil(s.T(), s.env.GetWorkflowError())
}

// Test_UpdateVolumeWorkflow_ExportPolicy_NilInParams_WithExistingRules covers lines 395-396:
// when params.FileProperties.ExportPolicy is nil but the volume already has export rules,
// isExportPolicyRulesUpdateRequired returns true and the workflow initialises an empty
// ExportPolicy before setting the policy name and calling UpdateExportPolicyRulesInONTAP.
func (s *VolumeUpdateTestSuite) Test_UpdateVolumeWorkflow_ExportPolicy_NilInParams_WithExistingRules() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateExportPolicyRulesInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInDB)

	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
			Name:         "test_volume",
		},
		AvailableSpace: 1000,
		Size:           1000,
		State:          "online",
	}, nil)
	// The nil-guard at lines 395-396 creates a new &models.ExportPolicy{} and sets its
	// ExportPolicyName from the existing volume policy before the activity is invoked.
	s.env.OnActivity(updateActivity.UpdateExportPolicyRulesInONTAP,
		mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInDB, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Volume has FileProperties with an ExportPolicy containing existing rules.
	existingExportPolicy := &datamodel.ExportPolicy{
		ExportPolicyName: "existing-policy",
		ExportRules: []*datamodel.ExportRule{
			{
				AllowedClients: "10.0.0.0/8",
				AccessType:     "ReadWrite",
				NFSv3:          true,
			},
		},
	}
	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password: "password",
			},
		},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			FileProperties: &datamodel.FileProperties{
				ExportPolicy: existingExportPolicy,
			},
		},
	}
	// params.FileProperties.ExportPolicy is nil — this is the triggering condition for
	// the nil-guard at lines 395-396 because isExportPolicyRulesUpdateRequired returns
	// true when currentPolicy has rules but updatePolicy is nil.
	params := &common.UpdateVolumeParams{
		FileProperties: &models.FileProperties{
			ExportPolicy: nil,
		},
	}

	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
	mockStorage.AssertNumberOfCalls(s.T(), "UpdateJob", 2)
}

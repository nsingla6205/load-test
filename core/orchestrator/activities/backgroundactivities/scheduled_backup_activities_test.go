package backgroundactivities

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	googleproxyclient "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/google-proxy-client"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/auth"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
	"go.temporal.io/sdk/temporal"
)

func TestCreateScheduledBackup(t *testing.T) {
	t.Run("CreateScheduledBackupSuccess", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		activity := ScheduledBackupActivity{SE: mockStorage}

		volume := &datamodel.Volume{BaseModel: datamodel.BaseModel{UUID: "vol-uuid"}}
		backupVault := &datamodel.BackupVault{BaseModel: datamodel.BaseModel{ID: 123}}
		timestamp := "20240610"
		scheduleTag := "test-schedule-tag"

		expectedBackup := &datamodel.Backup{
			Name:          mock.Anything,
			Type:          "SCHEDULED",
			ScheduleTag:   &scheduleTag,
			VolumeUUID:    volume.UUID,
			BackupVaultID: backupVault.ID,
			BackupVault:   backupVault,
			State:         datamodel.LifeCycleStateCreating,
			StateDetails:  datamodel.LifeCycleStateCreatingDetails,
		}

		mockStorage.On("CreateBackup", mock.Anything, mock.Anything).Return(expectedBackup, nil)

		backup, err := activity.CreateScheduledBackup(context.Background(), volume, backupVault, timestamp, scheduleTag, false)
		assert.NoError(t, err)
		assert.NotNil(t, backup)
		assert.Equal(t, expectedBackup, backup)
		mockStorage.AssertExpectations(t)
	})
	t.Run("CreateScheduledBackupFails", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		activity := ScheduledBackupActivity{SE: mockStorage}

		volume := &datamodel.Volume{BaseModel: datamodel.BaseModel{UUID: "vol-uuid"}}
		backupVault := &datamodel.BackupVault{BaseModel: datamodel.BaseModel{ID: 123}}
		timestamp := "20240610"
		scheduleTag := "test-schedule-tag"

		mockStorage.On("CreateBackup", mock.Anything, mock.Anything).Return(&datamodel.Backup{}, errors.New("db error"))

		backup, err := activity.CreateScheduledBackup(context.Background(), volume, backupVault, timestamp, scheduleTag, false)
		assert.Nil(t, backup)
		assert.Error(t, err)
		assert.Equal(t, err.Error(), "db error")

		mockStorage.AssertExpectations(t)
	})

	t.Run("CreateScheduledBackupExpertModeUsesExternalUUID", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		activity := ScheduledBackupActivity{SE: mockStorage}

		externalUUID := "ext-uuid-123"
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "vol-uuid"},
			VolumeAttributes: &datamodel.VolumeAttributes{
				ExternalUUID: externalUUID,
			},
		}
		backupVault := &datamodel.BackupVault{BaseModel: datamodel.BaseModel{ID: 123}}
		timestamp := "20240610"
		scheduleTag := "daily"

		// State check: volume is READY so creation proceeds.
		mockStorage.On("GetExpertModeVolumeByExternalUUID", mock.Anything, externalUUID).
			Return(&datamodel.ExpertModeVolumes{ExternalUUID: externalUUID, State: datamodel.LifeCycleStateREADY}, nil).Once()
		// For expert mode, VolumeUUID in the created backup must be ExternalUUID, not volume.UUID.
		mockStorage.On("CreateBackup", mock.Anything, mock.MatchedBy(func(b *datamodel.Backup) bool {
			return b.VolumeUUID == externalUUID
		})).Return(&datamodel.Backup{VolumeUUID: externalUUID}, nil).Once()

		backup, err := activity.CreateScheduledBackup(context.Background(), volume, backupVault, timestamp, scheduleTag, true)
		assert.NoError(t, err)
		assert.NotNil(t, backup)
		assert.Equal(t, externalUUID, backup.VolumeUUID)
		mockStorage.AssertExpectations(t)
	})

	t.Run("ExpertMode_VolumeInDeletingState_SkipsBackupCreation", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		activity := ScheduledBackupActivity{SE: mockStorage}

		externalUUID := "ext-uuid-deleting"
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "vol-uuid"},
			VolumeAttributes: &datamodel.VolumeAttributes{
				ExternalUUID: externalUUID,
			},
		}
		backupVault := &datamodel.BackupVault{BaseModel: datamodel.BaseModel{ID: 123}}

		mockStorage.On("GetExpertModeVolumeByExternalUUID", mock.Anything, externalUUID).
			Return(&datamodel.ExpertModeVolumes{ExternalUUID: externalUUID, State: datamodel.LifeCycleStateDeleting}, nil).Once()

		backup, err := activity.CreateScheduledBackup(context.Background(), volume, backupVault, "20240610", "daily", true)
		assert.Nil(t, backup)
		assert.Error(t, err)
		var appErr *temporal.ApplicationError
		assert.True(t, vsaerrors.As(err, &appErr))
		assert.Equal(t, "CustomError", appErr.Type())
		assert.True(t, appErr.NonRetryable())
		// CreateBackup must never be called
		mockStorage.AssertNotCalled(t, "CreateBackup", mock.Anything, mock.Anything)
		mockStorage.AssertExpectations(t)
	})

	t.Run("ExpertMode_VolumeInCreatingState_ProceedsWithBackupCreation", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		activity := ScheduledBackupActivity{SE: mockStorage}

		externalUUID := "ext-uuid-creating"
		volume := &datamodel.Volume{
			BaseModel:        datamodel.BaseModel{UUID: "vol-uuid"},
			VolumeAttributes: &datamodel.VolumeAttributes{ExternalUUID: externalUUID},
		}
		backupVault := &datamodel.BackupVault{BaseModel: datamodel.BaseModel{ID: 1}}
		expectedBackup := &datamodel.Backup{VolumeUUID: externalUUID}

		// CREATING is not DELETING/DELETED, so the guard passes and backup creation proceeds.
		mockStorage.On("GetExpertModeVolumeByExternalUUID", mock.Anything, externalUUID).
			Return(&datamodel.ExpertModeVolumes{ExternalUUID: externalUUID, State: datamodel.LifeCycleStateCreating}, nil).Once()
		mockStorage.On("CreateBackup", mock.Anything, mock.MatchedBy(func(b *datamodel.Backup) bool {
			return b.VolumeUUID == externalUUID
		})).Return(expectedBackup, nil).Once()

		backup, err := activity.CreateScheduledBackup(context.Background(), volume, backupVault, "20240610", "daily", true)
		assert.NoError(t, err)
		assert.NotNil(t, backup)
		mockStorage.AssertExpectations(t)
	})

	t.Run("ExpertMode_VolumeNotFound_SkipsBackupCreation", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		activity := ScheduledBackupActivity{SE: mockStorage}

		externalUUID := "ext-uuid-gone"
		volume := &datamodel.Volume{
			BaseModel:        datamodel.BaseModel{UUID: "vol-uuid"},
			VolumeAttributes: &datamodel.VolumeAttributes{ExternalUUID: externalUUID},
		}
		backupVault := &datamodel.BackupVault{BaseModel: datamodel.BaseModel{ID: 1}}

		notFoundErr := customerrors.NewNotFoundErr("ExpertModeVolume", &externalUUID)
		mockStorage.On("GetExpertModeVolumeByExternalUUID", mock.Anything, externalUUID).
			Return(nil, notFoundErr).Once()

		backup, err := activity.CreateScheduledBackup(context.Background(), volume, backupVault, "20240610", "daily", true)
		assert.Nil(t, backup)
		assert.Error(t, err)
		var appErr *temporal.ApplicationError
		assert.True(t, vsaerrors.As(err, &appErr))
		assert.Equal(t, "CustomError", appErr.Type())
		assert.True(t, appErr.NonRetryable())
		mockStorage.AssertNotCalled(t, "CreateBackup", mock.Anything, mock.Anything)
		mockStorage.AssertExpectations(t)
	})

	t.Run("ExpertMode_DBErrorFetchingVolume_ReturnsRetryableError", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		activity := ScheduledBackupActivity{SE: mockStorage}

		externalUUID := "ext-uuid-dberr"
		volume := &datamodel.Volume{
			BaseModel:        datamodel.BaseModel{UUID: "vol-uuid"},
			VolumeAttributes: &datamodel.VolumeAttributes{ExternalUUID: externalUUID},
		}
		backupVault := &datamodel.BackupVault{BaseModel: datamodel.BaseModel{ID: 1}}

		mockStorage.On("GetExpertModeVolumeByExternalUUID", mock.Anything, externalUUID).
			Return(nil, errors.New("db connection error")).Once()

		backup, err := activity.CreateScheduledBackup(context.Background(), volume, backupVault, "20240610", "daily", true)
		assert.Nil(t, backup)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "db connection error")
		mockStorage.AssertNotCalled(t, "CreateBackup", mock.Anything, mock.Anything)
		mockStorage.AssertExpectations(t)
	})

	t.Run("ExpertMode_VolumeReady_ProceedsWithBackupCreation", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		activity := ScheduledBackupActivity{SE: mockStorage}

		externalUUID := "ext-uuid-ready"
		volume := &datamodel.Volume{
			BaseModel:        datamodel.BaseModel{UUID: "vol-uuid"},
			VolumeAttributes: &datamodel.VolumeAttributes{ExternalUUID: externalUUID},
		}
		backupVault := &datamodel.BackupVault{BaseModel: datamodel.BaseModel{ID: 1}}
		scheduleTag := "daily"
		expectedBackup := &datamodel.Backup{VolumeUUID: externalUUID, Type: backupTypeSCHEDULED}

		mockStorage.On("GetExpertModeVolumeByExternalUUID", mock.Anything, externalUUID).
			Return(&datamodel.ExpertModeVolumes{ExternalUUID: externalUUID, State: datamodel.LifeCycleStateREADY}, nil).Once()
		mockStorage.On("CreateBackup", mock.Anything, mock.MatchedBy(func(b *datamodel.Backup) bool {
			return b.VolumeUUID == externalUUID
		})).Return(expectedBackup, nil).Once()

		backup, err := activity.CreateScheduledBackup(context.Background(), volume, backupVault, "20240610", scheduleTag, true)
		assert.NoError(t, err)
		assert.NotNil(t, backup)
		mockStorage.AssertExpectations(t)
	})

	t.Run("ExpertMode_VolumeAvailable_ProceedsWithBackupCreation", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		activity := ScheduledBackupActivity{SE: mockStorage}

		externalUUID := "ext-uuid-available"
		volume := &datamodel.Volume{
			BaseModel:        datamodel.BaseModel{UUID: "vol-uuid"},
			VolumeAttributes: &datamodel.VolumeAttributes{ExternalUUID: externalUUID},
		}
		backupVault := &datamodel.BackupVault{BaseModel: datamodel.BaseModel{ID: 1}}
		expectedBackup := &datamodel.Backup{VolumeUUID: externalUUID, Type: backupTypeSCHEDULED}

		mockStorage.On("GetExpertModeVolumeByExternalUUID", mock.Anything, externalUUID).
			Return(&datamodel.ExpertModeVolumes{ExternalUUID: externalUUID, State: datamodel.LifeCycleStateAvailable}, nil).Once()
		mockStorage.On("CreateBackup", mock.Anything, mock.MatchedBy(func(b *datamodel.Backup) bool {
			return b.VolumeUUID == externalUUID
		})).Return(expectedBackup, nil).Once()

		backup, err := activity.CreateScheduledBackup(context.Background(), volume, backupVault, "20240610", "weekly", true)
		assert.NoError(t, err)
		assert.NotNil(t, backup)
		mockStorage.AssertExpectations(t)
	})

	t.Run("CreateScheduledBackupNonExpertModeUsesVolumeUUID", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		activity := ScheduledBackupActivity{SE: mockStorage}

		volUUID := "vol-uuid-regular"
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: volUUID},
			VolumeAttributes: &datamodel.VolumeAttributes{
				ExternalUUID: "ext-uuid-should-not-be-used",
			},
		}
		backupVault := &datamodel.BackupVault{BaseModel: datamodel.BaseModel{ID: 456}}
		timestamp := "20240611"
		scheduleTag := "weekly"

		// For non-expert mode, VolumeUUID must be volume.UUID, not ExternalUUID
		mockStorage.On("CreateBackup", mock.Anything, mock.MatchedBy(func(b *datamodel.Backup) bool {
			return b.VolumeUUID == volUUID
		})).Return(&datamodel.Backup{VolumeUUID: volUUID}, nil).Once()

		backup, err := activity.CreateScheduledBackup(context.Background(), volume, backupVault, timestamp, scheduleTag, false)
		assert.NoError(t, err)
		assert.NotNil(t, backup)
		assert.Equal(t, volUUID, backup.VolumeUUID)
		mockStorage.AssertExpectations(t)
	})

	t.Run("WhenOntapPool_SetsIsExpertModeBackupAndAttributes", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		activity := ScheduledBackupActivity{SE: mockStorage}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "vol-uuid"},
			Name:      "my-volume",
			Pool:      &datamodel.Pool{APIAccessMode: "ONTAP"},
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{"NFSV3"},
			},
		}
		backupVault := &datamodel.BackupVault{BaseModel: datamodel.BaseModel{ID: 123}}

		mockStorage.On("CreateBackup", mock.Anything, mock.MatchedBy(func(b *datamodel.Backup) bool {
			return b.Attributes != nil &&
				b.Attributes.IsExpertModeBackup == true &&
				b.Attributes.VolumeName == volume.Name &&
				len(b.Attributes.Protocols) == 1 && b.Attributes.Protocols[0] == "NFSV3"
		})).Return(&datamodel.Backup{}, nil)

		_, err := activity.CreateScheduledBackup(context.Background(), volume, backupVault, "20240610", "tag", false)
		assert.NoError(t, err)
		mockStorage.AssertExpectations(t)
	})

	t.Run("WhenNonOntapPool_SetsIsExpertModeBackupFalse", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		activity := ScheduledBackupActivity{SE: mockStorage}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "vol-uuid"},
			Name:      "my-volume",
			Pool:      &datamodel.Pool{APIAccessMode: "GCP"},
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{"SMB"},
			},
		}
		backupVault := &datamodel.BackupVault{BaseModel: datamodel.BaseModel{ID: 456}}

		mockStorage.On("CreateBackup", mock.Anything, mock.MatchedBy(func(b *datamodel.Backup) bool {
			return b.Attributes != nil &&
				b.Attributes.IsExpertModeBackup == false &&
				b.Attributes.VolumeName == volume.Name
		})).Return(&datamodel.Backup{}, nil)

		_, err := activity.CreateScheduledBackup(context.Background(), volume, backupVault, "20240610", "tag", false)
		assert.NoError(t, err)
		mockStorage.AssertExpectations(t)
	})

	t.Run("WhenNilPool_SetsIsExpertModeBackupFalse", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		activity := ScheduledBackupActivity{SE: mockStorage}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "vol-uuid"},
			Name:      "my-volume",
			Pool:      nil,
		}
		backupVault := &datamodel.BackupVault{BaseModel: datamodel.BaseModel{ID: 789}}

		mockStorage.On("CreateBackup", mock.Anything, mock.MatchedBy(func(b *datamodel.Backup) bool {
			return b.Attributes != nil && b.Attributes.IsExpertModeBackup == false
		})).Return(&datamodel.Backup{}, nil)

		_, err := activity.CreateScheduledBackup(context.Background(), volume, backupVault, "20240610", "tag", false)
		assert.NoError(t, err)
		mockStorage.AssertExpectations(t)
	})

	t.Run("WhenNilVolumeAttributes_SetsEmptyProtocols", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		activity := ScheduledBackupActivity{SE: mockStorage}

		volume := &datamodel.Volume{
			BaseModel:        datamodel.BaseModel{UUID: "vol-uuid"},
			Name:             "my-volume",
			Pool:             &datamodel.Pool{APIAccessMode: "ONTAP"},
			VolumeAttributes: nil,
		}
		backupVault := &datamodel.BackupVault{BaseModel: datamodel.BaseModel{ID: 100}}

		mockStorage.On("CreateBackup", mock.Anything, mock.MatchedBy(func(b *datamodel.Backup) bool {
			return b.Attributes != nil &&
				b.Attributes.IsExpertModeBackup == true &&
				len(b.Attributes.Protocols) == 0
		})).Return(&datamodel.Backup{}, nil)

		_, err := activity.CreateScheduledBackup(context.Background(), volume, backupVault, "20240610", "tag", false)
		assert.NoError(t, err)
		mockStorage.AssertExpectations(t)
	})

	t.Run("WhenAccountIsPresent_SetsAccountIdentifier", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		activity := ScheduledBackupActivity{SE: mockStorage}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "vol-uuid"},
			Name:      "my-volume",
			Account:   &datamodel.Account{Name: "project-123"},
		}
		backupVault := &datamodel.BackupVault{BaseModel: datamodel.BaseModel{ID: 100}}

		mockStorage.On("CreateBackup", mock.Anything, mock.MatchedBy(func(b *datamodel.Backup) bool {
			return b.Attributes != nil &&
				b.Attributes.AccountIdentifier == "project-123"
		})).Return(&datamodel.Backup{}, nil)

		_, err := activity.CreateScheduledBackup(context.Background(), volume, backupVault, "20240610", "tag", false)
		assert.NoError(t, err)
		mockStorage.AssertExpectations(t)
	})

	t.Run("WhenNilAccount_LeavesAccountIdentifierEmpty", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		activity := ScheduledBackupActivity{SE: mockStorage}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "vol-uuid"},
			Name:      "my-volume",
			Account:   nil,
		}
		backupVault := &datamodel.BackupVault{BaseModel: datamodel.BaseModel{ID: 100}}

		mockStorage.On("CreateBackup", mock.Anything, mock.MatchedBy(func(b *datamodel.Backup) bool {
			return b.Attributes != nil &&
				b.Attributes.AccountIdentifier == ""
		})).Return(&datamodel.Backup{}, nil)

		_, err := activity.CreateScheduledBackup(context.Background(), volume, backupVault, "20240610", "tag", false)
		assert.NoError(t, err)
		mockStorage.AssertExpectations(t)
	})
}

func TestGenerateScheduledSnapshotName(t *testing.T) {
	activity := &ScheduledBackupActivity{}

	t.Run("ReturnsNameWithCorrectFormat", func(t *testing.T) {
		timestamp := "20240610"
		ctx := context.Background()
		name, err := activity.GenerateScheduledSnapshotName(ctx, timestamp)
		parts := strings.Split(name, "-")

		assert.NoError(t, err)
		assert.True(t, strings.HasPrefix(name, "scheduled-snapshot-"))
		assert.True(t, strings.HasSuffix(name, "-"+timestamp))

		assert.Equal(t, 4, len(parts))
		assert.Equal(t, "scheduled", parts[0])
		assert.Equal(t, "snapshot", parts[1])
		assert.Equal(t, timestamp, parts[3])
	})

	t.Run("ReturnsDifferentNames", func(t *testing.T) {
		timestamp := "20240610"
		ctx := context.Background()
		name1, _ := activity.GenerateScheduledSnapshotName(ctx, timestamp)
		name2, _ := activity.GenerateScheduledSnapshotName(ctx, timestamp)
		assert.NotEqual(t, name1, name2)
	})
}

func TestGetVolumesByBackupPolicyUUID(t *testing.T) {
	t.Run("GetVolumesByBackupPolicyUUIDSuccess", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		activity := ScheduledBackupActivity{SE: mockStorage}

		ctx := context.Background()
		backupPolicyUUID := "policy-uuid"
		policyEnabled := true
		accountID := int64(42)
		limit := 20
		offset := 0

		expectedVolumes := []*datamodel.Volume{
			{BaseModel: datamodel.BaseModel{UUID: "vol-1"}, DataProtection: &datamodel.DataProtection{BackupPolicyID: backupPolicyUUID, ScheduledBackupEnabled: &policyEnabled}},
			{BaseModel: datamodel.BaseModel{UUID: "vol-2"}, DataProtection: &datamodel.DataProtection{BackupPolicyID: backupPolicyUUID, ScheduledBackupEnabled: &policyEnabled}},
		}

		conditions := [][]interface{}{
			{"account_id = ?", accountID},
			{"state = ?", datamodel.LifeCycleStateREADY},
			{"data_protection->>'backup_policy_id' = ?", backupPolicyUUID},
			{"data_protection->>'scheduled_backup_enabled' = 'true'"},
		}
		expertModeConditions := [][]interface{}{
			{"account_id = ?", accountID},
			{"state = ?", datamodel.LifeCycleStateAvailable},
			{"data_protection->>'backup_policy_id' = ?", backupPolicyUUID},
			{"data_protection->>'scheduled_backup_enabled' = 'true'"},
		}
		mockStorage.On("ListVolumesWithPagination", ctx, conditions, mock.Anything).Return(expectedVolumes, nil).Once()
		mockStorage.On("ListExpertModeVolumesWithPagination", ctx, expertModeConditions, mock.Anything).Return([]*datamodel.ExpertModeVolumes{}, nil).Once()

		volumes, err := activity.GetVolumesByBackupPolicyUUID(ctx, backupPolicyUUID, accountID, limit, offset)
		assert.NoError(t, err)
		assert.Equal(t, expectedVolumes, volumes)
		mockStorage.AssertExpectations(t)
	})
	t.Run("GetVolumesByBackupPolicyUUIDFails", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		activity := ScheduledBackupActivity{SE: mockStorage}

		ctx := context.Background()
		backupPolicyUUID := "policy-uuid"
		accountID := int64(42)
		limit := 20
		offset := 0

		conditions := [][]interface{}{
			{"account_id = ?", accountID},
			{"state = ?", datamodel.LifeCycleStateREADY},
			{"data_protection->>'backup_policy_id' = ?", backupPolicyUUID},
			{"data_protection->>'scheduled_backup_enabled' = 'true'"},
		}

		mockStorage.On("ListVolumesWithPagination", ctx, conditions, mock.Anything).Return(nil, errors.New("db error")).Once()

		volumes, err := activity.GetVolumesByBackupPolicyUUID(ctx, backupPolicyUUID, accountID, limit, offset)
		assert.Nil(t, volumes)
		assert.Error(t, err)
		assert.Equal(t, err.Error(), "db error")

		mockStorage.AssertExpectations(t)
	})
	t.Run("GetVolumesByBackupPolicyUUIDWithDifferentPaginationParams", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		activity := ScheduledBackupActivity{SE: mockStorage}

		ctx := context.Background()
		backupPolicyUUID := "policy-uuid"
		policyEnabled := true
		accountID := int64(42)
		limit := 50
		offset := 100

		expectedVolumes := []*datamodel.Volume{
			{BaseModel: datamodel.BaseModel{UUID: "vol-101"}, DataProtection: &datamodel.DataProtection{BackupPolicyID: backupPolicyUUID, ScheduledBackupEnabled: &policyEnabled}},
		}

		conditions := [][]interface{}{
			{"account_id = ?", accountID},
			{"state = ?", datamodel.LifeCycleStateREADY},
			{"data_protection->>'backup_policy_id' = ?", backupPolicyUUID},
			{"data_protection->>'scheduled_backup_enabled' = 'true'"},
		}
		expertModeConditions := [][]interface{}{
			{"account_id = ?", accountID},
			{"state = ?", datamodel.LifeCycleStateAvailable},
			{"data_protection->>'backup_policy_id' = ?", backupPolicyUUID},
			{"data_protection->>'scheduled_backup_enabled' = 'true'"},
		}
		mockStorage.On("ListVolumesWithPagination", ctx, conditions, mock.Anything).Return(expectedVolumes, nil).Once()
		mockStorage.On("ListExpertModeVolumesWithPagination", ctx, expertModeConditions, mock.Anything).Return([]*datamodel.ExpertModeVolumes{}, nil).Once()

		volumes, err := activity.GetVolumesByBackupPolicyUUID(ctx, backupPolicyUUID, accountID, limit, offset)
		assert.NoError(t, err)
		assert.Equal(t, expectedVolumes, volumes)
		assert.Len(t, volumes, 1)
		mockStorage.AssertExpectations(t)
	})
	t.Run("GetVolumesByBackupPolicyUUIDReturnsEmptyList", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		activity := ScheduledBackupActivity{SE: mockStorage}

		ctx := context.Background()
		backupPolicyUUID := "policy-uuid"
		accountID := int64(42)
		limit := 20
		offset := 0

		conditions := [][]interface{}{
			{"account_id = ?", accountID},
			{"state = ?", datamodel.LifeCycleStateREADY},
			{"data_protection->>'backup_policy_id' = ?", backupPolicyUUID},
			{"data_protection->>'scheduled_backup_enabled' = 'true'"},
		}
		expertModeConditions := [][]interface{}{
			{"account_id = ?", accountID},
			{"state = ?", datamodel.LifeCycleStateAvailable},
			{"data_protection->>'backup_policy_id' = ?", backupPolicyUUID},
			{"data_protection->>'scheduled_backup_enabled' = 'true'"},
		}
		mockStorage.On("ListVolumesWithPagination", ctx, conditions, mock.Anything).Return([]*datamodel.Volume{}, nil).Once()
		mockStorage.On("ListExpertModeVolumesWithPagination", ctx, expertModeConditions, mock.Anything).Return([]*datamodel.ExpertModeVolumes{}, nil).Once()

		volumes, err := activity.GetVolumesByBackupPolicyUUID(ctx, backupPolicyUUID, accountID, limit, offset)
		assert.NoError(t, err)
		assert.NotNil(t, volumes)
		assert.Len(t, volumes, 0)
		mockStorage.AssertExpectations(t)
	})

	t.Run("GetVolumesByBackupPolicyUUIDIncludesExpertModeVolumes", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		activity := ScheduledBackupActivity{SE: mockStorage}

		ctx := context.Background()
		backupPolicyUUID := "policy-uuid"
		policyEnabled := true
		accountID := int64(42)
		limit := 20
		offset := 0

		regularVolumes := []*datamodel.Volume{
			{BaseModel: datamodel.BaseModel{UUID: "vol-1"}, DataProtection: &datamodel.DataProtection{BackupPolicyID: backupPolicyUUID, ScheduledBackupEnabled: &policyEnabled}},
		}
		network := "projects/test/global/networks/test-net"
		expertModeVolumes := []*datamodel.ExpertModeVolumes{
			{
				BaseModel:    datamodel.BaseModel{UUID: "em-vol-1"},
				Name:         "expert-vol",
				ExternalUUID: "em-ext-uuid",
				State:        datamodel.LifeCycleStateREADY,
				AccountID:    accountID,
				Pool:         &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-1"}, Network: network},
				BackupConfig: &datamodel.DataProtection{BackupPolicyID: backupPolicyUUID, ScheduledBackupEnabled: &policyEnabled},
			},
		}

		conditions := [][]interface{}{
			{"account_id = ?", accountID},
			{"state = ?", datamodel.LifeCycleStateREADY},
			{"data_protection->>'backup_policy_id' = ?", backupPolicyUUID},
			{"data_protection->>'scheduled_backup_enabled' = 'true'"},
		}
		expertModeConditions := [][]interface{}{
			{"account_id = ?", accountID},
			{"state = ?", datamodel.LifeCycleStateAvailable},
			{"data_protection->>'backup_policy_id' = ?", backupPolicyUUID},
			{"data_protection->>'scheduled_backup_enabled' = 'true'"},
		}
		mockStorage.On("ListVolumesWithPagination", ctx, conditions, mock.Anything).Return(regularVolumes, nil).Once()
		mockStorage.On("ListExpertModeVolumesWithPagination", ctx, expertModeConditions, mock.Anything).Return(expertModeVolumes, nil).Once()

		volumes, err := activity.GetVolumesByBackupPolicyUUID(ctx, backupPolicyUUID, accountID, limit, offset)
		assert.NoError(t, err)
		// Should include both the regular volume and the converted expert mode volume
		assert.Len(t, volumes, 2)
		// The second volume should be the converted expert mode volume with ExternalUUID set
		assert.Equal(t, "em-ext-uuid", volumes[1].VolumeAttributes.ExternalUUID)
		assert.Equal(t, network, volumes[1].VolumeAttributes.VendorSubnetID)
		assert.Equal(t, backupPolicyUUID, volumes[1].DataProtection.BackupPolicyID)
		mockStorage.AssertExpectations(t)
	})

	t.Run("GetVolumesByBackupPolicyUUIDExpertModeFailureGraceful", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		activity := ScheduledBackupActivity{SE: mockStorage}

		ctx := context.Background()
		backupPolicyUUID := "policy-uuid"
		policyEnabled := true
		accountID := int64(42)
		limit := 20
		offset := 0

		regularVolumes := []*datamodel.Volume{
			{BaseModel: datamodel.BaseModel{UUID: "vol-1"}, DataProtection: &datamodel.DataProtection{BackupPolicyID: backupPolicyUUID, ScheduledBackupEnabled: &policyEnabled}},
		}

		conditions := [][]interface{}{
			{"account_id = ?", accountID},
			{"state = ?", datamodel.LifeCycleStateREADY},
			{"data_protection->>'backup_policy_id' = ?", backupPolicyUUID},
			{"data_protection->>'scheduled_backup_enabled' = 'true'"},
		}
		expertModeConditions := [][]interface{}{
			{"account_id = ?", accountID},
			{"state = ?", datamodel.LifeCycleStateAvailable},
			{"data_protection->>'backup_policy_id' = ?", backupPolicyUUID},
			{"data_protection->>'scheduled_backup_enabled' = 'true'"},
		}
		mockStorage.On("ListVolumesWithPagination", ctx, conditions, mock.Anything).Return(regularVolumes, nil).Once()
		// Expert mode fetch fails — should NOT propagate error, just return regular volumes
		mockStorage.On("ListExpertModeVolumesWithPagination", ctx, expertModeConditions, mock.Anything).Return(nil, errors.New("expert mode db error")).Once()

		volumes, err := activity.GetVolumesByBackupPolicyUUID(ctx, backupPolicyUUID, accountID, limit, offset)
		assert.NoError(t, err)
		// Only regular volumes should be returned; expert mode error is swallowed
		assert.Equal(t, regularVolumes, volumes)
		mockStorage.AssertExpectations(t)
	})
}

func TestRandomString(t *testing.T) {
	t.Run("ReturnsStringOfCorrectLength", func(t *testing.T) {
		for _, n := range []int{0, 1, 5, 10, 20} {
			s := RandomString(n)
			if len(s) != n {
				t.Errorf("expected length %d, got %d", n, len(s))
			}
		}
	})

	t.Run("ReturnsOnlyLetters", func(t *testing.T) {
		s := RandomString(100)
		for _, c := range s {
			if c < 'a' || c > 'z' {
				t.Errorf("unexpected character: %c", c)
			}
		}
	})

	t.Run("ReturnsDifferentStrings", func(t *testing.T) {
		s1 := RandomString(10)
		s2 := RandomString(10)
		if s1 == s2 {
			t.Errorf("expected different strings, got %s and %s", s1, s2)
		}
	})
}

func TestFetchScheduledBackupForDeletion(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	activity := ScheduledBackupActivity{SE: mockStorage}

	ctx := context.Background()
	volume := &datamodel.Volume{BaseModel: datamodel.BaseModel{UUID: "vol-uuid"}}
	backupPolicy := &datamodel.BackupPolicy{BaseModel: datamodel.BaseModel{UUID: "policy-uuid"}}

	expectedBackups := []*datamodel.Backup{
		{BaseModel: datamodel.BaseModel{UUID: "backup-1"}},
		{BaseModel: datamodel.BaseModel{UUID: "backup-2"}},
	}

	t.Run("FetchScheduledBackupForDeletionSuccess", func(t *testing.T) {
		mockStorage.On("FetchScheduledBackupsForDeletion", ctx, volume, backupPolicy, false).Return(expectedBackups, nil).Once()
		backups, err := activity.FetchScheduledBackupForDeletion(ctx, volume, backupPolicy, false)
		assert.NoError(t, err)
		assert.Equal(t, expectedBackups, backups)
		mockStorage.AssertExpectations(t)
	})

	t.Run("FetchScheduledBackupForDeletionFails", func(t *testing.T) {
		mockStorage.On("FetchScheduledBackupsForDeletion", ctx, volume, backupPolicy, false).Return(nil, errors.New("db error")).Once()
		backups, err := activity.FetchScheduledBackupForDeletion(ctx, volume, backupPolicy, false)
		assert.Error(t, err)
		assert.Nil(t, backups)
		assert.Equal(t, err, errors.New("db error"))
		mockStorage.AssertExpectations(t)
	})

	t.Run("FetchScheduledBackupForDeletionExpertModeSuccess", func(t *testing.T) {
		mockStorageEM := database.NewMockStorage(t)
		activityEM := ScheduledBackupActivity{SE: mockStorageEM}

		ctxEM := context.Background()
		externalUUID := "ext-vol-uuid"
		volEM := &datamodel.Volume{
			BaseModel:        datamodel.BaseModel{UUID: "db-vol-uuid"},
			VolumeAttributes: &datamodel.VolumeAttributes{ExternalUUID: externalUUID},
		}
		policyEM := &datamodel.BackupPolicy{BaseModel: datamodel.BaseModel{UUID: "policy-em-uuid"}}
		expectedBackups := []*datamodel.Backup{
			{BaseModel: datamodel.BaseModel{UUID: "em-backup-1"}, VolumeUUID: externalUUID},
		}

		mockStorageEM.On("FetchScheduledBackupsForDeletion", ctxEM, volEM, policyEM, true).Return(expectedBackups, nil).Once()

		backups, err := activityEM.FetchScheduledBackupForDeletion(ctxEM, volEM, policyEM, true)
		assert.NoError(t, err)
		assert.Equal(t, expectedBackups, backups)
		mockStorageEM.AssertExpectations(t)
	})
}

func TestHydrateCreatedBackupsToCCFE(t *testing.T) {
	ctx := context.Background()
	mockStorage := database.NewMockStorage(t)
	activity := ScheduledBackupActivity{SE: mockStorage}

	originalGenerateCallbackToken := auth.GenerateCallbackToken
	originalHydrateCreatedScheduledBackups := common.HydrateCreatedBackups
	originalHydrateDeletedScheduledBackups := common.HydrateDeletedBackups
	defer func() {
		auth.GenerateCallbackToken = originalGenerateCallbackToken
		common.HydrateCreatedBackups = originalHydrateCreatedScheduledBackups
		common.HydrateDeletedBackups = originalHydrateDeletedScheduledBackups
	}()

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "volume-uuid-1"},
		Account: &datamodel.Account{
			Name: "account-1",
		},
	}
	backups := []*datamodel.Backup{
		{BaseModel: datamodel.BaseModel{UUID: "backup-uuid-1"}, Name: "backup1", SizeInBytes: 1024000, BackupVault: &datamodel.BackupVault{Name: "backup-vault-1", RegionName: "us-east1", SourceRegionName: nillable.ToPointer("us-east1")}},
		{BaseModel: datamodel.BaseModel{UUID: "backup-uuid-2"}, Name: "backup2", SizeInBytes: 1083532, BackupVault: &datamodel.BackupVault{Name: "backup-vault-1", RegionName: "us-east1"}, Attributes: &datamodel.BackupAttributes{SourceVolumeZone: "us-east1-b"}},
	}

	t.Run("HydrateCreatedBackupsToCcfeSuccess", func(t *testing.T) {
		auth.GenerateCallbackToken = func(ctx context.Context) (string, error) {
			return "mock-token", nil
		}
		common.HydrateCreatedBackups = func(ctx context.Context, logger log.Logger, resources []models.Request, backupVaultName string, location string, projectId string, token string) error {
			return nil
		}
		utils.GetBackupRegion = func(*datamodel.Volume) (string, error) {
			return "mock-region", nil
		}
		utils.GetSourceVolumePathFromBackup = func(*datamodel.Backup) string {
			return "mock-source-volume-path"
		}

		err := activity.HydrateCreatedBackupsToCCFE(ctx, volume, backups, "backup-vault-1")
		assert.NoError(t, err)
		mockStorage.AssertExpectations(t)
	})

	t.Run("WhenNoBackupsAreToBeHydrated", func(t *testing.T) {
		err := activity.HydrateCreatedBackupsToCCFE(ctx, volume, nil, "backup-vault-1")
		assert.NoError(t, err)
		mockStorage.AssertExpectations(t)

		err = activity.HydrateCreatedBackupsToCCFE(ctx, volume, []*datamodel.Backup{}, "backup-vault-1")
		assert.NoError(t, err)
		mockStorage.AssertExpectations(t)
	})

	t.Run("WhenGenerationOfCallbackTokenFails", func(t *testing.T) {
		auth.GenerateCallbackToken = func(ctx context.Context) (string, error) {
			return "", errors.New("could not generate CCFE auth token")
		}

		err := activity.HydrateCreatedBackupsToCCFE(ctx, volume, backups, "backup-vault-1")
		assert.Error(t, err)
		assert.Equal(t, "could not generate CCFE auth token", err.Error())
		mockStorage.AssertExpectations(t)
	})

	t.Run("WhenRegionCouldNotBeFetched", func(t *testing.T) {
		auth.GenerateCallbackToken = func(ctx context.Context) (string, error) {
			return "mock-token", nil
		}
		common.HydrateCreatedBackups = func(ctx context.Context, logger log.Logger, resources []models.Request, backupVaultName string, location string, projectId string, token string) error {
			return errors.New("could not hydrate backups to CCFE")
		}
		utils.GetBackupRegion = func(*datamodel.Volume) (string, error) {
			return "", errors.New("could not get backup region")
		}

		err := activity.HydrateCreatedBackupsToCCFE(ctx, volume, backups, "backup-vault-1")
		assert.Error(t, err)
		assert.Equal(t, "could not get backup region", err.Error())
		mockStorage.AssertExpectations(t)
	})

	t.Run("WhenHydrationToCcfeFails", func(t *testing.T) {
		auth.GenerateCallbackToken = func(ctx context.Context) (string, error) {
			return "mock-token", nil
		}
		common.HydrateCreatedBackups = func(ctx context.Context, logger log.Logger, resources []models.Request, backupVaultName string, location string, projectId string, token string) error {
			return errors.New("could not hydrate backups to CCFE")
		}
		utils.GetBackupRegion = func(*datamodel.Volume) (string, error) {
			return "mock-region", nil
		}
		utils.GetSourceVolumePathFromBackup = func(*datamodel.Backup) string {
			return "mock-source-volume-path"
		}

		err := activity.HydrateCreatedBackupsToCCFE(ctx, volume, backups, "backup-vault-1")
		assert.Error(t, err)
		assert.Equal(t, "could not hydrate backups to CCFE", err.Error())
		mockStorage.AssertExpectations(t)
	})

	t.Run("WhenHydrationIsDisabled", func(t *testing.T) {
		// Store original value
		originalHydrationEnabled := hydrationEnabled
		defer func() {
			hydrationEnabled = originalHydrationEnabled
		}()

		// Disable hydration
		hydrationEnabled = false

		// The method should return early without calling any external functions
		err := activity.HydrateCreatedBackupsToCCFE(ctx, volume, backups, "backup-vault-1")
		assert.NoError(t, err)
		mockStorage.AssertExpectations(t)
	})

	t.Run("OntapPool_UsesOntapModeAndSetsSourceStoragePool", func(t *testing.T) {
		ontapVolume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "vol-uuid"},
			Account:   &datamodel.Account{Name: "project-123"},
			Pool:      &datamodel.Pool{Name: "my-pool", APIAccessMode: "ONTAP", PoolAttributes: &datamodel.PoolAttributes{PrimaryZone: "us-central1"}},
		}

		auth.GenerateCallbackToken = func(ctx context.Context) (string, error) {
			return "mock-token", nil
		}
		utils.GetBackupRegion = func(*datamodel.Volume) (string, error) {
			return "us-central1", nil
		}
		utils.GetSourceVolumePathFromBackup = func(*datamodel.Backup) string {
			return "mock-source-volume-path"
		}
		var capturedMode, capturedPoolPath string
		common.HydrateCreatedBackups = func(ctx context.Context, logger log.Logger, resources []models.Request, backupVaultName string, location string, projectId string, token string) error {
			if len(resources) > 0 && resources[0].Backup != nil {
				capturedMode = resources[0].Backup.Mode
				capturedPoolPath = resources[0].Backup.SourceStoragePool
			}
			return nil
		}

		err := activity.HydrateCreatedBackupsToCCFE(ctx, ontapVolume, backups, "backup-vault-1")
		assert.NoError(t, err)
		assert.Equal(t, models.BackupHydrationModeONTAP, capturedMode)
		assert.Equal(t, "projects/project-123/locations/us-central1/storagePools/my-pool", capturedPoolPath)
	})

	t.Run("NonOntapPool_UsesDefaultModeNoPoolPath", func(t *testing.T) {
		gcpVolume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "vol-uuid"},
			Account:   &datamodel.Account{Name: "account-1"},
			Pool:      &datamodel.Pool{Name: "my-pool", APIAccessMode: "GCP"},
		}

		auth.GenerateCallbackToken = func(ctx context.Context) (string, error) {
			return "mock-token", nil
		}
		utils.GetBackupRegion = func(*datamodel.Volume) (string, error) {
			return "mock-region", nil
		}
		utils.GetSourceVolumePathFromBackup = func(*datamodel.Backup) string {
			return "mock-source-volume-path"
		}

		var capturedMode, capturedPoolPath string
		common.HydrateCreatedBackups = func(ctx context.Context, logger log.Logger, resources []models.Request, backupVaultName string, location string, projectId string, token string) error {
			if len(resources) > 0 && resources[0].Backup != nil {
				capturedMode = resources[0].Backup.Mode
				capturedPoolPath = resources[0].Backup.SourceStoragePool
			}
			return nil
		}

		err := activity.HydrateCreatedBackupsToCCFE(ctx, gcpVolume, backups, "backup-vault-1")
		assert.NoError(t, err)
		assert.Equal(t, models.BackupHydrationModeDefault, capturedMode)
		assert.Empty(t, capturedPoolPath)
	})

	t.Run("NilPool_UsesDefaultModeNoPoolPath", func(t *testing.T) {
		noPoolVolume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "vol-uuid"},
			Account:   &datamodel.Account{Name: "account-1"},
			Pool:      nil,
		}

		auth.GenerateCallbackToken = func(ctx context.Context) (string, error) {
			return "mock-token", nil
		}
		utils.GetBackupRegion = func(*datamodel.Volume) (string, error) {
			return "mock-region", nil
		}
		utils.GetSourceVolumePathFromBackup = func(*datamodel.Backup) string {
			return "mock-source-volume-path"
		}

		var capturedMode, capturedPoolPath string
		common.HydrateCreatedBackups = func(ctx context.Context, logger log.Logger, resources []models.Request, backupVaultName string, location string, projectId string, token string) error {
			if len(resources) > 0 && resources[0].Backup != nil {
				capturedMode = resources[0].Backup.Mode
				capturedPoolPath = resources[0].Backup.SourceStoragePool
			}
			return nil
		}

		err := activity.HydrateCreatedBackupsToCCFE(ctx, noPoolVolume, backups, "backup-vault-1")
		assert.NoError(t, err)
		assert.Equal(t, models.BackupHydrationModeDefault, capturedMode)
		assert.Empty(t, capturedPoolPath)
	})
}

func TestHydrateDeletedBackupsToCCFE(t *testing.T) {
	ctx := context.Background()
	mockStorage := database.NewMockStorage(t)
	activity := ScheduledBackupActivity{SE: mockStorage}

	originalGenerateCallbackToken := auth.GenerateCallbackToken
	originalHydrateCreatedScheduledBackups := common.HydrateCreatedBackups
	originalHydrateDeletedScheduledBackups := common.HydrateDeletedBackups
	originalGetBackupRegion := utils.GetBackupRegion
	defer func() {
		auth.GenerateCallbackToken = originalGenerateCallbackToken
		common.HydrateCreatedBackups = originalHydrateCreatedScheduledBackups
		common.HydrateDeletedBackups = originalHydrateDeletedScheduledBackups
		utils.GetBackupRegion = originalGetBackupRegion
	}()

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "volume-uuid-1"},
		Account: &datamodel.Account{
			Name: "account-1",
		},
	}
	backups := []*datamodel.Backup{
		{BaseModel: datamodel.BaseModel{UUID: "backup-uuid-1"}, Name: "backup1", SizeInBytes: 1024000, BackupVault: &datamodel.BackupVault{Name: "backup-vault-1", RegionName: "us-east1"}},
		{BaseModel: datamodel.BaseModel{UUID: "backup-uuid-2"}, Name: "backup2", SizeInBytes: 1083532, BackupVault: &datamodel.BackupVault{Name: "backup-vault-1", RegionName: "us-east1"}},
	}

	t.Run("WhenNoBackupsAreToBeHydrated", func(t *testing.T) {
		err := activity.HydrateDeletedBackupsToCCFE(ctx, volume, nil, "backup-vault-1")
		assert.NoError(t, err)
		mockStorage.AssertExpectations(t)

		err = activity.HydrateDeletedBackupsToCCFE(ctx, volume, []*datamodel.Backup{}, "backup-vault-1")
		assert.NoError(t, err)
		mockStorage.AssertExpectations(t)
	})

	t.Run("HydrateDeletedBackupsToCcfeSuccess", func(t *testing.T) {
		auth.GenerateCallbackToken = func(ctx context.Context) (string, error) {
			return "mock-token", nil
		}
		common.HydrateDeletedBackups = func(ctx context.Context, logger log.Logger, names []string, backupVaultName string, location string, projectId string, token string) error {
			return nil
		}
		utils.GetBackupRegion = func(*datamodel.Volume) (string, error) {
			return "mock-region", nil
		}

		err := activity.HydrateDeletedBackupsToCCFE(ctx, volume, backups, "backup-vault-1")
		assert.NoError(t, err)
		mockStorage.AssertExpectations(t)
	})

	t.Run("WhenGenerationOfCallbackTokenFails", func(t *testing.T) {
		auth.GenerateCallbackToken = func(ctx context.Context) (string, error) {
			return "", errors.New("could not generate CCFE auth token")
		}

		err := activity.HydrateDeletedBackupsToCCFE(ctx, volume, backups, "backup-vault-1")
		assert.Error(t, err)
		assert.Equal(t, "could not generate CCFE auth token", err.Error())
		mockStorage.AssertExpectations(t)
	})

	t.Run("WhenRegionCouldNotBeFetched", func(t *testing.T) {
		auth.GenerateCallbackToken = func(ctx context.Context) (string, error) {
			return "mock-token", nil
		}
		utils.GetBackupRegion = func(*datamodel.Volume) (string, error) {
			return "", errors.New("could not get backup region")
		}

		err := activity.HydrateDeletedBackupsToCCFE(ctx, volume, backups, "backup-vault-1")
		assert.Error(t, err)
		assert.Equal(t, "could not get backup region", err.Error())
		mockStorage.AssertExpectations(t)
	})

	t.Run("WhenHydrationToCcfeFails", func(t *testing.T) {
		auth.GenerateCallbackToken = func(ctx context.Context) (string, error) {
			return "mock-token", nil
		}
		common.HydrateDeletedBackups = func(ctx context.Context, logger log.Logger, names []string, backupVaultName string, location string, projectId string, token string) error {
			return errors.New("could not hydrate backups to CCFE")
		}
		utils.GetBackupRegion = func(*datamodel.Volume) (string, error) {
			return "mock-region", nil
		}

		err := activity.HydrateDeletedBackupsToCCFE(ctx, volume, backups, "backup-vault-1")
		assert.Error(t, err)
		assert.Equal(t, "could not hydrate backups to CCFE", err.Error())
		mockStorage.AssertExpectations(t)
	})

	t.Run("WhenHydrationIsDisabled", func(t *testing.T) {
		// Store original value
		originalHydrationEnabled := hydrationEnabled
		defer func() {
			hydrationEnabled = originalHydrationEnabled
		}()

		// Disable hydration
		hydrationEnabled = false

		// The method should return early without calling any external functions
		err := activity.HydrateDeletedBackupsToCCFE(ctx, volume, backups, "backup-vault-1")
		assert.NoError(t, err)
		mockStorage.AssertExpectations(t)
	})
}

func TestCreateBackupSnapshotInDB(t *testing.T) {
	ctx := context.Background()
	mockStorage := database.NewMockStorage(t)
	activity := ScheduledBackupActivity{SE: mockStorage}

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{ID: 123, UUID: "volume-uuid"},
		AccountID: 456,
		Account: &datamodel.Account{
			Name: "test-account",
		},
	}
	snapshotName := "test-snapshot-name"

	t.Run("CreateBackupSnapshotInDBSuccess", func(t *testing.T) {
		expectedSnapshot := &datamodel.Snapshot{
			BaseModel: datamodel.BaseModel{ID: 789, UUID: "snapshot-uuid"},
			Name:      snapshotName,
		}

		mockStorage.On("CreatingSnapshot", ctx, mock.Anything).Return(expectedSnapshot, nil).Once()

		snapshot, err := activity.CreateBackupSnapshotInDB(ctx, volume, snapshotName)
		assert.NoError(t, err)
		assert.Equal(t, expectedSnapshot, snapshot)

		// Verify the snapshot object passed to CreatingSnapshot
		callArgs := mockStorage.Calls[0].Arguments
		passedSnapshot := callArgs[1].(*datamodel.Snapshot)
		assert.Equal(t, snapshotName, passedSnapshot.Name)
		assert.Equal(t, activities.BackupComment, passedSnapshot.Description)
		assert.Equal(t, volume.ID, passedSnapshot.VolumeID)
		assert.Equal(t, volume.AccountID, passedSnapshot.AccountID)
		assert.Equal(t, volume, passedSnapshot.Volume)
		assert.Equal(t, volume.Account, passedSnapshot.Account)
		assert.False(t, passedSnapshot.IsAppConsistent)
		assert.Equal(t, SnapshotTypeBackup, passedSnapshot.Type)
		assert.NotNil(t, passedSnapshot.SnapshotAttributes)

		mockStorage.AssertExpectations(t)
	})

	t.Run("CreateBackupSnapshotInDBFails", func(t *testing.T) {
		mockStorage.On("CreatingSnapshot", ctx, mock.Anything).Return(nil, errors.New("database error")).Once()

		snapshot, err := activity.CreateBackupSnapshotInDB(ctx, volume, snapshotName)
		assert.Error(t, err)
		assert.Nil(t, snapshot)
		// CreateBackupSnapshotInDB wraps plain errors with WrapAsTemporalApplicationError,
		// but since it's not a CustomError, it returns the original error unchanged
		assert.Equal(t, "database error", err.Error())

		mockStorage.AssertExpectations(t)
	})
}

func TestUpdateBackupSnapshotInDB(t *testing.T) {
	ctx := context.Background()
	mockStorage := database.NewMockStorage(t)
	activity := ScheduledBackupActivity{SE: mockStorage}

	dbSnapshot := &datamodel.Snapshot{
		BaseModel:          datamodel.BaseModel{ID: 789, UUID: "snapshot-uuid"},
		Name:               "test-snapshot",
		SnapshotAttributes: &datamodel.SnapshotAttributes{},
	}

	ontapSnapshot := &vsa.SnapshotProviderResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "ontap-external-uuid",
		},
		SizeInBytes:        1024000,
		LogicalSizeInBytes: 512000,
	}

	t.Run("UpdateBackupSnapshotInDBSuccess", func(t *testing.T) {
		updatedSnapshot := &datamodel.Snapshot{
			BaseModel:    datamodel.BaseModel{ID: 789, UUID: "snapshot-uuid"},
			Name:         "test-snapshot",
			State:        datamodel.LifeCycleStateREADY,
			StateDetails: datamodel.LifeCycleStateAvailableDetails,
			ExternalUUID: "ontap-external-uuid",
			SnapshotAttributes: &datamodel.SnapshotAttributes{
				SizeInBytes:            1024000,
				ExternalUUID:           "ontap-external-uuid",
				LogicalSizeUsedInBytes: 512000,
			},
		}

		mockStorage.On("UpdateSnapshot", ctx, mock.Anything).Return(updatedSnapshot, nil).Once()

		result, err := activity.UpdateBackupSnapshotInDB(ctx, dbSnapshot, ontapSnapshot)
		assert.NoError(t, err)
		assert.Equal(t, updatedSnapshot, result)

		// Verify the snapshot object was updated correctly
		assert.Equal(t, datamodel.LifeCycleStateREADY, dbSnapshot.State)
		assert.Equal(t, datamodel.LifeCycleStateAvailableDetails, dbSnapshot.StateDetails)
		assert.Equal(t, ontapSnapshot.SizeInBytes, dbSnapshot.SnapshotAttributes.SizeInBytes)
		assert.Equal(t, ontapSnapshot.ExternalUUID, dbSnapshot.ExternalUUID)
		assert.Equal(t, ontapSnapshot.ExternalUUID, dbSnapshot.SnapshotAttributes.ExternalUUID)
		assert.Equal(t, ontapSnapshot.LogicalSizeInBytes, dbSnapshot.SnapshotAttributes.LogicalSizeUsedInBytes)

		mockStorage.AssertExpectations(t)
	})

	t.Run("UpdateBackupSnapshotInDBFails", func(t *testing.T) {
		mockStorage.On("UpdateSnapshot", ctx, mock.Anything).Return(nil, errors.New("database error")).Once()

		result, err := activity.UpdateBackupSnapshotInDB(ctx, dbSnapshot, ontapSnapshot)
		assert.Error(t, err)
		assert.Nil(t, result)
		// UpdateBackupSnapshotInDB returns the raw error, not wrapped as ApplicationError
		assert.Equal(t, "database error", err.Error())

		mockStorage.AssertExpectations(t)
	})
}

func TestDeleteBackupSnapshotInDB(t *testing.T) {
	ctx := context.Background()
	mockStorage := database.NewMockStorage(t)
	activity := ScheduledBackupActivity{SE: mockStorage}

	snapshotName := "test-snapshot-name"

	t.Run("DeleteBackupSnapshotInDBSuccess", func(t *testing.T) {
		mockStorage.On("DeleteSnapshot", ctx, snapshotName).Return(&datamodel.Snapshot{}, nil).Once()

		err := activity.DeleteBackupSnapshotInDB(ctx, snapshotName)
		assert.NoError(t, err)

		mockStorage.AssertExpectations(t)
	})

	t.Run("DeleteBackupSnapshotInDBFails", func(t *testing.T) {
		mockStorage.On("DeleteSnapshot", ctx, snapshotName).Return(nil, errors.New("database error")).Once()

		err := activity.DeleteBackupSnapshotInDB(ctx, snapshotName)
		assert.Error(t, err)
		// DeleteBackupSnapshotInDB wraps plain errors with WrapAsTemporalApplicationError,
		// but since it's not a CustomError, it returns the original error unchanged
		assert.Equal(t, "database error", err.Error())

		mockStorage.AssertExpectations(t)
	})
}

func expectRecalcVolumeChainBytes(mockStorage *database.MockStorage, ctx context.Context, volumeUUID string, sum int64) {
	mockStorage.On("SumVolumeBackupChainBytes", ctx, volumeUUID).Return(sum, nil).Once()
	mockStorage.On("GetExpertModeVolumeByExternalUUID", ctx, volumeUUID).Return(nil, customerrors.NewNotFoundErr("expert mode volume", nil)).Once()
	recalcVol := &datamodel.Volume{BaseModel: datamodel.BaseModel{UUID: volumeUUID}, DataProtection: &datamodel.DataProtection{}}
	mockStorage.On("GetVolume", ctx, volumeUUID).Return(recalcVol, nil).Once()
	mockStorage.On("UpdateVolumeFields", ctx, volumeUUID, mock.Anything).Return(nil).Once()
}

func TestUpdateBackupSize(t *testing.T) {
	ctx := context.Background()
	mockStorage := database.NewMockStorage(t)
	activity := ScheduledBackupActivity{SE: mockStorage}

	t.Run("UpdateBackupSizeSuccessWithNonZeroSize", func(t *testing.T) {
		backup := &datamodel.Backup{
			BaseModel: datamodel.BaseModel{
				UUID: "backup-uuid",
			},
			VolumeUUID:              "volume-uuid",
			LatestLogicalBackupSize: 1024000,
		}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{
				UUID: "volume-uuid",
			},
			DataProtection: &datamodel.DataProtection{},
		}

		mockStorage.On("FinishBackup", ctx, backup).Return(backup, nil).Once()
		mockStorage.On("UpdateBackupLatestLogicalBackupSizeByVolume", ctx, "volume-uuid", "backup-uuid").Return(nil).Once()
		expectRecalcVolumeChainBytes(mockStorage, ctx, "volume-uuid", 1024000)

		err := activity.UpdateBackupSize(ctx, backup, volume, false)
		assert.NoError(t, err)
		mockStorage.AssertExpectations(t)
	})

	t.Run("UpdateBackupSizeSuccessWithZeroSize", func(t *testing.T) {
		backup := &datamodel.Backup{
			BaseModel: datamodel.BaseModel{
				UUID: "backup-uuid",
			},
			VolumeUUID:              "volume-uuid",
			LatestLogicalBackupSize: 0,
		}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{
				UUID: "volume-uuid",
			},
			DataProtection: &datamodel.DataProtection{},
		}

		mockStorage.On("FinishBackup", ctx, backup).Return(backup, nil).Once()
		expectRecalcVolumeChainBytes(mockStorage, ctx, "volume-uuid", 0)

		err := activity.UpdateBackupSize(ctx, backup, volume, false)
		assert.NoError(t, err)
		mockStorage.AssertExpectations(t)
	})

	t.Run("UpdateBackupSizeFailsOnUpdateBackup", func(t *testing.T) {
		backup := &datamodel.Backup{
			BaseModel: datamodel.BaseModel{
				UUID: "backup-uuid",
			},
			VolumeUUID:              "volume-uuid",
			LatestLogicalBackupSize: 1024000,
		}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{
				UUID: "volume-uuid",
			},
		}

		// Mock UpdateBackup call to fail
		mockStorage.On("FinishBackup", ctx, backup).Return(nil, errors.New("database error")).Once()

		err := activity.UpdateBackupSize(ctx, backup, volume, false)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "database error")
		mockStorage.AssertExpectations(t)
	})

	t.Run("UpdateBackupSizeFailsOnRecalculateVolumeChainBytes", func(t *testing.T) {
		backup := &datamodel.Backup{
			BaseModel: datamodel.BaseModel{
				UUID: "backup-uuid",
			},
			VolumeUUID:              "volume-uuid",
			LatestLogicalBackupSize: 1024000,
		}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{
				UUID: "volume-uuid",
			},
			DataProtection: &datamodel.DataProtection{},
		}

		mockStorage.On("FinishBackup", ctx, backup).Return(backup, nil).Once()
		mockStorage.On("UpdateBackupLatestLogicalBackupSizeByVolume", ctx, "volume-uuid", "backup-uuid").Return(nil).Once()
		mockStorage.On("SumVolumeBackupChainBytes", ctx, "volume-uuid").Return(int64(0), errors.New("sum failed")).Once()

		err := activity.UpdateBackupSize(ctx, backup, volume, false)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "sum failed")
		mockStorage.AssertExpectations(t)
	})

	t.Run("UpdateBackupSizeFailsOnUpdateVolumeFields", func(t *testing.T) {
		backup := &datamodel.Backup{
			BaseModel: datamodel.BaseModel{
				UUID: "backup-uuid",
			},
			VolumeUUID:              "volume-uuid",
			LatestLogicalBackupSize: 1024000,
		}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{
				UUID: "volume-uuid",
			},
			DataProtection: &datamodel.DataProtection{},
		}

		mockStorage.On("FinishBackup", ctx, backup).Return(backup, nil).Once()
		mockStorage.On("UpdateBackupLatestLogicalBackupSizeByVolume", ctx, "volume-uuid", "backup-uuid").Return(nil).Once()
		mockStorage.On("SumVolumeBackupChainBytes", ctx, "volume-uuid").Return(int64(1024000), nil).Once()
		mockStorage.On("GetExpertModeVolumeByExternalUUID", ctx, "volume-uuid").Return(nil, customerrors.NewNotFoundErr("expert mode volume", nil)).Once()
		recalcVol := &datamodel.Volume{BaseModel: datamodel.BaseModel{UUID: "volume-uuid"}, DataProtection: &datamodel.DataProtection{}}
		mockStorage.On("GetVolume", ctx, "volume-uuid").Return(recalcVol, nil).Once()
		mockStorage.On("UpdateVolumeFields", ctx, "volume-uuid", mock.Anything).Return(errors.New("volume update error")).Once()

		err := activity.UpdateBackupSize(ctx, backup, volume, false)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "volume update error")
		mockStorage.AssertExpectations(t)
	})

	t.Run("UpdateBackupSizeFailsOnUpdateVolumeFieldsWithZeroSize", func(t *testing.T) {
		backup := &datamodel.Backup{
			BaseModel: datamodel.BaseModel{
				UUID: "backup-uuid",
			},
			VolumeUUID:              "volume-uuid",
			LatestLogicalBackupSize: 0,
		}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{
				UUID: "volume-uuid",
			},
			DataProtection: &datamodel.DataProtection{},
		}

		mockStorage.On("FinishBackup", ctx, backup).Return(backup, nil).Once()
		mockStorage.On("SumVolumeBackupChainBytes", ctx, "volume-uuid").Return(int64(0), nil).Once()
		mockStorage.On("GetExpertModeVolumeByExternalUUID", ctx, "volume-uuid").Return(nil, customerrors.NewNotFoundErr("expert mode volume", nil)).Once()
		recalcVol := &datamodel.Volume{BaseModel: datamodel.BaseModel{UUID: "volume-uuid"}, DataProtection: &datamodel.DataProtection{}}
		mockStorage.On("GetVolume", ctx, "volume-uuid").Return(recalcVol, nil).Once()
		mockStorage.On("UpdateVolumeFields", ctx, "volume-uuid", mock.Anything).Return(errors.New("volume update error")).Once()

		err := activity.UpdateBackupSize(ctx, backup, volume, false)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "volume update error")
		mockStorage.AssertExpectations(t)
	})

	t.Run("UpdateBackupSizeExpertModeUsesExpertModeVolumeFields", func(t *testing.T) {
		ctx := context.Background()
		mockStorage := database.NewMockStorage(t)
		activity := ScheduledBackupActivity{SE: mockStorage}

		externalUUID := "ext-vol-uuid"
		backup := &datamodel.Backup{
			BaseModel:               datamodel.BaseModel{UUID: "backup-uuid"},
			VolumeUUID:              externalUUID,
			LatestLogicalBackupSize: 2048,
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "db-vol-uuid"},
			VolumeAttributes: &datamodel.VolumeAttributes{
				ExternalUUID: externalUUID,
			},
			DataProtection: &datamodel.DataProtection{},
		}

		expertVol := &datamodel.ExpertModeVolumes{BaseModel: datamodel.BaseModel{UUID: "expert-internal-uuid"}, ExternalUUID: externalUUID}

		mockStorage.On("FinishBackup", ctx, backup).Return(backup, nil).Once()
		mockStorage.On("UpdateBackupLatestLogicalBackupSizeByVolume", ctx, externalUUID, "backup-uuid").Return(nil).Once()
		mockStorage.On("SumVolumeBackupChainBytes", ctx, externalUUID).Return(int64(2048), nil).Once()
		mockStorage.On("GetExpertModeVolumeByExternalUUID", ctx, externalUUID).Return(expertVol, nil).Once()
		mockStorage.On("UpdateExpertModeVolumeFields", ctx, externalUUID, mock.Anything).Return(nil).Once()

		err := activity.UpdateBackupSize(ctx, backup, volume, true)
		assert.NoError(t, err)
		mockStorage.AssertExpectations(t)
	})

	t.Run("UpdateBackupSizeExpertModeFailsOnUpdateExpertModeVolumeFields", func(t *testing.T) {
		ctx := context.Background()
		mockStorage := database.NewMockStorage(t)
		activity := ScheduledBackupActivity{SE: mockStorage}

		externalUUID := "ext-vol-uuid-err"
		backup := &datamodel.Backup{
			BaseModel:               datamodel.BaseModel{UUID: "backup-uuid"},
			VolumeUUID:              externalUUID,
			LatestLogicalBackupSize: 2048,
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "db-vol-uuid"},
			VolumeAttributes: &datamodel.VolumeAttributes{
				ExternalUUID: externalUUID,
			},
			DataProtection: &datamodel.DataProtection{},
		}

		expertVol := &datamodel.ExpertModeVolumes{BaseModel: datamodel.BaseModel{UUID: "expert-internal-uuid-err"}, ExternalUUID: externalUUID}

		mockStorage.On("FinishBackup", ctx, backup).Return(backup, nil).Once()
		mockStorage.On("UpdateBackupLatestLogicalBackupSizeByVolume", ctx, externalUUID, "backup-uuid").Return(nil).Once()
		mockStorage.On("SumVolumeBackupChainBytes", ctx, externalUUID).Return(int64(2048), nil).Once()
		mockStorage.On("GetExpertModeVolumeByExternalUUID", ctx, externalUUID).Return(expertVol, nil).Once()
		mockStorage.On("UpdateExpertModeVolumeFields", ctx, externalUUID, mock.Anything).Return(errors.New("expert mode update error")).Once()

		err := activity.UpdateBackupSize(ctx, backup, volume, true)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "expert mode update error")
		mockStorage.AssertExpectations(t)
	})

	t.Run("UpdateBackupSizeExpertModeDoesNotCallUpdateVolumeFields", func(t *testing.T) {
		ctx := context.Background()
		mockStorage := database.NewMockStorage(t)
		activity := ScheduledBackupActivity{SE: mockStorage}

		externalUUID := "ext-vol-uuid-check"
		backup := &datamodel.Backup{
			BaseModel:               datamodel.BaseModel{UUID: "backup-uuid"},
			VolumeUUID:              externalUUID,
			LatestLogicalBackupSize: 0,
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "db-vol-uuid"},
			VolumeAttributes: &datamodel.VolumeAttributes{
				ExternalUUID: externalUUID,
			},
			DataProtection: &datamodel.DataProtection{},
		}

		expertVol := &datamodel.ExpertModeVolumes{BaseModel: datamodel.BaseModel{UUID: "expert-internal-uuid-check"}, ExternalUUID: externalUUID}

		mockStorage.On("FinishBackup", ctx, backup).Return(backup, nil).Once()
		mockStorage.On("SumVolumeBackupChainBytes", ctx, externalUUID).Return(int64(0), nil).Once()
		mockStorage.On("GetExpertModeVolumeByExternalUUID", ctx, externalUUID).Return(expertVol, nil).Once()
		// UpdateExpertModeVolumeFields called; UpdateVolumeFields must NOT be called
		mockStorage.On("UpdateExpertModeVolumeFields", ctx, externalUUID, mock.Anything).Return(nil).Once()

		err := activity.UpdateBackupSize(ctx, backup, volume, true)
		assert.NoError(t, err)
		// Verify UpdateVolumeFields was never called
		mockStorage.AssertNotCalled(t, "UpdateVolumeFields", mock.Anything, mock.Anything, mock.Anything)
		mockStorage.AssertExpectations(t)
	})
}

func TestGetSnapshotByNameAndVolumeID(t *testing.T) {
	ctx := context.Background()
	mockStorage := database.NewMockStorage(t)
	activity := ScheduledBackupActivity{SE: mockStorage}

	snapshotName := "test-snapshot-name"
	accountID := int64(123)
	volumeID := int64(456)

	t.Run("GetSnapshotByNameAndVolumeIDSuccess", func(t *testing.T) {
		expectedSnapshot := &datamodel.Snapshot{
			BaseModel: datamodel.BaseModel{
				ID:   789,
				UUID: "snapshot-uuid",
			},
			Name:      snapshotName,
			AccountID: accountID,
			VolumeID:  volumeID,
			Volume: &datamodel.Volume{
				BaseModel: datamodel.BaseModel{
					ID:   volumeID,
					UUID: "volume-uuid",
				},
				AccountID: accountID,
			},
		}

		mockStorage.On("GetSnapshotByNameAndVolumeId", ctx, snapshotName, accountID, volumeID).Return(expectedSnapshot, nil).Once()

		snapshot, err := activity.GetSnapshotByNameAndVolumeID(ctx, snapshotName, accountID, volumeID)
		assert.NoError(t, err)
		assert.Equal(t, expectedSnapshot, snapshot)
		assert.Equal(t, snapshotName, snapshot.Name)
		assert.Equal(t, accountID, snapshot.AccountID)
		assert.Equal(t, volumeID, snapshot.VolumeID)

		mockStorage.AssertExpectations(t)
	})

	t.Run("GetSnapshotByNameAndVolumeIDDatabaseError", func(t *testing.T) {
		dbError := errors.New("database connection failed")
		mockStorage.On("GetSnapshotByNameAndVolumeId", ctx, snapshotName, accountID, volumeID).Return(nil, dbError).Once()

		snapshot, err := activity.GetSnapshotByNameAndVolumeID(ctx, snapshotName, accountID, volumeID)
		assert.Error(t, err)
		assert.Nil(t, snapshot)
		assert.Contains(t, err.Error(), "database connection failed")

		mockStorage.AssertExpectations(t)
	})

	t.Run("GetSnapshotByNameAndVolumeIDNotFound", func(t *testing.T) {
		notFoundError := errors.New("snapshot not found")
		mockStorage.On("GetSnapshotByNameAndVolumeId", ctx, snapshotName, accountID, volumeID).Return(nil, notFoundError).Once()

		snapshot, err := activity.GetSnapshotByNameAndVolumeID(ctx, snapshotName, accountID, volumeID)
		assert.Error(t, err)
		assert.Nil(t, snapshot)
		assert.Contains(t, err.Error(), "snapshot not found")

		mockStorage.AssertExpectations(t)
	})

	t.Run("GetSnapshotByNameAndVolumeIDEmptySnapshotName", func(t *testing.T) {
		emptySnapshotName := ""
		expectedSnapshot := &datamodel.Snapshot{
			BaseModel: datamodel.BaseModel{
				ID:   789,
				UUID: "snapshot-uuid",
			},
			Name:      emptySnapshotName,
			AccountID: accountID,
			VolumeID:  volumeID,
		}

		mockStorage.On("GetSnapshotByNameAndVolumeId", ctx, emptySnapshotName, accountID, volumeID).Return(expectedSnapshot, nil).Once()

		snapshot, err := activity.GetSnapshotByNameAndVolumeID(ctx, emptySnapshotName, accountID, volumeID)
		assert.NoError(t, err)
		assert.Equal(t, expectedSnapshot, snapshot)
		assert.Equal(t, emptySnapshotName, snapshot.Name)

		mockStorage.AssertExpectations(t)
	})

	t.Run("GetSnapshotByNameAndVolumeIDZeroIDs", func(t *testing.T) {
		zeroAccountID := int64(0)
		zeroVolumeID := int64(0)
		expectedSnapshot := &datamodel.Snapshot{
			BaseModel: datamodel.BaseModel{
				ID:   789,
				UUID: "snapshot-uuid",
			},
			Name:      snapshotName,
			AccountID: zeroAccountID,
			VolumeID:  zeroVolumeID,
		}

		mockStorage.On("GetSnapshotByNameAndVolumeId", ctx, snapshotName, zeroAccountID, zeroVolumeID).Return(expectedSnapshot, nil).Once()

		snapshot, err := activity.GetSnapshotByNameAndVolumeID(ctx, snapshotName, zeroAccountID, zeroVolumeID)
		assert.NoError(t, err)
		assert.Equal(t, expectedSnapshot, snapshot)
		assert.Equal(t, zeroAccountID, snapshot.AccountID)
		assert.Equal(t, zeroVolumeID, snapshot.VolumeID)

		mockStorage.AssertExpectations(t)
	})

	t.Run("GetSnapshotByNameAndVolumeIDNegativeIDs", func(t *testing.T) {
		negativeAccountID := int64(-1)
		negativeVolumeID := int64(-1)
		dbError := errors.New("invalid ID")
		mockStorage.On("GetSnapshotByNameAndVolumeId", ctx, snapshotName, negativeAccountID, negativeVolumeID).Return(nil, dbError).Once()

		snapshot, err := activity.GetSnapshotByNameAndVolumeID(ctx, snapshotName, negativeAccountID, negativeVolumeID)
		assert.Error(t, err)
		assert.Nil(t, snapshot)
		assert.Contains(t, err.Error(), "invalid ID")

		mockStorage.AssertExpectations(t)
	})

	t.Run("GetSnapshotByNameAndVolumeIDLargeIDs", func(t *testing.T) {
		largeAccountID := int64(9223372036854775807) // Max int64
		largeVolumeID := int64(9223372036854775807)  // Max int64
		expectedSnapshot := &datamodel.Snapshot{
			BaseModel: datamodel.BaseModel{
				ID:   789,
				UUID: "snapshot-uuid",
			},
			Name:      snapshotName,
			AccountID: largeAccountID,
			VolumeID:  largeVolumeID,
		}

		mockStorage.On("GetSnapshotByNameAndVolumeId", ctx, snapshotName, largeAccountID, largeVolumeID).Return(expectedSnapshot, nil).Once()

		snapshot, err := activity.GetSnapshotByNameAndVolumeID(ctx, snapshotName, largeAccountID, largeVolumeID)
		assert.NoError(t, err)
		assert.Equal(t, expectedSnapshot, snapshot)
		assert.Equal(t, largeAccountID, snapshot.AccountID)
		assert.Equal(t, largeVolumeID, snapshot.VolumeID)

		mockStorage.AssertExpectations(t)
	})

	t.Run("GetSnapshotByNameAndVolumeIDSpecialCharactersInName", func(t *testing.T) {
		specialSnapshotName := "test-snapshot_@#$%^&*()_+-=[]{}|;':\",./<>?"
		expectedSnapshot := &datamodel.Snapshot{
			BaseModel: datamodel.BaseModel{
				ID:   789,
				UUID: "snapshot-uuid",
			},
			Name:      specialSnapshotName,
			AccountID: accountID,
			VolumeID:  volumeID,
		}

		mockStorage.On("GetSnapshotByNameAndVolumeId", ctx, specialSnapshotName, accountID, volumeID).Return(expectedSnapshot, nil).Once()

		snapshot, err := activity.GetSnapshotByNameAndVolumeID(ctx, specialSnapshotName, accountID, volumeID)
		assert.NoError(t, err)
		assert.Equal(t, expectedSnapshot, snapshot)
		assert.Equal(t, specialSnapshotName, snapshot.Name)

		mockStorage.AssertExpectations(t)
	})

	t.Run("GetSnapshotByNameAndVolumeIDLongSnapshotName", func(t *testing.T) {
		longSnapshotName := strings.Repeat("a", 1000) // 1000 character long name
		expectedSnapshot := &datamodel.Snapshot{
			BaseModel: datamodel.BaseModel{
				ID:   789,
				UUID: "snapshot-uuid",
			},
			Name:      longSnapshotName,
			AccountID: accountID,
			VolumeID:  volumeID,
		}

		mockStorage.On("GetSnapshotByNameAndVolumeId", ctx, longSnapshotName, accountID, volumeID).Return(expectedSnapshot, nil).Once()

		snapshot, err := activity.GetSnapshotByNameAndVolumeID(ctx, longSnapshotName, accountID, volumeID)
		assert.NoError(t, err)
		assert.Equal(t, expectedSnapshot, snapshot)
		assert.Equal(t, longSnapshotName, snapshot.Name)

		mockStorage.AssertExpectations(t)
	})
}

func TestUpdateBackupState(t *testing.T) {
	ctx := context.Background()
	mockStorage := database.NewMockStorage(t)
	activity := ScheduledBackupActivity{SE: mockStorage}

	t.Run("UpdateBackupStateSuccess", func(t *testing.T) {
		backup := &datamodel.Backup{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "backup-uuid-1",
			},
			Name:  "test-backup",
			State: datamodel.LifeCycleStateREADY,
		}

		expectedUpdatedBackup := &datamodel.Backup{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "backup-uuid-1",
			},
			Name:  "test-backup",
			State: datamodel.LifeCycleStateREADY,
		}

		mockStorage.On("UpdateBackupState", ctx, backup).Return(expectedUpdatedBackup, nil).Once()

		updatedBackup, err := activity.UpdateBackupState(ctx, backup)
		assert.NoError(t, err)
		assert.NotNil(t, updatedBackup)
		assert.Equal(t, expectedUpdatedBackup, updatedBackup)
		assert.Equal(t, datamodel.LifeCycleStateREADY, updatedBackup.State)

		mockStorage.AssertExpectations(t)
	})

	t.Run("UpdateBackupStateWithNilBackup", func(t *testing.T) {
		mockStorage.On("UpdateBackupState", ctx, (*datamodel.Backup)(nil)).Return(nil, errors.New("backup cannot be nil")).Once()

		updatedBackup, err := activity.UpdateBackupState(ctx, nil)
		assert.Error(t, err)
		assert.Nil(t, updatedBackup)
		assert.Equal(t, "backup cannot be nil", err.Error())

		mockStorage.AssertExpectations(t)
	})

	t.Run("UpdateBackupStateDatabaseError", func(t *testing.T) {
		backup := &datamodel.Backup{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "backup-uuid-1",
			},
			Name:  "test-backup",
			State: datamodel.LifeCycleStateCreating,
		}

		mockStorage.On("UpdateBackupState", ctx, backup).Return(nil, errors.New("database connection failed")).Once()

		updatedBackup, err := activity.UpdateBackupState(ctx, backup)
		assert.Error(t, err)
		assert.Nil(t, updatedBackup)
		assert.Equal(t, "database connection failed", err.Error())

		mockStorage.AssertExpectations(t)
	})

	t.Run("UpdateBackupStateWithDifferentStates", func(t *testing.T) {
		testCases := []struct {
			name          string
			initialState  string
			expectedState string
		}{
			{
				name:          "CreatingToReady",
				initialState:  datamodel.LifeCycleStateCreating,
				expectedState: datamodel.LifeCycleStateREADY,
			},
			{
				name:          "ReadyToError",
				initialState:  datamodel.LifeCycleStateREADY,
				expectedState: datamodel.LifeCycleStateError,
			},
			{
				name:          "CreatingToError",
				initialState:  datamodel.LifeCycleStateCreating,
				expectedState: datamodel.LifeCycleStateError,
			},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				backup := &datamodel.Backup{
					BaseModel: datamodel.BaseModel{
						ID:   1,
						UUID: "backup-uuid-1",
					},
					Name:  "test-backup",
					State: tc.initialState,
				}

				expectedUpdatedBackup := &datamodel.Backup{
					BaseModel: datamodel.BaseModel{
						ID:   1,
						UUID: "backup-uuid-1",
					},
					Name:  "test-backup",
					State: tc.expectedState,
				}

				mockStorage.On("UpdateBackupState", ctx, backup).Return(expectedUpdatedBackup, nil).Once()

				updatedBackup, err := activity.UpdateBackupState(ctx, backup)
				assert.NoError(t, err)
				assert.NotNil(t, updatedBackup)
				assert.Equal(t, tc.expectedState, updatedBackup.State)

				mockStorage.AssertExpectations(t)
			})
		}
	})
}

func TestCreateRemoteScheduledBackupsFromVCPActivity(t *testing.T) {
	// Common test data
	projectNumber := "123456789"
	backupRegion := "us-west1"

	t.Run("Success_MultipleBackups", func(t *testing.T) {
		// Arrange
		mockStorage := database.NewMockStorage(t)
		activity := ScheduledBackupActivity{SE: mockStorage}
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "vol-uuid"},
			Account:   &datamodel.Account{Name: projectNumber},
			State:     datamodel.LifeCycleStateREADY,
		}

		backupVault := &datamodel.BackupVault{
			BaseModel:        datamodel.BaseModel{UUID: "bv-uuid"},
			BackupVaultType:  "CROSS_REGION",
			BackupRegionName: nillable.ToPointer(backupRegion),
		}

		backups := []*datamodel.Backup{
			{
				BaseModel: datamodel.BaseModel{UUID: "backup-1"},
				Name:      "daily-backup",
				Attributes: &datamodel.BackupAttributes{
					SnapshotID:   "snap-1",
					SnapshotName: "snap-daily",
				},
			},
			{
				BaseModel: datamodel.BaseModel{UUID: "backup-2"},
				Name:      "weekly-backup",
				Attributes: &datamodel.BackupAttributes{
					SnapshotID:   "snap-2",
					SnapshotName: "snap-weekly",
				},
			},
		}

		// Mock GetRemoteRegionConfig
		originalGetRemoteRegionConfig := commonparams.GetRemoteRegionConfig
		defer func() { commonparams.GetRemoteRegionConfig = originalGetRemoteRegionConfig }()
		commonparams.GetRemoteRegionConfig = func(regionParam, projectNumberParam string) (string, string, error) {
			return "https://example.com", "test-jwt-token", nil
		}

		// Mock googleproxyclient.GetGProxyClient
		mockInvoker := googleproxyclient.NewMockInvoker(t)
		mockClient := &googleproxyclient.ProxyClient{
			Invoker: mockInvoker,
		}
		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() { googleproxyclient.GetGProxyClient = originalGetGProxyClient }()
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mockClient
		}

		// Mock V1betaInternalCreateBackup for each backup
		mockResponse := &googleproxyclient.InternalBackupV1beta{
			ResourceId: googleproxyclient.NewOptString("test-backup"),
		}
		mockInvoker.On("V1betaInternalCreateBackup", mock.Anything, mock.Anything, mock.Anything).Return(mockResponse, nil).Times(2)

		// Act
		err := activity.CreateRemoteScheduledBackupsFromVCPActivity(ctx, backupVault, backups, volume, projectNumber)

		// Assert
		assert.NoError(t, err)
		mockInvoker.AssertExpectations(t)
	})

	t.Run("Success_NonCrossRegion", func(t *testing.T) {
		// Arrange
		mockStorage := database.NewMockStorage(t)
		activity := ScheduledBackupActivity{SE: mockStorage}
		ctx := context.Background()

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "vol-uuid"},
			Account:   &datamodel.Account{Name: projectNumber},
		}

		backupVault := &datamodel.BackupVault{
			BaseModel:       datamodel.BaseModel{UUID: "bv-uuid"},
			BackupVaultType: "LOCAL", // Not cross-region
		}

		backups := []*datamodel.Backup{
			{BaseModel: datamodel.BaseModel{UUID: "backup-1"}},
		}

		// Act
		err := activity.CreateRemoteScheduledBackupsFromVCPActivity(ctx, backupVault, backups, volume, projectNumber)

		// Assert
		assert.NoError(t, err)
		// No remote calls should be made for non-cross-region
	})

	t.Run("Success_NilBackupRegionName", func(t *testing.T) {
		// Arrange
		mockStorage := database.NewMockStorage(t)
		activity := ScheduledBackupActivity{SE: mockStorage}
		ctx := context.Background()

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "vol-uuid"},
			Account:   &datamodel.Account{Name: projectNumber},
		}

		backupVault := &datamodel.BackupVault{
			BaseModel:        datamodel.BaseModel{UUID: "bv-uuid"},
			BackupVaultType:  "CROSS_REGION",
			BackupRegionName: nil, // Nil region name
		}

		backups := []*datamodel.Backup{
			{BaseModel: datamodel.BaseModel{UUID: "backup-1"}},
		}

		// Act
		err := activity.CreateRemoteScheduledBackupsFromVCPActivity(ctx, backupVault, backups, volume, projectNumber)

		// Assert
		assert.NoError(t, err)
	})

	t.Run("Error_GetRemoteRegionConfigFails", func(t *testing.T) {
		// Arrange
		mockStorage := database.NewMockStorage(t)
		activity := ScheduledBackupActivity{SE: mockStorage}
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "vol-uuid"},
			Account:   &datamodel.Account{Name: projectNumber},
		}

		backupVault := &datamodel.BackupVault{
			BaseModel:        datamodel.BaseModel{UUID: "bv-uuid"},
			BackupVaultType:  "CROSS_REGION",
			BackupRegionName: nillable.ToPointer(backupRegion),
		}

		backups := []*datamodel.Backup{
			{BaseModel: datamodel.BaseModel{UUID: "backup-1"}},
		}

		// Mock GetRemoteRegionConfig to return error
		originalGetRemoteRegionConfig := common.GetRemoteRegionConfig
		defer func() { common.GetRemoteRegionConfig = originalGetRemoteRegionConfig }()
		common.GetRemoteRegionConfig = func(regionParam, projectNumberParam string) (string, string, error) {
			return "", "", errors.New("failed to get remote region config")
		}

		// Act
		err := activity.CreateRemoteScheduledBackupsFromVCPActivity(ctx, backupVault, backups, volume, projectNumber)

		// Assert
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get remote region config")
	})

	t.Run("Error_RemoteBackupCreationFails", func(t *testing.T) {
		// Arrange
		mockStorage := database.NewMockStorage(t)
		activity := ScheduledBackupActivity{SE: mockStorage}
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "vol-uuid"},
			Account:   &datamodel.Account{Name: projectNumber},
		}

		backupVault := &datamodel.BackupVault{
			BaseModel:        datamodel.BaseModel{UUID: "bv-uuid"},
			BackupVaultType:  "CROSS_REGION",
			BackupRegionName: nillable.ToPointer(backupRegion),
		}

		backups := []*datamodel.Backup{
			{
				BaseModel: datamodel.BaseModel{UUID: "backup-1"},
				Name:      "daily-backup",
				Attributes: &datamodel.BackupAttributes{
					SnapshotID: "snap-1",
				},
			},
		}

		// Mock GetRemoteRegionConfig
		originalGetRemoteRegionConfig := common.GetRemoteRegionConfig
		defer func() { common.GetRemoteRegionConfig = originalGetRemoteRegionConfig }()
		common.GetRemoteRegionConfig = func(regionParam, projectNumberParam string) (string, string, error) {
			return "https://example.com", "test-jwt-token", nil
		}

		// Mock googleproxyclient.GetGProxyClient
		mockInvoker := googleproxyclient.NewMockInvoker(t)
		mockClient := &googleproxyclient.ProxyClient{
			Invoker: mockInvoker,
		}
		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() { googleproxyclient.GetGProxyClient = originalGetGProxyClient }()
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mockClient
		}

		// Mock V1betaInternalCreateBackup to fail
		mockInvoker.On("V1betaInternalCreateBackup", mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("remote backup creation failed"))

		// Act
		err := activity.CreateRemoteScheduledBackupsFromVCPActivity(ctx, backupVault, backups, volume, projectNumber)

		// Assert
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "remote backup creation failed")
		mockInvoker.AssertExpectations(t)
	})

	t.Run("Success_EmptyBackupList", func(t *testing.T) {
		// Arrange
		mockStorage := database.NewMockStorage(t)
		activity := ScheduledBackupActivity{SE: mockStorage}
		ctx := context.Background()

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "vol-uuid"},
			Account:   &datamodel.Account{Name: projectNumber},
		}

		backupVault := &datamodel.BackupVault{
			BaseModel:        datamodel.BaseModel{UUID: "bv-uuid"},
			BackupVaultType:  "CROSS_REGION",
			BackupRegionName: nillable.ToPointer(backupRegion),
		}

		backups := []*datamodel.Backup{}

		// Act
		err := activity.CreateRemoteScheduledBackupsFromVCPActivity(ctx, backupVault, backups, volume, projectNumber)

		// Assert
		assert.NoError(t, err)
	})
}

func TestDeleteRemoteScheduledBackupFromVCPActivity(t *testing.T) {
	// Common test data
	backupUUID := "test-backup-uuid"
	backupVaultUUID := "test-backup-vault-uuid"
	projectNumber := "123456789"
	region := "us-central1"

	t.Run("Success", func(t *testing.T) {
		// Arrange
		mockStorage := database.NewMockStorage(t)
		activity := ScheduledBackupActivity{SE: mockStorage}
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})

		// Mock GetRemoteRegionConfig
		originalGetRemoteRegionConfig := common.GetRemoteRegionConfig
		defer func() { common.GetRemoteRegionConfig = originalGetRemoteRegionConfig }()
		common.GetRemoteRegionConfig = func(regionParam, projectNumberParam string) (string, string, error) {
			return "https://example.com", "test-jwt-token", nil
		}

		// Mock googleproxyclient.GetGProxyClient
		mockInvoker := googleproxyclient.NewMockInvoker(t)
		mockClient := &googleproxyclient.ProxyClient{
			Invoker: mockInvoker,
		}
		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() { googleproxyclient.GetGProxyClient = originalGetGProxyClient }()
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mockClient
		}

		// Mock V1betaInternalDeleteBackupUnderBackupVault
		mockResponse := &googleproxyclient.OperationV1beta{
			Name: googleproxyclient.NewOptString("operations/test-operation"),
			Done: googleproxyclient.NewOptBool(true),
		}
		mockInvoker.On("V1betaInternalDeleteBackupUnderBackupVault", mock.Anything, mock.Anything).Return(mockResponse, nil)

		// Act
		err := activity.DeleteRemoteScheduledBackupFromVCPActivity(ctx, backupUUID, backupVaultUUID, projectNumber, region)

		// Assert
		assert.NoError(t, err)
		mockInvoker.AssertExpectations(t)
	})

	t.Run("Error_GetRemoteRegionConfigFails", func(t *testing.T) {
		// Arrange
		mockStorage := database.NewMockStorage(t)
		activity := ScheduledBackupActivity{SE: mockStorage}
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})

		// Mock GetRemoteRegionConfig to return error
		originalGetRemoteRegionConfig := common.GetRemoteRegionConfig
		defer func() { common.GetRemoteRegionConfig = originalGetRemoteRegionConfig }()
		common.GetRemoteRegionConfig = func(regionParam, projectNumberParam string) (string, string, error) {
			return "", "", errors.New("VCP_PAIRED_REGIONS environment variable not set")
		}

		// Act
		err := activity.DeleteRemoteScheduledBackupFromVCPActivity(ctx, backupUUID, backupVaultUUID, projectNumber, region)

		// Assert
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "VCP_PAIRED_REGIONS environment variable not set")
	})

	t.Run("Error_DeleteBackupFails", func(t *testing.T) {
		// Arrange
		mockStorage := database.NewMockStorage(t)
		activity := ScheduledBackupActivity{SE: mockStorage}
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})

		// Mock GetRemoteRegionConfig
		originalGetRemoteRegionConfig := common.GetRemoteRegionConfig
		defer func() { common.GetRemoteRegionConfig = originalGetRemoteRegionConfig }()
		common.GetRemoteRegionConfig = func(regionParam, projectNumberParam string) (string, string, error) {
			return "https://example.com", "test-jwt-token", nil
		}

		// Mock googleproxyclient.GetGProxyClient
		mockInvoker := googleproxyclient.NewMockInvoker(t)
		mockClient := &googleproxyclient.ProxyClient{
			Invoker: mockInvoker,
		}
		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() { googleproxyclient.GetGProxyClient = originalGetGProxyClient }()
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mockClient
		}

		// Mock V1betaInternalDeleteBackupUnderBackupVault to return error
		mockInvoker.On("V1betaInternalDeleteBackupUnderBackupVault", mock.Anything, mock.Anything).Return(nil, errors.New("delete failed"))

		// Act
		err := activity.DeleteRemoteScheduledBackupFromVCPActivity(ctx, backupUUID, backupVaultUUID, projectNumber, region)

		// Assert
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "delete failed")
		mockInvoker.AssertExpectations(t)
	})

	t.Run("Error_RegionNotFound", func(t *testing.T) {
		// Arrange
		mockStorage := database.NewMockStorage(t)
		activity := ScheduledBackupActivity{SE: mockStorage}
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})

		// Mock GetRemoteRegionConfig to return region not found error
		originalGetRemoteRegionConfig := common.GetRemoteRegionConfig
		defer func() { common.GetRemoteRegionConfig = originalGetRemoteRegionConfig }()
		common.GetRemoteRegionConfig = func(regionParam, projectNumberParam string) (string, string, error) {
			return "", "", errors.New("no base path configured for region: unknown-region in VCP_PAIRED_REGIONS")
		}

		// Act
		err := activity.DeleteRemoteScheduledBackupFromVCPActivity(ctx, backupUUID, backupVaultUUID, projectNumber, "unknown-region")

		// Assert
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "no base path configured for region")
	})
}

func TestCheckBackupInCreatingStateByVolume(t *testing.T) {
	ctx := context.Background()
	volumeUUID := "test-volume-uuid"

	t.Run("Success_NoBackupsInProgress", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		activity := ScheduledBackupActivity{SE: mockStorage}
		excludeBackupUUIDs := []string{"backup-1", "backup-2"}

		mockStorage.On("AreBackupsInProgressForVolume", ctx, volumeUUID, excludeBackupUUIDs, mock.Anything).Return(false, nil).Once()

		err := activity.CheckBackupsInProgressByVolume(ctx, volumeUUID, excludeBackupUUIDs, nil)
		assert.NoError(t, err)
		mockStorage.AssertExpectations(t)
	})

	t.Run("Success_NoBackupsInProgressWithEmptyExcludeList", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		activity := ScheduledBackupActivity{SE: mockStorage}
		excludeBackupUUIDs := []string{}

		mockStorage.On("AreBackupsInProgressForVolume", ctx, volumeUUID, excludeBackupUUIDs, mock.Anything).Return(false, nil).Once()

		err := activity.CheckBackupsInProgressByVolume(ctx, volumeUUID, excludeBackupUUIDs, nil)
		assert.NoError(t, err)
		mockStorage.AssertExpectations(t)
	})

	t.Run("Success_NoBackupsInProgressWithNilExcludeList", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		activity := ScheduledBackupActivity{SE: mockStorage}
		var excludeBackupUUIDs []string

		mockStorage.On("AreBackupsInProgressForVolume", ctx, volumeUUID, excludeBackupUUIDs, mock.Anything).Return(false, nil).Once()

		err := activity.CheckBackupsInProgressByVolume(ctx, volumeUUID, excludeBackupUUIDs, nil)
		assert.NoError(t, err)
		mockStorage.AssertExpectations(t)
	})

	t.Run("Error_BackupInProgress", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		activity := ScheduledBackupActivity{SE: mockStorage}
		excludeBackupUUIDs := []string{"backup-1"}

		mockStorage.On("AreBackupsInProgressForVolume", ctx, volumeUUID, excludeBackupUUIDs, mock.Anything).Return(true, nil).Once()

		err := activity.CheckBackupsInProgressByVolume(ctx, volumeUUID, excludeBackupUUIDs, nil)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "another backup operation is already in progress")
		assert.Contains(t, err.Error(), volumeUUID)
		mockStorage.AssertExpectations(t)
	})

	t.Run("Error_DatabaseError", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		activity := ScheduledBackupActivity{SE: mockStorage}
		excludeBackupUUIDs := []string{"backup-1"}
		dbError := errors.New("database connection failed")

		mockStorage.On("AreBackupsInProgressForVolume", ctx, volumeUUID, excludeBackupUUIDs, mock.Anything).Return(false, dbError).Once()

		err := activity.CheckBackupsInProgressByVolume(ctx, volumeUUID, excludeBackupUUIDs, nil)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "database connection failed")
		mockStorage.AssertExpectations(t)
	})

	t.Run("Error_DatabaseErrorWhenBackupInProgress", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		activity := ScheduledBackupActivity{SE: mockStorage}
		excludeBackupUUIDs := []string{"backup-1"}
		dbError := errors.New("database query timeout")

		mockStorage.On("AreBackupsInProgressForVolume", ctx, volumeUUID, excludeBackupUUIDs, mock.Anything).Return(true, dbError).Once()

		err := activity.CheckBackupsInProgressByVolume(ctx, volumeUUID, excludeBackupUUIDs, nil)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "database query timeout")
		mockStorage.AssertExpectations(t)
	})

	t.Run("Error_BackupInProgressWithMultipleExcludedBackups", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		activity := ScheduledBackupActivity{SE: mockStorage}
		excludeBackupUUIDs := []string{"backup-1", "backup-2", "backup-3"}

		mockStorage.On("AreBackupsInProgressForVolume", ctx, volumeUUID, excludeBackupUUIDs, mock.Anything).Return(true, nil).Once()

		err := activity.CheckBackupsInProgressByVolume(ctx, volumeUUID, excludeBackupUUIDs, nil)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "another backup operation is already in progress")
		mockStorage.AssertExpectations(t)
	})

	t.Run("Success_DifferentVolumeUUIDs", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		activity := ScheduledBackupActivity{SE: mockStorage}
		volumeUUID1 := "volume-uuid-1"
		volumeUUID2 := "volume-uuid-2"
		excludeBackupUUIDs := []string{"backup-1"}

		mockStorage.On("AreBackupsInProgressForVolume", ctx, volumeUUID1, excludeBackupUUIDs, mock.Anything).Return(false, nil).Once()
		mockStorage.On("AreBackupsInProgressForVolume", ctx, volumeUUID2, excludeBackupUUIDs, mock.Anything).Return(false, nil).Once()

		err1 := activity.CheckBackupsInProgressByVolume(ctx, volumeUUID1, excludeBackupUUIDs, nil)
		assert.NoError(t, err1)

		err2 := activity.CheckBackupsInProgressByVolume(ctx, volumeUUID2, excludeBackupUUIDs, nil)
		assert.NoError(t, err2)

		mockStorage.AssertExpectations(t)
	})

	t.Run("Error_EmptyVolumeUUID", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		activity := ScheduledBackupActivity{SE: mockStorage}
		emptyVolumeUUID := ""
		excludeBackupUUIDs := []string{"backup-1"}

		mockStorage.On("AreBackupsInProgressForVolume", ctx, emptyVolumeUUID, excludeBackupUUIDs, mock.Anything).Return(false, nil).Once()

		err := activity.CheckBackupsInProgressByVolume(ctx, emptyVolumeUUID, excludeBackupUUIDs, nil)
		assert.NoError(t, err)
		mockStorage.AssertExpectations(t)
	})

	t.Run("Success_PassesCreatedBeforeToStorage", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		activity := ScheduledBackupActivity{SE: mockStorage}
		excludeBackupUUIDs := []string{"backup-1"}
		cb := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
		mockStorage.On("AreBackupsInProgressForVolume", ctx, volumeUUID, excludeBackupUUIDs, &cb).Return(false, nil).Once()
		err := activity.CheckBackupsInProgressByVolume(ctx, volumeUUID, excludeBackupUUIDs, &cb)
		assert.NoError(t, err)
		mockStorage.AssertExpectations(t)
	})
}

func TestGetBackupPolicyByUUID(t *testing.T) {
	ctx := context.Background()
	backupPolicyUUID := "policy-uuid-123"
	accountID := int64(42)

	t.Run("GetBackupPolicyByUUIDSuccess", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		activity := ScheduledBackupActivity{SE: mockStorage}

		expectedBackupPolicy := &datamodel.BackupPolicy{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: backupPolicyUUID,
			},
			Name:      "test-backup-policy",
			AccountID: accountID,
		}

		mockStorage.On("GetBackupPolicyByUUIDAndOwnerID", ctx, backupPolicyUUID, accountID).Return(expectedBackupPolicy, nil).Once()

		backupPolicy, err := activity.GetBackupPolicyByUUID(ctx, backupPolicyUUID, accountID)
		assert.NoError(t, err)
		assert.NotNil(t, backupPolicy)
		assert.Equal(t, expectedBackupPolicy, backupPolicy)
		assert.Equal(t, backupPolicyUUID, backupPolicy.UUID)
		assert.Equal(t, accountID, backupPolicy.AccountID)

		mockStorage.AssertExpectations(t)
	})

	t.Run("GetBackupPolicyByUUIDNotFound", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		activity := ScheduledBackupActivity{SE: mockStorage}

		notFoundErr := customerrors.NewNotFoundErr("BackupPolicy", &backupPolicyUUID)
		mockStorage.On("GetBackupPolicyByUUIDAndOwnerID", ctx, backupPolicyUUID, accountID).Return(nil, notFoundErr).Once()

		backupPolicy, err := activity.GetBackupPolicyByUUID(ctx, backupPolicyUUID, accountID)
		assert.Error(t, err)
		assert.Nil(t, backupPolicy)

		// Verify it's wrapped as TemporalApplicationError
		var appErr *temporal.ApplicationError
		assert.True(t, vsaerrors.As(err, &appErr))
		assert.Equal(t, "CustomError", appErr.Type())

		mockStorage.AssertExpectations(t)
	})

	t.Run("GetBackupPolicyByUUIDDatabaseError", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		activity := ScheduledBackupActivity{SE: mockStorage}

		dbError := errors.New("database connection failed")
		mockStorage.On("GetBackupPolicyByUUIDAndOwnerID", ctx, backupPolicyUUID, accountID).Return(nil, dbError).Once()

		backupPolicy, err := activity.GetBackupPolicyByUUID(ctx, backupPolicyUUID, accountID)
		assert.Error(t, err)
		assert.Nil(t, backupPolicy)
		// WrapAsTemporalApplicationError wraps plain errors with WrapAsTemporalApplicationError,
		// but since it's not a CustomError, it returns the original error unchanged
		assert.Equal(t, "database connection failed", err.Error())

		mockStorage.AssertExpectations(t)
	})

	t.Run("GetBackupPolicyByUUIDWithDifferentAccountID", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		activity := ScheduledBackupActivity{SE: mockStorage}

		differentAccountID := int64(999)
		expectedBackupPolicy := &datamodel.BackupPolicy{
			BaseModel: datamodel.BaseModel{
				ID:   2,
				UUID: backupPolicyUUID,
			},
			Name:      "test-backup-policy-2",
			AccountID: differentAccountID,
		}

		mockStorage.On("GetBackupPolicyByUUIDAndOwnerID", ctx, backupPolicyUUID, differentAccountID).Return(expectedBackupPolicy, nil).Once()

		backupPolicy, err := activity.GetBackupPolicyByUUID(ctx, backupPolicyUUID, differentAccountID)
		assert.NoError(t, err)
		assert.NotNil(t, backupPolicy)
		assert.Equal(t, expectedBackupPolicy, backupPolicy)
		assert.Equal(t, differentAccountID, backupPolicy.AccountID)

		mockStorage.AssertExpectations(t)
	})

	t.Run("GetBackupPolicyByUUIDWithEmptyUUID", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		activity := ScheduledBackupActivity{SE: mockStorage}

		emptyUUID := ""
		notFoundErr := customerrors.NewNotFoundErr("BackupPolicy", &emptyUUID)
		mockStorage.On("GetBackupPolicyByUUIDAndOwnerID", ctx, emptyUUID, accountID).Return(nil, notFoundErr).Once()

		backupPolicy, err := activity.GetBackupPolicyByUUID(ctx, emptyUUID, accountID)
		assert.Error(t, err)
		assert.Nil(t, backupPolicy)

		mockStorage.AssertExpectations(t)
	})

	t.Run("GetBackupPolicyByUUIDWithZeroAccountID", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		activity := ScheduledBackupActivity{SE: mockStorage}

		zeroAccountID := int64(0)
		notFoundErr := customerrors.NewNotFoundErr("BackupPolicy", &backupPolicyUUID)
		mockStorage.On("GetBackupPolicyByUUIDAndOwnerID", ctx, backupPolicyUUID, zeroAccountID).Return(nil, notFoundErr).Once()

		backupPolicy, err := activity.GetBackupPolicyByUUID(ctx, backupPolicyUUID, zeroAccountID)
		assert.Error(t, err)
		assert.Nil(t, backupPolicy)

		mockStorage.AssertExpectations(t)
	})

	t.Run("GetBackupPolicyByUUIDWithNegativeAccountID", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		activity := ScheduledBackupActivity{SE: mockStorage}

		negativeAccountID := int64(-1)
		dbError := errors.New("invalid account ID")
		mockStorage.On("GetBackupPolicyByUUIDAndOwnerID", ctx, backupPolicyUUID, negativeAccountID).Return(nil, dbError).Once()

		backupPolicy, err := activity.GetBackupPolicyByUUID(ctx, backupPolicyUUID, negativeAccountID)
		assert.Error(t, err)
		assert.Nil(t, backupPolicy)
		assert.Contains(t, err.Error(), "invalid account ID")

		mockStorage.AssertExpectations(t)
	})
}

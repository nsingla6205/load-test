package activities

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/metadata"
)

func TestCreateCrossRegionBackupTransferBillingLedger(t *testing.T) {
	ctx := context.Background()
	mockSE := database.NewMockStorage(t)

	backup := &datamodel.Backup{
		VolumeUUID:    "vol-1",
		BackupVaultID: 10,
		Attributes: &datamodel.BackupAttributes{
			AccountIdentifier:  "project-1",
			VolumeName:         "my-volume",
			TotalTransferBytes: 5000,
		},
	}
	backupVault := &datamodel.BackupVault{
		BaseModel:       datamodel.BaseModel{ID: 10},
		Name:            "cr-vault",
		BackupVaultType: CrossRegionBackupType,
	}

	mockSE.On("GetBackupVaultById", ctx, int64(10)).Return(backupVault, nil)
	mockSE.On("CreateBillingLedger", ctx, mock.MatchedBy(func(l *datamodel.BillingLedger) bool {
		return l.ResourceUUID == "vol-1" &&
			l.ResourceType == metadata.Backup &&
			l.MeasuredType == metadata.CbsCrossRegionVolumeBackupTransferBytes &&
			l.Quantity == 5000
	})).Return(&datamodel.BillingLedger{}, nil)

	activity := &BillingActivity{SE: mockSE}
	assert.NoError(t, activity.CreateCrossRegionBackupTransferBillingLedger(ctx, backup))
}

func TestCreateCrossRegionBackupTransferBillingLedger_NilBackup_NoOp(t *testing.T) {
	ctx := context.Background()
	mockSE := database.NewMockStorage(t)

	activity := &BillingActivity{SE: mockSE}
	assert.NoError(t, activity.CreateCrossRegionBackupTransferBillingLedger(ctx, nil))
}

func TestCreateCrossRegionBackupTransferBillingLedger_InRegion_LeavesLedgerUnchanged(t *testing.T) {
	ctx := context.Background()
	mockSE := database.NewMockStorage(t)

	backup := &datamodel.Backup{
		VolumeUUID:    "vol-1",
		BackupVaultID: 10,
		Attributes:    &datamodel.BackupAttributes{AccountIdentifier: "project-1"},
	}
	backupVault := &datamodel.BackupVault{
		BaseModel:       datamodel.BaseModel{ID: 10},
		Name:            "vault",
		BackupVaultType: "STANDARD",
	}

	mockSE.On("GetBackupVaultById", ctx, int64(10)).Return(backupVault, nil)

	activity := &BillingActivity{SE: mockSE}
	assert.NoError(t, activity.CreateCrossRegionBackupTransferBillingLedger(ctx, backup))
}

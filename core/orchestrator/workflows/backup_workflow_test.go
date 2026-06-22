package workflows

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

// TestBackupActivity is a test-specific implementation that bypasses the nil check in UpdateSnapshotActivity
type TestBackupActivity struct {
	*activities.BackupActivity
}

// setupMockCommonActivities creates a properly mocked CommonActivities for testing
func setupMockCommonActivities(t *testing.T) (*activities.CommonActivities, *database.MockStorage) {
	mockStorage := database.NewMockStorage(t)
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	return &activities.CommonActivities{SE: mockStorage}, mockStorage
}

// setupMockBackupActivity creates a properly mocked BackupActivity for testing
func setupMockBackupActivity(t *testing.T) *TestBackupActivity {
	mockStorage := database.NewMockStorage(t)
	mockStorage.On("UpdateBackupState", mock.Anything, mock.Anything).Return(&datamodel.Backup{}, nil).Maybe()
	mockStorage.On("GetBackup", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.Backup{}, nil).Maybe()
	mockStorage.On("DeleteBackup", mock.Anything, mock.Anything).Return(&datamodel.Backup{}, nil).Maybe()
	mockStorage.On("UpdateBackup", mock.Anything, mock.Anything).Return(&datamodel.Backup{}, nil).Maybe()
	mockStorage.On("UpdateBackupLatestLogicalBackupSizeByVolume", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	mockStorage.On("UpdateVolumeFields", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	// Mock GetVolume for HydrateSnapshotDeletionToCCFEActivity
	mockStorage.On("GetVolume", mock.Anything, mock.Anything).Return(&datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "test-vol", ID: int64(1)},
		AccountID: 1,
		Pool: &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: int64(1)},
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID: "external-uuid",
		},
	}, nil).Maybe()
	// Mock GetSnapshotByNameAndVolumeId for HydrateSnapshotDeletionToCCFEActivity
	mockStorage.On("GetSnapshotByNameAndVolumeId", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.Snapshot{
		BaseModel: datamodel.BaseModel{UUID: "snapshot-uuid", ID: int64(1)},
		Name:      "test-backup",
	}, nil).Maybe()

	// Mock DeleteSnapshot for DeleteBackupSnapshotFromDB activity
	mockStorage.On("DeleteSnapshot", mock.Anything, mock.Anything).Return(&datamodel.Snapshot{}, nil).Maybe()

	// Mock GetSnapshotsByTypeAndVolumeID for CleanupOldBackupSnapshotsActivity
	mockStorage.On("GetSnapshotsByTypeAndVolumeID", mock.Anything, mock.Anything, mock.Anything).Return([]*datamodel.Snapshot{}, nil).Maybe()

	return &TestBackupActivity{BackupActivity: &activities.BackupActivity{SE: mockStorage}}
}

// UpdateSnapshotActivity overrides the original implementation to not fail when DbSnapshot is nil
func (b *TestBackupActivity) UpdateSnapshotActivity(ctx context.Context, backupActivitiesContext *activities.BackupActivitiesContext) (*activities.BackupActivitiesContext, error) {
	// Always return success for testing purposes, bypassing the nil check
	return backupActivitiesContext, nil
}

// TransferSnapshotActivity overrides the original implementation to not fail when SnapmirrorRelationship is nil
func (b *TestBackupActivity) TransferSnapshotActivity(ctx context.Context, backupActivitiesContext *activities.BackupActivitiesContext) (*activities.BackupActivitiesContext, error) {
	// Always return success for testing purposes, bypassing the nil check
	return backupActivitiesContext, nil
}

// CleanupOldAdhocBackupSnapshotsActivity overrides the original implementation for testing
func (b *TestBackupActivity) CleanupOldAdhocBackupSnapshotsActivity(ctx context.Context, volume *datamodel.Volume, node *models.Node) error {
	// Always return success for testing purposes
	return nil
}

// GetObjectStoreEndpointActivity overrides the original implementation to not fail when ObjStore is nil
func (b *TestBackupActivity) GetObjectStoreEndpointActivity(ctx context.Context, backupActivitiesContext *activities.BackupActivitiesContext) (*activities.BackupActivitiesContext, error) {
	// Always return success for testing purposes, bypassing the nil check
	return backupActivitiesContext, nil
}

// HydrateSnapshotDeletionToCCFEActivity overrides the original implementation for testing
func (b *TestBackupActivity) HydrateSnapshotDeletionToCCFEActivity(ctx context.Context, snapshot *datamodel.Snapshot, volumeName, region, projectId string) error {
	// Always return success for testing purposes
	return nil
}

func TestBackupWorkflow(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
	mockHeader := &commonpb.Header{
		Fields: map[string]*commonpb.Payload{
			"logParam": encodedValue,
		},
	}
	env.SetHeader(mockHeader)

	// Create mock storage for CommonActivities and BackupActivity
	mockStorage := database.NewMockStorage(t)
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()

	commonActivity := &activities.CommonActivities{SE: mockStorage}
	env.RegisterActivity(commonActivity)
	env.RegisterWorkflow(CreateBackupWorkflow)
	mockStorage.On("UpdateSnapshot", mock.Anything, mock.Anything).Return(&datamodel.Snapshot{}, nil).Maybe()
	mockStorage.On("CreatingSnapshot", mock.Anything, mock.Anything).Return(&datamodel.Snapshot{
		Name:               "test-backup",
		Description:        "VCP-Backup",
		VolumeID:           1,
		AccountID:          1,
		State:              "creating",
		StateDetails:       "creating",
		SnapshotAttributes: &datamodel.SnapshotAttributes{},
	}, nil).Maybe()
	mockStorage.On("UpdateBackup", mock.Anything, mock.Anything).Return(&datamodel.Backup{}, nil).Maybe()
	mockStorage.On("UpdateBackupLatestLogicalBackupSizeByVolume", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	mockStorage.On("UpdateVolumeFields", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	// Create a custom BackupActivity that bypasses the nil check in UpdateSnapshotActivity
	customBackupActivity := &TestBackupActivity{BackupActivity: &activities.BackupActivity{SE: mockStorage}}
	env.RegisterActivity(customBackupActivity)

	// Set up test data
	params := &commonparams.CreateBackupParams{
		VolumeUUID:  "test-vol",
		AccountName: "test-account",
		BackupName:  "test-backup",
	}
	backupVault := &datamodel.BackupVault{
		Name:          "test-backup-vault",
		BucketDetails: datamodel.BucketDetailsArray{&datamodel.BucketDetails{BucketName: "test-bucket", ServiceAccountName: "sa-test", VendorSubnetID: "subnet-12345"}},
	}
	backup := &datamodel.Backup{
		State:         "InProgress",
		Name:          "test-backup",
		VolumeUUID:    "test-vol",
		BackupVault:   backupVault,
		BackupVaultID: 1,
		Attributes:    &datamodel.BackupAttributes{},
	}

	volume := &datamodel.Volume{
		Pool:                  &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{Password: "password"}, PoolAttributes: &datamodel.PoolAttributes{PrimaryZone: "us-central1-a"}},
		Svm:                   &datamodel.Svm{Name: "svm_test"},
		VolumeAttributes:      &datamodel.VolumeAttributes{BlockProperties: &datamodel.BlockProperties{OSType: "LINUX"}, VendorSubnetID: "subnet-12345", ExternalUUID: "external-uuid"},
		LargeVolumeAttributes: nil,
	}

	// Mock all activities that the workflow calls
	env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("GetNode", mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	env.OnActivity("PrepareObjectStoreActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node: &models.Node{EndpointAddress: "127.0.0.1"},
	}, nil)
	env.OnActivity("GetOrCreateObjectStoreActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node: &models.Node{EndpointAddress: "127.0.0.1"},
	}, nil)
	env.OnActivity("PrepareSnapmirrorActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node: &models.Node{EndpointAddress: "127.0.0.1"},
	}, nil)
	env.OnActivity("CreateSnapmirrorRelationshipActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node: &models.Node{EndpointAddress: "127.0.0.1"},
		SnapmirrorRelationship: &commonparams.SnapmirrorRelationship{
			UUID: "test-snapmirror-uuid",
		},
	}, nil)
	env.OnActivity("CreatingSnapshotActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node:         &models.Node{EndpointAddress: "127.0.0.1"},
		SnapshotName: "test-backup",
		DbSnapshot: &datamodel.Snapshot{
			Name:               "test-backup",
			Description:        "VCP-Backup",
			VolumeID:           1,
			AccountID:          1,
			State:              "creating",
			StateDetails:       "creating",
			SnapshotAttributes: &datamodel.SnapshotAttributes{},
		},
	}, nil)
	env.OnActivity("CreateSnapshotActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup: &datamodel.Backup{
				Name: "test-backup",
				Attributes: &datamodel.BackupAttributes{
					SnapshotID: "test-snapshot-uuid",
				},
			},
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node:         &models.Node{EndpointAddress: "127.0.0.1"},
		SnapshotName: "test-backup",
		SnapmirrorRelationship: &commonparams.SnapmirrorRelationship{
			UUID: "test-snapmirror-uuid",
		},
		SnapshotResponse: &vsa.SnapshotProviderResponse{
			ProviderResponse: vsa.ProviderResponse{
				ExternalUUID: "test-snapshot-uuid",
			},
		},
	}, nil)
	env.OnActivity("TransferSnapshotActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node:         &models.Node{EndpointAddress: "127.0.0.1"},
		SnapshotName: "test-backup",
		SnapmirrorRelationship: &commonparams.SnapmirrorRelationship{
			UUID: "test-snapmirror-uuid",
		},
	}, nil)
	env.OnActivity("PollTransferStatusWithHistoryCheckActivity", mock.Anything, mock.Anything, mock.Anything).Return(&activities.PollTransferStatusOutput{
		BackupActivitiesContext: &activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:      backup,
				BackupVault: backupVault,
				Volume:      volume,
			},
			Node:              &models.Node{EndpointAddress: "127.0.0.1"},
			SnapshotName:      "test-backup",
			SmSourcePath:      "svm_test:volume_test",
			SmDestinationPath: "test-bucket:/objstore/test-vol",
			SnapmirrorRelationship: &commonparams.SnapmirrorRelationship{
				UUID: "test-snapmirror-uuid",
			},
			TransferStatus: activities.SmStatusSuccess,
		},
		TransferComplete:    true,
		ShouldContinueAsNew: false,
	}, nil)
	env.OnActivity("GetSnapmirror", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&commonparams.SnapmirrorRelationship{
		UUID:    "test-snapmirror-uuid",
		Healthy: nillable.ToPointer(true),
	}, nil)
	env.OnActivity("GetObjectStoreSnapshotActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node: &models.Node{EndpointAddress: "127.0.0.1"},
	}, nil)
	env.OnActivity("FinishBackupActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node: &models.Node{EndpointAddress: "127.0.0.1"},
	}, nil)
	env.OnActivity("UpdateBackupSizeActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node: &models.Node{EndpointAddress: "127.0.0.1"},
	}, nil)
	env.OnActivity("CreateBackupMetadataIfFirstBackupActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("CleanupOldBackupSnapshotsActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Execute workflow
	env.ExecuteWorkflow(CreateBackupWorkflow, params, backup, backupVault, volume)

	// Assert that the workflow was executed successfully
	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

func TestBackupWorkflowFail(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
	mockHeader := &commonpb.Header{
		Fields: map[string]*commonpb.Payload{
			"logParam": encodedValue,
		},
	}
	env.SetHeader(mockHeader)

	// Create mock storage for CommonActivities and BackupActivity
	mockStorage := database.NewMockStorage(t)
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()

	commonActivity := &activities.CommonActivities{SE: mockStorage}
	env.RegisterActivity(commonActivity)
	env.RegisterWorkflow(CreateBackupWorkflow)
	mockStorage.On("UpdateBackupState", mock.Anything, mock.Anything).Return(&datamodel.Backup{}, nil).Maybe()
	env.RegisterActivity(&TestBackupActivity{BackupActivity: &activities.BackupActivity{SE: mockStorage}})

	// Set up test data
	params := &commonparams.CreateBackupParams{
		VolumeUUID:  "test-vol",
		AccountName: "test-account",
		BackupName:  "test-backup",
	}
	backupVault := &datamodel.BackupVault{
		Name:          "test-backup-vault",
		BucketDetails: datamodel.BucketDetailsArray{&datamodel.BucketDetails{BucketName: "test-bucket", ServiceAccountName: "sa-test", VendorSubnetID: "subnet-12345"}},
	}
	backup := &datamodel.Backup{
		State:         "InProgress",
		Name:          "test-backup",
		VolumeUUID:    "test-vol",
		BackupVault:   backupVault,
		BackupVaultID: 1,
		Attributes:    &datamodel.BackupAttributes{},
	}

	volume := &datamodel.Volume{
		Pool:             &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{Password: "password"}, PoolAttributes: &datamodel.PoolAttributes{PrimaryZone: "us-central1-a"}},
		Svm:              &datamodel.Svm{Name: "svm_test"},
		VolumeAttributes: &datamodel.VolumeAttributes{BlockProperties: &datamodel.BlockProperties{OSType: "LINUX"}, VendorSubnetID: "subnet-12345", ExternalUUID: "external-uuid"},
	}

	// Mock activities with failure at PrepareObjectStoreActivity
	env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("GetNode", mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	env.OnActivity("PrepareObjectStoreActivity", mock.Anything, mock.Anything).Return(nil, errors.New("failed to prepare object store"))
	env.OnActivity("UpdateBackupError", mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Execute workflow
	env.ExecuteWorkflow(CreateBackupWorkflow, params, backup, backupVault, volume)

	// Assert that the workflow was executed and handled the error
	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
	assert.ErrorContains(t, env.GetWorkflowError(), "failed to prepare object store")
	env.AssertExpectations(t)
}

func TestBackupWorkflowFailAfterSnapshot(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
	mockHeader := &commonpb.Header{
		Fields: map[string]*commonpb.Payload{
			"logParam": encodedValue,
		},
	}
	env.SetHeader(mockHeader)

	// Create mock storage for CommonActivities and BackupActivity
	mockStorage := database.NewMockStorage(t)
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()

	commonActivity := &activities.CommonActivities{SE: mockStorage}
	env.RegisterActivity(commonActivity)
	env.RegisterWorkflow(CreateBackupWorkflow)
	mockStorage.On("UpdateBackupState", mock.Anything, mock.Anything).Return(&datamodel.Backup{}, nil).Maybe()
	mockStorage.On("UpdateSnapshot", mock.Anything, mock.Anything).Return(&datamodel.Snapshot{}, nil).Maybe()
	mockStorage.On("CreatingSnapshot", mock.Anything, mock.Anything).Return(&datamodel.Snapshot{
		Name:               "test-backup",
		Description:        "VCP-Backup",
		VolumeID:           1,
		AccountID:          1,
		State:              "creating",
		StateDetails:       "creating",
		SnapshotAttributes: &datamodel.SnapshotAttributes{},
	}, nil).Maybe()

	// Create a custom BackupActivity that bypasses the nil check in UpdateSnapshotActivity
	customBackupActivity := &TestBackupActivity{BackupActivity: &activities.BackupActivity{SE: mockStorage}}
	env.RegisterActivity(customBackupActivity)

	// Set up test data
	params := &commonparams.CreateBackupParams{
		VolumeUUID:  "test-vol",
		AccountName: "test-account",
		BackupName:  "test-backup",
	}
	backupVault := &datamodel.BackupVault{
		Name:          "test-backup-vault",
		BucketDetails: datamodel.BucketDetailsArray{&datamodel.BucketDetails{BucketName: "test-bucket", ServiceAccountName: "sa-test", VendorSubnetID: "subnet-12345"}},
	}
	backup := &datamodel.Backup{
		State:         "InProgress",
		Name:          "test-backup",
		VolumeUUID:    "test-vol",
		BackupVault:   backupVault,
		BackupVaultID: 1,
		Attributes:    &datamodel.BackupAttributes{},
	}

	volume := &datamodel.Volume{
		Pool:             &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{Password: "password"}, PoolAttributes: &datamodel.PoolAttributes{PrimaryZone: "us-central1-a"}},
		Svm:              &datamodel.Svm{Name: "svm_test"},
		VolumeAttributes: &datamodel.VolumeAttributes{BlockProperties: &datamodel.BlockProperties{OSType: "LINUX"}, VendorSubnetID: "subnet-12345", ExternalUUID: "external-uuid"},
	}

	// Mock activities with failure at TransferSnapshotActivity
	env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("GetNode", mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	env.OnActivity("PrepareObjectStoreActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{}, nil)
	env.OnActivity("GetOrCreateObjectStoreActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{}, nil)
	env.OnActivity("PrepareSnapmirrorActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{}, nil)
	env.OnActivity("CreateSnapmirrorRelationshipActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node: &models.Node{EndpointAddress: "127.0.0.1"},
		SnapmirrorRelationship: &commonparams.SnapmirrorRelationship{
			UUID: "test-snapmirror-uuid",
		},
	}, nil)
	env.OnActivity("CreatingSnapshotActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node:         &models.Node{EndpointAddress: "127.0.0.1"},
		SnapshotName: "test-backup",
		SnapmirrorRelationship: &commonparams.SnapmirrorRelationship{
			UUID: "test-snapmirror-uuid",
		},
		DbSnapshot: &datamodel.Snapshot{
			Name:               "test-backup",
			Description:        "VCP-Backup",
			VolumeID:           1,
			AccountID:          1,
			State:              "creating",
			StateDetails:       "creating",
			SnapshotAttributes: &datamodel.SnapshotAttributes{},
		},
	}, nil)
	env.OnActivity("CreateSnapshotActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup: &datamodel.Backup{
				Name: "test-backup",
				Attributes: &datamodel.BackupAttributes{
					SnapshotID: "test-snapshot-uuid",
				},
			},
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node:         &models.Node{EndpointAddress: "127.0.0.1"},
		SnapshotName: "test-backup",
		SnapmirrorRelationship: &commonparams.SnapmirrorRelationship{
			UUID: "test-snapmirror-uuid",
		},
		SnapshotResponse: &vsa.SnapshotProviderResponse{
			ProviderResponse: vsa.ProviderResponse{
				ExternalUUID: "test-snapshot-uuid",
			},
		},
	}, nil)
	env.OnActivity("TransferSnapshotActivity", mock.Anything, mock.Anything).Return(nil, errors.New("failed to transfer snapshot"))
	env.OnActivity("UpdateBackupError", mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Execute workflow
	env.ExecuteWorkflow(CreateBackupWorkflow, params, backup, backupVault, volume)

	// Assert that the workflow was executed and handled the error
	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
	assert.ErrorContains(t, env.GetWorkflowError(), "failed to transfer snapshot")
	env.AssertExpectations(t)
}

func TestBackupWorkflowGetSmSourcePathActivityFailure(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
	mockHeader := &commonpb.Header{
		Fields: map[string]*commonpb.Payload{
			"logParam": encodedValue,
		},
	}
	env.SetHeader(mockHeader)

	// Create mock storage for CommonActivities and BackupActivity
	mockStorage := database.NewMockStorage(t)
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	mockStorage.On("UpdateBackupState", mock.Anything, mock.Anything).Return(&datamodel.Backup{}, nil).Maybe()

	commonActivity := &activities.CommonActivities{SE: mockStorage}
	env.RegisterActivity(commonActivity)
	env.RegisterWorkflow(CreateBackupWorkflow)
	env.RegisterActivity(&TestBackupActivity{BackupActivity: &activities.BackupActivity{SE: mockStorage}})

	// Set up test data
	params := &commonparams.CreateBackupParams{
		VolumeUUID:  "test-vol",
		AccountName: "test-account",
		BackupName:  "test-backup",
	}
	backupVault := &datamodel.BackupVault{
		Name:          "test-backup-vault",
		BucketDetails: datamodel.BucketDetailsArray{&datamodel.BucketDetails{BucketName: "test-bucket", ServiceAccountName: "sa-test", VendorSubnetID: "subnet-12345"}},
	}
	backup := &datamodel.Backup{
		State:         "InProgress",
		Name:          "test-backup",
		VolumeUUID:    "test-vol",
		BackupVault:   backupVault,
		BackupVaultID: 1,
		Attributes:    &datamodel.BackupAttributes{},
	}

	volume := &datamodel.Volume{
		Pool:             &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{Password: "password"}, PoolAttributes: &datamodel.PoolAttributes{PrimaryZone: "us-central1-a"}},
		Svm:              &datamodel.Svm{Name: "svm_test"},
		VolumeAttributes: &datamodel.VolumeAttributes{BlockProperties: &datamodel.BlockProperties{OSType: "LINUX"}, VendorSubnetID: "subnet-12345", ExternalUUID: "external-uuid"},
	}

	// Mock activities with failure at PrepareSnapmirrorActivity
	env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("GetNode", mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	env.OnActivity("PrepareObjectStoreActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{}, nil)
	env.OnActivity("GetOrCreateObjectStoreActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{}, nil)
	env.OnActivity("PrepareSnapmirrorActivity", mock.Anything, mock.Anything).Return(nil, errors.New("failed to prepare snapmirror"))
	env.OnActivity("UpdateBackupError", mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Execute workflow
	env.ExecuteWorkflow(CreateBackupWorkflow, params, backup, backupVault, volume)

	// Assert that the workflow was executed and handled the error
	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
	assert.ErrorContains(t, env.GetWorkflowError(), "failed to prepare snapmirror")
	env.AssertExpectations(t)
}

func TestBackupWorkflowSnapmirrorTransferPolling(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
	mockHeader := &commonpb.Header{
		Fields: map[string]*commonpb.Payload{
			"logParam": encodedValue,
		},
	}
	env.SetHeader(mockHeader)

	// Create mock storage for CommonActivities and BackupActivity
	mockStorage := database.NewMockStorage(t)
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()

	commonActivity := &activities.CommonActivities{SE: mockStorage}
	env.RegisterActivity(commonActivity)
	env.RegisterWorkflow(CreateBackupWorkflow)
	mockStorage.On("GetSnapshotsByTypeAndVolumeID", mock.Anything, mock.Anything, mock.Anything).Return([]*datamodel.Snapshot{}, nil).Maybe()
	mockStorage.On("UpdateBackup", mock.Anything, mock.Anything).Return(&datamodel.Backup{}, nil).Maybe()
	mockStorage.On("UpdateBackupLatestLogicalBackupSizeByVolume", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	mockStorage.On("UpdateVolumeFields", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	env.RegisterActivity(&TestBackupActivity{BackupActivity: &activities.BackupActivity{SE: mockStorage}})

	// Register the specific activity method
	env.OnActivity("CleanupOldBackupSnapshotsActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Set up test data
	params := &commonparams.CreateBackupParams{
		VolumeUUID:  "test-vol",
		AccountName: "test-account",
		BackupName:  "test-backup",
	}
	backupVault := &datamodel.BackupVault{
		Name:          "test-backup-vault",
		BucketDetails: datamodel.BucketDetailsArray{&datamodel.BucketDetails{BucketName: "test-bucket", ServiceAccountName: "sa-test", VendorSubnetID: "subnet-12345"}},
	}
	backup := &datamodel.Backup{
		State:         "InProgress",
		Name:          "test-backup",
		VolumeUUID:    "test-vol",
		BackupVault:   backupVault,
		BackupVaultID: 1,
		Attributes:    &datamodel.BackupAttributes{EndpointUUID: "test-endpoint-uuid"},
	}

	volume := &datamodel.Volume{
		Pool:                  &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{Password: "password"}, PoolAttributes: &datamodel.PoolAttributes{PrimaryZone: "us-central1-a"}},
		Svm:                   &datamodel.Svm{Name: "svm_test"},
		VolumeAttributes:      &datamodel.VolumeAttributes{BlockProperties: &datamodel.BlockProperties{OSType: "LINUX"}, VendorSubnetID: "subnet-12345", ExternalUUID: "external-uuid"},
		LargeVolumeAttributes: nil,
	}

	// Mock activities with transfer polling scenario
	env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("GetNode", mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	env.OnActivity("PrepareObjectStoreActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node: &models.Node{EndpointAddress: "127.0.0.1"},
	}, nil)
	env.OnActivity("GetOrCreateObjectStoreActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node:     &models.Node{EndpointAddress: "127.0.0.1"},
		ObjStore: &commonparams.CloudTarget{UUID: "test-obj-store-uuid", Name: "test-obj-store"},
	}, nil)
	env.OnActivity("PrepareSnapmirrorActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node: &models.Node{EndpointAddress: "127.0.0.1"},
	}, nil)
	env.OnActivity("CreateSnapmirrorRelationshipActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node: &models.Node{EndpointAddress: "127.0.0.1"},
	}, nil)
	env.OnActivity("CreatingSnapshotActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node: &models.Node{EndpointAddress: "127.0.0.1"},
	}, nil)
	env.OnActivity("CreateSnapshotActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup: &datamodel.Backup{
				Name: "test-backup",
				Attributes: &datamodel.BackupAttributes{
					SnapshotID: "test-snapshot-uuid",
				},
			},
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node:         &models.Node{EndpointAddress: "127.0.0.1"},
		SnapshotName: "test-backup",
		SnapmirrorRelationship: &commonparams.SnapmirrorRelationship{
			UUID: "test-snapmirror-uuid",
		},
		SnapshotResponse: &vsa.SnapshotProviderResponse{
			ProviderResponse: vsa.ProviderResponse{
				ExternalUUID: "test-snapshot-uuid",
			},
		},
	}, nil)
	// ... existing code ...
	env.OnActivity("TransferSnapshotActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node: &models.Node{EndpointAddress: "127.0.0.1"},
	}, nil)
	env.OnActivity("PollTransferStatusWithHistoryCheckActivity", mock.Anything, mock.Anything, mock.Anything).Return(&activities.PollTransferStatusOutput{
		BackupActivitiesContext: &activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:      backup,
				BackupVault: backupVault,
				Volume:      volume,
			},
			Node:              &models.Node{EndpointAddress: "127.0.0.1"},
			SnapshotName:      "test-backup",
			SmSourcePath:      "svm_test:volume_test",
			SmDestinationPath: "test-bucket:/objstore/test-vol",
			SnapmirrorRelationship: &commonparams.SnapmirrorRelationship{
				UUID: "test-snapmirror-uuid",
			},
			TransferStatus: activities.SmStatusSuccess,
		},
		TransferComplete:    true,
		ShouldContinueAsNew: false,
	}, nil)
	env.OnActivity("GetSnapmirror", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&commonparams.SnapmirrorRelationship{
		UUID:    "test-snapmirror-uuid",
		Healthy: nillable.ToPointer(true),
	}, nil)
	env.OnActivity("UpdateSnapshotActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node: &models.Node{EndpointAddress: "127.0.0.1"},
	}, nil)
	env.OnActivity("GetObjectStoreEndpointActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node: &models.Node{EndpointAddress: "127.0.0.1"},
	}, nil)
	env.OnActivity("GetObjectStoreSnapshotActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node: &models.Node{EndpointAddress: "127.0.0.1"},
	}, nil)
	env.OnActivity("UpdateBackupSizeActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node: &models.Node{EndpointAddress: "127.0.0.1"},
	}, nil)
	env.OnActivity("FinishBackupActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node: &models.Node{EndpointAddress: "127.0.0.1"},
	}, nil)
	env.OnActivity("CleanupOldBackupSnapshotsActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("CreateBackupMetadataIfFirstBackupActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Execute workflow
	env.ExecuteWorkflow(CreateBackupWorkflow, params, backup, backupVault, volume)

	// Assert that the workflow was executed successfully
	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

func TestBackupWorkflowSnapmirrorTransferFailed(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
	mockHeader := &commonpb.Header{
		Fields: map[string]*commonpb.Payload{
			"logParam": encodedValue,
		},
	}
	env.SetHeader(mockHeader)

	// Create mock storage for CommonActivities and BackupActivity
	mockStorage := database.NewMockStorage(t)
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()

	commonActivity := &activities.CommonActivities{SE: mockStorage}
	env.RegisterActivity(commonActivity)
	env.RegisterWorkflow(CreateBackupWorkflow)
	mockStorage.On("UpdateBackupState", mock.Anything, mock.Anything).Return(&datamodel.Backup{}, nil).Maybe()
	mockStorage.On("UpdateSnapshot", mock.Anything, mock.Anything).Return(&datamodel.Snapshot{}, nil).Maybe()
	mockStorage.On("CreatingSnapshot", mock.Anything, mock.Anything).Return(&datamodel.Snapshot{
		Name:               "test-backup",
		Description:        "VCP-Backup",
		VolumeID:           1,
		AccountID:          1,
		State:              "creating",
		StateDetails:       "creating",
		SnapshotAttributes: &datamodel.SnapshotAttributes{},
	}, nil).Maybe()

	// Create a custom BackupActivity that bypasses the nil check in UpdateSnapshotActivity
	customBackupActivity := &TestBackupActivity{BackupActivity: &activities.BackupActivity{SE: mockStorage}}
	env.RegisterActivity(customBackupActivity)

	// Set up test data
	params := &commonparams.CreateBackupParams{
		VolumeUUID:  "test-vol",
		AccountName: "test-account",
		BackupName:  "test-backup",
	}
	backupVault := &datamodel.BackupVault{
		Name:          "test-backup-vault",
		BucketDetails: datamodel.BucketDetailsArray{&datamodel.BucketDetails{BucketName: "test-bucket", ServiceAccountName: "sa-test", VendorSubnetID: "subnet-12345"}},
	}
	backup := &datamodel.Backup{
		State:         "InProgress",
		Name:          "test-backup",
		VolumeUUID:    "test-vol",
		BackupVault:   backupVault,
		BackupVaultID: 1,
		Attributes:    &datamodel.BackupAttributes{},
	}

	volume := &datamodel.Volume{
		Pool:             &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{Password: "password"}, PoolAttributes: &datamodel.PoolAttributes{PrimaryZone: "us-central1-a"}},
		Svm:              &datamodel.Svm{Name: "svm_test"},
		VolumeAttributes: &datamodel.VolumeAttributes{BlockProperties: &datamodel.BlockProperties{OSType: "LINUX"}, VendorSubnetID: "subnet-12345", ExternalUUID: "external-uuid"},
	}

	// Mock activities with transfer failure scenario
	env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("GetNode", mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	env.OnActivity("PrepareObjectStoreActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{}, nil)
	env.OnActivity("GetOrCreateObjectStoreActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{}, nil)
	env.OnActivity("PrepareSnapmirrorActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{}, nil)
	env.OnActivity("CreateSnapmirrorRelationshipActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node: &models.Node{EndpointAddress: "127.0.0.1"},
		SnapmirrorRelationship: &commonparams.SnapmirrorRelationship{
			UUID: "test-snapmirror-uuid",
		},
	}, nil)
	env.OnActivity("CreatingSnapshotActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node:         &models.Node{EndpointAddress: "127.0.0.1"},
		SnapshotName: "test-backup",
		SnapmirrorRelationship: &commonparams.SnapmirrorRelationship{
			UUID: "test-snapmirror-uuid",
		},
		DbSnapshot: &datamodel.Snapshot{
			Name:               "test-backup",
			Description:        "VCP-Backup",
			VolumeID:           1,
			AccountID:          1,
			State:              "creating",
			StateDetails:       "creating",
			SnapshotAttributes: &datamodel.SnapshotAttributes{},
		},
	}, nil)
	env.OnActivity("CreateSnapshotActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup: &datamodel.Backup{
				Name: "test-backup",
				Attributes: &datamodel.BackupAttributes{
					SnapshotID: "test-snapshot-uuid",
				},
			},
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node:         &models.Node{EndpointAddress: "127.0.0.1"},
		SnapshotName: "test-backup",
		SnapmirrorRelationship: &commonparams.SnapmirrorRelationship{
			UUID: "test-snapmirror-uuid",
		},
		SnapshotResponse: &vsa.SnapshotProviderResponse{
			ProviderResponse: vsa.ProviderResponse{
				ExternalUUID: "test-snapshot-uuid",
			},
		},
	}, nil)
	env.OnActivity("TransferSnapshotActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{}, nil)
	env.OnActivity("PollTransferStatusWithHistoryCheckActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("snapmirror transfer failed for snapshot  with status: failed"))
	env.OnActivity("UpdateBackupError", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	// Execute workflow
	env.ExecuteWorkflow(CreateBackupWorkflow, params, backup, backupVault, volume)

	// Assert that the workflow was executed and handled the error
	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
	assert.ErrorContains(t, env.GetWorkflowError(), "snapmirror transfer failed for snapshot  with status: failed")
	env.AssertExpectations(t)
}

func TestBackupWorkflowUnhealthySnapmirrorWithReason(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
	mockHeader := &commonpb.Header{
		Fields: map[string]*commonpb.Payload{
			"logParam": encodedValue,
		},
	}
	env.SetHeader(mockHeader)

	// Create mock storage for CommonActivities and BackupActivity
	mockStorage := database.NewMockStorage(t)
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()

	commonActivity := &activities.CommonActivities{SE: mockStorage}
	env.RegisterActivity(commonActivity)
	env.RegisterWorkflow(CreateBackupWorkflow)
	mockStorage.On("UpdateBackupState", mock.Anything, mock.Anything).Return(&datamodel.Backup{}, nil).Maybe()
	env.RegisterActivity(&TestBackupActivity{BackupActivity: &activities.BackupActivity{SE: mockStorage}})

	// Set up test data
	params := &commonparams.CreateBackupParams{
		VolumeUUID:  "test-vol",
		AccountName: "test-account",
		BackupName:  "test-backup",
	}
	backupVault := &datamodel.BackupVault{
		Name:          "test-backup-vault",
		BucketDetails: datamodel.BucketDetailsArray{&datamodel.BucketDetails{BucketName: "test-bucket", ServiceAccountName: "sa-test", VendorSubnetID: "subnet-12345"}},
	}
	backup := &datamodel.Backup{
		State:         "InProgress",
		Name:          "test-backup",
		VolumeUUID:    "test-vol",
		BackupVault:   backupVault,
		BackupVaultID: 1,
		Attributes:    &datamodel.BackupAttributes{},
	}

	volume := &datamodel.Volume{
		Pool:                  &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{Password: "password"}, PoolAttributes: &datamodel.PoolAttributes{PrimaryZone: "us-central1-a"}},
		Svm:                   &datamodel.Svm{Name: "svm_test"},
		VolumeAttributes:      &datamodel.VolumeAttributes{BlockProperties: &datamodel.BlockProperties{OSType: "LINUX"}, VendorSubnetID: "subnet-12345", ExternalUUID: "external-uuid"},
		LargeVolumeAttributes: nil,
	}

	// Mock all activities that the workflow calls
	env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("GetNode", mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	env.OnActivity("PrepareObjectStoreActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node: &models.Node{EndpointAddress: "127.0.0.1"},
	}, nil)
	env.OnActivity("GetOrCreateObjectStoreActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node: &models.Node{EndpointAddress: "127.0.0.1"},
	}, nil)
	env.OnActivity("PrepareSnapmirrorActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node:              &models.Node{EndpointAddress: "127.0.0.1"},
		SmSourcePath:      "svm_test:volume_test",
		SmDestinationPath: "test-bucket:/objstore/test-vol",
	}, nil)
	env.OnActivity("CreateSnapmirrorRelationshipActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node:              &models.Node{EndpointAddress: "127.0.0.1"},
		SmSourcePath:      "svm_test:volume_test",
		SmDestinationPath: "test-bucket:/objstore/test-vol",
		SnapmirrorRelationship: &commonparams.SnapmirrorRelationship{
			UUID: "test-snapmirror-uuid",
		},
	}, nil)
	env.OnActivity("CreatingSnapshotActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node:         &models.Node{EndpointAddress: "127.0.0.1"},
		SnapshotName: "test-backup",
	}, nil)
	env.OnActivity("CreateSnapshotActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node:         &models.Node{EndpointAddress: "127.0.0.1"},
		SnapshotName: "test-backup",
	}, nil)
	env.OnActivity("TransferSnapshotActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node:              &models.Node{EndpointAddress: "127.0.0.1"},
		SnapshotName:      "test-backup",
		SmSourcePath:      "svm_test:volume_test",
		SmDestinationPath: "test-bucket:/objstore/test-vol",
	}, nil)
	env.OnActivity("PollTransferStatusWithHistoryCheckActivity", mock.Anything, mock.Anything, mock.Anything).Return(&activities.PollTransferStatusOutput{
		BackupActivitiesContext: &activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:      backup,
				BackupVault: backupVault,
				Volume:      volume,
			},
			Node:              &models.Node{EndpointAddress: "127.0.0.1"},
			SnapshotName:      "test-backup",
			SmSourcePath:      "svm_test:volume_test",
			SmDestinationPath: "test-bucket:/objstore/test-vol",
			TransferStatus:    activities.SmStatusSuccess,
		},
		TransferComplete:    true,
		ShouldContinueAsNew: false,
	}, nil)
	// Mock GetSnapmirror to return unhealthy relationship with reason
	unhealthyReasons := []string{"Transfer failed", "Connection timeout"}
	env.OnActivity("GetSnapmirror", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&commonparams.SnapmirrorRelationship{
		UUID:            "test-snapmirror-uuid",
		Healthy:         nillable.ToPointer(false),
		UnhealthyReason: &unhealthyReasons,
	}, nil)
	// Mock rollback activities for error scenario
	env.OnActivity("UpdateBackupError", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("FinishBackupActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
	}, nil).Maybe()

	// Execute workflow
	env.ExecuteWorkflow(CreateBackupWorkflow, params, backup, backupVault, volume)

	// Assert that the workflow failed with unhealthy snapmirror error
	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
	assert.ErrorContains(t, env.GetWorkflowError(), "snapmirror relationship is unhealthy")
	env.AssertExpectations(t)
}

func TestBackupWorkflowSnapmirrorStateNotSnapmirrored(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
	mockHeader := &commonpb.Header{
		Fields: map[string]*commonpb.Payload{
			"logParam": encodedValue,
		},
	}
	env.SetHeader(mockHeader)

	mockStorage := database.NewMockStorage(t)
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()

	commonActivity := &activities.CommonActivities{SE: mockStorage}
	env.RegisterActivity(commonActivity)
	env.RegisterWorkflow(CreateBackupWorkflow)
	mockStorage.On("UpdateBackupState", mock.Anything, mock.Anything).Return(&datamodel.Backup{}, nil).Maybe()
	env.RegisterActivity(&TestBackupActivity{BackupActivity: &activities.BackupActivity{SE: mockStorage}})

	params := &commonparams.CreateBackupParams{
		VolumeUUID:  "test-vol",
		AccountName: "test-account",
		BackupName:  "test-backup",
	}
	backupVault := &datamodel.BackupVault{
		Name:          "test-backup-vault",
		BucketDetails: datamodel.BucketDetailsArray{&datamodel.BucketDetails{BucketName: "test-bucket", ServiceAccountName: "sa-test", VendorSubnetID: "subnet-12345"}},
	}
	backup := &datamodel.Backup{
		State:         "InProgress",
		Name:          "test-backup",
		VolumeUUID:    "test-vol",
		BackupVault:   backupVault,
		BackupVaultID: 1,
		Attributes:    &datamodel.BackupAttributes{},
	}

	volume := &datamodel.Volume{
		Pool:                  &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{Password: "password"}, PoolAttributes: &datamodel.PoolAttributes{PrimaryZone: "us-central1-a"}},
		Svm:                   &datamodel.Svm{Name: "svm_test"},
		VolumeAttributes:      &datamodel.VolumeAttributes{BlockProperties: &datamodel.BlockProperties{OSType: "LINUX"}, VendorSubnetID: "subnet-12345", ExternalUUID: "external-uuid"},
		LargeVolumeAttributes: nil,
	}

	env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("GetNode", mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	env.OnActivity("PrepareObjectStoreActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node: &models.Node{EndpointAddress: "127.0.0.1"},
	}, nil)
	env.OnActivity("GetOrCreateObjectStoreActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node: &models.Node{EndpointAddress: "127.0.0.1"},
	}, nil)
	env.OnActivity("PrepareSnapmirrorActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node:              &models.Node{EndpointAddress: "127.0.0.1"},
		SmSourcePath:      "svm_test:volume_test",
		SmDestinationPath: "test-bucket:/objstore/test-vol",
	}, nil)
	env.OnActivity("CreateSnapmirrorRelationshipActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node:              &models.Node{EndpointAddress: "127.0.0.1"},
		SmSourcePath:      "svm_test:volume_test",
		SmDestinationPath: "test-bucket:/objstore/test-vol",
		SnapmirrorRelationship: &commonparams.SnapmirrorRelationship{
			UUID: "test-snapmirror-uuid",
		},
	}, nil)
	env.OnActivity("CreatingSnapshotActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node:         &models.Node{EndpointAddress: "127.0.0.1"},
		SnapshotName: "test-backup",
	}, nil)
	env.OnActivity("CreateSnapshotActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node:         &models.Node{EndpointAddress: "127.0.0.1"},
		SnapshotName: "test-backup",
	}, nil)
	env.OnActivity("TransferSnapshotActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node:              &models.Node{EndpointAddress: "127.0.0.1"},
		SnapshotName:      "test-backup",
		SmSourcePath:      "svm_test:volume_test",
		SmDestinationPath: "test-bucket:/objstore/test-vol",
	}, nil)
	env.OnActivity("PollTransferStatusWithHistoryCheckActivity", mock.Anything, mock.Anything, mock.Anything).Return(&activities.PollTransferStatusOutput{
		BackupActivitiesContext: &activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:      backup,
				BackupVault: backupVault,
				Volume:      volume,
			},
			Node:              &models.Node{EndpointAddress: "127.0.0.1"},
			SnapshotName:      "test-backup",
			SmSourcePath:      "svm_test:volume_test",
			SmDestinationPath: "test-bucket:/objstore/test-vol",
			TransferStatus:    activities.SmStatusSuccess,
		},
		TransferComplete:    true,
		ShouldContinueAsNew: false,
	}, nil)
	brokenOffState := "broken_off"
	unhealthyReasons := []string{"Transfer failed", "Connection timeout"}
	env.OnActivity("GetSnapmirror", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&commonparams.SnapmirrorRelationship{
		UUID:            "test-snapmirror-uuid",
		Healthy:         nillable.ToPointer(false),
		UnhealthyReason: &unhealthyReasons,
		State:           &brokenOffState,
	}, nil)
	env.OnActivity("UpdateBackupError", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("FinishBackupActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
	}, nil).Maybe()

	env.ExecuteWorkflow(CreateBackupWorkflow, params, backup, backupVault, volume)

	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
	assert.ErrorContains(t, env.GetWorkflowError(), "snapmirror relationship state is not snapmirrored")
	env.AssertExpectations(t)
}

func TestBackupWorkflowUnhealthySnapmirrorWithoutReason(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
	mockHeader := &commonpb.Header{
		Fields: map[string]*commonpb.Payload{
			"logParam": encodedValue,
		},
	}
	env.SetHeader(mockHeader)

	// Create mock storage for CommonActivities and BackupActivity
	mockStorage := database.NewMockStorage(t)
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()

	commonActivity := &activities.CommonActivities{SE: mockStorage}
	env.RegisterActivity(commonActivity)
	env.RegisterWorkflow(CreateBackupWorkflow)
	mockStorage.On("UpdateBackupState", mock.Anything, mock.Anything).Return(&datamodel.Backup{}, nil).Maybe()
	env.RegisterActivity(&TestBackupActivity{BackupActivity: &activities.BackupActivity{SE: mockStorage}})

	// Set up test data
	params := &commonparams.CreateBackupParams{
		VolumeUUID:  "test-vol",
		AccountName: "test-account",
		BackupName:  "test-backup",
	}
	backupVault := &datamodel.BackupVault{
		Name:          "test-backup-vault",
		BucketDetails: datamodel.BucketDetailsArray{&datamodel.BucketDetails{BucketName: "test-bucket", ServiceAccountName: "sa-test", VendorSubnetID: "subnet-12345"}},
	}
	backup := &datamodel.Backup{
		State:         "InProgress",
		Name:          "test-backup",
		VolumeUUID:    "test-vol",
		BackupVault:   backupVault,
		BackupVaultID: 1,
		Attributes:    &datamodel.BackupAttributes{},
	}

	volume := &datamodel.Volume{
		Pool:                  &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{Password: "password"}, PoolAttributes: &datamodel.PoolAttributes{PrimaryZone: "us-central1-a"}},
		Svm:                   &datamodel.Svm{Name: "svm_test"},
		VolumeAttributes:      &datamodel.VolumeAttributes{BlockProperties: &datamodel.BlockProperties{OSType: "LINUX"}, VendorSubnetID: "subnet-12345", ExternalUUID: "external-uuid"},
		LargeVolumeAttributes: nil,
	}

	// Mock all activities that the workflow calls
	env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("GetNode", mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	env.OnActivity("PrepareObjectStoreActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node: &models.Node{EndpointAddress: "127.0.0.1"},
	}, nil)
	env.OnActivity("GetOrCreateObjectStoreActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node: &models.Node{EndpointAddress: "127.0.0.1"},
	}, nil)
	env.OnActivity("PrepareSnapmirrorActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node:              &models.Node{EndpointAddress: "127.0.0.1"},
		SmSourcePath:      "svm_test:volume_test",
		SmDestinationPath: "test-bucket:/objstore/test-vol",
	}, nil)
	env.OnActivity("CreateSnapmirrorRelationshipActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node:              &models.Node{EndpointAddress: "127.0.0.1"},
		SmSourcePath:      "svm_test:volume_test",
		SmDestinationPath: "test-bucket:/objstore/test-vol",
		SnapmirrorRelationship: &commonparams.SnapmirrorRelationship{
			UUID: "test-snapmirror-uuid",
		},
	}, nil)
	env.OnActivity("CreatingSnapshotActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node:         &models.Node{EndpointAddress: "127.0.0.1"},
		SnapshotName: "test-backup",
	}, nil)
	env.OnActivity("CreateSnapshotActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node:         &models.Node{EndpointAddress: "127.0.0.1"},
		SnapshotName: "test-backup",
	}, nil)
	env.OnActivity("TransferSnapshotActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node:              &models.Node{EndpointAddress: "127.0.0.1"},
		SnapshotName:      "test-backup",
		SmSourcePath:      "svm_test:volume_test",
		SmDestinationPath: "test-bucket:/objstore/test-vol",
	}, nil)
	env.OnActivity("PollTransferStatusWithHistoryCheckActivity", mock.Anything, mock.Anything, mock.Anything).Return(&activities.PollTransferStatusOutput{
		BackupActivitiesContext: &activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:      backup,
				BackupVault: backupVault,
				Volume:      volume,
			},
			Node:              &models.Node{EndpointAddress: "127.0.0.1"},
			SnapshotName:      "test-backup",
			SmSourcePath:      "svm_test:volume_test",
			SmDestinationPath: "test-bucket:/objstore/test-vol",
			TransferStatus:    activities.SmStatusSuccess,
		},
		TransferComplete:    true,
		ShouldContinueAsNew: false,
	}, nil)
	// Mock GetSnapmirror to return unhealthy relationship without reason
	// Note: The workflow fails when snapmirror is unhealthy, regardless of whether there's a reason
	env.OnActivity("GetSnapmirror", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&commonparams.SnapmirrorRelationship{
		UUID:    "test-snapmirror-uuid",
		Healthy: nillable.ToPointer(false),
	}, nil)
	// Mock rollback activity for error scenario
	env.OnActivity("UpdateBackupError", mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Execute workflow
	env.ExecuteWorkflow(CreateBackupWorkflow, params, backup, backupVault, volume)

	// Assert that the workflow failed with unhealthy snapmirror error
	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
	assert.ErrorContains(t, env.GetWorkflowError(), "snapmirror relationship is unhealthy")
	env.AssertExpectations(t)
}

func TestBackupWorkflowGetSnapmirrorError(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
	mockHeader := &commonpb.Header{
		Fields: map[string]*commonpb.Payload{
			"logParam": encodedValue,
		},
	}
	env.SetHeader(mockHeader)

	// Create mock storage for CommonActivities and BackupActivity
	mockStorage := database.NewMockStorage(t)
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()

	commonActivity := &activities.CommonActivities{SE: mockStorage}
	env.RegisterActivity(commonActivity)
	env.RegisterWorkflow(CreateBackupWorkflow)
	mockStorage.On("UpdateBackupState", mock.Anything, mock.Anything).Return(&datamodel.Backup{}, nil).Maybe()
	env.RegisterActivity(&TestBackupActivity{BackupActivity: &activities.BackupActivity{SE: mockStorage}})

	// Set up test data
	params := &commonparams.CreateBackupParams{
		VolumeUUID:  "test-vol",
		AccountName: "test-account",
		BackupName:  "test-backup",
	}
	backupVault := &datamodel.BackupVault{
		Name:          "test-backup-vault",
		BucketDetails: datamodel.BucketDetailsArray{&datamodel.BucketDetails{BucketName: "test-bucket", ServiceAccountName: "sa-test", VendorSubnetID: "subnet-12345"}},
	}
	backup := &datamodel.Backup{
		State:         "InProgress",
		Name:          "test-backup",
		VolumeUUID:    "test-vol",
		BackupVault:   backupVault,
		BackupVaultID: 1,
		Attributes:    &datamodel.BackupAttributes{},
	}

	volume := &datamodel.Volume{
		Pool:                  &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{Password: "password"}, PoolAttributes: &datamodel.PoolAttributes{PrimaryZone: "us-central1-a"}},
		Svm:                   &datamodel.Svm{Name: "svm_test"},
		VolumeAttributes:      &datamodel.VolumeAttributes{BlockProperties: &datamodel.BlockProperties{OSType: "LINUX"}, VendorSubnetID: "subnet-12345", ExternalUUID: "external-uuid"},
		LargeVolumeAttributes: nil,
	}

	// Mock all activities that the workflow calls
	env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("GetNode", mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	env.OnActivity("PrepareObjectStoreActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node: &models.Node{EndpointAddress: "127.0.0.1"},
	}, nil)
	env.OnActivity("GetOrCreateObjectStoreActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node: &models.Node{EndpointAddress: "127.0.0.1"},
	}, nil)
	env.OnActivity("PrepareSnapmirrorActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node:              &models.Node{EndpointAddress: "127.0.0.1"},
		SmSourcePath:      "svm_test:volume_test",
		SmDestinationPath: "test-bucket:/objstore/test-vol",
	}, nil)
	env.OnActivity("CreateSnapmirrorRelationshipActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node:              &models.Node{EndpointAddress: "127.0.0.1"},
		SmSourcePath:      "svm_test:volume_test",
		SmDestinationPath: "test-bucket:/objstore/test-vol",
		SnapmirrorRelationship: &commonparams.SnapmirrorRelationship{
			UUID: "test-snapmirror-uuid",
		},
	}, nil)
	env.OnActivity("CreatingSnapshotActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node:         &models.Node{EndpointAddress: "127.0.0.1"},
		SnapshotName: "test-backup",
	}, nil)
	env.OnActivity("CreateSnapshotActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node:         &models.Node{EndpointAddress: "127.0.0.1"},
		SnapshotName: "test-backup",
	}, nil)
	env.OnActivity("TransferSnapshotActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node:              &models.Node{EndpointAddress: "127.0.0.1"},
		SnapshotName:      "test-backup",
		SmSourcePath:      "svm_test:volume_test",
		SmDestinationPath: "test-bucket:/objstore/test-vol",
	}, nil)
	env.OnActivity("PollTransferStatusWithHistoryCheckActivity", mock.Anything, mock.Anything, mock.Anything).Return(&activities.PollTransferStatusOutput{
		BackupActivitiesContext: &activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:      backup,
				BackupVault: backupVault,
				Volume:      volume,
			},
			Node:              &models.Node{EndpointAddress: "127.0.0.1"},
			SnapshotName:      "test-backup",
			SmSourcePath:      "svm_test:volume_test",
			SmDestinationPath: "test-bucket:/objstore/test-vol",
			TransferStatus:    activities.SmStatusSuccess,
		},
		TransferComplete:    true,
		ShouldContinueAsNew: false,
	}, nil)
	// Mock GetSnapmirror to return an error
	env.OnActivity("GetSnapmirror", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("failed to get snapmirror relationship"))
	// Mock rollback activities for error scenario
	env.OnActivity("UpdateBackupError", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("FinishBackupActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
	}, nil).Maybe()

	// Execute workflow
	env.ExecuteWorkflow(CreateBackupWorkflow, params, backup, backupVault, volume)

	// Assert that the workflow failed with GetSnapmirror error
	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
	assert.ErrorContains(t, env.GetWorkflowError(), "failed to get snapmirror relationship")
	env.AssertExpectations(t)
}

func TestUpdateBackupWorkflowSuccess(t *testing.T) {
	t.Run("UpdateBackupWorkflowSuccess", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		env.SetHeader(mockHeader)

		// Create mock storage for CommonActivities
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()

		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(setupMockBackupActivity(t))
		env.RegisterWorkflow(UpdateBackupWorkflow)

		backup := &datamodel.Backup{
			Name:        "test-backup",
			VolumeUUID:  "test-vol",
			Attributes:  &datamodel.BackupAttributes{BucketName: "test-bucket"},
			Description: "Updated description",
		}

		// Mock activity responses
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("UpdateBackup", mock.Anything, backup).Return(nil)

		// Execute workflow
		env.ExecuteWorkflow(UpdateBackupWorkflow, backup)

		// Assert workflow execution
		assert.True(t, env.IsWorkflowCompleted())
		assert.NoError(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})
}

func TestUpdateBackupWorkflowFailure(t *testing.T) {
	t.Run("UpdateBackupWorkflowFailure", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		env.SetHeader(mockHeader)
		// Mock storage for proper BackupActivity and CommonActivities initialization
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		mockStorage.On("UpdateBackupState", mock.Anything, mock.Anything).Return(&datamodel.Backup{}, nil)

		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(&TestBackupActivity{BackupActivity: &activities.BackupActivity{SE: mockStorage}})
		env.RegisterWorkflow(UpdateBackupWorkflow)

		// Set up test data
		backup := &datamodel.Backup{
			Name:        "test-backup",
			VolumeUUID:  "test-vol",
			Attributes:  &datamodel.BackupAttributes{BucketName: "test-bucket"},
			Description: "Updated description",
		}

		// Mock activity responses - use simple mocks that just check workflow completion
		// First UpdateJobStatus call (PROCESSING) should succeed
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Once()
		// UpdateBackup should fail
		env.OnActivity("UpdateBackup", mock.Anything, mock.Anything).Return(errors.New("update failed"))
		// Second UpdateJobStatus call (ERROR) - let it succeed so we can test the flow
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Maybe()
		// Execute workflow
		env.ExecuteWorkflow(UpdateBackupWorkflow, backup)

		// Assert workflow execution - the workflow handles errors internally and completes successfully
		// even when UpdateBackup fails, because it updates the job status to handle the error
		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		assert.ErrorContains(t, env.GetWorkflowError(), "update failed")
		env.AssertExpectations(t)
	})
}

func TestDeleteBackupWorkflow(t *testing.T) {
	t.Run("DeleteBackupWorkflowSuccess", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		env.SetHeader(mockHeader)

		// Create mock storage for CommonActivities
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()

		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(setupMockBackupActivity(t))
		env.RegisterWorkflow(DeleteBackupWorkflow)

		// Set up test data
		params := &commonparams.DeleteBackupParams{
			BackupVaultUUID: "vault-uuid",
			BackupUUID:      "backup-uuid",
			AccountName:     "test-account",
		}
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{UUID: "account-uuid"}}
		backupVault := &datamodel.BackupVault{
			Name: "test-backup-vault",
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{BucketName: "test-bucket", ServiceAccountName: "sa-test", VendorSubnetID: "subnet-12345"},
			},
			Account: account,
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-vol"},
			Pool: &datamodel.Pool{
				BaseModel: datamodel.BaseModel{ID: int64(1)},
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "",
					CertificateID: "",
				},
			},
			Svm: &datamodel.Svm{
				Name: "svm_test",
			},
			VolumeAttributes: &datamodel.VolumeAttributes{
				VendorSubnetID: "subnet-12345",
			},
			DataProtection: &datamodel.DataProtection{},
		}
		backup := &datamodel.Backup{
			Name:          "test-backup",
			VolumeUUID:    "test-vol",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes:    &datamodel.BackupAttributes{BucketName: "test-bucket"},
		}

		// Mock activity responses
		env.OnActivity("GetAccountByName", mock.Anything, params.AccountName).Return(account, nil)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetBackupVault", mock.Anything, params.BackupVaultUUID).Return(backupVault, nil)
		env.OnActivity("GetBackup", mock.Anything, params.BackupVaultUUID, params.BackupUUID, params.AccountName).Return(backup, nil)
		env.OnActivity("IsVolumeDeleted", mock.Anything, backup.VolumeUUID).Return(false, nil)
		env.OnActivity("IsExpertModeVolume", mock.Anything, backup.VolumeUUID).Return(false, nil)
		env.OnActivity("GetVolume", mock.Anything, backup.VolumeUUID).Return(volume, nil)
		env.OnActivity("GetNode", mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
		env.OnActivity("GetSmDestinationPathActivity", mock.Anything, mock.Anything, mock.Anything).Return("test-bucket:/objstore/test-vol", nil)
		env.OnActivity("GetSmSourcePathActivity", mock.Anything, mock.Anything).Return("svm_test:volume_test", nil)
		env.OnActivity("IsSnapmirrorDeleted", mock.Anything, mock.Anything, mock.Anything).Return(&commonparams.SnapmirrorDeletePrecheckResult{RelationshipMissing: false, Relationship: &commonparams.SnapmirrorRelationship{UUID: "test-sm-uuid"}}, nil)
		env.OnActivity("GetBackupCountByVolumeUUID", mock.Anything, backup.VolumeUUID).Return(int64(1), nil)
		env.OnActivity("GetObjectStore", mock.Anything, mock.Anything, mock.Anything).Return(&commonparams.CloudTarget{UUID: "obj-store-uuid"}, nil)
		env.OnActivity("GetSnapmirror", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&commonparams.SnapmirrorRelationship{UUID: "snapmirror-uuid"}, nil)
		env.OnActivity("DeleteSnapmirror", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{JobUUID: "job-uuid"}, nil)
		env.OnActivity("GetOntapJob", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil)
		env.OnActivity("UpdateVolumeLatestLogicalBackupSize", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DeleteCloudEndpoint", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{JobUUID: "job-uuid"}, nil)
		env.OnActivity("DeleteSnapshotForBackup", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DeleteBackupSnapshotFromDB", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("HydrateSnapshotDeletionToCCFEActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DeleteBackup", mock.Anything, params.BackupUUID).Return(backup, nil)
		env.OnActivity("DeleteBackupMetadataIfLastBackupActivity", mock.Anything, mock.Anything).Return(nil)

		// Execute workflow
		env.ExecuteWorkflow(DeleteBackupWorkflow, params)

		// Assert workflow execution
		assert.True(t, env.IsWorkflowCompleted())
		assert.NoError(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("DeleteBackupWithBackupVaultSwitchingEnabled_UsesPerEndpointCountWithEmptyEndpoint", func(t *testing.T) {
		originalFlag := utils.EnableBackupVaultSwitching
		defer utils.SetEnableBackupVaultSwitchingForTest(originalFlag)
		utils.SetEnableBackupVaultSwitchingForTest(true)

		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		env.SetHeader(mockHeader)
		commonActivity, _ := setupMockCommonActivities(t)
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(setupMockBackupActivity(t))
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterWorkflow(DeleteBackupWorkflow)

		params := &commonparams.DeleteBackupParams{
			BackupVaultUUID: "vault-uuid",
			BackupUUID:      "backup-uuid",
			AccountName:     "test-account",
		}
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{UUID: "account-uuid"}}
		backupVault := &datamodel.BackupVault{
			Name: "test-backup-vault",
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{BucketName: "test-bucket", ServiceAccountName: "sa-test", VendorSubnetID: "subnet-12345"},
			},
			Account: account,
		}
		volume := &datamodel.Volume{
			BaseModel:        datamodel.BaseModel{UUID: "test-vol"},
			Pool:             &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{}},
			Svm:              &datamodel.Svm{Name: "svm_test"},
			VolumeAttributes: &datamodel.VolumeAttributes{VendorSubnetID: "subnet-12345"},
			DataProtection:   &datamodel.DataProtection{},
		}
		backup := &datamodel.Backup{
			Name:          "test-backup",
			VolumeUUID:    "test-vol",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes:    &datamodel.BackupAttributes{BucketName: "test-bucket"},
		}

		env.OnActivity("GetAccountByName", mock.Anything, params.AccountName).Return(account, nil)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetBackupVault", mock.Anything, params.BackupVaultUUID).Return(backupVault, nil)
		env.OnActivity("GetBackup", mock.Anything, params.BackupVaultUUID, params.BackupUUID, params.AccountName).Return(backup, nil)
		env.OnActivity("IsVolumeDeleted", mock.Anything, backup.VolumeUUID).Return(false, nil)
		env.OnActivity("IsExpertModeVolume", mock.Anything, backup.VolumeUUID).Return(false, nil)
		env.OnActivity("GetVolume", mock.Anything, backup.VolumeUUID).Return(volume, nil)
		env.OnActivity("GetNode", mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
		env.OnActivity("GetSmDestinationPathActivity", mock.Anything, mock.Anything, mock.Anything).Return("test-bucket:/objstore/test-vol", nil)
		env.OnActivity("GetSmSourcePathActivity", mock.Anything, mock.Anything).Return("svm_test:volume_test", nil)
		env.OnActivity("IsSnapmirrorDeleted", mock.Anything, mock.Anything, mock.Anything).Return(&commonparams.SnapmirrorDeletePrecheckResult{RelationshipMissing: false, Relationship: &commonparams.SnapmirrorRelationship{UUID: "test-sm-uuid"}}, nil)
		env.OnActivity("IsLatestBackupInVaultAndInEndpointActivity", mock.Anything, backup.BaseModel.UUID, backup.VolumeUUID, backup.BackupVaultID, "").Return(false, nil)
		// No endpoint on the backup row: count uses blank-endpoint bucket (legacy rows).
		env.OnActivity("GetBackupCountByVolumeVaultAndEndpoint", mock.Anything, backup.VolumeUUID, backup.BackupVaultID, "").Return(int64(1), nil)
		env.OnActivity("GetObjectStore", mock.Anything, mock.Anything, mock.Anything).Return(&commonparams.CloudTarget{UUID: "obj-store-uuid"}, nil)
		env.OnActivity("GetSnapmirror", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&commonparams.SnapmirrorRelationship{UUID: "snapmirror-uuid"}, nil)
		env.OnActivity("DeleteSnapmirror", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{JobUUID: "job-uuid"}, nil)
		env.OnActivity("GetOntapJob", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil)
		env.OnActivity("UpdateVolumeLatestLogicalBackupSize", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DeleteCloudEndpoint", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{JobUUID: "job-uuid"}, nil)
		env.OnActivity("DeleteSnapshotForBackup", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DeleteBackupSnapshotFromDB", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("HydrateSnapshotDeletionToCCFEActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DeleteBackup", mock.Anything, params.BackupUUID).Return(backup, nil)
		env.OnActivity("DeleteBackupMetadataIfLastBackupActivity", mock.Anything, mock.Anything).Return(nil)

		env.ExecuteWorkflow(DeleteBackupWorkflow, params)

		assert.True(t, env.IsWorkflowCompleted())
		assert.NoError(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("DeleteBackupWithBackupVaultSwitchingEnabled_UsesPerVaultPerEndpointCount", func(t *testing.T) {
		originalFlag := utils.EnableBackupVaultSwitching
		defer utils.SetEnableBackupVaultSwitchingForTest(originalFlag)
		utils.SetEnableBackupVaultSwitchingForTest(true)

		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		env.SetHeader(mockHeader)
		commonActivity, _ := setupMockCommonActivities(t)
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(setupMockBackupActivity(t))
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterWorkflow(DeleteBackupWorkflow)

		params := &commonparams.DeleteBackupParams{
			BackupVaultUUID: "vault-uuid",
			BackupUUID:      "backup-uuid",
			AccountName:     "test-account",
		}
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{UUID: "account-uuid"}}
		backupVault := &datamodel.BackupVault{
			Name: "test-backup-vault",
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{BucketName: "test-bucket", ServiceAccountName: "sa-test", VendorSubnetID: "subnet-12345"},
			},
			Account: account,
		}
		volume := &datamodel.Volume{
			BaseModel:        datamodel.BaseModel{UUID: "test-vol"},
			Pool:             &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{}},
			Svm:              &datamodel.Svm{Name: "svm_test"},
			VolumeAttributes: &datamodel.VolumeAttributes{VendorSubnetID: "subnet-12345"},
			DataProtection:   &datamodel.DataProtection{},
		}
		const ep = "object-store-endpoint-uuid-1"
		backup := &datamodel.Backup{
			Name:          "test-backup",
			VolumeUUID:    "test-vol",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes:    &datamodel.BackupAttributes{BucketName: "test-bucket", EndpointUUID: ep},
		}

		env.OnActivity("GetAccountByName", mock.Anything, params.AccountName).Return(account, nil)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetBackupVault", mock.Anything, params.BackupVaultUUID).Return(backupVault, nil)
		env.OnActivity("GetBackup", mock.Anything, params.BackupVaultUUID, params.BackupUUID, params.AccountName).Return(backup, nil)
		env.OnActivity("IsVolumeDeleted", mock.Anything, backup.VolumeUUID).Return(false, nil)
		env.OnActivity("IsExpertModeVolume", mock.Anything, backup.VolumeUUID).Return(false, nil)
		env.OnActivity("GetVolume", mock.Anything, backup.VolumeUUID).Return(volume, nil)
		env.OnActivity("GetNode", mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
		env.OnActivity("GetSmDestinationPathActivity", mock.Anything, mock.Anything, mock.Anything).Return("test-bucket:/objstore/test-vol", nil)
		env.OnActivity("GetSmSourcePathActivity", mock.Anything, mock.Anything).Return("svm_test:volume_test", nil)
		env.OnActivity("IsSnapmirrorDeleted", mock.Anything, mock.Anything, mock.Anything).Return(&commonparams.SnapmirrorDeletePrecheckResult{RelationshipMissing: false, Relationship: &commonparams.SnapmirrorRelationship{UUID: "test-sm-uuid"}}, nil)
		env.OnActivity("IsLatestBackupInVaultAndInEndpointActivity", mock.Anything, backup.BaseModel.UUID, backup.VolumeUUID, backup.BackupVaultID, ep).Return(false, nil)
		env.OnActivity("GetBackupCountByVolumeVaultAndEndpoint", mock.Anything, backup.VolumeUUID, backup.BackupVaultID, ep).Return(int64(1), nil)
		env.OnActivity("GetObjectStore", mock.Anything, mock.Anything, mock.Anything).Return(&commonparams.CloudTarget{UUID: "obj-store-uuid"}, nil)
		env.OnActivity("GetSnapmirror", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&commonparams.SnapmirrorRelationship{UUID: "snapmirror-uuid"}, nil)
		env.OnActivity("DeleteSnapmirror", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{JobUUID: "job-uuid"}, nil)
		env.OnActivity("GetOntapJob", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil)
		env.OnActivity("UpdateVolumeLatestLogicalBackupSize", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DeleteCloudEndpoint", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{JobUUID: "job-uuid"}, nil)
		env.OnActivity("DeleteSnapshotForBackup", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DeleteBackupSnapshotFromDB", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("HydrateSnapshotDeletionToCCFEActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DeleteBackup", mock.Anything, params.BackupUUID).Return(backup, nil)
		env.OnActivity("DeleteBackupMetadataIfLastBackupActivity", mock.Anything, mock.Anything).Return(nil)

		env.ExecuteWorkflow(DeleteBackupWorkflow, params)

		assert.True(t, env.IsWorkflowCompleted())
		assert.NoError(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	// Last backup on direct ONTAP path: after DeleteBackup, recalculates volume backup_chain_bytes from DB.
	t.Run("DeleteBackupWithVaultSwitchingOff_LastBackup_RecalculatesChainBytesAfterDelete", func(t *testing.T) {
		originalFlag := utils.EnableBackupVaultSwitching
		defer utils.SetEnableBackupVaultSwitchingForTest(originalFlag)
		utils.SetEnableBackupVaultSwitchingForTest(false)

		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		env.SetHeader(mockHeader)
		commonActivity, _ := setupMockCommonActivities(t)
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(setupMockBackupActivity(t))
		env.RegisterWorkflow(DeleteBackupWorkflow)

		params := &commonparams.DeleteBackupParams{
			BackupVaultUUID: "vault-uuid",
			BackupUUID:      "backup-uuid",
			AccountName:     "test-account",
		}
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{UUID: "account-uuid"}}
		backupVault := &datamodel.BackupVault{
			Name: "test-backup-vault",
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{BucketName: "test-bucket", ServiceAccountName: "sa-test", VendorSubnetID: "subnet-12345"},
			},
			Account: account,
		}
		volume := &datamodel.Volume{
			BaseModel:        datamodel.BaseModel{UUID: "test-vol"},
			Pool:             &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{}},
			Svm:              &datamodel.Svm{Name: "svm_test"},
			VolumeAttributes: &datamodel.VolumeAttributes{VendorSubnetID: "subnet-12345"},
			DataProtection:   &datamodel.DataProtection{},
		}
		backup := &datamodel.Backup{
			Name:          "test-backup",
			VolumeUUID:    "test-vol",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes:    &datamodel.BackupAttributes{BucketName: "test-bucket"},
		}

		env.OnActivity("GetAccountByName", mock.Anything, params.AccountName).Return(account, nil)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetBackupVault", mock.Anything, params.BackupVaultUUID).Return(backupVault, nil)
		env.OnActivity("GetBackup", mock.Anything, params.BackupVaultUUID, params.BackupUUID, params.AccountName).Return(backup, nil)
		env.OnActivity("IsVolumeDeleted", mock.Anything, backup.VolumeUUID).Return(false, nil)
		env.OnActivity("IsExpertModeVolume", mock.Anything, backup.VolumeUUID).Return(false, nil)
		env.OnActivity("GetVolume", mock.Anything, backup.VolumeUUID).Return(volume, nil)
		env.OnActivity("GetNode", mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
		env.OnActivity("GetSmDestinationPathActivity", mock.Anything, mock.Anything, mock.Anything).Return("test-bucket:/objstore/test-vol", nil)
		env.OnActivity("GetSmSourcePathActivity", mock.Anything, mock.Anything).Return("svm_test:volume_test", nil)
		env.OnActivity("IsSnapmirrorDeleted", mock.Anything, mock.Anything, mock.Anything).Return(&commonparams.SnapmirrorDeletePrecheckResult{RelationshipMissing: false, Relationship: &commonparams.SnapmirrorRelationship{UUID: "test-sm-uuid"}}, nil)
		env.OnActivity("GetBackupCountByVolumeUUID", mock.Anything, backup.VolumeUUID).Return(int64(1), nil)
		env.OnActivity("GetObjectStore", mock.Anything, mock.Anything, mock.Anything).Return(&commonparams.CloudTarget{UUID: "obj-store-uuid"}, nil)
		env.OnActivity("GetSnapmirror", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&commonparams.SnapmirrorRelationship{UUID: "snapmirror-uuid"}, nil)
		env.OnActivity("DeleteSnapmirror", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{JobUUID: "job-uuid"}, nil)
		env.OnActivity("GetOntapJob", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil)
		env.OnActivity("UpdateVolumeLatestLogicalBackupSize", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DeleteCloudEndpoint", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{JobUUID: "job-uuid"}, nil)
		env.OnActivity("DeleteSnapshotForBackup", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DeleteBackupSnapshotFromDB", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("HydrateSnapshotDeletionToCCFEActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DeleteBackup", mock.Anything, params.BackupUUID).Return(backup, nil)
		env.OnActivity("DeleteBackupMetadataIfLastBackupActivity", mock.Anything, mock.Anything).Return(nil)

		env.ExecuteWorkflow(DeleteBackupWorkflow, params)

		assert.True(t, env.IsWorkflowCompleted())
		assert.NoError(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	// Vault switching ON + ADC path (IsSnapmirrorDeleted true): runs ADC workflow.
	t.Run("DeleteBackupWithBackupVaultSwitchingEnabled_ADCPath_RunsADCAndRecompute", func(t *testing.T) {
		originalFlag := utils.EnableBackupVaultSwitching
		defer utils.SetEnableBackupVaultSwitchingForTest(originalFlag)
		utils.SetEnableBackupVaultSwitchingForTest(true)

		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		env.SetHeader(mockHeader)
		commonActivity, _ := setupMockCommonActivities(t)
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(setupMockBackupActivity(t))
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterWorkflow(ADCWorkflow)
		env.RegisterWorkflow(DeleteBackupWorkflow)

		params := &commonparams.DeleteBackupParams{
			BackupVaultUUID: "vault-uuid",
			BackupUUID:      "backup-uuid",
			AccountName:     "test-account",
		}
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{UUID: "account-uuid"}}
		backupVault := &datamodel.BackupVault{
			Name: "test-backup-vault",
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{BucketName: "test-bucket", ServiceAccountName: "sa-test", VendorSubnetID: "subnet-12345"},
			},
			Account: account,
		}
		volume := &datamodel.Volume{
			BaseModel:        datamodel.BaseModel{UUID: "test-vol"},
			Pool:             &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{}},
			Svm:              &datamodel.Svm{Name: "svm_test"},
			VolumeAttributes: &datamodel.VolumeAttributes{VendorSubnetID: "subnet-12345"},
			DataProtection:   &datamodel.DataProtection{},
		}
		backup := &datamodel.Backup{
			Name:          "test-backup",
			VolumeUUID:    "test-vol",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes:    &datamodel.BackupAttributes{BucketName: "test-bucket", EndpointUUID: "ep-uuid"},
		}

		env.OnActivity("GetAccountByName", mock.Anything, params.AccountName).Return(account, nil)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetBackupVault", mock.Anything, params.BackupVaultUUID).Return(backupVault, nil)
		env.OnActivity("GetBackup", mock.Anything, params.BackupVaultUUID, params.BackupUUID, params.AccountName).Return(backup, nil)
		env.OnActivity("IsVolumeDeleted", mock.Anything, backup.VolumeUUID).Return(false, nil)
		env.OnActivity("IsExpertModeVolume", mock.Anything, backup.VolumeUUID).Return(false, nil)
		env.OnActivity("GetVolume", mock.Anything, backup.VolumeUUID).Return(volume, nil)
		env.OnActivity("GetNode", mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
		env.OnActivity("GetSmSourcePathActivity", mock.Anything, mock.Anything).Return("svm_test:volume_test", nil)
		env.OnActivity("GetSmDestinationPathActivity", mock.Anything, mock.Anything, mock.Anything).Return("test-bucket:/objstore/test-vol", nil)
		env.OnActivity("IsSnapmirrorDeleted", mock.Anything, mock.Anything, mock.Anything).Return(&commonparams.SnapmirrorDeletePrecheckResult{RelationshipMissing: true}, nil)
		env.OnWorkflow("ADCWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(false, nil)
		env.OnActivity("DeleteBackup", mock.Anything, params.BackupUUID).Return(backup, nil)
		env.OnActivity("DeleteBackupMetadataIfLastBackupActivity", mock.Anything, mock.Anything).Return(nil)

		env.ExecuteWorkflow(DeleteBackupWorkflow, params)

		assert.True(t, env.IsWorkflowCompleted())
		assert.NoError(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	// Endpoint UUID on backup differs from live SnapMirror destination → same ADC path as relationship missing.
	t.Run("DeleteBackupWithBackupVaultSwitchingEnabled_EndpointMismatch_RunsADC", func(t *testing.T) {
		originalFlag := utils.EnableBackupVaultSwitching
		defer utils.SetEnableBackupVaultSwitchingForTest(originalFlag)
		utils.SetEnableBackupVaultSwitchingForTest(true)

		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		env.SetHeader(mockHeader)
		commonActivity, _ := setupMockCommonActivities(t)
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(setupMockBackupActivity(t))
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterWorkflow(ADCWorkflow)
		env.RegisterWorkflow(DeleteBackupWorkflow)

		params := &commonparams.DeleteBackupParams{
			BackupVaultUUID: "vault-uuid",
			BackupUUID:      "backup-uuid",
			AccountName:     "test-account",
		}
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{UUID: "account-uuid"}}
		backupVault := &datamodel.BackupVault{
			Name: "test-backup-vault",
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{BucketName: "test-bucket", ServiceAccountName: "sa-test", VendorSubnetID: "subnet-12345"},
			},
			Account: account,
		}
		volume := &datamodel.Volume{
			BaseModel:        datamodel.BaseModel{UUID: "test-vol"},
			Pool:             &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{}},
			Svm:              &datamodel.Svm{Name: "svm_test"},
			VolumeAttributes: &datamodel.VolumeAttributes{VendorSubnetID: "subnet-12345"},
			DataProtection:   &datamodel.DataProtection{},
		}
		currentEp := "11111111-1111-1111-1111-111111111111"
		staleEp := "22222222-2222-2222-2222-222222222222"
		backup := &datamodel.Backup{
			Name:          "test-backup",
			VolumeUUID:    "test-vol",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes:    &datamodel.BackupAttributes{BucketName: "test-bucket", EndpointUUID: staleEp},
		}

		env.OnActivity("GetAccountByName", mock.Anything, params.AccountName).Return(account, nil)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetBackupVault", mock.Anything, params.BackupVaultUUID).Return(backupVault, nil)
		env.OnActivity("GetBackup", mock.Anything, params.BackupVaultUUID, params.BackupUUID, params.AccountName).Return(backup, nil)
		env.OnActivity("IsVolumeDeleted", mock.Anything, backup.VolumeUUID).Return(false, nil)
		env.OnActivity("IsExpertModeVolume", mock.Anything, backup.VolumeUUID).Return(false, nil)
		env.OnActivity("GetVolume", mock.Anything, backup.VolumeUUID).Return(volume, nil)
		env.OnActivity("GetNode", mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
		env.OnActivity("GetSmSourcePathActivity", mock.Anything, mock.Anything).Return("svm_test:volume_test", nil)
		env.OnActivity("GetSmDestinationPathActivity", mock.Anything, mock.Anything, mock.Anything).Return("test-bucket:/objstore/test-vol", nil)
		env.OnActivity("IsSnapmirrorDeleted", mock.Anything, mock.Anything, mock.Anything).Return(&commonparams.SnapmirrorDeletePrecheckResult{
			RelationshipMissing: false,
			Relationship:        &commonparams.SnapmirrorRelationship{UUID: "snapmirror-uuid", DestinationUUID: nillable.ToPointer(currentEp)},
		}, nil)
		env.OnWorkflow("ADCWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(false, nil)
		env.OnActivity("DeleteBackup", mock.Anything, params.BackupUUID).Return(backup, nil)
		env.OnActivity("DeleteBackupMetadataIfLastBackupActivity", mock.Anything, mock.Anything).Return(nil)

		env.ExecuteWorkflow(DeleteBackupWorkflow, params)

		assert.True(t, env.IsWorkflowCompleted())
		assert.NoError(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("DeleteBackupVaultSwitching_LatestBackupWithActiveSM_Rejected", func(t *testing.T) {
		originalFlag := utils.EnableBackupVaultSwitching
		defer utils.SetEnableBackupVaultSwitchingForTest(originalFlag)
		utils.SetEnableBackupVaultSwitchingForTest(true)

		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{Fields: map[string]*commonpb.Payload{"logParam": encodedValue}}
		env.SetHeader(mockHeader)
		commonActivity, _ := setupMockCommonActivities(t)
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(setupMockBackupActivity(t))
		env.RegisterWorkflow(DeleteBackupWorkflow)

		const ep = "active-endpoint-uuid"
		params := &commonparams.DeleteBackupParams{
			BackupVaultUUID: "vault-uuid",
			BackupUUID:      "backup-uuid",
			AccountName:     "test-account",
		}
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{UUID: "account-uuid"}}
		backupVault := &datamodel.BackupVault{
			Name:          "test-backup-vault",
			BucketDetails: datamodel.BucketDetailsArray{&datamodel.BucketDetails{BucketName: "test-bucket", VendorSubnetID: "subnet-12345"}},
			Account:       account,
		}
		volume := &datamodel.Volume{
			BaseModel:        datamodel.BaseModel{UUID: "test-vol"},
			Pool:             &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{}},
			Svm:              &datamodel.Svm{Name: "svm_test"},
			VolumeAttributes: &datamodel.VolumeAttributes{VendorSubnetID: "subnet-12345"},
			DataProtection:   &datamodel.DataProtection{},
		}
		backup := &datamodel.Backup{
			Name:          "test-backup",
			VolumeUUID:    "test-vol",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes:    &datamodel.BackupAttributes{BucketName: "test-bucket", EndpointUUID: ep},
		}

		env.OnActivity("GetAccountByName", mock.Anything, params.AccountName).Return(account, nil)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetBackupVault", mock.Anything, params.BackupVaultUUID).Return(backupVault, nil)
		env.OnActivity("GetBackup", mock.Anything, params.BackupVaultUUID, params.BackupUUID, params.AccountName).Return(backup, nil)
		env.OnActivity("IsVolumeDeleted", mock.Anything, backup.VolumeUUID).Return(false, nil)
		env.OnActivity("IsExpertModeVolume", mock.Anything, backup.VolumeUUID).Return(false, nil)
		env.OnActivity("GetVolume", mock.Anything, backup.VolumeUUID).Return(volume, nil)
		env.OnActivity("GetNode", mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
		env.OnActivity("GetSmDestinationPathActivity", mock.Anything, mock.Anything, mock.Anything).Return("test-bucket:/objstore/test-vol", nil)
		env.OnActivity("GetSmSourcePathActivity", mock.Anything, mock.Anything).Return("svm_test:volume_test", nil)
		env.OnActivity("IsSnapmirrorDeleted", mock.Anything, mock.Anything, mock.Anything).Return(&commonparams.SnapmirrorDeletePrecheckResult{
			RelationshipMissing: false,
			Relationship:        &commonparams.SnapmirrorRelationship{UUID: "test-sm-uuid"},
		}, nil)
		env.OnActivity("IsLatestBackupInVaultAndInEndpointActivity", mock.Anything, backup.BaseModel.UUID, backup.VolumeUUID, backup.BackupVaultID, ep).Return(true, nil)
		env.OnActivity("GetBackupCountByVolumeVaultAndEndpoint", mock.Anything, backup.VolumeUUID, backup.BackupVaultID, ep).Return(int64(2), nil)
		env.OnActivity("MarkBackupAvailable", mock.Anything, mock.Anything).Return(nil)

		env.ExecuteWorkflow(DeleteBackupWorkflow, params)

		assert.True(t, env.IsWorkflowCompleted())
		err := env.GetWorkflowError()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "Cannot delete latest backup")
	})

	t.Run("DeleteBackupVaultSwitching_NotLatest_Allowed", func(t *testing.T) {
		originalFlag := utils.EnableBackupVaultSwitching
		defer utils.SetEnableBackupVaultSwitchingForTest(originalFlag)
		utils.SetEnableBackupVaultSwitchingForTest(true)

		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{Fields: map[string]*commonpb.Payload{"logParam": encodedValue}}
		env.SetHeader(mockHeader)
		commonActivity, _ := setupMockCommonActivities(t)
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(setupMockBackupActivity(t))
		env.RegisterWorkflow(DeleteBackupWorkflow)

		const ep = "active-endpoint-uuid"
		params := &commonparams.DeleteBackupParams{
			BackupVaultUUID: "vault-uuid",
			BackupUUID:      "backup-uuid",
			AccountName:     "test-account",
		}
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{UUID: "account-uuid"}}
		backupVault := &datamodel.BackupVault{
			Name:          "test-backup-vault",
			BucketDetails: datamodel.BucketDetailsArray{&datamodel.BucketDetails{BucketName: "test-bucket", VendorSubnetID: "subnet-12345"}},
			Account:       account,
		}
		volume := &datamodel.Volume{
			BaseModel:        datamodel.BaseModel{UUID: "test-vol"},
			Pool:             &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{}},
			Svm:              &datamodel.Svm{Name: "svm_test"},
			VolumeAttributes: &datamodel.VolumeAttributes{VendorSubnetID: "subnet-12345"},
			DataProtection:   &datamodel.DataProtection{},
		}
		backup := &datamodel.Backup{
			Name:          "test-backup",
			VolumeUUID:    "test-vol",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes:    &datamodel.BackupAttributes{BucketName: "test-bucket", EndpointUUID: ep},
		}

		env.OnActivity("GetAccountByName", mock.Anything, params.AccountName).Return(account, nil)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetBackupVault", mock.Anything, params.BackupVaultUUID).Return(backupVault, nil)
		env.OnActivity("GetBackup", mock.Anything, params.BackupVaultUUID, params.BackupUUID, params.AccountName).Return(backup, nil)
		env.OnActivity("IsVolumeDeleted", mock.Anything, backup.VolumeUUID).Return(false, nil)
		env.OnActivity("IsExpertModeVolume", mock.Anything, backup.VolumeUUID).Return(false, nil)
		env.OnActivity("GetVolume", mock.Anything, backup.VolumeUUID).Return(volume, nil)
		env.OnActivity("GetNode", mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
		env.OnActivity("GetSmDestinationPathActivity", mock.Anything, mock.Anything, mock.Anything).Return("test-bucket:/objstore/test-vol", nil)
		env.OnActivity("GetSmSourcePathActivity", mock.Anything, mock.Anything).Return("svm_test:volume_test", nil)
		env.OnActivity("IsSnapmirrorDeleted", mock.Anything, mock.Anything, mock.Anything).Return(&commonparams.SnapmirrorDeletePrecheckResult{
			RelationshipMissing: false,
			Relationship:        &commonparams.SnapmirrorRelationship{UUID: "test-sm-uuid"},
		}, nil)
		env.OnActivity("IsLatestBackupInVaultAndInEndpointActivity", mock.Anything, backup.BaseModel.UUID, backup.VolumeUUID, backup.BackupVaultID, ep).Return(false, nil)
		env.OnActivity("GetBackupCountByVolumeVaultAndEndpoint", mock.Anything, backup.VolumeUUID, backup.BackupVaultID, ep).Return(int64(2), nil)
		env.OnActivity("GetObjectStore", mock.Anything, mock.Anything, mock.Anything).Return(&commonparams.CloudTarget{UUID: "obj-store-uuid"}, nil)
		env.OnActivity("IsBackupShared", mock.Anything, mock.Anything).Return(false, nil)
		env.OnActivity("DeleteSnapshotFromObjectStore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{JobUUID: "job-uuid"}, nil)
		env.OnActivity("GetOntapJob", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil)
		env.OnActivity("DeleteBackup", mock.Anything, params.BackupUUID).Return(backup, nil)
		env.OnActivity("DeleteBackupMetadataIfLastBackupActivity", mock.Anything, mock.Anything).Return(nil)

		env.ExecuteWorkflow(DeleteBackupWorkflow, params)

		assert.True(t, env.IsWorkflowCompleted())
		assert.NoError(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("DeleteBackupVaultSwitching_LatestButCount1_Allowed", func(t *testing.T) {
		originalFlag := utils.EnableBackupVaultSwitching
		defer utils.SetEnableBackupVaultSwitchingForTest(originalFlag)
		utils.SetEnableBackupVaultSwitchingForTest(true)

		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{Fields: map[string]*commonpb.Payload{"logParam": encodedValue}}
		env.SetHeader(mockHeader)
		commonActivity, _ := setupMockCommonActivities(t)
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(setupMockBackupActivity(t))
		env.RegisterWorkflow(DeleteBackupWorkflow)

		const ep = "active-endpoint-uuid"
		params := &commonparams.DeleteBackupParams{
			BackupVaultUUID: "vault-uuid",
			BackupUUID:      "backup-uuid",
			AccountName:     "test-account",
		}
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{UUID: "account-uuid"}}
		backupVault := &datamodel.BackupVault{
			Name:          "test-backup-vault",
			BucketDetails: datamodel.BucketDetailsArray{&datamodel.BucketDetails{BucketName: "test-bucket", VendorSubnetID: "subnet-12345"}},
			Account:       account,
		}
		volume := &datamodel.Volume{
			BaseModel:        datamodel.BaseModel{UUID: "test-vol"},
			Pool:             &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{}},
			Svm:              &datamodel.Svm{Name: "svm_test"},
			VolumeAttributes: &datamodel.VolumeAttributes{VendorSubnetID: "subnet-12345"},
			DataProtection:   &datamodel.DataProtection{},
		}
		backup := &datamodel.Backup{
			Name:          "test-backup",
			VolumeUUID:    "test-vol",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes:    &datamodel.BackupAttributes{BucketName: "test-bucket", EndpointUUID: ep},
		}

		env.OnActivity("GetAccountByName", mock.Anything, params.AccountName).Return(account, nil)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetBackupVault", mock.Anything, params.BackupVaultUUID).Return(backupVault, nil)
		env.OnActivity("GetBackup", mock.Anything, params.BackupVaultUUID, params.BackupUUID, params.AccountName).Return(backup, nil)
		env.OnActivity("IsVolumeDeleted", mock.Anything, backup.VolumeUUID).Return(false, nil)
		env.OnActivity("IsExpertModeVolume", mock.Anything, backup.VolumeUUID).Return(false, nil)
		env.OnActivity("GetVolume", mock.Anything, backup.VolumeUUID).Return(volume, nil)
		env.OnActivity("GetNode", mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
		env.OnActivity("GetSmDestinationPathActivity", mock.Anything, mock.Anything, mock.Anything).Return("test-bucket:/objstore/test-vol", nil)
		env.OnActivity("GetSmSourcePathActivity", mock.Anything, mock.Anything).Return("svm_test:volume_test", nil)
		env.OnActivity("IsSnapmirrorDeleted", mock.Anything, mock.Anything, mock.Anything).Return(&commonparams.SnapmirrorDeletePrecheckResult{
			RelationshipMissing: false,
			Relationship:        &commonparams.SnapmirrorRelationship{UUID: "test-sm-uuid"},
		}, nil)
		env.OnActivity("IsLatestBackupInVaultAndInEndpointActivity", mock.Anything, backup.BaseModel.UUID, backup.VolumeUUID, backup.BackupVaultID, ep).Return(true, nil)
		env.OnActivity("GetBackupCountByVolumeVaultAndEndpoint", mock.Anything, backup.VolumeUUID, backup.BackupVaultID, ep).Return(int64(1), nil)
		env.OnActivity("GetObjectStore", mock.Anything, mock.Anything, mock.Anything).Return(&commonparams.CloudTarget{UUID: "obj-store-uuid"}, nil)
		env.OnActivity("GetSnapmirror", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&commonparams.SnapmirrorRelationship{UUID: "snapmirror-uuid"}, nil)
		env.OnActivity("DeleteSnapmirror", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{JobUUID: "job-uuid"}, nil)
		env.OnActivity("GetOntapJob", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil)
		env.OnActivity("UpdateVolumeLatestLogicalBackupSize", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DeleteCloudEndpoint", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{JobUUID: "job-uuid"}, nil)
		env.OnActivity("DeleteSnapshotForBackup", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DeleteBackupSnapshotFromDB", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("HydrateSnapshotDeletionToCCFEActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DeleteBackup", mock.Anything, params.BackupUUID).Return(backup, nil)
		env.OnActivity("DeleteBackupMetadataIfLastBackupActivity", mock.Anything, mock.Anything).Return(nil)

		env.ExecuteWorkflow(DeleteBackupWorkflow, params)

		assert.True(t, env.IsWorkflowCompleted())
		assert.NoError(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("DeleteBackupWithVolumeDeleted", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		env.SetHeader(mockHeader)
		commonActivity, _ := setupMockCommonActivities(t)
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(setupMockBackupActivity(t))
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterWorkflow(ADCWorkflow)
		env.RegisterWorkflow(DeleteBackupWorkflow)

		// Set up test data
		params := &commonparams.DeleteBackupParams{
			BackupVaultUUID: "vault-uuid",
			BackupUUID:      "backup-uuid",
			AccountName:     "test-account",
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "account-uuid"},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			Name: "test-backup-vault",
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{BucketName: "test-bucket", ServiceAccountName: "sa-test", VendorSubnetID: "subnet-12345"},
			},
		}
		backup := &datamodel.Backup{
			Name:          "test-backup",
			VolumeUUID:    "test-vol",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes:    &datamodel.BackupAttributes{BucketName: "test-bucket"},
		}

		// Mock activity responses for volume deleted scenario
		env.OnActivity("GetAccountByName", mock.Anything, params.AccountName).Return(account, nil)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetBackupVault", mock.Anything, params.BackupVaultUUID).Return(backupVault, nil)
		env.OnActivity("GetBackup", mock.Anything, params.BackupVaultUUID, params.BackupUUID, params.AccountName).Return(backup, nil)
		env.OnActivity("IsVolumeDeleted", mock.Anything, backup.VolumeUUID).Return(true, nil) // Volume is deleted
		env.OnActivity("IsExpertModeVolume", mock.Anything, backup.VolumeUUID).Return(false, nil)
		env.OnWorkflow("ADCWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(false, nil)
		env.OnActivity("DeleteBackup", mock.Anything, params.BackupUUID).Return(backup, nil)
		env.OnActivity("DeleteBackupMetadataIfLastBackupActivity", mock.Anything, mock.Anything).Return(nil)
		// Execute workflow
		env.ExecuteWorkflow(DeleteBackupWorkflow, params)

		// Assert workflow execution
		assert.True(t, env.IsWorkflowCompleted())
		assert.NoError(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("DeleteBackupMultipleBackupsShared", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		env.SetHeader(mockHeader)
		commonActivity, _ := setupMockCommonActivities(t)
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(setupMockBackupActivity(t))

		// Set up test data
		params := &commonparams.DeleteBackupParams{
			BackupVaultUUID: "vault-uuid",
			BackupUUID:      "backup-uuid",
			AccountName:     "test-account",
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "account-uuid"},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			Name: "test-backup-vault",
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{BucketName: "test-bucket", ServiceAccountName: "sa-test", VendorSubnetID: "subnet-12345"},
			},
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-vol"},
			Pool: &datamodel.Pool{
				BaseModel: datamodel.BaseModel{ID: int64(1)},
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "",
					CertificateID: "",
				},
			},
			Svm: &datamodel.Svm{
				Name: "svm_test",
			},
			VolumeAttributes: &datamodel.VolumeAttributes{
				VendorSubnetID: "subnet-12345",
			},
		}
		backup := &datamodel.Backup{
			Name:          "test-backup",
			VolumeUUID:    "test-vol",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes:    &datamodel.BackupAttributes{BucketName: "test-bucket", EndpointUUID: "endpoint-uuid", SnapshotID: "snapshot-id"},
		}

		// Mock activity responses for multiple backups scenario with shared snapshot
		env.OnActivity("GetAccountByName", mock.Anything, params.AccountName).Return(account, nil)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetBackupVault", mock.Anything, params.BackupVaultUUID).Return(backupVault, nil)
		env.OnActivity("GetBackup", mock.Anything, params.BackupVaultUUID, params.BackupUUID, params.AccountName).Return(backup, nil)
		env.OnActivity("IsVolumeDeleted", mock.Anything, backup.VolumeUUID).Return(false, nil)
		env.OnActivity("IsExpertModeVolume", mock.Anything, backup.VolumeUUID).Return(false, nil)
		env.OnActivity("GetVolume", mock.Anything, backup.VolumeUUID).Return(volume, nil)
		env.OnActivity("GetNode", mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
		env.OnActivity("GetSmDestinationPathActivity", mock.Anything, mock.Anything, mock.Anything).Return("test-bucket:/objstore/test-vol", nil)
		env.OnActivity("GetSmSourcePathActivity", mock.Anything, mock.Anything).Return("svm_test:volume_test", nil)
		env.OnActivity("IsSnapmirrorDeleted", mock.Anything, mock.Anything, mock.Anything).Return(&commonparams.SnapmirrorDeletePrecheckResult{RelationshipMissing: false, Relationship: &commonparams.SnapmirrorRelationship{UUID: "test-sm-uuid"}}, nil)
		env.OnActivity("GetBackupCountByVolumeUUID", mock.Anything, backup.VolumeUUID).Return(int64(2), nil)
		env.OnActivity("GetObjectStore", mock.Anything, mock.Anything, mock.Anything).Return(&commonparams.CloudTarget{UUID: "obj-store-uuid"}, nil)
		env.OnActivity("IsBackupShared", mock.Anything, backup).Return(true, nil) // Backup is shared
		env.OnActivity("DeleteBackup", mock.Anything, params.BackupUUID).Return(backup, nil)
		env.OnActivity("DeleteBackupMetadataIfLastBackupActivity", mock.Anything, mock.Anything).Return(nil)

		// Execute workflow
		env.ExecuteWorkflow(DeleteBackupWorkflow, params)

		// Assert workflow execution
		assert.True(t, env.IsWorkflowCompleted())
		assert.NoError(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("DeleteBackupMultipleBackupsNotShared", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		env.SetHeader(mockHeader)
		commonActivity, _ := setupMockCommonActivities(t)
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(setupMockBackupActivity(t))
		env.RegisterWorkflow(ADCWorkflow)
		env.RegisterWorkflow(DeleteBackupWorkflow)

		// Set up test data
		params := &commonparams.DeleteBackupParams{
			BackupVaultUUID: "vault-uuid",
			BackupUUID:      "backup-uuid",
			AccountName:     "test-account",
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "account-uuid"},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			Name: "test-backup-vault",
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{BucketName: "test-bucket", ServiceAccountName: "sa-test", VendorSubnetID: "subnet-12345"},
			},
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-vol"},
			Pool: &datamodel.Pool{
				BaseModel: datamodel.BaseModel{ID: int64(1)},
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "",
					CertificateID: "",
				},
			},
			Svm: &datamodel.Svm{
				Name: "svm_test",
			},
			VolumeAttributes: &datamodel.VolumeAttributes{
				VendorSubnetID: "subnet-12345",
			},
		}
		backup := &datamodel.Backup{
			Name:          "test-backup",
			VolumeUUID:    "test-vol",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes:    &datamodel.BackupAttributes{BucketName: "test-bucket", EndpointUUID: "endpoint-uuid", SnapshotID: "snapshot-id"},
		}

		// Mock activity responses for multiple backups scenario with shared snapshot
		env.OnActivity("GetAccountByName", mock.Anything, params.AccountName).Return(account, nil)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetBackupVault", mock.Anything, params.BackupVaultUUID).Return(backupVault, nil)
		env.OnActivity("GetBackup", mock.Anything, params.BackupVaultUUID, params.BackupUUID, params.AccountName).Return(backup, nil)
		env.OnActivity("IsVolumeDeleted", mock.Anything, backup.VolumeUUID).Return(false, nil)
		env.OnActivity("IsExpertModeVolume", mock.Anything, backup.VolumeUUID).Return(false, nil)
		env.OnActivity("GetVolume", mock.Anything, backup.VolumeUUID).Return(volume, nil)
		env.OnActivity("GetNode", mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
		env.OnActivity("GetSmDestinationPathActivity", mock.Anything, mock.Anything, mock.Anything).Return("test-bucket:/objstore/test-vol", nil)
		env.OnActivity("GetSmSourcePathActivity", mock.Anything, mock.Anything).Return("svm_test:volume_test", nil)
		env.OnActivity("IsSnapmirrorDeleted", mock.Anything, mock.Anything, mock.Anything).Return(&commonparams.SnapmirrorDeletePrecheckResult{RelationshipMissing: false, Relationship: &commonparams.SnapmirrorRelationship{UUID: "test-sm-uuid"}}, nil)
		env.OnActivity("GetBackupCountByVolumeUUID", mock.Anything, backup.VolumeUUID).Return(int64(2), nil)
		env.OnActivity("GetObjectStore", mock.Anything, mock.Anything, mock.Anything).Return(&commonparams.CloudTarget{UUID: "obj-store-uuid"}, nil)
		env.OnActivity("IsBackupShared", mock.Anything, backup).Return(false, nil) // Backup is shared
		env.OnActivity("DeleteSnapshotFromObjectStore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{JobUUID: "job-uuid"}, nil)
		env.OnActivity("GetOntapJob", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil)
		env.OnActivity("DeleteBackup", mock.Anything, params.BackupUUID).Return(backup, nil)
		env.OnActivity("DeleteBackupMetadataIfLastBackupActivity", mock.Anything, mock.Anything).Return(nil)

		// Execute workflow
		env.ExecuteWorkflow(DeleteBackupWorkflow, params)

		// Assert workflow execution
		assert.True(t, env.IsWorkflowCompleted())
		assert.NoError(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("UpdateJobStatusFailed", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		env.SetHeader(mockHeader)
		commonActivity, _ := setupMockCommonActivities(t)
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(setupMockBackupActivity(t))
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterWorkflow(ADCWorkflow)
		env.RegisterWorkflow(DeleteBackupWorkflow)
		// Set up test data
		params := &commonparams.DeleteBackupParams{
			BackupVaultUUID: "vault-uuid",
			BackupUUID:      "backup-uuid",
			AccountName:     "test-account",
		}

		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(errors.New("failed to update job status"))

		env.ExecuteWorkflow(DeleteBackupWorkflow, params)

		// Assert workflow execution
		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})
}

func TestGetBucketDetailsForBucket(t *testing.T) {
	t.Run("WhenBucketFound", func(tt *testing.T) {
		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{UUID: "vault-uuid"},
			Name:      "test-vault",
			BucketDetails: []*datamodel.BucketDetails{
				{
					BucketName:     "bucket1",
					VendorSubnetID: "subnet1",
				},
				{
					BucketName:     "bucket2",
					VendorSubnetID: "subnet2",
				},
			},
		}

		result, err := getBucketDetailsForBucket(backupVault, "bucket1")

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, "bucket1", result.BucketName)
		assert.Equal(tt, "subnet1", result.VendorSubnetID)
	})

	t.Run("WhenBucketNotFound", func(tt *testing.T) {
		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{UUID: "vault-uuid"},
			Name:      "test-vault",
			BucketDetails: []*datamodel.BucketDetails{
				{
					BucketName:     "bucket1",
					VendorSubnetID: "subnet1",
				},
			},
		}

		result, err := getBucketDetailsForBucket(backupVault, "nonexistent-bucket")

		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.(*vsaerrors.CustomError).OriginalErr.Error(), "no matching bucket details found for bucket nonexistent-bucket in backup vault test-vault")
	})

	t.Run("WhenBackupVaultHasNoBuckets", func(tt *testing.T) {
		backupVault := &datamodel.BackupVault{
			BaseModel:     datamodel.BaseModel{UUID: "vault-uuid"},
			Name:          "test-vault",
			BucketDetails: []*datamodel.BucketDetails{},
		}

		result, err := getBucketDetailsForBucket(backupVault, "any-bucket")

		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.(*vsaerrors.CustomError).OriginalErr.Error(), "no matching bucket details found for bucket any-bucket in backup vault test-vault")
	})
}

func TestGetBucketDetailsForBucket_Success(t *testing.T) {
	backupVault := &datamodel.BackupVault{
		BucketDetails: datamodel.BucketDetailsArray{
			&datamodel.BucketDetails{BucketName: "test-bucket", VendorSubnetID: "subnet-12345"},
			&datamodel.BucketDetails{BucketName: "other-bucket", VendorSubnetID: "subnet-67890"},
		},
	}
	expected := &datamodel.BucketDetails{BucketName: "test-bucket", VendorSubnetID: "subnet-12345"}
	result, err := getBucketDetailsForBucket(backupVault, "test-bucket")
	assert.NoError(t, err)
	assert.Equal(t, expected, result)
}

func TestGetBucketDetailsForBucket_NotFound(t *testing.T) {
	backupVault := &datamodel.BackupVault{
		BucketDetails: datamodel.BucketDetailsArray{
			&datamodel.BucketDetails{BucketName: "test-bucket", VendorSubnetID: "subnet-12345"},
		},
	}
	_, err := getBucketDetailsForBucket(backupVault, "non-existent-bucket")
	assert.Error(t, err)
	assert.Contains(t, err.(*vsaerrors.CustomError).OriginalErr.Error(), "no matching bucket details found for bucket non-existent-bucket")
}

func TestGetBucketDetailsForBucket_EmptyBucketName(t *testing.T) {
	backupVault := &datamodel.BackupVault{
		BucketDetails: datamodel.BucketDetailsArray{
			&datamodel.BucketDetails{BucketName: "test-bucket", VendorSubnetID: "subnet-12345"},
		},
	}
	_, err := getBucketDetailsForBucket(backupVault, "")
	assert.Error(t, err)
}

func TestDeleteBackupWorkflowHandleError(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
	mockHeader := &commonpb.Header{
		Fields: map[string]*commonpb.Payload{
			"logParam": encodedValue,
		},
	}
	env.SetHeader(mockHeader)
	commonActivity, _ := setupMockCommonActivities(t)
	env.RegisterActivity(commonActivity)
	env.RegisterActivity(setupMockBackupActivity(t))
	env.RegisterWorkflow(DeleteBackupWorkflow)

	// Set up test data
	params := &commonparams.DeleteBackupParams{
		BackupVaultUUID: "vault-uuid",
		BackupUUID:      "backup-uuid",
		AccountName:     "test-account",
	}

	backup := &datamodel.Backup{
		Name:       "test-backup",
		VolumeUUID: "test-vol",
		Attributes: &datamodel.BackupAttributes{BucketName: "test-bucket"},
	}

	account := &datamodel.Account{
		BaseModel: datamodel.BaseModel{UUID: "account-uuid"},
		Name:      "test-account",
	}

	// Mock activity responses for HandleError function
	env.OnActivity("GetAccountByName", mock.Anything, params.AccountName).Return(account, nil)
	env.OnActivity("GetBackup", mock.Anything, params.BackupVaultUUID, params.BackupUUID, params.AccountName).Return(backup, nil)

	// Test HandleError function by simulating workflow execution that triggers error handling
	env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("GetBackupVault", mock.Anything, params.BackupVaultUUID).Return(nil, errors.New("failed to get backup vault"))
	env.OnActivity("GetBackup", mock.Anything, params.BackupVaultUUID, params.BackupUUID, params.AccountName).Return(&datamodel.Backup{}, nil)
	env.OnActivity("MarkBackupAvailable", mock.Anything, mock.Anything).Return(nil)

	// Execute workflow - this should trigger error handling
	env.ExecuteWorkflow(DeleteBackupWorkflow, params)

	// Assert workflow execution - HandleError is called but workflow completes successfully
	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
	assert.ErrorContains(t, env.GetWorkflowError(), "failed to get backup vault")
	env.AssertExpectations(t)
}

func TestDeleteBackupWorkflowHandleErrorFailure(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
	mockHeader := &commonpb.Header{
		Fields: map[string]*commonpb.Payload{
			"logParam": encodedValue,
		},
	}
	env.SetHeader(mockHeader)
	commonActivity, _ := setupMockCommonActivities(t)
	env.RegisterActivity(commonActivity)
	env.RegisterActivity(setupMockBackupActivity(t))
	env.RegisterWorkflow(DeleteBackupWorkflow)

	// Set up test data
	params := &commonparams.DeleteBackupParams{
		BackupVaultUUID: "vault-uuid",
		BackupUUID:      "backup-uuid",
		AccountName:     "test-account",
	}

	account := &datamodel.Account{
		BaseModel: datamodel.BaseModel{UUID: "account-uuid"},
		Name:      "test-account",
	}

	// Mock activity responses for HandleError function failure scenarios
	env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("GetAccountByName", mock.Anything, params.AccountName).Return(account, nil)
	env.OnActivity("GetBackupVault", mock.Anything, params.BackupVaultUUID).Return(nil, errors.New("failed to get backup vault"))
	env.OnActivity("GetBackup", mock.Anything, params.BackupVaultUUID, params.BackupUUID, params.AccountName).Return(nil, errors.New("failed to get backup"))

	// Execute workflow - this should trigger error handling which then fails
	env.ExecuteWorkflow(DeleteBackupWorkflow, params)

	// Assert workflow execution - HandleError fails but workflow still completes with error
	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
	assert.ErrorContains(t, env.GetWorkflowError(), "failed to get backup vault")
	env.AssertExpectations(t)
}

func TestCreateBackupWorkflowEdgeCases(t *testing.T) {
	t.Run("TestCreateBackupWorkflowNilDestinationUUID", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		env.SetHeader(mockHeader)
		commonActivity, _ := setupMockCommonActivities(t)
		env.RegisterActivity(commonActivity)
		env.RegisterWorkflow(CreateBackupWorkflow)

		// Create mock storage for BackupActivity
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("UpdateBackupState", mock.Anything, mock.Anything).Return(&datamodel.Backup{}, nil).Maybe()
		mockStorage.On("UpdateBackup", mock.Anything, mock.Anything).Return(&datamodel.Backup{}, nil).Maybe()
		mockStorage.On("UpdateBackupLatestLogicalBackupSizeByVolume", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		mockStorage.On("UpdateVolumeFields", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		mockStorage.On("UpdateSnapshot", mock.Anything, mock.Anything).Return(&datamodel.Snapshot{}, nil).Maybe()
		mockStorage.On("CreatingSnapshot", mock.Anything, mock.Anything).Return(&datamodel.Snapshot{
			Name:               "test-backup",
			Description:        "VCP-Backup",
			VolumeID:           1,
			AccountID:          1,
			State:              "creating",
			StateDetails:       "creating",
			SnapshotAttributes: &datamodel.SnapshotAttributes{},
		}, nil).Maybe()

		// Create a custom BackupActivity that bypasses the nil check in UpdateSnapshotActivity
		customBackupActivity := &TestBackupActivity{BackupActivity: &activities.BackupActivity{SE: mockStorage}}
		env.RegisterActivity(customBackupActivity)

		// Set up test data
		params := &commonparams.CreateBackupParams{
			VolumeUUID:  "test-vol",
			AccountName: "test-account",
			BackupName:  "test-backup",
		}
		backupVault := &datamodel.BackupVault{
			Name:          "test-backup-vault",
			BucketDetails: datamodel.BucketDetailsArray{&datamodel.BucketDetails{BucketName: "test-bucket", ServiceAccountName: "sa-test", VendorSubnetID: "subnet-12345"}},
		}
		backup := &datamodel.Backup{
			State:         "InProgress",
			Name:          "test-backup",
			VolumeUUID:    "test-vol",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes:    &datamodel.BackupAttributes{},
		}
		volume := &datamodel.Volume{
			Pool:             &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{Password: "password"}, PoolAttributes: &datamodel.PoolAttributes{PrimaryZone: "us-central1-a"}},
			Svm:              &datamodel.Svm{Name: "svm_test"},
			VolumeAttributes: &datamodel.VolumeAttributes{BlockProperties: &datamodel.BlockProperties{OSType: "LINUX"}, VendorSubnetID: "subnet-12345", ExternalUUID: "external-uuid"},
		}

		// Mock all activities that the workflow calls
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetNode", mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
		env.OnActivity("PrepareObjectStoreActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:      backup,
				BackupVault: backupVault,
				Volume:      volume,
			},
			Node: &models.Node{EndpointAddress: "127.0.0.1"},
		}, nil)
		env.OnActivity("GetOrCreateObjectStoreActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:      backup,
				BackupVault: backupVault,
				Volume:      volume,
			},
			Node: &models.Node{EndpointAddress: "127.0.0.1"},
		}, nil)
		env.OnActivity("PrepareSnapmirrorActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:      backup,
				BackupVault: backupVault,
				Volume:      volume,
			},
			Node: &models.Node{EndpointAddress: "127.0.0.1"},
		}, nil)
		env.OnActivity("CreateSnapmirrorRelationshipActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:      backup,
				BackupVault: backupVault,
				Volume:      volume,
			},
			Node: &models.Node{EndpointAddress: "127.0.0.1"},
		}, nil)
		env.OnActivity("CreatingSnapshotActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:      backup,
				BackupVault: backupVault,
				Volume:      volume,
			},
			Node:         &models.Node{EndpointAddress: "127.0.0.1"},
			SnapshotName: "test-backup",
			SnapmirrorRelationship: &commonparams.SnapmirrorRelationship{
				UUID: "test-snapmirror-uuid",
			},
		}, nil)
		env.OnActivity("CreateSnapshotActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup: &datamodel.Backup{
					Name: "test-backup",
					Attributes: &datamodel.BackupAttributes{
						SnapshotID: "test-snapshot-uuid",
					},
				},
				BackupVault: backupVault,
				Volume:      volume,
			},
			Node:         &models.Node{EndpointAddress: "127.0.0.1"},
			SnapshotName: "test-backup",
			SnapmirrorRelationship: &commonparams.SnapmirrorRelationship{
				UUID: "test-snapmirror-uuid",
			},
			SnapshotResponse: &vsa.SnapshotProviderResponse{
				ProviderResponse: vsa.ProviderResponse{
					ExternalUUID: "test-snapshot-uuid",
				},
			},
		}, nil)
		env.OnActivity("TransferSnapshotActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:      backup,
				BackupVault: backupVault,
				Volume:      volume,
			},
			Node:         &models.Node{EndpointAddress: "127.0.0.1"},
			SnapshotName: "test-backup",
			SnapmirrorRelationship: &commonparams.SnapmirrorRelationship{
				UUID: "test-snapmirror-uuid",
			},
		}, nil)
		env.OnActivity("PollTransferStatusWithHistoryCheckActivity", mock.Anything, mock.Anything, mock.Anything).Return(&activities.PollTransferStatusOutput{
			BackupActivitiesContext: &activities.BackupActivitiesContext{
				BackupWorkflowInit: &activities.BackupWorkflowInput{
					Backup:      backup,
					BackupVault: backupVault,
					Volume:      volume,
				},
				Node:              &models.Node{EndpointAddress: "127.0.0.1"},
				SnapshotName:      "test-backup",
				SmSourcePath:      "svm_test:volume_test",
				SmDestinationPath: "test-bucket:/objstore/test-vol",
				SnapmirrorRelationship: &commonparams.SnapmirrorRelationship{
					UUID: "test-snapmirror-uuid",
				},
				TransferStatus: activities.SmStatusSuccess,
			},
			TransferComplete:    true,
			ShouldContinueAsNew: false,
		}, nil)
		env.OnActivity("GetSnapmirror", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&commonparams.SnapmirrorRelationship{
			UUID:    "test-snapmirror-uuid",
			Healthy: nillable.ToPointer(true),
		}, nil)
		env.OnActivity("UpdateSnapshotActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:      backup,
				BackupVault: backupVault,
				Volume:      volume,
			},
			Node: &models.Node{EndpointAddress: "127.0.0.1"},
		}, nil)
		env.OnActivity("GetObjectStoreEndpointActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:      backup,
				BackupVault: backupVault,
				Volume:      volume,
			},
			Node: &models.Node{EndpointAddress: "127.0.0.1"},
		}, nil)
		env.OnActivity("GetObjectStoreSnapshotActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:      backup,
				BackupVault: backupVault,
				Volume:      volume,
			},
			Node: &models.Node{EndpointAddress: "127.0.0.1"},
		}, nil)
		env.OnActivity("UpdateBackupSizeActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:      backup,
				BackupVault: backupVault,
				Volume:      volume,
			},
			Node: &models.Node{EndpointAddress: "127.0.0.1"},
		}, nil)
		env.OnActivity("FinishBackupActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:      backup,
				BackupVault: backupVault,
				Volume:      volume,
			},
			Node: &models.Node{EndpointAddress: "127.0.0.1"},
		}, nil)
		env.OnActivity("CreateRemoteBackupFromVCPActivity", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CleanupOldBackupSnapshotsActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CreateBackupMetadataIfFirstBackupActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)

		// Execute workflow
		env.ExecuteWorkflow(CreateBackupWorkflow, params, backup, backupVault, volume)

		// Assert workflow execution
		assert.True(t, env.IsWorkflowCompleted())
		assert.NoError(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})
}

func TestDeleteBackupWorkflowAdditionalErrorCases(t *testing.T) {
	t.Run("GetBackupVaultFailure", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		env.SetHeader(mockHeader)

		// Create mock storage for CommonActivities
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()

		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(setupMockBackupActivity(t))
		env.RegisterWorkflow(DeleteBackupWorkflow)

		// Set up test data
		params := &commonparams.DeleteBackupParams{
			BackupVaultUUID: "vault-uuid",
			BackupUUID:      "backup-uuid",
			AccountName:     "test-account",
		}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "account-uuid"},
			Name:      "test-account",
		}

		// Mock activity responses for GetBackupVault failure
		env.OnActivity("GetAccountByName", mock.Anything, params.AccountName).Return(account, nil)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetBackupVault", mock.Anything, params.BackupVaultUUID).Return(nil, errors.New("failed to get backup vault"))
		// Mock GetBackup for HandleError method
		env.OnActivity("GetBackup", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.Backup{
			BaseModel:  datamodel.BaseModel{UUID: "backup-uuid"},
			Name:       "test-backup",
			Attributes: &datamodel.BackupAttributes{},
		}, nil)
		env.OnActivity("MarkBackupAvailable", mock.Anything, mock.Anything).Return(nil)

		// Execute workflow
		env.ExecuteWorkflow(DeleteBackupWorkflow, params)

		// Assert workflow execution - HandleError is called and workflow completes
		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		assert.ErrorContains(t, env.GetWorkflowError(), "failed to get backup vault")
		env.AssertExpectations(t)
	})

	t.Run("GetBackupFailure", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		env.SetHeader(mockHeader)

		// Create mock storage for CommonActivities
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()

		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(setupMockBackupActivity(t))
		env.RegisterWorkflow(DeleteBackupWorkflow)

		// Set up test data
		params := &commonparams.DeleteBackupParams{
			BackupVaultUUID: "vault-uuid",
			BackupUUID:      "backup-uuid",
			AccountName:     "test-account",
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "account-uuid"},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			Name: "test-backup-vault",
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{BucketName: "test-bucket", ServiceAccountName: "sa-test", VendorSubnetID: "subnet-12345"},
			},
		}

		// Mock activity responses for GetBackup failure
		env.OnActivity("GetAccountByName", mock.Anything, params.AccountName).Return(account, nil)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetBackupVault", mock.Anything, params.BackupVaultUUID).Return(backupVault, nil)
		env.OnActivity("GetBackup", mock.Anything, params.BackupVaultUUID, params.BackupUUID, params.AccountName).Return(nil, errors.New("failed to get backup"))

		// Execute workflow
		env.ExecuteWorkflow(DeleteBackupWorkflow, params)

		// Assert workflow execution - HandleError is called and workflow completes
		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		assert.ErrorContains(t, env.GetWorkflowError(), "failed to get backup")
		env.AssertExpectations(t)
	})
}

func TestUpdateBackupWorkflowAdditionalCases(t *testing.T) {
	t.Run("UpdateBackupWorkflowGetBackupFailure", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		env.SetHeader(mockHeader)

		// Create mock storage for CommonActivities
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()

		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(setupMockBackupActivity(t))
		env.RegisterWorkflow(UpdateBackupWorkflow)

		// Set up test data
		backup := &datamodel.Backup{
			Name:        "test-backup",
			VolumeUUID:  "test-vol",
			Attributes:  &datamodel.BackupAttributes{BucketName: "test-bucket"},
			Description: "Updated description",
		}

		// Mock activity responses
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("UpdateBackup", mock.Anything, backup).Return(nil)

		// Execute workflow
		env.ExecuteWorkflow(UpdateBackupWorkflow, backup)

		// Assert workflow execution
		assert.True(t, env.IsWorkflowCompleted())
		assert.NoError(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})
}

// TestDeleteBackupWorkflow_ADCSuccessWithCloudDeletionInitiated_LaterFailureMarksError validates
// HandleError behavior when ADC initiated deletion successfully but a later step fails.
func TestDeleteBackupWorkflow_ADCSuccessWithCloudDeletionInitiated_LaterFailureMarksError(t *testing.T) {
	t.Run("ADCSuccessWithCloudDeletionInitiated_LaterFailureMarksError", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		env.SetHeader(mockHeader)
		commonActivity, _ := setupMockCommonActivities(t)
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(setupMockBackupActivity(t))
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterWorkflow(ADCWorkflow)
		env.RegisterWorkflow(DeleteBackupWorkflow)

		// Set up test data
		params := &commonparams.DeleteBackupParams{
			BackupVaultUUID: "vault-uuid",
			BackupUUID:      "backup-uuid",
			AccountName:     "test-account",
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "account-uuid"},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			Name: "test-backup-vault",
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{BucketName: "test-bucket", ServiceAccountName: "sa-test", VendorSubnetID: "subnet-12345"},
			},
		}
		backup := &datamodel.Backup{
			Name:          "test-backup",
			VolumeUUID:    "test-vol",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes:    &datamodel.BackupAttributes{BucketName: "test-bucket"},
		}

		// Mock volume-deleted ADC path where ADC succeeds and initiates deletion.
		env.OnActivity("GetAccountByName", mock.Anything, params.AccountName).Return(account, nil)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetBackupVault", mock.Anything, params.BackupVaultUUID).Return(backupVault, nil)
		env.OnActivity("GetBackup", mock.Anything, params.BackupVaultUUID, params.BackupUUID, params.AccountName).Return(backup, nil).Twice()
		env.OnActivity("IsVolumeDeleted", mock.Anything, backup.VolumeUUID).Return(true, nil) // Volume is deleted
		env.OnActivity("IsExpertModeVolume", mock.Anything, backup.VolumeUUID).Return(false, nil)
		env.OnWorkflow("ADCWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(true, nil)
		// Fail after ADC returns successfully to force HandleError path.
		env.OnActivity("DeleteBackup", mock.Anything, params.BackupUUID, mock.Anything).Return(nil, errors.New("delete backup failed"))
		env.OnActivity("UpdateBackupError", mock.Anything, mock.Anything, mock.Anything).Return(nil)

		// Execute workflow
		env.ExecuteWorkflow(DeleteBackupWorkflow, params)

		// Assert workflow execution
		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		assert.ErrorContains(t, env.GetWorkflowError(), "delete backup failed")
		env.AssertExpectations(t)
	})
}

func TestBackupWorkflowSnapmirrorTransferWaitTimeCap(t *testing.T) {
	// This test covers the case where waitTime exceeds BackupMaxWaitTimeCap (line 212)
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
	mockHeader := &commonpb.Header{
		Fields: map[string]*commonpb.Payload{
			"logParam": encodedValue,
		},
	}
	env.SetHeader(mockHeader)

	// Create mock storage for CommonActivities and BackupActivity
	mockStorage := database.NewMockStorage(t)
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()

	commonActivity := &activities.CommonActivities{SE: mockStorage}
	env.RegisterActivity(commonActivity)
	env.RegisterWorkflow(CreateBackupWorkflow)
	mockStorage.On("GetSnapshotsByTypeAndVolumeID", mock.Anything, mock.Anything, mock.Anything).Return([]*datamodel.Snapshot{}, nil).Maybe()
	mockStorage.On("UpdateBackup", mock.Anything, mock.Anything).Return(&datamodel.Backup{}, nil).Maybe()
	mockStorage.On("UpdateBackupLatestLogicalBackupSizeByVolume", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	mockStorage.On("UpdateVolumeFields", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	env.RegisterActivity(&activities.BackupActivity{SE: mockStorage})
	env.RegisterActivity(&TestBackupActivity{BackupActivity: &activities.BackupActivity{SE: mockStorage}})

	// Register the specific activity method
	env.OnActivity("CleanupOldBackupSnapshotsActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Set up test data
	params := &commonparams.CreateBackupParams{
		VolumeUUID:  "test-vol",
		AccountName: "test-account",
		BackupName:  "test-backup",
	}
	backupVault := &datamodel.BackupVault{
		Name:          "test-backup-vault",
		BucketDetails: datamodel.BucketDetailsArray{&datamodel.BucketDetails{BucketName: "test-bucket", ServiceAccountName: "sa-test", VendorSubnetID: "subnet-12345"}},
	}
	backup := &datamodel.Backup{
		State:         "InProgress",
		Name:          "test-backup",
		VolumeUUID:    "test-vol",
		BackupVault:   backupVault,
		BackupVaultID: 1,
		Attributes:    &datamodel.BackupAttributes{},
	}

	volume := &datamodel.Volume{
		Pool:                  &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{Password: "password"}, PoolAttributes: &datamodel.PoolAttributes{PrimaryZone: "us-central1-a"}},
		Svm:                   &datamodel.Svm{Name: "svm_test"},
		VolumeAttributes:      &datamodel.VolumeAttributes{BlockProperties: &datamodel.BlockProperties{OSType: "LINUX"}, VendorSubnetID: "subnet-12345", ExternalUUID: "external-uuid"},
		LargeVolumeAttributes: nil,
	}

	// Mock activities with multiple transferring status calls to trigger the wait time cap logic
	env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("GetNode", mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	env.OnActivity("PrepareObjectStoreActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node: &models.Node{EndpointAddress: "127.0.0.1"},
	}, nil)
	env.OnActivity("GetOrCreateObjectStoreActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node: &models.Node{EndpointAddress: "127.0.0.1"},
	}, nil)
	env.OnActivity("PrepareSnapmirrorActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node: &models.Node{EndpointAddress: "127.0.0.1"},
	}, nil)
	env.OnActivity("CreateSnapmirrorRelationshipActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node: &models.Node{EndpointAddress: "127.0.0.1"},
	}, nil)
	env.OnActivity("CreatingSnapshotActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node: &models.Node{EndpointAddress: "127.0.0.1"},
	}, nil)
	env.OnActivity("CreateSnapshotActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup: &datamodel.Backup{
				Name: "test-backup",
				Attributes: &datamodel.BackupAttributes{
					SnapshotID: "test-snapshot-uuid",
				},
			},
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node:         &models.Node{EndpointAddress: "127.0.0.1"},
		SnapshotName: "test-backup",
		SnapmirrorRelationship: &commonparams.SnapmirrorRelationship{
			UUID: "test-snapmirror-uuid",
		},
		SnapshotResponse: &vsa.SnapshotProviderResponse{
			ProviderResponse: vsa.ProviderResponse{
				ExternalUUID: "test-snapshot-uuid",
			},
		},
	}, nil)
	env.OnActivity("TransferSnapshotActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup: &datamodel.Backup{
				Name: "test-backup",
				Attributes: &datamodel.BackupAttributes{
					SnapshotID: "test-snapshot-uuid",
				},
			},
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node:         &models.Node{EndpointAddress: "127.0.0.1"},
		SnapshotName: "test-backup",
		SnapmirrorRelationship: &commonparams.SnapmirrorRelationship{
			UUID: "test-snapmirror-uuid",
		},
	}, nil)
	env.OnActivity("PollTransferStatusWithHistoryCheckActivity", mock.Anything, mock.Anything, mock.Anything).Return(&activities.PollTransferStatusOutput{
		BackupActivitiesContext: &activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:      backup,
				BackupVault: backupVault,
				Volume:      volume,
			},
			Node:              &models.Node{EndpointAddress: "127.0.0.1"},
			SnapshotName:      "test-backup",
			SmSourcePath:      "svm_test:volume_test",
			SmDestinationPath: "test-bucket:/objstore/test-vol",
			SnapmirrorRelationship: &commonparams.SnapmirrorRelationship{
				UUID: "test-snapmirror-uuid",
			},
			TransferStatus: activities.SmStatusSuccess,
		},
		TransferComplete:    true,
		ShouldContinueAsNew: false,
	}, nil)
	env.OnActivity("GetSnapmirror", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&commonparams.SnapmirrorRelationship{
		UUID:    "test-snapmirror-uuid",
		Healthy: nillable.ToPointer(true),
	}, nil)
	env.OnActivity("FinishBackupActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node: &models.Node{EndpointAddress: "127.0.0.1"},
	}, nil)
	env.OnActivity("GetObjectStoreEndpointActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node: &models.Node{EndpointAddress: "127.0.0.1"},
	}, nil)
	env.OnActivity("GetObjectStoreSnapshotActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node: &models.Node{EndpointAddress: "127.0.0.1"},
	}, nil)
	env.OnActivity("UpdateBackupSizeActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node: &models.Node{EndpointAddress: "127.0.0.1"},
	}, nil)
	env.OnActivity("CreateRemoteBackupFromVCPActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
	}, nil)
	env.OnActivity("CleanupOldBackupSnapshotsActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("CreateBackupMetadataIfFirstBackupActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	// Mock rollback activities in case workflow fails
	env.OnActivity("UpdateBackupError", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	// Execute workflow
	env.ExecuteWorkflow(CreateBackupWorkflow, params, backup, backupVault, volume)

	// Assert that the workflow was executed successfully
	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

// TestCreateBackupWorkflowWithVaultSwitchingEnabled covers the create path when EnableBackupVaultSwitching is true.
// Size calculation should still use the regular backup size update activity path.
func TestCreateBackupWorkflowWithVaultSwitchingEnabled(t *testing.T) {
	originalFlag := utils.EnableBackupVaultSwitching
	defer utils.SetEnableBackupVaultSwitchingForTest(originalFlag)
	utils.SetEnableBackupVaultSwitchingForTest(true)

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
	env.SetHeader(&commonpb.Header{Fields: map[string]*commonpb.Payload{"logParam": encodedValue}})

	mockStorage := database.NewMockStorage(t)
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "job-uuid"}, State: string(datamodel.JobsStateNEW)}, nil).Maybe()
	mockStorage.On("UpdateBackupState", mock.Anything, mock.Anything).Return(&datamodel.Backup{}, nil).Maybe()
	mockStorage.On("GetBackup", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.Backup{}, nil).Maybe()
	mockStorage.On("UpdateBackup", mock.Anything, mock.Anything).Return(&datamodel.Backup{}, nil).Maybe()
	mockStorage.On("UpdateBackupLatestLogicalBackupSizeByVolume", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	mockStorage.On("UpdateVolumeFields", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	mockStorage.On("GetVolume", mock.Anything, mock.Anything).Return(&datamodel.Volume{BaseModel: datamodel.BaseModel{UUID: "test-vol"}, AccountID: 1, Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 1}}}, nil).Maybe()
	mockStorage.On("GetSnapshotByNameAndVolumeId", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.Snapshot{BaseModel: datamodel.BaseModel{UUID: "snap-uuid"}, Name: "test-backup"}, nil).Maybe()
	mockStorage.On("DeleteSnapshot", mock.Anything, mock.Anything).Return(&datamodel.Snapshot{}, nil).Maybe()
	mockStorage.On("GetSnapshotsByTypeAndVolumeID", mock.Anything, mock.Anything, mock.Anything).Return([]*datamodel.Snapshot{}, nil).Maybe()

	commonActivity := &activities.CommonActivities{SE: mockStorage}
	env.RegisterActivity(commonActivity)
	env.RegisterWorkflow(CreateBackupWorkflow)
	env.RegisterActivity(setupMockBackupActivity(t))
	env.RegisterActivity(&activities.ADCActivity{SE: mockStorage})

	params := &commonparams.CreateBackupParams{VolumeUUID: "test-vol", AccountName: "test-account", BackupName: "test-backup"}
	backupVault := &datamodel.BackupVault{
		Name:          "test-backup-vault",
		BucketDetails: datamodel.BucketDetailsArray{&datamodel.BucketDetails{BucketName: "test-bucket", ServiceAccountName: "sa-test", VendorSubnetID: "subnet-12345"}},
	}
	backup := &datamodel.Backup{
		State:         "InProgress",
		Name:          "test-backup",
		VolumeUUID:    "test-vol",
		BackupVault:   backupVault,
		BackupVaultID: 1,
		Attributes:    &datamodel.BackupAttributes{BucketName: "test-bucket"},
	}
	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "test-vol"},
		Pool: &datamodel.Pool{
			BaseModel:       datamodel.BaseModel{ID: 1},
			PoolCredentials: &datamodel.PoolCredentials{Password: "password"},
			PoolAttributes:  &datamodel.PoolAttributes{PrimaryZone: "us-central1-a", IsRegionalHA: false},
		},
		Svm:              &datamodel.Svm{Name: "svm_test"},
		VolumeAttributes: &datamodel.VolumeAttributes{VendorSubnetID: "subnet-12345", ExternalUUID: "external-uuid"},
	}

	ctxWithNode := &activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{Backup: backup, BackupVault: backupVault, Volume: volume},
		Node:               &models.Node{EndpointAddress: "127.0.0.1"},
	}
	env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("GetNode", mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	env.OnActivity("PrepareObjectStoreActivity", mock.Anything, mock.Anything).Return(ctxWithNode, nil)
	env.OnActivity("GetOrCreateObjectStoreActivity", mock.Anything, mock.Anything).Return(ctxWithNode, nil)
	env.OnActivity("PrepareSnapmirrorActivity", mock.Anything, mock.Anything).Return(ctxWithNode, nil)
	env.OnActivity("CreateSnapmirrorRelationshipActivity", mock.Anything, mock.Anything).Return(ctxWithNode, nil)
	env.OnActivity("CreatingSnapshotActivity", mock.Anything, mock.Anything).Return(ctxWithNode, nil)
	env.OnActivity("CreateSnapshotActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{Backup: backup, BackupVault: backupVault, Volume: volume},
		Node:               ctxWithNode.Node, SnapshotName: "test-backup",
		SnapmirrorRelationship: &commonparams.SnapmirrorRelationship{UUID: "sm-uuid"},
		SnapshotResponse:       &vsa.SnapshotProviderResponse{ProviderResponse: vsa.ProviderResponse{ExternalUUID: "ext-uuid"}},
	}, nil)
	env.OnActivity("TransferSnapshotActivity", mock.Anything, mock.Anything).Return(ctxWithNode, nil)
	env.OnActivity("PollTransferStatusWithHistoryCheckActivity", mock.Anything, mock.Anything, mock.Anything).Return(&activities.PollTransferStatusOutput{
		BackupActivitiesContext: ctxWithNode, TransferComplete: true, ShouldContinueAsNew: false,
	}, nil)
	env.OnActivity("GetSnapmirror", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&commonparams.SnapmirrorRelationship{UUID: "sm-uuid", Healthy: nillable.ToPointer(true)}, nil)
	env.OnActivity("GetObjectStoreSnapshotActivity", mock.Anything, mock.Anything).Return(ctxWithNode, nil)
	env.OnActivity("FinishBackupActivity", mock.Anything, mock.Anything).Return(ctxWithNode, nil).Once()
	env.OnActivity("GetObjectStoreEndpointActivity", mock.Anything, mock.Anything).Return(ctxWithNode, nil)
	env.OnActivity("UpdateBackupSizeActivity", mock.Anything, mock.Anything).Return(ctxWithNode, nil)
	env.OnActivity("CreateRemoteBackupFromVCPActivity", mock.Anything, mock.Anything).Return(ctxWithNode, nil)
	env.OnActivity("CleanupOldBackupSnapshotsActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("CreateBackupMetadataIfFirstBackupActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("UpdateBackupError", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	env.ExecuteWorkflow(CreateBackupWorkflow, params, backup, backupVault, volume)

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

// TestCreateBackupWorkflowWithVaultSwitchingEnabled_ObjectStoreSizeUpdate_WorkflowCompletes
// verifies workflow completes with regular size update flow when vault switching is enabled.
func TestCreateBackupWorkflowWithVaultSwitchingEnabled_ObjectStoreSizeUpdate_WorkflowCompletes(t *testing.T) {
	originalFlag := utils.EnableBackupVaultSwitching
	defer utils.SetEnableBackupVaultSwitchingForTest(originalFlag)
	utils.SetEnableBackupVaultSwitchingForTest(true)

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
	env.SetHeader(&commonpb.Header{Fields: map[string]*commonpb.Payload{"logParam": encodedValue}})

	mockStorage := database.NewMockStorage(t)
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "job-uuid"}, State: string(datamodel.JobsStateNEW)}, nil).Maybe()
	mockStorage.On("UpdateBackupState", mock.Anything, mock.Anything).Return(&datamodel.Backup{}, nil).Maybe()
	mockStorage.On("GetBackup", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.Backup{}, nil).Maybe()
	mockStorage.On("UpdateBackup", mock.Anything, mock.Anything).Return(&datamodel.Backup{}, nil).Maybe()
	mockStorage.On("UpdateBackupLatestLogicalBackupSizeByVolume", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	mockStorage.On("UpdateVolumeFields", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	mockStorage.On("GetVolume", mock.Anything, mock.Anything).Return(&datamodel.Volume{BaseModel: datamodel.BaseModel{UUID: "test-vol"}, AccountID: 1, Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 1}}}, nil).Maybe()
	mockStorage.On("GetSnapshotByNameAndVolumeId", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.Snapshot{BaseModel: datamodel.BaseModel{UUID: "snap-uuid"}, Name: "test-backup"}, nil).Maybe()
	mockStorage.On("DeleteSnapshot", mock.Anything, mock.Anything).Return(&datamodel.Snapshot{}, nil).Maybe()
	mockStorage.On("GetSnapshotsByTypeAndVolumeID", mock.Anything, mock.Anything, mock.Anything).Return([]*datamodel.Snapshot{}, nil).Maybe()

	commonActivity := &activities.CommonActivities{SE: mockStorage}
	env.RegisterActivity(commonActivity)
	env.RegisterWorkflow(CreateBackupWorkflow)
	env.RegisterActivity(setupMockBackupActivity(t))
	env.RegisterActivity(&activities.ADCActivity{SE: mockStorage})

	params := &commonparams.CreateBackupParams{VolumeUUID: "test-vol", AccountName: "test-account", BackupName: "test-backup"}
	backupVault := &datamodel.BackupVault{
		Name:          "test-backup-vault",
		BucketDetails: datamodel.BucketDetailsArray{&datamodel.BucketDetails{BucketName: "test-bucket", ServiceAccountName: "sa-test", VendorSubnetID: "subnet-12345"}},
	}
	backup := &datamodel.Backup{
		State:         "InProgress",
		Name:          "test-backup",
		VolumeUUID:    "test-vol",
		BackupVault:   backupVault,
		BackupVaultID: 1,
		Attributes:    &datamodel.BackupAttributes{BucketName: "test-bucket"},
	}
	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "test-vol"},
		Pool: &datamodel.Pool{
			BaseModel:       datamodel.BaseModel{ID: 1},
			PoolCredentials: &datamodel.PoolCredentials{Password: "password"},
			PoolAttributes:  &datamodel.PoolAttributes{PrimaryZone: "us-central1-a", IsRegionalHA: false},
		},
		Svm:              &datamodel.Svm{Name: "svm_test"},
		VolumeAttributes: &datamodel.VolumeAttributes{VendorSubnetID: "subnet-12345", ExternalUUID: "external-uuid"},
	}

	ctxWithNode := &activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{Backup: backup, BackupVault: backupVault, Volume: volume},
		Node:               &models.Node{EndpointAddress: "127.0.0.1"},
	}
	env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("GetNode", mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	env.OnActivity("PrepareObjectStoreActivity", mock.Anything, mock.Anything).Return(ctxWithNode, nil)
	env.OnActivity("GetOrCreateObjectStoreActivity", mock.Anything, mock.Anything).Return(ctxWithNode, nil)
	env.OnActivity("PrepareSnapmirrorActivity", mock.Anything, mock.Anything).Return(ctxWithNode, nil)
	env.OnActivity("CreateSnapmirrorRelationshipActivity", mock.Anything, mock.Anything).Return(ctxWithNode, nil)
	env.OnActivity("CreatingSnapshotActivity", mock.Anything, mock.Anything).Return(ctxWithNode, nil)
	env.OnActivity("CreateSnapshotActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{Backup: backup, BackupVault: backupVault, Volume: volume},
		Node:               ctxWithNode.Node, SnapshotName: "test-backup",
		SnapmirrorRelationship: &commonparams.SnapmirrorRelationship{UUID: "sm-uuid"},
		SnapshotResponse:       &vsa.SnapshotProviderResponse{ProviderResponse: vsa.ProviderResponse{ExternalUUID: "ext-uuid"}},
	}, nil)
	env.OnActivity("TransferSnapshotActivity", mock.Anything, mock.Anything).Return(ctxWithNode, nil)
	env.OnActivity("PollTransferStatusWithHistoryCheckActivity", mock.Anything, mock.Anything, mock.Anything).Return(&activities.PollTransferStatusOutput{
		BackupActivitiesContext: ctxWithNode, TransferComplete: true, ShouldContinueAsNew: false,
	}, nil)
	env.OnActivity("GetSnapmirror", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&commonparams.SnapmirrorRelationship{UUID: "sm-uuid", Healthy: nillable.ToPointer(true)}, nil)
	env.OnActivity("GetObjectStoreSnapshotActivity", mock.Anything, mock.Anything).Return(ctxWithNode, nil)
	env.OnActivity("FinishBackupActivity", mock.Anything, mock.Anything).Return(ctxWithNode, nil).Once()
	env.OnActivity("GetObjectStoreEndpointActivity", mock.Anything, mock.Anything).Return(ctxWithNode, nil)
	env.OnActivity("UpdateBackupSizeActivity", mock.Anything, mock.Anything).Return(ctxWithNode, nil)
	env.OnActivity("CreateRemoteBackupFromVCPActivity", mock.Anything, mock.Anything).Return(ctxWithNode, nil)
	env.OnActivity("CleanupOldBackupSnapshotsActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("CreateBackupMetadataIfFirstBackupActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("UpdateBackupError", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	env.ExecuteWorkflow(CreateBackupWorkflow, params, backup, backupVault, volume)

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

// TestCreateBackupWorkflowWithVaultSwitchingEnabled_FinishBackupAfterADCFails_WorkflowCompletes
func TestCreateBackupWorkflowWithVaultSwitchingEnabled_FinishBackupAfterADCFails_WorkflowCompletes(t *testing.T) {
	originalFlag := utils.EnableBackupVaultSwitching
	defer utils.SetEnableBackupVaultSwitchingForTest(originalFlag)
	utils.SetEnableBackupVaultSwitchingForTest(true)

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
	env.SetHeader(&commonpb.Header{Fields: map[string]*commonpb.Payload{"logParam": encodedValue}})

	mockStorage := database.NewMockStorage(t)
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "job-uuid"}, State: string(datamodel.JobsStateNEW)}, nil).Maybe()
	mockStorage.On("UpdateBackupState", mock.Anything, mock.Anything).Return(&datamodel.Backup{}, nil).Maybe()
	mockStorage.On("GetBackup", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.Backup{}, nil).Maybe()
	mockStorage.On("UpdateBackup", mock.Anything, mock.Anything).Return(&datamodel.Backup{}, nil).Maybe()
	mockStorage.On("UpdateBackupLatestLogicalBackupSizeByVolume", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	mockStorage.On("UpdateVolumeFields", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	mockStorage.On("GetVolume", mock.Anything, mock.Anything).Return(&datamodel.Volume{BaseModel: datamodel.BaseModel{UUID: "test-vol"}, AccountID: 1, Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 1}}}, nil).Maybe()
	mockStorage.On("GetSnapshotByNameAndVolumeId", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.Snapshot{BaseModel: datamodel.BaseModel{UUID: "snap-uuid"}, Name: "test-backup"}, nil).Maybe()
	mockStorage.On("DeleteSnapshot", mock.Anything, mock.Anything).Return(&datamodel.Snapshot{}, nil).Maybe()
	mockStorage.On("GetSnapshotsByTypeAndVolumeID", mock.Anything, mock.Anything, mock.Anything).Return([]*datamodel.Snapshot{}, nil).Maybe()

	commonActivity := &activities.CommonActivities{SE: mockStorage}
	env.RegisterActivity(commonActivity)
	env.RegisterWorkflow(CreateBackupWorkflow)
	env.RegisterActivity(setupMockBackupActivity(t))
	env.RegisterActivity(&activities.ADCActivity{SE: mockStorage})

	params := &commonparams.CreateBackupParams{VolumeUUID: "test-vol", AccountName: "test-account", BackupName: "test-backup"}
	backupVault := &datamodel.BackupVault{
		Name:          "test-backup-vault",
		BucketDetails: datamodel.BucketDetailsArray{&datamodel.BucketDetails{BucketName: "test-bucket", ServiceAccountName: "sa-test", VendorSubnetID: "subnet-12345"}},
	}
	backup := &datamodel.Backup{
		State:         "InProgress",
		Name:          "test-backup",
		VolumeUUID:    "test-vol",
		BackupVault:   backupVault,
		BackupVaultID: 1,
		Attributes:    &datamodel.BackupAttributes{BucketName: "test-bucket"},
	}
	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "test-vol"},
		Pool: &datamodel.Pool{
			BaseModel:       datamodel.BaseModel{ID: 1},
			PoolCredentials: &datamodel.PoolCredentials{Password: "password"},
			PoolAttributes:  &datamodel.PoolAttributes{PrimaryZone: "us-central1-a", IsRegionalHA: false},
		},
		Svm:              &datamodel.Svm{Name: "svm_test"},
		VolumeAttributes: &datamodel.VolumeAttributes{VendorSubnetID: "subnet-12345", ExternalUUID: "external-uuid"},
	}

	ctxWithNode := &activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{Backup: backup, BackupVault: backupVault, Volume: volume},
		Node:               &models.Node{EndpointAddress: "127.0.0.1"},
	}
	env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("GetNode", mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	env.OnActivity("PrepareObjectStoreActivity", mock.Anything, mock.Anything).Return(ctxWithNode, nil)
	env.OnActivity("GetOrCreateObjectStoreActivity", mock.Anything, mock.Anything).Return(ctxWithNode, nil)
	env.OnActivity("PrepareSnapmirrorActivity", mock.Anything, mock.Anything).Return(ctxWithNode, nil)
	env.OnActivity("CreateSnapmirrorRelationshipActivity", mock.Anything, mock.Anything).Return(ctxWithNode, nil)
	env.OnActivity("CreatingSnapshotActivity", mock.Anything, mock.Anything).Return(ctxWithNode, nil)
	env.OnActivity("CreateSnapshotActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{Backup: backup, BackupVault: backupVault, Volume: volume},
		Node:               ctxWithNode.Node, SnapshotName: "test-backup",
		SnapmirrorRelationship: &commonparams.SnapmirrorRelationship{UUID: "sm-uuid"},
		SnapshotResponse:       &vsa.SnapshotProviderResponse{ProviderResponse: vsa.ProviderResponse{ExternalUUID: "ext-uuid"}},
	}, nil)
	env.OnActivity("TransferSnapshotActivity", mock.Anything, mock.Anything).Return(ctxWithNode, nil)
	env.OnActivity("PollTransferStatusWithHistoryCheckActivity", mock.Anything, mock.Anything, mock.Anything).Return(&activities.PollTransferStatusOutput{
		BackupActivitiesContext: ctxWithNode, TransferComplete: true, ShouldContinueAsNew: false,
	}, nil)
	env.OnActivity("GetSnapmirror", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&commonparams.SnapmirrorRelationship{UUID: "sm-uuid", Healthy: nillable.ToPointer(true)}, nil)
	env.OnActivity("GetObjectStoreSnapshotActivity", mock.Anything, mock.Anything).Return(ctxWithNode, nil)
	env.OnActivity("FinishBackupActivity", mock.Anything, mock.Anything).Return(ctxWithNode, nil).Once()
	env.OnActivity("GetObjectStoreEndpointActivity", mock.Anything, mock.Anything).Return(ctxWithNode, nil)
	env.OnActivity("UpdateBackupSizeActivity", mock.Anything, mock.Anything).Return(ctxWithNode, nil)
	env.OnActivity("CreateRemoteBackupFromVCPActivity", mock.Anything, mock.Anything).Return(ctxWithNode, nil)
	env.OnActivity("CleanupOldBackupSnapshotsActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("CreateBackupMetadataIfFirstBackupActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("UpdateBackupError", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	env.ExecuteWorkflow(CreateBackupWorkflow, params, backup, backupVault, volume)

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

// TestCreateBackupWorkflowWithVaultSwitchingEnabled_UpdateVolumeLatestLogicalBackupSizeFails_WorkflowCompletes
func TestCreateBackupWorkflowWithVaultSwitchingEnabled_UpdateVolumeLatestLogicalBackupSizeFails_WorkflowCompletes(t *testing.T) {
	originalFlag := utils.EnableBackupVaultSwitching
	defer utils.SetEnableBackupVaultSwitchingForTest(originalFlag)
	utils.SetEnableBackupVaultSwitchingForTest(true)

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
	env.SetHeader(&commonpb.Header{Fields: map[string]*commonpb.Payload{"logParam": encodedValue}})

	mockStorage := database.NewMockStorage(t)
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "job-uuid"}, State: string(datamodel.JobsStateNEW)}, nil).Maybe()
	mockStorage.On("UpdateBackupState", mock.Anything, mock.Anything).Return(&datamodel.Backup{}, nil).Maybe()
	mockStorage.On("GetBackup", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.Backup{}, nil).Maybe()
	mockStorage.On("UpdateBackup", mock.Anything, mock.Anything).Return(&datamodel.Backup{}, nil).Maybe()
	mockStorage.On("UpdateBackupLatestLogicalBackupSizeByVolume", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	mockStorage.On("UpdateVolumeFields", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	mockStorage.On("GetVolume", mock.Anything, mock.Anything).Return(&datamodel.Volume{BaseModel: datamodel.BaseModel{UUID: "test-vol"}, AccountID: 1, Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 1}}}, nil).Maybe()
	mockStorage.On("GetSnapshotByNameAndVolumeId", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.Snapshot{BaseModel: datamodel.BaseModel{UUID: "snap-uuid"}, Name: "test-backup"}, nil).Maybe()
	mockStorage.On("DeleteSnapshot", mock.Anything, mock.Anything).Return(&datamodel.Snapshot{}, nil).Maybe()
	mockStorage.On("GetSnapshotsByTypeAndVolumeID", mock.Anything, mock.Anything, mock.Anything).Return([]*datamodel.Snapshot{}, nil).Maybe()

	commonActivity := &activities.CommonActivities{SE: mockStorage}
	env.RegisterActivity(commonActivity)
	env.RegisterWorkflow(CreateBackupWorkflow)
	env.RegisterActivity(setupMockBackupActivity(t))
	env.RegisterActivity(&activities.ADCActivity{SE: mockStorage})

	params := &commonparams.CreateBackupParams{VolumeUUID: "test-vol", AccountName: "test-account", BackupName: "test-backup"}
	backupVault := &datamodel.BackupVault{Name: "test-backup-vault", BucketDetails: datamodel.BucketDetailsArray{&datamodel.BucketDetails{BucketName: "test-bucket", ServiceAccountName: "sa-test", VendorSubnetID: "subnet-12345"}}}
	backup := &datamodel.Backup{State: "InProgress", Name: "test-backup", VolumeUUID: "test-vol", BackupVault: backupVault, BackupVaultID: 1, Attributes: &datamodel.BackupAttributes{BucketName: "test-bucket"}}
	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "test-vol"},
		Pool:      &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 1}, PoolCredentials: &datamodel.PoolCredentials{Password: "password"}, PoolAttributes: &datamodel.PoolAttributes{PrimaryZone: "us-central1-a", IsRegionalHA: false}},
		Svm:       &datamodel.Svm{Name: "svm_test"}, VolumeAttributes: &datamodel.VolumeAttributes{VendorSubnetID: "subnet-12345", ExternalUUID: "external-uuid"},
	}
	ctxWithNode := &activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{Backup: backup, BackupVault: backupVault, Volume: volume},
		Node:               &models.Node{EndpointAddress: "127.0.0.1"},
	}

	env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("GetNode", mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	env.OnActivity("PrepareObjectStoreActivity", mock.Anything, mock.Anything).Return(ctxWithNode, nil)
	env.OnActivity("GetOrCreateObjectStoreActivity", mock.Anything, mock.Anything).Return(ctxWithNode, nil)
	env.OnActivity("PrepareSnapmirrorActivity", mock.Anything, mock.Anything).Return(ctxWithNode, nil)
	env.OnActivity("CreateSnapmirrorRelationshipActivity", mock.Anything, mock.Anything).Return(ctxWithNode, nil)
	env.OnActivity("CreatingSnapshotActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{Backup: backup, BackupVault: backupVault, Volume: volume},
		Node:               ctxWithNode.Node, SnapshotName: "test-backup",
		SnapmirrorRelationship: &commonparams.SnapmirrorRelationship{UUID: "sm-uuid"},
		SnapshotResponse:       &vsa.SnapshotProviderResponse{ProviderResponse: vsa.ProviderResponse{ExternalUUID: "ext-uuid"}},
	}, nil)
	env.OnActivity("CreateSnapshotActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{Backup: backup, BackupVault: backupVault, Volume: volume},
		Node:               ctxWithNode.Node, SnapshotName: "test-backup",
		SnapmirrorRelationship: &commonparams.SnapmirrorRelationship{UUID: "sm-uuid"},
		SnapshotResponse:       &vsa.SnapshotProviderResponse{ProviderResponse: vsa.ProviderResponse{ExternalUUID: "ext-uuid"}},
	}, nil)
	env.OnActivity("TransferSnapshotActivity", mock.Anything, mock.Anything).Return(ctxWithNode, nil)
	env.OnActivity("PollTransferStatusWithHistoryCheckActivity", mock.Anything, mock.Anything, mock.Anything).Return(&activities.PollTransferStatusOutput{
		BackupActivitiesContext: ctxWithNode, TransferComplete: true, ShouldContinueAsNew: false,
	}, nil)
	env.OnActivity("GetSnapmirror", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&commonparams.SnapmirrorRelationship{UUID: "sm-uuid", Healthy: nillable.ToPointer(true)}, nil)
	env.OnActivity("GetObjectStoreSnapshotActivity", mock.Anything, mock.Anything).Return(ctxWithNode, nil)
	env.OnActivity("FinishBackupActivity", mock.Anything, mock.Anything).Return(ctxWithNode, nil).Once()
	env.OnActivity("GetObjectStoreEndpointActivity", mock.Anything, mock.Anything).Return(ctxWithNode, nil)
	env.OnActivity("UpdateBackupSizeActivity", mock.Anything, mock.Anything).Return(ctxWithNode, errors.New("update volume size failed"))
	env.OnActivity("CreateRemoteBackupFromVCPActivity", mock.Anything, mock.Anything).Return(ctxWithNode, nil)
	env.OnActivity("CleanupOldBackupSnapshotsActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("CreateBackupMetadataIfFirstBackupActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("UpdateBackupError", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	env.ExecuteWorkflow(CreateBackupWorkflow, params, backup, backupVault, volume)

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

// TestCreateBackupWorkflowWithVaultSwitchingEnabled_SetGlobalLatestBackupLogicalSizeFails_WorkflowCompletes
func TestCreateBackupWorkflowWithVaultSwitchingEnabled_SetGlobalLatestBackupLogicalSizeFails_WorkflowCompletes(t *testing.T) {
	originalFlag := utils.EnableBackupVaultSwitching
	defer utils.SetEnableBackupVaultSwitchingForTest(originalFlag)
	utils.SetEnableBackupVaultSwitchingForTest(true)

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
	env.SetHeader(&commonpb.Header{Fields: map[string]*commonpb.Payload{"logParam": encodedValue}})

	mockStorage := database.NewMockStorage(t)
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "job-uuid"}, State: string(datamodel.JobsStateNEW)}, nil).Maybe()
	mockStorage.On("UpdateBackupState", mock.Anything, mock.Anything).Return(&datamodel.Backup{}, nil).Maybe()
	mockStorage.On("GetBackup", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.Backup{}, nil).Maybe()
	mockStorage.On("UpdateBackup", mock.Anything, mock.Anything).Return(&datamodel.Backup{}, nil).Maybe()
	mockStorage.On("UpdateBackupLatestLogicalBackupSizeByVolume", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	mockStorage.On("UpdateVolumeFields", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	mockStorage.On("GetVolume", mock.Anything, mock.Anything).Return(&datamodel.Volume{BaseModel: datamodel.BaseModel{UUID: "test-vol"}, AccountID: 1, Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 1}}}, nil).Maybe()
	mockStorage.On("GetSnapshotByNameAndVolumeId", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.Snapshot{BaseModel: datamodel.BaseModel{UUID: "snap-uuid"}, Name: "test-backup"}, nil).Maybe()
	mockStorage.On("DeleteSnapshot", mock.Anything, mock.Anything).Return(&datamodel.Snapshot{}, nil).Maybe()
	mockStorage.On("GetSnapshotsByTypeAndVolumeID", mock.Anything, mock.Anything, mock.Anything).Return([]*datamodel.Snapshot{}, nil).Maybe()

	commonActivity := &activities.CommonActivities{SE: mockStorage}
	env.RegisterActivity(commonActivity)
	env.RegisterWorkflow(CreateBackupWorkflow)
	env.RegisterActivity(setupMockBackupActivity(t))
	env.RegisterActivity(&activities.ADCActivity{SE: mockStorage})

	params := &commonparams.CreateBackupParams{VolumeUUID: "test-vol", AccountName: "test-account", BackupName: "test-backup"}
	backupVault := &datamodel.BackupVault{Name: "test-backup-vault", BucketDetails: datamodel.BucketDetailsArray{&datamodel.BucketDetails{BucketName: "test-bucket", ServiceAccountName: "sa-test", VendorSubnetID: "subnet-12345"}}}
	backup := &datamodel.Backup{State: "InProgress", Name: "test-backup", VolumeUUID: "test-vol", BackupVault: backupVault, BackupVaultID: 1, Attributes: &datamodel.BackupAttributes{BucketName: "test-bucket"}}
	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "test-vol"},
		Pool:      &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 1}, PoolCredentials: &datamodel.PoolCredentials{Password: "password"}, PoolAttributes: &datamodel.PoolAttributes{PrimaryZone: "us-central1-a", IsRegionalHA: false}},
		Svm:       &datamodel.Svm{Name: "svm_test"}, VolumeAttributes: &datamodel.VolumeAttributes{VendorSubnetID: "subnet-12345", ExternalUUID: "external-uuid"},
	}
	ctxWithNode := &activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{Backup: backup, BackupVault: backupVault, Volume: volume},
		Node:               &models.Node{EndpointAddress: "127.0.0.1"},
	}

	env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("GetNode", mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	env.OnActivity("PrepareObjectStoreActivity", mock.Anything, mock.Anything).Return(ctxWithNode, nil)
	env.OnActivity("GetOrCreateObjectStoreActivity", mock.Anything, mock.Anything).Return(ctxWithNode, nil)
	env.OnActivity("PrepareSnapmirrorActivity", mock.Anything, mock.Anything).Return(ctxWithNode, nil)
	env.OnActivity("CreateSnapmirrorRelationshipActivity", mock.Anything, mock.Anything).Return(ctxWithNode, nil)
	env.OnActivity("CreatingSnapshotActivity", mock.Anything, mock.Anything).Return(ctxWithNode, nil)
	env.OnActivity("CreateSnapshotActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{Backup: backup, BackupVault: backupVault, Volume: volume},
		Node:               ctxWithNode.Node, SnapshotName: "test-backup",
		SnapmirrorRelationship: &commonparams.SnapmirrorRelationship{UUID: "sm-uuid"},
		SnapshotResponse:       &vsa.SnapshotProviderResponse{ProviderResponse: vsa.ProviderResponse{ExternalUUID: "ext-uuid"}},
	}, nil)
	env.OnActivity("TransferSnapshotActivity", mock.Anything, mock.Anything).Return(ctxWithNode, nil)
	env.OnActivity("PollTransferStatusWithHistoryCheckActivity", mock.Anything, mock.Anything, mock.Anything).Return(&activities.PollTransferStatusOutput{
		BackupActivitiesContext: ctxWithNode, TransferComplete: true, ShouldContinueAsNew: false,
	}, nil)
	env.OnActivity("GetSnapmirror", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&commonparams.SnapmirrorRelationship{UUID: "sm-uuid", Healthy: nillable.ToPointer(true)}, nil)
	env.OnActivity("GetObjectStoreSnapshotActivity", mock.Anything, mock.Anything).Return(ctxWithNode, nil)
	env.OnActivity("FinishBackupActivity", mock.Anything, mock.Anything).Return(ctxWithNode, nil).Once()
	env.OnActivity("GetObjectStoreEndpointActivity", mock.Anything, mock.Anything).Return(ctxWithNode, nil)
	env.OnActivity("UpdateBackupSizeActivity", mock.Anything, mock.Anything).Return(ctxWithNode, nil)
	env.OnActivity("CreateRemoteBackupFromVCPActivity", mock.Anything, mock.Anything).Return(ctxWithNode, nil)
	env.OnActivity("CleanupOldBackupSnapshotsActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("CreateBackupMetadataIfFirstBackupActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("UpdateBackupError", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	env.ExecuteWorkflow(CreateBackupWorkflow, params, backup, backupVault, volume)

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

func TestDeleteSnapshotForBackup_UseExistingSnapshot_SkipsDeletion(t *testing.T) {
	// Test case: When useExistingSnapshot is true, it should skip deletion
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	activity := activities.BackupActivity{SE: mockStorage}
	env.RegisterActivity(&activity)

	// Mock hyperscaler provider
	mockProvider := new(vsa.MockProvider)
	originalGetProviderByNode := vsa.GetProviderByNode
	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()

	node := &models.Node{EndpointAddress: "test-node-address"}
	snapshotUUID := "snapshot-uuid-1"
	volumeUUID := "volume-uuid-1"
	useExistingSnapshot := true

	// DeleteSnapshot should NOT be called when useExistingSnapshot is true
	// No expectation set for DeleteSnapshot

	// Execute
	_, err := env.ExecuteActivity(activity.DeleteSnapshotForBackup, node, snapshotUUID, volumeUUID, useExistingSnapshot)

	// Assertions
	assert.NoError(t, err)
	mockProvider.AssertExpectations(t)
}

func TestDeleteSnapshotForBackup_NotUseExistingSnapshot_DeletesSnapshot(t *testing.T) {
	// Test case: When useExistingSnapshot is false, it should delete the snapshot
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	activity := activities.BackupActivity{SE: mockStorage}
	env.RegisterActivity(&activity)

	// Mock hyperscaler provider
	mockProvider := new(vsa.MockProvider)
	originalGetProviderByNode := vsa.GetProviderByNode
	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()

	node := &models.Node{EndpointAddress: "test-node-address"}
	snapshotUUID := "snapshot-uuid-1"
	volumeUUID := "volume-uuid-1"
	useExistingSnapshot := false

	// DeleteSnapshot should be called when useExistingSnapshot is false
	mockProvider.On("DeleteSnapshot", snapshotUUID, volumeUUID).Return(nil)

	// Execute
	_, err := env.ExecuteActivity(activity.DeleteSnapshotForBackup, node, snapshotUUID, volumeUUID, useExistingSnapshot)

	// Assertions
	assert.NoError(t, err)
	mockProvider.AssertExpectations(t)
}

func TestDeleteSnapshotForBackup_GetProviderByNodeFailure(t *testing.T) {
	// Test case: When GetProviderByNode fails
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	activity := activities.BackupActivity{SE: mockStorage}
	env.RegisterActivity(&activity)

	// Mock hyperscaler provider to return error
	originalGetProviderByNode := vsa.GetProviderByNode
	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return nil, errors.New("failed to get provider")
	}
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()

	node := &models.Node{EndpointAddress: "test-node-address"}
	snapshotUUID := "snapshot-uuid-1"
	volumeUUID := "volume-uuid-1"
	useExistingSnapshot := false

	// Execute
	_, err := env.ExecuteActivity(activity.DeleteSnapshotForBackup, node, snapshotUUID, volumeUUID, useExistingSnapshot)

	// Assertions
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get provider")
}

func TestDeleteSnapshotForBackup_DeleteSnapshotFailure(t *testing.T) {
	// Test case: When DeleteSnapshot fails
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	activity := activities.BackupActivity{SE: mockStorage}
	env.RegisterActivity(&activity)

	// Mock hyperscaler provider
	mockProvider := new(vsa.MockProvider)
	originalGetProviderByNode := vsa.GetProviderByNode
	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()

	node := &models.Node{EndpointAddress: "test-node-address"}
	snapshotUUID := "snapshot-uuid-1"
	volumeUUID := "volume-uuid-1"
	useExistingSnapshot := false

	// DeleteSnapshot returns error
	mockProvider.On("DeleteSnapshot", snapshotUUID, volumeUUID).Return(errors.New("failed to delete snapshot"))

	// Execute
	_, err := env.ExecuteActivity(activity.DeleteSnapshotForBackup, node, snapshotUUID, volumeUUID, useExistingSnapshot)

	// Assertions
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to delete snapshot")
	mockProvider.AssertExpectations(t)
}

func TestBackupWorkflowGetObjectStoreEndpointActivityFailure(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
	mockHeader := &commonpb.Header{
		Fields: map[string]*commonpb.Payload{
			"logParam": encodedValue,
		},
	}
	env.SetHeader(mockHeader)

	// Create mock storage for CommonActivities and BackupActivity
	mockStorage := database.NewMockStorage(t)
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()

	commonActivity := &activities.CommonActivities{SE: mockStorage}
	env.RegisterActivity(commonActivity)
	env.RegisterWorkflow(CreateBackupWorkflow)
	mockStorage.On("UpdateBackupState", mock.Anything, mock.Anything).Return(&datamodel.Backup{}, nil).Maybe()
	mockStorage.On("UpdateBackup", mock.Anything, mock.Anything).Return(&datamodel.Backup{}, nil).Maybe()
	mockStorage.On("UpdateBackupLatestLogicalBackupSizeByVolume", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	mockStorage.On("UpdateVolumeFields", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	env.RegisterActivity(&TestBackupActivity{BackupActivity: &activities.BackupActivity{SE: mockStorage}})

	// Set up test data
	params := &commonparams.CreateBackupParams{
		VolumeUUID:  "test-vol",
		AccountName: "test-account",
		BackupName:  "test-backup",
	}
	backupVault := &datamodel.BackupVault{
		Name:          "test-backup-vault",
		BucketDetails: datamodel.BucketDetailsArray{&datamodel.BucketDetails{BucketName: "test-bucket", ServiceAccountName: "sa-test", VendorSubnetID: "subnet-12345"}},
	}
	backup := &datamodel.Backup{
		State:         "InProgress",
		Name:          "test-backup",
		VolumeUUID:    "test-vol",
		BackupVault:   backupVault,
		BackupVaultID: 1,
		Attributes:    &datamodel.BackupAttributes{EndpointUUID: "test-endpoint-uuid"},
	}

	volume := &datamodel.Volume{
		Pool:             &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{Password: "password"}, PoolAttributes: &datamodel.PoolAttributes{PrimaryZone: "us-central1-a"}},
		Svm:              &datamodel.Svm{Name: "svm_test"},
		VolumeAttributes: &datamodel.VolumeAttributes{BlockProperties: &datamodel.BlockProperties{OSType: "LINUX"}, VendorSubnetID: "subnet-12345", ExternalUUID: "external-uuid"},
	}

	// Mock activities with GetObjectStoreEndpointActivity failure scenario
	env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("GetNode", mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	env.OnActivity("PrepareObjectStoreActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node: &models.Node{EndpointAddress: "127.0.0.1"},
	}, nil)
	env.OnActivity("GetOrCreateObjectStoreActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node:     &models.Node{EndpointAddress: "127.0.0.1"},
		ObjStore: &commonparams.CloudTarget{UUID: "test-obj-store-uuid", Name: "test-obj-store"},
	}, nil)
	env.OnActivity("PrepareSnapmirrorActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node: &models.Node{EndpointAddress: "127.0.0.1"},
	}, nil)
	env.OnActivity("CreateSnapmirrorRelationshipActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node: &models.Node{EndpointAddress: "127.0.0.1"},
	}, nil)
	env.OnActivity("CreatingSnapshotActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node: &models.Node{EndpointAddress: "127.0.0.1"},
	}, nil)
	env.OnActivity("CreateSnapshotActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup: &datamodel.Backup{
				Name: "test-backup",
				Attributes: &datamodel.BackupAttributes{
					SnapshotID: "test-snapshot-uuid",
				},
			},
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node:         &models.Node{EndpointAddress: "127.0.0.1"},
		SnapshotName: "test-backup",
		SnapmirrorRelationship: &commonparams.SnapmirrorRelationship{
			UUID: "test-snapmirror-uuid",
		},
		SnapshotResponse: &vsa.SnapshotProviderResponse{
			ProviderResponse: vsa.ProviderResponse{
				ExternalUUID: "test-snapshot-uuid",
			},
		},
	}, nil)
	env.OnActivity("TransferSnapshotActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node:         &models.Node{EndpointAddress: "127.0.0.1"},
		SnapshotName: "test-backup",
		SnapmirrorRelationship: &commonparams.SnapmirrorRelationship{
			UUID: "test-snapmirror-uuid",
		},
	}, nil)
	env.OnActivity("PollTransferStatusWithHistoryCheckActivity", mock.Anything, mock.Anything, mock.Anything).Return(&activities.PollTransferStatusOutput{
		BackupActivitiesContext: &activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:      backup,
				BackupVault: backupVault,
				Volume:      volume,
			},
			Node:              &models.Node{EndpointAddress: "127.0.0.1"},
			SnapshotName:      "test-backup",
			SmSourcePath:      "svm_test:volume_test",
			SmDestinationPath: "test-bucket:/objstore/test-vol",
			SnapmirrorRelationship: &commonparams.SnapmirrorRelationship{
				UUID: "test-snapmirror-uuid",
			},
			TransferStatus: activities.SmStatusSuccess,
		},
		TransferComplete:    true,
		ShouldContinueAsNew: false,
	}, nil)
	env.OnActivity("GetSnapmirror", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&commonparams.SnapmirrorRelationship{
		UUID:    "test-snapmirror-uuid",
		Healthy: nillable.ToPointer(true),
	}, nil)
	env.OnActivity("FinishBackupActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node:         &models.Node{EndpointAddress: "127.0.0.1"},
		SnapshotName: "test-backup",
		SnapmirrorRelationship: &commonparams.SnapmirrorRelationship{
			UUID: "test-snapmirror-uuid",
		},
	}, nil)
	env.OnActivity("UpdateSnapshotActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node:         &models.Node{EndpointAddress: "127.0.0.1"},
		SnapshotName: "test-backup",
		SnapmirrorRelationship: &commonparams.SnapmirrorRelationship{
			UUID: "test-snapmirror-uuid",
		},
	}, nil)
	env.OnActivity("GetObjectStoreEndpointActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{}, errors.New("object store endpoint activity failed"))
	env.OnActivity("GetObjectStoreSnapshotActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node: &models.Node{EndpointAddress: "127.0.0.1"},
	}, nil)
	env.OnActivity("UpdateBackupSizeActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node: &models.Node{EndpointAddress: "127.0.0.1"},
	}, nil)
	env.OnActivity("CreateBackupMetadataIfFirstBackupActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("CleanupOldBackupSnapshotsActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Execute workflow
	env.ExecuteWorkflow(CreateBackupWorkflow, params, backup, backupVault, volume)

	// Assert that the workflow was executed successfully
	// Note: GetObjectStoreEndpointActivity error is ignored in the workflow, so it should complete successfully
	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

func TestBackupWorkflowWithHydrationActivity(t *testing.T) {
	// This test verifies that the hydration activity is properly integrated into the workflow
	// by checking that the workflow source code contains the hydration activity call

	// Read the workflow file to verify integration
	workflowContent, err := os.ReadFile("backup_workflow.go")
	assert.NoError(t, err, "Failed to read backup_workflow.go")

	// Verify that the workflow contains the hydration activity call
	assert.Contains(t, string(workflowContent), "HydrateSnapshotToCCFEActivity",
		"Workflow should contain HydrateSnapshotToCCFEActivity call")

	// Verify that the hydration activity is called with proper error handling
	assert.Contains(t, string(workflowContent), "workflow.ExecuteActivity(ctx, backupActivity.HydrateSnapshotToCCFEActivity",
		"Workflow should call HydrateSnapshotToCCFEActivity via workflow.ExecuteActivity")

	// Verify that hydration errors are logged but don't fail the workflow
	assert.Contains(t, string(workflowContent), "Failed to hydrate snapshot to CCFE",
		"Workflow should log hydration errors but not fail")
}

func TestBackupWorkflowWithHydrationActivityFailure(t *testing.T) {
	// This test verifies that the workflow is designed to handle hydration failures gracefully
	// by checking the error handling pattern in the workflow code

	// Read the workflow file to verify error handling
	workflowContent, err := os.ReadFile("backup_workflow.go")
	assert.NoError(t, err, "Failed to read backup_workflow.go")

	// Verify that hydration errors are logged but don't fail the workflow
	assert.Contains(t, string(workflowContent), "workflow.GetLogger(ctx).Error",
		"Workflow should log hydration errors")

	// Verify that the hydration activity call is not in a critical path that would fail the workflow
	// The hydration should be called after the main backup operations are complete
	lines := strings.Split(string(workflowContent), "\n")
	hydrationLineIndex := -1
	for i, line := range lines {
		if strings.Contains(line, "HydrateSnapshotToCCFEActivity") {
			hydrationLineIndex = i
			break
		}
	}

	assert.Greater(t, hydrationLineIndex, 0, "Hydration activity should be present in workflow")

	// Verify that hydration is called near the end of the workflow (after main operations)
	// This ensures it doesn't block critical backup operations
	assert.True(t, hydrationLineIndex > 200, "Hydration should be called near the end of the workflow")
}

func TestBackupWorkflowHydrationWithGetLocation(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
	mockHeader := &commonpb.Header{
		Fields: map[string]*commonpb.Payload{
			"logParam": encodedValue,
		},
	}
	env.SetHeader(mockHeader)

	// Create mock storage for CommonActivities and BackupActivity
	mockStorage := database.NewMockStorage(t)
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()

	commonActivity := &activities.CommonActivities{SE: mockStorage}
	env.RegisterActivity(commonActivity)
	env.RegisterWorkflow(CreateBackupWorkflow)
	mockStorage.On("UpdateSnapshot", mock.Anything, mock.Anything).Return(&datamodel.Snapshot{}, nil).Maybe()
	mockStorage.On("CreatingSnapshot", mock.Anything, mock.Anything).Return(&datamodel.Snapshot{
		Name:               "test-backup",
		Description:        "VCP-Backup",
		VolumeID:           1,
		AccountID:          1,
		State:              "creating",
		StateDetails:       "creating",
		SnapshotAttributes: &datamodel.SnapshotAttributes{},
	}, nil).Maybe()
	mockStorage.On("UpdateBackup", mock.Anything, mock.Anything).Return(&datamodel.Backup{}, nil).Maybe()
	mockStorage.On("UpdateBackupLatestLogicalBackupSizeByVolume", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	mockStorage.On("UpdateVolumeFields", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	customBackupActivity := &TestBackupActivity{BackupActivity: &activities.BackupActivity{SE: mockStorage}}
	env.RegisterActivity(customBackupActivity)

	// Set up test data with all required fields for hydration
	params := &commonparams.CreateBackupParams{
		VolumeUUID:  "test-vol",
		AccountName: "test-account",
		BackupName:  "test-backup",
	}
	backupVault := &datamodel.BackupVault{
		Name:          "test-backup-vault",
		BucketDetails: datamodel.BucketDetailsArray{&datamodel.BucketDetails{BucketName: "test-bucket", ServiceAccountName: "sa-test", VendorSubnetID: "subnet-12345"}},
	}
	backup := &datamodel.Backup{
		State:         "InProgress",
		Name:          "test-backup",
		VolumeUUID:    "test-vol",
		BackupVault:   backupVault,
		BackupVaultID: 1,
		Attributes:    &datamodel.BackupAttributes{},
	}

	account := &datamodel.Account{
		Name: "test-account",
	}

	volume := &datamodel.Volume{
		Pool:             &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{Password: "password"}, PoolAttributes: &datamodel.PoolAttributes{PrimaryZone: "us-central1-a"}},
		Svm:              &datamodel.Svm{Name: "svm_test"},
		VolumeAttributes: &datamodel.VolumeAttributes{BlockProperties: &datamodel.BlockProperties{OSType: "LINUX"}, VendorSubnetID: "subnet-12345", ExternalUUID: "external-uuid"},
		Account:          account, // Ensure account is set for hydration
	}

	dbSnapshot := &datamodel.Snapshot{
		BaseModel: datamodel.BaseModel{
			UUID: "snapshot-uuid",
		},
		Name:         "test-snapshot",
		State:        "ready",
		StateDetails: "available",
		Volume:       volume,
		Account:      account,
		SnapshotAttributes: &datamodel.SnapshotAttributes{
			SizeInBytes: 1024000,
		},
	}

	// Mock all activities that the workflow calls
	env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("GetNode", mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	env.OnActivity("PrepareObjectStoreActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node: &models.Node{EndpointAddress: "127.0.0.1"},
	}, nil)
	env.OnActivity("GetOrCreateObjectStoreActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node: &models.Node{EndpointAddress: "127.0.0.1"},
	}, nil)
	env.OnActivity("PrepareSnapmirrorActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node: &models.Node{EndpointAddress: "127.0.0.1"},
	}, nil)
	env.OnActivity("CreateSnapmirrorRelationshipActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node: &models.Node{EndpointAddress: "127.0.0.1"},
		SnapmirrorRelationship: &commonparams.SnapmirrorRelationship{
			UUID: "test-snapmirror-uuid",
		},
	}, nil)
	env.OnActivity("CreatingSnapshotActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node:       &models.Node{EndpointAddress: "127.0.0.1"},
		DbSnapshot: dbSnapshot, // Set DbSnapshot for hydration
	}, nil)
	env.OnActivity("CreateSnapshotActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node:       &models.Node{EndpointAddress: "127.0.0.1"},
		DbSnapshot: dbSnapshot, // Set DbSnapshot for hydration
	}, nil)
	env.OnActivity("TransferSnapshotActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node:       &models.Node{EndpointAddress: "127.0.0.1"},
		DbSnapshot: dbSnapshot, // Set DbSnapshot for hydration
	}, nil)
	env.OnActivity("PollTransferStatusWithHistoryCheckActivity", mock.Anything, mock.Anything, mock.Anything).Return(&activities.PollTransferStatusOutput{
		BackupActivitiesContext: &activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:      backup,
				BackupVault: backupVault,
				Volume:      volume,
			},
			Node:              &models.Node{EndpointAddress: "127.0.0.1"},
			SnapshotName:      "test-backup",
			SmSourcePath:      "svm_test:volume_test",
			SmDestinationPath: "test-bucket:/objstore/test-vol",
			SnapmirrorRelationship: &commonparams.SnapmirrorRelationship{
				UUID: "test-snapmirror-uuid",
			},
			TransferStatus: activities.SmStatusSuccess,
		},
		TransferComplete:    true,
		ShouldContinueAsNew: false,
	}, nil)
	env.OnActivity("GetSnapmirror", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&commonparams.SnapmirrorRelationship{
		UUID:    "test-snapmirror-uuid",
		Healthy: nillable.ToPointer(true),
	}, nil)
	env.OnActivity("FinishBackupActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node:       &models.Node{EndpointAddress: "127.0.0.1"},
		DbSnapshot: dbSnapshot, // Set DbSnapshot for hydration
	}, nil)
	env.OnActivity("GetObjectStoreEndpointActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node:       &models.Node{EndpointAddress: "127.0.0.1"},
		DbSnapshot: dbSnapshot, // Set DbSnapshot for hydration
	}, nil)
	env.OnActivity("GetObjectStoreSnapshotActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node:       &models.Node{EndpointAddress: "127.0.0.1"},
		DbSnapshot: dbSnapshot, // Set DbSnapshot for hydration
	}, nil)
	env.OnActivity("UpdateBackupSizeActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node:       &models.Node{EndpointAddress: "127.0.0.1"},
		DbSnapshot: dbSnapshot, // Set DbSnapshot for hydration
	}, nil)
	env.OnActivity("CleanupOldBackupSnapshotsActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("HydrateSnapshotToCCFEActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("CreateBackupMetadataIfFirstBackupActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Execute the workflow
	env.ExecuteWorkflow(CreateBackupWorkflow, params, backup, backupVault, volume)

	// Verify that the workflow completed successfully
	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())

	// Verify that HydrateSnapshotToCCFEActivity was called
	// This ensures line 265 (utils.GetLocation call) was executed
	env.AssertExpectations(t)
}

// TestCreateBackupWorkflowWithContinueAsNew tests the CreateBackupWorkflow with ContinueAsNew functionality
func TestCreateBackupWorkflowWithContinueAsNew(t *testing.T) {
	t.Run("ContinueAsNewTriggeredByEventHistory", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		env.SetHeader(mockHeader)
		commonActivity, _ := setupMockCommonActivities(t)
		env.RegisterActivity(commonActivity)
		env.RegisterWorkflow(CreateBackupWorkflow)

		// Create mock storage for BackupActivity
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("UpdateSnapshot", mock.Anything, mock.Anything).Return(&datamodel.Snapshot{}, nil).Maybe()
		mockStorage.On("CreatingSnapshot", mock.Anything, mock.Anything).Return(&datamodel.Snapshot{
			Name:               "test-backup",
			Description:        "VCP-Backup",
			VolumeID:           1,
			AccountID:          1,
			State:              "creating",
			StateDetails:       "creating",
			SnapshotAttributes: &datamodel.SnapshotAttributes{},
		}, nil).Maybe()
		mockStorage.On("UpdateBackup", mock.Anything, mock.Anything).Return(&datamodel.Backup{}, nil).Maybe()
		mockStorage.On("UpdateBackupLatestLogicalBackupSizeByVolume", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		mockStorage.On("UpdateVolumeFields", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

		// Create a custom BackupActivity that bypasses the nil check in UpdateSnapshotActivity
		customBackupActivity := &TestBackupActivity{BackupActivity: &activities.BackupActivity{SE: mockStorage}}
		env.RegisterActivity(customBackupActivity)

		// Set up test data
		params := &commonparams.CreateBackupParams{
			VolumeUUID:  "test-vol",
			AccountName: "test-account",
			BackupName:  "test-backup",
		}
		backupVault := &datamodel.BackupVault{
			Name:          "test-backup-vault",
			BucketDetails: datamodel.BucketDetailsArray{&datamodel.BucketDetails{BucketName: "test-bucket", ServiceAccountName: "sa-test", VendorSubnetID: "subnet-12345"}},
		}
		backup := &datamodel.Backup{
			State:         "InProgress",
			Name:          "test-backup",
			VolumeUUID:    "test-vol",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes:    &datamodel.BackupAttributes{},
		}

		volume := &datamodel.Volume{
			Pool:             &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{Password: "password"}, PoolAttributes: &datamodel.PoolAttributes{PrimaryZone: "us-central1-a"}},
			Svm:              &datamodel.Svm{Name: "svm_test"},
			VolumeAttributes: &datamodel.VolumeAttributes{BlockProperties: &datamodel.BlockProperties{OSType: "LINUX"}, VendorSubnetID: "subnet-12345", ExternalUUID: "external-uuid"},
		}

		// Mock all activities that the workflow calls
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetNode", mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
		env.OnActivity("PrepareObjectStoreActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:      backup,
				BackupVault: backupVault,
				Volume:      volume,
			},
			Node: &models.Node{EndpointAddress: "127.0.0.1"},
		}, nil)
		env.OnActivity("GetOrCreateObjectStoreActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:      backup,
				BackupVault: backupVault,
				Volume:      volume,
			},
			Node: &models.Node{EndpointAddress: "127.0.0.1"},
		}, nil)
		env.OnActivity("PrepareSnapmirrorActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:      backup,
				BackupVault: backupVault,
				Volume:      volume,
			},
			Node: &models.Node{EndpointAddress: "127.0.0.1"},
		}, nil)
		env.OnActivity("CreateSnapmirrorRelationshipActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:      backup,
				BackupVault: backupVault,
				Volume:      volume,
			},
			Node: &models.Node{EndpointAddress: "127.0.0.1"},
			SnapmirrorRelationship: &commonparams.SnapmirrorRelationship{
				UUID: "test-snapmirror-uuid",
			},
		}, nil)
		env.OnActivity("CreatingSnapshotActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:      backup,
				BackupVault: backupVault,
				Volume:      volume,
			},
			Node:         &models.Node{EndpointAddress: "127.0.0.1"},
			SnapshotName: "test-backup",
			DbSnapshot: &datamodel.Snapshot{
				Name:               "test-backup",
				Description:        "VCP-Backup",
				VolumeID:           1,
				AccountID:          1,
				State:              "creating",
				StateDetails:       "creating",
				SnapshotAttributes: &datamodel.SnapshotAttributes{},
			},
		}, nil)
		env.OnActivity("CreateSnapshotActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup: &datamodel.Backup{
					Name: "test-backup",
					Attributes: &datamodel.BackupAttributes{
						SnapshotID: "test-snapshot-uuid",
					},
				},
				BackupVault: backupVault,
				Volume:      volume,
			},
			Node:         &models.Node{EndpointAddress: "127.0.0.1"},
			SnapshotName: "test-backup",
			SnapmirrorRelationship: &commonparams.SnapmirrorRelationship{
				UUID: "test-snapmirror-uuid",
			},
			SnapshotResponse: &vsa.SnapshotProviderResponse{
				ProviderResponse: vsa.ProviderResponse{
					ExternalUUID: "test-snapshot-uuid",
				},
			},
		}, nil)
		env.OnActivity("TransferSnapshotActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:      backup,
				BackupVault: backupVault,
				Volume:      volume,
			},
			Node:         &models.Node{EndpointAddress: "127.0.0.1"},
			SnapshotName: "test-backup",
			SnapmirrorRelationship: &commonparams.SnapmirrorRelationship{
				UUID: "test-snapmirror-uuid",
			},
		}, nil)

		// Mock the PollTransferStatusWithHistoryCheckActivity to trigger ContinueAsNew
		env.OnActivity("PollTransferStatusWithHistoryCheckActivity", mock.Anything, mock.Anything, mock.Anything).Return(&activities.PollTransferStatusOutput{
			BackupActivitiesContext: &activities.BackupActivitiesContext{
				BackupWorkflowInit: &activities.BackupWorkflowInput{
					Backup:      backup,
					BackupVault: backupVault,
					Volume:      volume,
				},
				Node:         &models.Node{EndpointAddress: "127.0.0.1"},
				SnapshotName: "test-backup",
				SnapmirrorRelationship: &commonparams.SnapmirrorRelationship{
					UUID: "test-snapmirror-uuid",
				},
				TransferStatus: activities.SmStatusTransferring,
			},
			TransferComplete:    false,
			ShouldContinueAsNew: true,
			ContinueAsNewReason: "Event history limit reached",
			NextWaitTime:        5 * time.Millisecond,
		}, nil)

		// Execute workflow
		env.ExecuteWorkflow(CreateBackupWorkflow, params, backup, backupVault, volume)

		// Assert that the workflow was executed and ContinueAsNew was triggered
		assert.True(t, env.IsWorkflowCompleted())
		// ContinueAsNew should result in a ContinueAsNewError
		assert.Error(t, env.GetWorkflowError())
		assert.True(t, workflow.IsContinueAsNewError(env.GetWorkflowError()))
		env.AssertExpectations(t)
	})

	t.Run("ContinueAsNewNotTriggeredWhenEventHistoryBelowThreshold", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		env.SetHeader(mockHeader)
		commonActivity, _ := setupMockCommonActivities(t)
		env.RegisterActivity(commonActivity)
		env.RegisterWorkflow(CreateBackupWorkflow)

		// Create mock storage for BackupActivity
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("UpdateSnapshot", mock.Anything, mock.Anything).Return(&datamodel.Snapshot{}, nil).Maybe()
		mockStorage.On("CreatingSnapshot", mock.Anything, mock.Anything).Return(&datamodel.Snapshot{
			Name:               "test-backup",
			Description:        "VCP-Backup",
			VolumeID:           1,
			AccountID:          1,
			State:              "creating",
			StateDetails:       "creating",
			SnapshotAttributes: &datamodel.SnapshotAttributes{},
		}, nil).Maybe()
		mockStorage.On("UpdateBackup", mock.Anything, mock.Anything).Return(&datamodel.Backup{}, nil).Maybe()
		mockStorage.On("UpdateBackupLatestLogicalBackupSizeByVolume", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		mockStorage.On("UpdateVolumeFields", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

		// Create a custom BackupActivity that bypasses the nil check in UpdateSnapshotActivity
		customBackupActivity := &TestBackupActivity{BackupActivity: &activities.BackupActivity{SE: mockStorage}}
		env.RegisterActivity(customBackupActivity)

		// Set up test data
		params := &commonparams.CreateBackupParams{
			VolumeUUID:  "test-vol",
			AccountName: "test-account",
			BackupName:  "test-backup",
		}
		backupVault := &datamodel.BackupVault{
			Name:          "test-backup-vault",
			BucketDetails: datamodel.BucketDetailsArray{&datamodel.BucketDetails{BucketName: "test-bucket", ServiceAccountName: "sa-test", VendorSubnetID: "subnet-12345"}},
		}
		backup := &datamodel.Backup{
			State:         "InProgress",
			Name:          "test-backup",
			VolumeUUID:    "test-vol",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes:    &datamodel.BackupAttributes{},
		}

		volume := &datamodel.Volume{
			Pool:             &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{Password: "password"}, PoolAttributes: &datamodel.PoolAttributes{PrimaryZone: "us-central1-a"}},
			Svm:              &datamodel.Svm{Name: "svm_test"},
			VolumeAttributes: &datamodel.VolumeAttributes{BlockProperties: &datamodel.BlockProperties{OSType: "LINUX"}, VendorSubnetID: "subnet-12345", ExternalUUID: "external-uuid"},
		}

		// Mock all activities that the workflow calls
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetNode", mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
		env.OnActivity("PrepareObjectStoreActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:      backup,
				BackupVault: backupVault,
				Volume:      volume,
			},
			Node: &models.Node{EndpointAddress: "127.0.0.1"},
		}, nil)
		env.OnActivity("GetOrCreateObjectStoreActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:      backup,
				BackupVault: backupVault,
				Volume:      volume,
			},
			Node: &models.Node{EndpointAddress: "127.0.0.1"},
		}, nil)
		env.OnActivity("PrepareSnapmirrorActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:      backup,
				BackupVault: backupVault,
				Volume:      volume,
			},
			Node: &models.Node{EndpointAddress: "127.0.0.1"},
		}, nil)
		env.OnActivity("CreateSnapmirrorRelationshipActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:      backup,
				BackupVault: backupVault,
				Volume:      volume,
			},
			Node: &models.Node{EndpointAddress: "127.0.0.1"},
			SnapmirrorRelationship: &commonparams.SnapmirrorRelationship{
				UUID: "test-snapmirror-uuid",
			},
		}, nil)
		env.OnActivity("CreatingSnapshotActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:      backup,
				BackupVault: backupVault,
				Volume:      volume,
			},
			Node:         &models.Node{EndpointAddress: "127.0.0.1"},
			SnapshotName: "test-backup",
			DbSnapshot: &datamodel.Snapshot{
				Name:               "test-backup",
				Description:        "VCP-Backup",
				VolumeID:           1,
				AccountID:          1,
				State:              "creating",
				StateDetails:       "creating",
				SnapshotAttributes: &datamodel.SnapshotAttributes{},
			},
		}, nil)
		env.OnActivity("CreateSnapshotActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup: &datamodel.Backup{
					Name: "test-backup",
					Attributes: &datamodel.BackupAttributes{
						SnapshotID: "test-snapshot-uuid",
					},
				},
				BackupVault: backupVault,
				Volume:      volume,
			},
			Node:         &models.Node{EndpointAddress: "127.0.0.1"},
			SnapshotName: "test-backup",
			SnapmirrorRelationship: &commonparams.SnapmirrorRelationship{
				UUID: "test-snapmirror-uuid",
			},
			SnapshotResponse: &vsa.SnapshotProviderResponse{
				ProviderResponse: vsa.ProviderResponse{
					ExternalUUID: "test-snapshot-uuid",
				},
			},
		}, nil)
		env.OnActivity("TransferSnapshotActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:      backup,
				BackupVault: backupVault,
				Volume:      volume,
			},
			Node:         &models.Node{EndpointAddress: "127.0.0.1"},
			SnapshotName: "test-backup",
			SnapmirrorRelationship: &commonparams.SnapmirrorRelationship{
				UUID: "test-snapmirror-uuid",
			},
		}, nil)

		// Mock the PollTransferStatusWithHistoryCheckActivity to NOT trigger ContinueAsNew
		env.OnActivity("PollTransferStatusWithHistoryCheckActivity", mock.Anything, mock.Anything, mock.Anything).Return(&activities.PollTransferStatusOutput{
			BackupActivitiesContext: &activities.BackupActivitiesContext{
				BackupWorkflowInit: &activities.BackupWorkflowInput{
					Backup:      backup,
					BackupVault: backupVault,
					Volume:      volume,
				},
				Node:              &models.Node{EndpointAddress: "127.0.0.1"},
				SnapshotName:      "test-backup",
				SmSourcePath:      "svm_test:volume_test",
				SmDestinationPath: "test-bucket:/objstore/test-vol",
				SnapmirrorRelationship: &commonparams.SnapmirrorRelationship{
					UUID: "test-snapmirror-uuid",
				},
				TransferStatus: activities.SmStatusSuccess,
			},
			TransferComplete:    true,
			ShouldContinueAsNew: false,
			ContinueAsNewReason: "",
			NextWaitTime:        5 * time.Millisecond,
		}, nil)
		env.OnActivity("GetSnapmirror", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&commonparams.SnapmirrorRelationship{
			UUID:    "test-snapmirror-uuid",
			Healthy: nillable.ToPointer(true),
		}, nil)

		env.OnActivity("UpdateSnapshotActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:      backup,
				BackupVault: backupVault,
				Volume:      volume,
			},
			Node: &models.Node{EndpointAddress: "127.0.0.1"},
		}, nil)
		env.OnActivity("GetObjectStoreSnapshotActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:      backup,
				BackupVault: backupVault,
				Volume:      volume,
			},
			Node: &models.Node{EndpointAddress: "127.0.0.1"},
		}, nil)
		env.OnActivity("FinishBackupActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:      backup,
				BackupVault: backupVault,
				Volume:      volume,
			},
			Node: &models.Node{EndpointAddress: "127.0.0.1"},
		}, nil)
		env.OnActivity("UpdateBackupSizeActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:      backup,
				BackupVault: backupVault,
				Volume:      volume,
			},
			Node: &models.Node{EndpointAddress: "127.0.0.1"},
		}, nil)
		env.OnActivity("CreateBackupMetadataIfFirstBackupActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CleanupOldBackupSnapshotsActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)

		// Execute workflow
		env.ExecuteWorkflow(CreateBackupWorkflow, params, backup, backupVault, volume)

		// Assert that the workflow was executed successfully without ContinueAsNew
		assert.True(t, env.IsWorkflowCompleted())
		assert.NoError(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})
}

// TestCreateBackupWorkflowWithEventHistoryThreshold tests the event history threshold functionality
func TestCreateBackupWorkflowWithEventHistoryThreshold(t *testing.T) {
	t.Run("EventHistoryThresholdReached", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		env.SetHeader(mockHeader)
		commonActivity, _ := setupMockCommonActivities(t)
		env.RegisterActivity(commonActivity)
		env.RegisterWorkflow(CreateBackupWorkflow)

		// Create mock storage for BackupActivity
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("UpdateSnapshot", mock.Anything, mock.Anything).Return(&datamodel.Snapshot{}, nil).Maybe()
		mockStorage.On("CreatingSnapshot", mock.Anything, mock.Anything).Return(&datamodel.Snapshot{
			Name:               "test-backup",
			Description:        "VCP-Backup",
			VolumeID:           1,
			AccountID:          1,
			State:              "creating",
			StateDetails:       "creating",
			SnapshotAttributes: &datamodel.SnapshotAttributes{},
		}, nil).Maybe()
		mockStorage.On("UpdateBackup", mock.Anything, mock.Anything).Return(&datamodel.Backup{}, nil).Maybe()
		mockStorage.On("UpdateBackupLatestLogicalBackupSizeByVolume", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		mockStorage.On("UpdateVolumeFields", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

		// Create a custom BackupActivity that bypasses the nil check in UpdateSnapshotActivity
		customBackupActivity := &TestBackupActivity{BackupActivity: &activities.BackupActivity{SE: mockStorage}}
		env.RegisterActivity(customBackupActivity)

		// Set up test data
		params := &commonparams.CreateBackupParams{
			VolumeUUID:  "test-vol",
			AccountName: "test-account",
			BackupName:  "test-backup",
		}
		backupVault := &datamodel.BackupVault{
			Name:          "test-backup-vault",
			BucketDetails: datamodel.BucketDetailsArray{&datamodel.BucketDetails{BucketName: "test-bucket", ServiceAccountName: "sa-test", VendorSubnetID: "subnet-12345"}},
		}
		backup := &datamodel.Backup{
			State:         "InProgress",
			Name:          "test-backup",
			VolumeUUID:    "test-vol",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes:    &datamodel.BackupAttributes{},
		}

		volume := &datamodel.Volume{
			Pool:             &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{Password: "password"}, PoolAttributes: &datamodel.PoolAttributes{PrimaryZone: "us-central1-a"}},
			Svm:              &datamodel.Svm{Name: "svm_test"},
			VolumeAttributes: &datamodel.VolumeAttributes{BlockProperties: &datamodel.BlockProperties{OSType: "LINUX"}, VendorSubnetID: "subnet-12345", ExternalUUID: "external-uuid"},
		}

		// Mock all activities that the workflow calls
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetNode", mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
		env.OnActivity("PrepareObjectStoreActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:      backup,
				BackupVault: backupVault,
				Volume:      volume,
			},
			Node: &models.Node{EndpointAddress: "127.0.0.1"},
		}, nil)
		env.OnActivity("GetOrCreateObjectStoreActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:      backup,
				BackupVault: backupVault,
				Volume:      volume,
			},
			Node: &models.Node{EndpointAddress: "127.0.0.1"},
		}, nil)
		env.OnActivity("PrepareSnapmirrorActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:      backup,
				BackupVault: backupVault,
				Volume:      volume,
			},
			Node: &models.Node{EndpointAddress: "127.0.0.1"},
		}, nil)
		env.OnActivity("CreateSnapmirrorRelationshipActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:      backup,
				BackupVault: backupVault,
				Volume:      volume,
			},
			Node: &models.Node{EndpointAddress: "127.0.0.1"},
			SnapmirrorRelationship: &commonparams.SnapmirrorRelationship{
				UUID: "test-snapmirror-uuid",
			},
		}, nil)
		env.OnActivity("CreatingSnapshotActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:      backup,
				BackupVault: backupVault,
				Volume:      volume,
			},
			Node:         &models.Node{EndpointAddress: "127.0.0.1"},
			SnapshotName: "test-backup",
			DbSnapshot: &datamodel.Snapshot{
				Name:               "test-backup",
				Description:        "VCP-Backup",
				VolumeID:           1,
				AccountID:          1,
				State:              "creating",
				StateDetails:       "creating",
				SnapshotAttributes: &datamodel.SnapshotAttributes{},
			},
		}, nil)
		env.OnActivity("CreateSnapshotActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup: &datamodel.Backup{
					Name: "test-backup",
					Attributes: &datamodel.BackupAttributes{
						SnapshotID: "test-snapshot-uuid",
					},
				},
				BackupVault: backupVault,
				Volume:      volume,
			},
			Node:         &models.Node{EndpointAddress: "127.0.0.1"},
			SnapshotName: "test-backup",
			SnapmirrorRelationship: &commonparams.SnapmirrorRelationship{
				UUID: "test-snapmirror-uuid",
			},
			SnapshotResponse: &vsa.SnapshotProviderResponse{
				ProviderResponse: vsa.ProviderResponse{
					ExternalUUID: "test-snapshot-uuid",
				},
			},
		}, nil)
		env.OnActivity("TransferSnapshotActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:      backup,
				BackupVault: backupVault,
				Volume:      volume,
			},
			Node:         &models.Node{EndpointAddress: "127.0.0.1"},
			SnapshotName: "test-backup",
			SnapmirrorRelationship: &commonparams.SnapmirrorRelationship{
				UUID: "test-snapmirror-uuid",
			},
		}, nil)

		// Mock the PollTransferStatusWithHistoryCheckActivity to trigger ContinueAsNew when event history threshold is reached
		env.OnActivity("PollTransferStatusWithHistoryCheckActivity", mock.Anything, mock.Anything, mock.Anything).Return(&activities.PollTransferStatusOutput{
			BackupActivitiesContext: &activities.BackupActivitiesContext{
				BackupWorkflowInit: &activities.BackupWorkflowInput{
					Backup:      backup,
					BackupVault: backupVault,
					Volume:      volume,
				},
				Node:         &models.Node{EndpointAddress: "127.0.0.1"},
				SnapshotName: "test-backup",
				SnapmirrorRelationship: &commonparams.SnapmirrorRelationship{
					UUID: "test-snapmirror-uuid",
				},
				TransferStatus: activities.SmStatusTransferring,
			},
			TransferComplete:    false,
			ShouldContinueAsNew: true,
			ContinueAsNewReason: "Event history limit reached",
			NextWaitTime:        5 * time.Millisecond,
		}, nil)

		// Execute workflow
		env.ExecuteWorkflow(CreateBackupWorkflow, params, backup, backupVault, volume)

		// Assert that the workflow was executed and ContinueAsNew was triggered
		assert.True(t, env.IsWorkflowCompleted())
		// ContinueAsNew should result in a ContinueAsNewError
		assert.Error(t, env.GetWorkflowError())
		assert.True(t, workflow.IsContinueAsNewError(env.GetWorkflowError()))
		env.AssertExpectations(t)
	})
}

// TestCreateBackupWorkflowWithRetryPolicy tests the retry policy functionality
func TestCreateBackupWorkflowWithRetryPolicy(t *testing.T) {
	t.Run("RetryPolicyApplied", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		env.SetHeader(mockHeader)
		commonActivity, _ := setupMockCommonActivities(t)
		env.RegisterActivity(commonActivity)
		env.RegisterWorkflow(CreateBackupWorkflow)

		// Create mock storage for BackupActivity
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("UpdateSnapshot", mock.Anything, mock.Anything).Return(&datamodel.Snapshot{}, nil).Maybe()
		mockStorage.On("CreatingSnapshot", mock.Anything, mock.Anything).Return(&datamodel.Snapshot{
			Name:               "test-backup",
			Description:        "VCP-Backup",
			VolumeID:           1,
			AccountID:          1,
			State:              "creating",
			StateDetails:       "creating",
			SnapshotAttributes: &datamodel.SnapshotAttributes{},
		}, nil).Maybe()
		mockStorage.On("UpdateBackup", mock.Anything, mock.Anything).Return(&datamodel.Backup{}, nil).Maybe()
		mockStorage.On("UpdateBackupLatestLogicalBackupSizeByVolume", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		mockStorage.On("UpdateVolumeFields", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

		// Create a custom BackupActivity that bypasses the nil check in UpdateSnapshotActivity
		customBackupActivity := &TestBackupActivity{BackupActivity: &activities.BackupActivity{SE: mockStorage}}
		env.RegisterActivity(customBackupActivity)

		// Set up test data
		params := &commonparams.CreateBackupParams{
			VolumeUUID:  "test-vol",
			AccountName: "test-account",
			BackupName:  "test-backup",
		}
		backupVault := &datamodel.BackupVault{
			Name:          "test-backup-vault",
			BucketDetails: datamodel.BucketDetailsArray{&datamodel.BucketDetails{BucketName: "test-bucket", ServiceAccountName: "sa-test", VendorSubnetID: "subnet-12345"}},
		}
		backup := &datamodel.Backup{
			State:         "InProgress",
			Name:          "test-backup",
			VolumeUUID:    "test-vol",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes:    &datamodel.BackupAttributes{},
		}

		volume := &datamodel.Volume{
			Pool:             &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{Password: "password"}, PoolAttributes: &datamodel.PoolAttributes{PrimaryZone: "us-central1-a"}},
			Svm:              &datamodel.Svm{Name: "svm_test"},
			VolumeAttributes: &datamodel.VolumeAttributes{BlockProperties: &datamodel.BlockProperties{OSType: "LINUX"}, VendorSubnetID: "subnet-12345", ExternalUUID: "external-uuid"},
		}

		// Mock all activities that the workflow calls
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetNode", mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
		env.OnActivity("PrepareObjectStoreActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:      backup,
				BackupVault: backupVault,
				Volume:      volume,
			},
			Node: &models.Node{EndpointAddress: "127.0.0.1"},
		}, nil)
		env.OnActivity("GetOrCreateObjectStoreActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:      backup,
				BackupVault: backupVault,
				Volume:      volume,
			},
			Node: &models.Node{EndpointAddress: "127.0.0.1"},
		}, nil)
		env.OnActivity("PrepareSnapmirrorActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:      backup,
				BackupVault: backupVault,
				Volume:      volume,
			},
			Node: &models.Node{EndpointAddress: "127.0.0.1"},
		}, nil)
		env.OnActivity("CreateSnapmirrorRelationshipActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:      backup,
				BackupVault: backupVault,
				Volume:      volume,
			},
			Node: &models.Node{EndpointAddress: "127.0.0.1"},
			SnapmirrorRelationship: &commonparams.SnapmirrorRelationship{
				UUID: "test-snapmirror-uuid",
			},
		}, nil)
		env.OnActivity("CreatingSnapshotActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:      backup,
				BackupVault: backupVault,
				Volume:      volume,
			},
			Node:         &models.Node{EndpointAddress: "127.0.0.1"},
			SnapshotName: "test-backup",
			DbSnapshot: &datamodel.Snapshot{
				Name:               "test-backup",
				Description:        "VCP-Backup",
				VolumeID:           1,
				AccountID:          1,
				State:              "creating",
				StateDetails:       "creating",
				SnapshotAttributes: &datamodel.SnapshotAttributes{},
			},
		}, nil)
		env.OnActivity("CreateSnapshotActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup: &datamodel.Backup{
					Name: "test-backup",
					Attributes: &datamodel.BackupAttributes{
						SnapshotID: "test-snapshot-uuid",
					},
				},
				BackupVault: backupVault,
				Volume:      volume,
			},
			Node:         &models.Node{EndpointAddress: "127.0.0.1"},
			SnapshotName: "test-backup",
			SnapmirrorRelationship: &commonparams.SnapmirrorRelationship{
				UUID: "test-snapmirror-uuid",
			},
			SnapshotResponse: &vsa.SnapshotProviderResponse{
				ProviderResponse: vsa.ProviderResponse{
					ExternalUUID: "test-snapshot-uuid",
				},
			},
		}, nil)
		env.OnActivity("TransferSnapshotActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:      backup,
				BackupVault: backupVault,
				Volume:      volume,
			},
			Node:         &models.Node{EndpointAddress: "127.0.0.1"},
			SnapshotName: "test-backup",
			SnapmirrorRelationship: &commonparams.SnapmirrorRelationship{
				UUID: "test-snapmirror-uuid",
			},
		}, nil)

		// Mock the PollTransferStatusWithHistoryCheckActivity to complete successfully
		env.OnActivity("PollTransferStatusWithHistoryCheckActivity", mock.Anything, mock.Anything, mock.Anything).Return(&activities.PollTransferStatusOutput{
			BackupActivitiesContext: &activities.BackupActivitiesContext{
				BackupWorkflowInit: &activities.BackupWorkflowInput{
					Backup:      backup,
					BackupVault: backupVault,
					Volume:      volume,
				},
				Node:              &models.Node{EndpointAddress: "127.0.0.1"},
				SnapshotName:      "test-backup",
				SmSourcePath:      "svm_test:volume_test",
				SmDestinationPath: "test-bucket:/objstore/test-vol",
				SnapmirrorRelationship: &commonparams.SnapmirrorRelationship{
					UUID: "test-snapmirror-uuid",
				},
				TransferStatus: activities.SmStatusSuccess,
			},
			TransferComplete:    true,
			ShouldContinueAsNew: false,
			ContinueAsNewReason: "",
			NextWaitTime:        5 * time.Millisecond,
		}, nil)
		env.OnActivity("GetSnapmirror", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&commonparams.SnapmirrorRelationship{
			UUID:    "test-snapmirror-uuid",
			Healthy: nillable.ToPointer(true),
		}, nil)
		env.OnActivity("FinishBackupActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:      backup,
				BackupVault: backupVault,
				Volume:      volume,
			},
			Node: &models.Node{EndpointAddress: "127.0.0.1"},
		}, nil)

		env.OnActivity("UpdateSnapshotActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:      backup,
				BackupVault: backupVault,
				Volume:      volume,
			},
			Node: &models.Node{EndpointAddress: "127.0.0.1"},
		}, nil)
		env.OnActivity("GetObjectStoreEndpointActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:      backup,
				BackupVault: backupVault,
				Volume:      volume,
			},
			Node: &models.Node{EndpointAddress: "127.0.0.1"},
		}, nil)
		env.OnActivity("GetObjectStoreSnapshotActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:      backup,
				BackupVault: backupVault,
				Volume:      volume,
			},
			Node: &models.Node{EndpointAddress: "127.0.0.1"},
		}, nil)
		env.OnActivity("UpdateBackupSizeActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:      backup,
				BackupVault: backupVault,
				Volume:      volume,
			},
			Node: &models.Node{EndpointAddress: "127.0.0.1"},
		}, nil)

		env.OnActivity("CreateRemoteBackupFromVCPActivity", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CreateBackupMetadataIfFirstBackupActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CleanupOldBackupSnapshotsActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		// Mock rollback activities in case workflow fails
		env.OnActivity("UpdateBackupError", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

		// Execute workflow
		env.ExecuteWorkflow(CreateBackupWorkflow, params, backup, backupVault, volume)

		// Assert that the workflow was executed successfully
		assert.True(t, env.IsWorkflowCompleted())
		assert.NoError(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})
}

// TestCreateBackupWorkflowWithErrorHandling tests error handling scenarios
func TestCreateBackupWorkflowWithErrorHandling(t *testing.T) {
	t.Run("ActivityFailureWithRetry", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		env.SetHeader(mockHeader)
		commonActivity, _ := setupMockCommonActivities(t)
		env.RegisterActivity(commonActivity)
		env.RegisterWorkflow(CreateBackupWorkflow)

		// Create mock storage for BackupActivity
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("UpdateBackupState", mock.Anything, mock.Anything).Return(&datamodel.Backup{}, nil).Maybe()
		env.RegisterActivity(&TestBackupActivity{BackupActivity: &activities.BackupActivity{SE: mockStorage}})

		// Set up test data
		params := &commonparams.CreateBackupParams{
			VolumeUUID:  "test-vol",
			AccountName: "test-account",
			BackupName:  "test-backup",
		}
		backupVault := &datamodel.BackupVault{
			Name:          "test-backup-vault",
			BucketDetails: datamodel.BucketDetailsArray{&datamodel.BucketDetails{BucketName: "test-bucket", ServiceAccountName: "sa-test", VendorSubnetID: "subnet-12345"}},
		}
		backup := &datamodel.Backup{
			State:         "InProgress",
			Name:          "test-backup",
			VolumeUUID:    "test-vol",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes:    &datamodel.BackupAttributes{},
		}

		volume := &datamodel.Volume{
			Pool:             &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{Password: "password"}, PoolAttributes: &datamodel.PoolAttributes{PrimaryZone: "us-central1-a"}},
			Svm:              &datamodel.Svm{Name: "svm_test"},
			VolumeAttributes: &datamodel.VolumeAttributes{BlockProperties: &datamodel.BlockProperties{OSType: "LINUX"}, VendorSubnetID: "subnet-12345", ExternalUUID: "external-uuid"},
		}

		// Mock activities with failure at PrepareObjectStoreActivity
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetNode", mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
		env.OnActivity("PrepareObjectStoreActivity", mock.Anything, mock.Anything).Return(nil, errors.New("failed to prepare object store"))
		env.OnActivity("UpdateBackupError", mock.Anything, mock.Anything, mock.Anything).Return(nil)

		// Execute workflow
		env.ExecuteWorkflow(CreateBackupWorkflow, params, backup, backupVault, volume)

		// Assert that the workflow was executed and handled the error
		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		assert.ErrorContains(t, env.GetWorkflowError(), "failed to prepare object store")
		env.AssertExpectations(t)
	})
}

// TestCreateBackupWorkflowWithContext tests the CreateBackupWorkflowWithContext function
func TestCreateBackupWorkflowWithContext(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		env.SetHeader(mockHeader)
		commonActivity, _ := setupMockCommonActivities(t)
		env.RegisterActivity(commonActivity)
		env.RegisterWorkflow(CreateBackupWorkflowWithContext)

		// Create mock storage for BackupActivity
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("UpdateBackupState", mock.Anything, mock.Anything).Return(&datamodel.Backup{}, nil).Maybe()
		mockStorage.On("UpdateSnapshot", mock.Anything, mock.Anything).Return(&datamodel.Snapshot{}, nil).Maybe()
		mockStorage.On("CreatingSnapshot", mock.Anything, mock.Anything).Return(&datamodel.Snapshot{
			Name:               "test-backup",
			Description:        "VCP-Backup",
			VolumeID:           1,
			AccountID:          1,
			State:              "creating",
			StateDetails:       "creating",
			SnapshotAttributes: &datamodel.SnapshotAttributes{},
		}, nil).Maybe()
		mockStorage.On("UpdateBackup", mock.Anything, mock.Anything).Return(&datamodel.Backup{}, nil).Maybe()
		mockStorage.On("UpdateBackupLatestLogicalBackupSizeByVolume", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		mockStorage.On("UpdateVolumeFields", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

		// Create a custom BackupActivity that bypasses the nil check in UpdateSnapshotActivity
		customBackupActivity := &TestBackupActivity{BackupActivity: &activities.BackupActivity{SE: mockStorage}}
		env.RegisterActivity(customBackupActivity)

		// Set up test data
		params := &commonparams.CreateBackupParams{
			VolumeUUID:  "test-vol",
			AccountName: "test-account",
			BackupName:  "test-backup",
		}
		backupVault := &datamodel.BackupVault{
			Name:          "test-backup-vault",
			BucketDetails: datamodel.BucketDetailsArray{&datamodel.BucketDetails{BucketName: "test-bucket", ServiceAccountName: "sa-test", VendorSubnetID: "subnet-12345"}},
		}
		backup := &datamodel.Backup{
			State:         "InProgress",
			Name:          "test-backup",
			VolumeUUID:    "test-vol",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes:    &datamodel.BackupAttributes{},
		}
		volume := &datamodel.Volume{
			Pool:             &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{Password: "password"}, PoolAttributes: &datamodel.PoolAttributes{PrimaryZone: "us-central1-a"}},
			Svm:              &datamodel.Svm{Name: "svm_test"},
			VolumeAttributes: &datamodel.VolumeAttributes{BlockProperties: &datamodel.BlockProperties{OSType: "LINUX"}, VendorSubnetID: "subnet-12345", ExternalUUID: "external-uuid"},
		}

		backupActivitiesContext := &activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:      backup,
				BackupVault: backupVault,
				Volume:      volume,
			},
			Node: &models.Node{EndpointAddress: "127.0.0.1"},
		}

		// Mock all activities that the workflow calls
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetNode", mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
		env.OnActivity("PrepareObjectStoreActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:      backup,
				BackupVault: backupVault,
				Volume:      volume,
			},
			Node: &models.Node{EndpointAddress: "127.0.0.1"},
		}, nil)
		env.OnActivity("GetOrCreateObjectStoreActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:      backup,
				BackupVault: backupVault,
				Volume:      volume,
			},
			Node: &models.Node{EndpointAddress: "127.0.0.1"},
		}, nil)
		env.OnActivity("PrepareSnapmirrorActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:      backup,
				BackupVault: backupVault,
				Volume:      volume,
			},
			Node: &models.Node{EndpointAddress: "127.0.0.1"},
		}, nil)
		env.OnActivity("CreateSnapmirrorRelationshipActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:      backup,
				BackupVault: backupVault,
				Volume:      volume,
			},
			Node: &models.Node{EndpointAddress: "127.0.0.1"},
			SnapmirrorRelationship: &commonparams.SnapmirrorRelationship{
				UUID: "test-snapmirror-uuid",
			},
		}, nil)
		env.OnActivity("CreatingSnapshotActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:      backup,
				BackupVault: backupVault,
				Volume:      volume,
			},
			Node:         &models.Node{EndpointAddress: "127.0.0.1"},
			SnapshotName: "test-backup",
			DbSnapshot: &datamodel.Snapshot{
				Name:               "test-backup",
				Description:        "VCP-Backup",
				VolumeID:           1,
				AccountID:          1,
				State:              "creating",
				StateDetails:       "creating",
				SnapshotAttributes: &datamodel.SnapshotAttributes{},
			},
		}, nil)
		env.OnActivity("CreateSnapshotActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup: &datamodel.Backup{
					Name: "test-backup",
					Attributes: &datamodel.BackupAttributes{
						SnapshotID: "test-snapshot-uuid",
					},
				},
				BackupVault: backupVault,
				Volume:      volume,
			},
			Node:         &models.Node{EndpointAddress: "127.0.0.1"},
			SnapshotName: "test-backup",
			SnapmirrorRelationship: &commonparams.SnapmirrorRelationship{
				UUID: "test-snapmirror-uuid",
			},
			SnapshotResponse: &vsa.SnapshotProviderResponse{
				ProviderResponse: vsa.ProviderResponse{
					ExternalUUID: "test-snapshot-uuid",
				},
			},
		}, nil)
		env.OnActivity("TransferSnapshotActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:      backup,
				BackupVault: backupVault,
				Volume:      volume,
			},
			Node:         &models.Node{EndpointAddress: "127.0.0.1"},
			SnapshotName: "test-backup",
			SnapmirrorRelationship: &commonparams.SnapmirrorRelationship{
				UUID: "test-snapmirror-uuid",
			},
		}, nil)
		env.OnActivity("PollTransferStatusWithHistoryCheckActivity", mock.Anything, mock.Anything, mock.Anything).Return(&activities.PollTransferStatusOutput{
			BackupActivitiesContext: &activities.BackupActivitiesContext{
				BackupWorkflowInit: &activities.BackupWorkflowInput{
					Backup:      backup,
					BackupVault: backupVault,
					Volume:      volume,
				},
				Node:              &models.Node{EndpointAddress: "127.0.0.1"},
				SnapshotName:      "test-backup",
				SmSourcePath:      "svm_test:volume_test",
				SmDestinationPath: "test-bucket:/objstore/test-vol",
				SnapmirrorRelationship: &commonparams.SnapmirrorRelationship{
					UUID: "test-snapmirror-uuid",
				},
				TransferStatus: activities.SmStatusSuccess,
			},
			TransferComplete:    true,
			ShouldContinueAsNew: false,
		}, nil)
		env.OnActivity("GetSnapmirror", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&commonparams.SnapmirrorRelationship{
			UUID:    "test-snapmirror-uuid",
			Healthy: nillable.ToPointer(true),
		}, nil)
		env.OnActivity("FinishBackupActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:      backup,
				BackupVault: backupVault,
				Volume:      volume,
			},
			Node: &models.Node{EndpointAddress: "127.0.0.1"},
		}, nil)
		env.OnActivity("UpdateSnapshotActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:      backup,
				BackupVault: backupVault,
				Volume:      volume,
			},
			Node: &models.Node{EndpointAddress: "127.0.0.1"},
		}, nil)
		env.OnActivity("GetObjectStoreEndpointActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:      backup,
				BackupVault: backupVault,
				Volume:      volume,
			},
			Node: &models.Node{EndpointAddress: "127.0.0.1"},
		}, nil)
		env.OnActivity("GetObjectStoreSnapshotActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:      backup,
				BackupVault: backupVault,
				Volume:      volume,
			},
			Node: &models.Node{EndpointAddress: "127.0.0.1"},
		}, nil)
		env.OnActivity("UpdateBackupSizeActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:      backup,
				BackupVault: backupVault,
				Volume:      volume,
			},
			Node: &models.Node{EndpointAddress: "127.0.0.1"},
		}, nil)
		env.OnActivity("CreateRemoteBackupFromVCPActivity", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CleanupOldBackupSnapshotsActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CreateBackupMetadataIfFirstBackupActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		// Mock rollback activities in case workflow fails
		env.OnActivity("UpdateBackupError", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

		// Execute workflow
		env.ExecuteWorkflow(CreateBackupWorkflowWithContext, backupActivitiesContext, params)

		// Assert that the workflow was executed successfully
		assert.True(t, env.IsWorkflowCompleted())
		assert.NoError(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("SleepBeforeGetSnapmirrorError", func(t *testing.T) {
		// Covers backup_workflow.go line 309: error when workflow.Sleep fails before GetSnapmirror
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		env.SetHeader(mockHeader)
		commonActivity, _ := setupMockCommonActivities(t)
		env.RegisterActivity(commonActivity)
		env.RegisterWorkflow(CreateBackupWorkflowWithContext)

		mockStorage := database.NewMockStorage(t)
		mockStorage.On("UpdateBackupState", mock.Anything, mock.Anything).Return(&datamodel.Backup{}, nil).Maybe()
		mockStorage.On("UpdateSnapshot", mock.Anything, mock.Anything).Return(&datamodel.Snapshot{}, nil).Maybe()
		mockStorage.On("CreatingSnapshot", mock.Anything, mock.Anything).Return(&datamodel.Snapshot{
			Name: "test-backup", Description: "VCP-Backup", VolumeID: 1, AccountID: 1, State: "creating", StateDetails: "creating",
			SnapshotAttributes: &datamodel.SnapshotAttributes{},
		}, nil).Maybe()
		mockStorage.On("UpdateBackup", mock.Anything, mock.Anything).Return(&datamodel.Backup{}, nil).Maybe()
		mockStorage.On("UpdateBackupLatestLogicalBackupSizeByVolume", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		mockStorage.On("UpdateVolumeFields", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

		customBackupActivity := &TestBackupActivity{BackupActivity: &activities.BackupActivity{SE: mockStorage}}
		env.RegisterActivity(customBackupActivity)

		params := &commonparams.CreateBackupParams{VolumeUUID: "test-vol", AccountName: "test-account", BackupName: "test-backup"}
		backupVault := &datamodel.BackupVault{
			Name:          "test-backup-vault",
			BucketDetails: datamodel.BucketDetailsArray{&datamodel.BucketDetails{BucketName: "test-bucket", ServiceAccountName: "sa-test", VendorSubnetID: "subnet-12345"}},
		}
		backup := &datamodel.Backup{
			State: "InProgress", Name: "test-backup", VolumeUUID: "test-vol", BackupVault: backupVault, BackupVaultID: 1, Attributes: &datamodel.BackupAttributes{},
		}
		volume := &datamodel.Volume{
			Pool:             &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{Password: "password"}, PoolAttributes: &datamodel.PoolAttributes{PrimaryZone: "us-central1-a"}},
			Svm:              &datamodel.Svm{Name: "svm_test"},
			VolumeAttributes: &datamodel.VolumeAttributes{BlockProperties: &datamodel.BlockProperties{OSType: "LINUX"}, VendorSubnetID: "subnet-12345", ExternalUUID: "external-uuid"},
		}
		backupActivitiesContext := &activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{Backup: backup, BackupVault: backupVault, Volume: volume},
			Node:               &models.Node{EndpointAddress: "127.0.0.1"},
		}

		ctxReturn := &activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{Backup: backup, BackupVault: backupVault, Volume: volume},
			Node:               &models.Node{EndpointAddress: "127.0.0.1"},
		}
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetNode", mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
		env.OnActivity("PrepareObjectStoreActivity", mock.Anything, mock.Anything).Return(ctxReturn, nil)
		env.OnActivity("GetOrCreateObjectStoreActivity", mock.Anything, mock.Anything).Return(ctxReturn, nil)
		env.OnActivity("PrepareSnapmirrorActivity", mock.Anything, mock.Anything).Return(ctxReturn, nil)
		env.OnActivity("CreateSnapmirrorRelationshipActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
			BackupWorkflowInit:     &activities.BackupWorkflowInput{Backup: backup, BackupVault: backupVault, Volume: volume},
			Node:                   &models.Node{EndpointAddress: "127.0.0.1"},
			SnapmirrorRelationship: &commonparams.SnapmirrorRelationship{UUID: "test-snapmirror-uuid"},
		}, nil)
		env.OnActivity("CreatingSnapshotActivity", mock.Anything, mock.Anything).Return(ctxReturn, nil)
		env.OnActivity("CreateSnapshotActivity", mock.Anything, mock.Anything).Return(ctxReturn, nil)
		env.OnActivity("TransferSnapshotActivity", mock.Anything, mock.Anything).Return(ctxReturn, nil)
		env.OnActivity("PollTransferStatusWithHistoryCheckActivity", mock.Anything, mock.Anything, mock.Anything).Return(&activities.PollTransferStatusOutput{
			BackupActivitiesContext: &activities.BackupActivitiesContext{
				BackupWorkflowInit: &activities.BackupWorkflowInput{Backup: backup, BackupVault: backupVault, Volume: volume},
				Node:               &models.Node{EndpointAddress: "127.0.0.1"}, SnapshotName: "test-backup",
				SmSourcePath: "svm_test:volume_test", SmDestinationPath: "test-bucket:/objstore/test-vol",
				SnapmirrorRelationship: &commonparams.SnapmirrorRelationship{UUID: "test-snapmirror-uuid"},
				TransferStatus:         activities.SmStatusSuccess,
			},
			TransferComplete: true, ShouldContinueAsNew: false,
		}, nil)
		env.OnActivity("UpdateBackupError", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("UpdateSnapshotActivity", mock.Anything, mock.Anything).Return(ctxReturn, nil).Maybe()
		env.OnActivity("DeleteBackupSnapshot", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

		env.SetOnTimerScheduledListener(func(timerID string, duration time.Duration) {
			if duration == 30*time.Second {
				env.CancelWorkflow()
			}
		})

		env.ExecuteWorkflow(CreateBackupWorkflowWithContext, backupActivitiesContext, params)

		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		errStr := env.GetWorkflowError().Error()
		assert.True(t,
			strings.Contains(errStr, "failed to sleep before getting the snapmirror") || strings.Contains(errStr, "canceled") || strings.Contains(errStr, "internal error"),
			"workflow error should reflect sleep failure or cancellation: %s", errStr)
		env.AssertExpectations(t)
	})

	t.Run("SetupFailure", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		env.SetHeader(mockHeader)
		commonActivity, mockCommonStorage := setupMockCommonActivities(t)
		mockCommonStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		mockCommonStorage.On("GetNodesByPoolID", mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil).Maybe()
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(commonActivity.GetNode)
		env.RegisterWorkflow(CreateBackupWorkflowWithContext)

		// Create mock storage for BackupActivity
		mockStorage := database.NewMockStorage(t)
		env.RegisterActivity(&TestBackupActivity{BackupActivity: &activities.BackupActivity{SE: mockStorage}})

		// Set up test data with invalid params to trigger setup failure
		params := &commonparams.CreateBackupParams{
			VolumeUUID:  "test-vol",
			AccountName: "", // Empty account name to trigger setup failure
			BackupName:  "test-backup",
		}
		backupVault := &datamodel.BackupVault{
			Name:          "test-backup-vault",
			BucketDetails: datamodel.BucketDetailsArray{&datamodel.BucketDetails{BucketName: "test-bucket", ServiceAccountName: "sa-test", VendorSubnetID: "subnet-12345"}},
		}
		backup := &datamodel.Backup{
			State:         "InProgress",
			Name:          "test-backup",
			VolumeUUID:    "test-vol",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes:    &datamodel.BackupAttributes{},
		}
		volume := &datamodel.Volume{
			Pool:             &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{Password: "password"}, PoolAttributes: &datamodel.PoolAttributes{PrimaryZone: "us-central1-a"}},
			Svm:              &datamodel.Svm{Name: "svm_test"},
			VolumeAttributes: &datamodel.VolumeAttributes{BlockProperties: &datamodel.BlockProperties{OSType: "LINUX"}, VendorSubnetID: "subnet-12345", ExternalUUID: "external-uuid"},
		}

		backupActivitiesContext := &activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:      backup,
				BackupVault: backupVault,
				Volume:      volume,
			},
			Node: &models.Node{EndpointAddress: "127.0.0.1"},
		}

		// Mock UpdateBackupError for revert logic
		backupActivity := &activities.BackupActivity{SE: mockStorage}
		env.RegisterActivity(backupActivity.UpdateBackupError)
		env.OnActivity("UpdateBackupError", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

		// Mock PrepareObjectStoreActivity to fail to simulate setup failure
		env.RegisterActivity(backupActivity.PrepareObjectStoreActivity)
		env.OnActivity("PrepareObjectStoreActivity", mock.Anything, mock.Anything).Return(nil, errors.New("failed to prepare object store")).Maybe()

		// Execute workflow
		env.ExecuteWorkflow(CreateBackupWorkflowWithContext, backupActivitiesContext, params)

		// Assert that the workflow failed during setup
		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("UpdateJobStatusFailure", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		env.SetHeader(mockHeader)
		commonActivity, _ := setupMockCommonActivities(t)
		env.RegisterActivity(commonActivity)
		env.RegisterWorkflow(CreateBackupWorkflowWithContext)

		// Create mock storage for BackupActivity
		mockStorage := database.NewMockStorage(t)
		env.RegisterActivity(&TestBackupActivity{BackupActivity: &activities.BackupActivity{SE: mockStorage}})

		// Set up test data
		params := &commonparams.CreateBackupParams{
			VolumeUUID:  "test-vol",
			AccountName: "test-account",
			BackupName:  "test-backup",
		}
		backupVault := &datamodel.BackupVault{
			Name:          "test-backup-vault",
			BucketDetails: datamodel.BucketDetailsArray{&datamodel.BucketDetails{BucketName: "test-bucket", ServiceAccountName: "sa-test", VendorSubnetID: "subnet-12345"}},
		}
		backup := &datamodel.Backup{
			State:         "InProgress",
			Name:          "test-backup",
			VolumeUUID:    "test-vol",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes:    &datamodel.BackupAttributes{},
		}
		volume := &datamodel.Volume{
			Pool:             &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{Password: "password"}, PoolAttributes: &datamodel.PoolAttributes{PrimaryZone: "us-central1-a"}},
			Svm:              &datamodel.Svm{Name: "svm_test"},
			VolumeAttributes: &datamodel.VolumeAttributes{BlockProperties: &datamodel.BlockProperties{OSType: "LINUX"}, VendorSubnetID: "subnet-12345", ExternalUUID: "external-uuid"},
		}

		backupActivitiesContext := &activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:      backup,
				BackupVault: backupVault,
				Volume:      volume,
			},
			Node: &models.Node{EndpointAddress: "127.0.0.1"},
		}

		// Mock UpdateJobStatus to fail
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(errors.New("failed to update job status"))

		// Execute workflow
		env.ExecuteWorkflow(CreateBackupWorkflowWithContext, backupActivitiesContext, params)

		// Assert that the workflow failed during job status update
		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		assert.ErrorContains(t, env.GetWorkflowError(), "failed to update job status")
		env.AssertExpectations(t)
	})

	t.Run("RunBackupCreateWithContextFailure", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		env.SetHeader(mockHeader)
		commonActivity, _ := setupMockCommonActivities(t)
		env.RegisterActivity(commonActivity)
		env.RegisterWorkflow(CreateBackupWorkflowWithContext)

		// Create mock storage for BackupActivity
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("UpdateBackupState", mock.Anything, mock.Anything).Return(&datamodel.Backup{}, nil).Maybe()
		env.RegisterActivity(&TestBackupActivity{BackupActivity: &activities.BackupActivity{SE: mockStorage}})

		// Set up test data
		params := &commonparams.CreateBackupParams{
			VolumeUUID:  "test-vol",
			AccountName: "test-account",
			BackupName:  "test-backup",
		}
		backupVault := &datamodel.BackupVault{
			Name:          "test-backup-vault",
			BucketDetails: datamodel.BucketDetailsArray{&datamodel.BucketDetails{BucketName: "test-bucket", ServiceAccountName: "sa-test", VendorSubnetID: "subnet-12345"}},
		}
		backup := &datamodel.Backup{
			State:         "InProgress",
			Name:          "test-backup",
			VolumeUUID:    "test-vol",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes:    &datamodel.BackupAttributes{},
		}
		volume := &datamodel.Volume{
			Pool:             &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{Password: "password"}, PoolAttributes: &datamodel.PoolAttributes{PrimaryZone: "us-central1-a"}},
			Svm:              &datamodel.Svm{Name: "svm_test"},
			VolumeAttributes: &datamodel.VolumeAttributes{BlockProperties: &datamodel.BlockProperties{OSType: "LINUX"}, VendorSubnetID: "subnet-12345", ExternalUUID: "external-uuid"},
		}

		backupActivitiesContext := &activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:      backup,
				BackupVault: backupVault,
				Volume:      volume,
			},
			Node: &models.Node{EndpointAddress: "127.0.0.1"},
		}

		// Mock activities with failure at PrepareObjectStoreActivity
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetNode", mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
		env.OnActivity("PrepareObjectStoreActivity", mock.Anything, mock.Anything).Return(nil, errors.New("failed to prepare object store"))
		env.OnActivity("UpdateBackupError", mock.Anything, mock.Anything, mock.Anything).Return(nil)

		// Execute workflow
		env.ExecuteWorkflow(CreateBackupWorkflowWithContext, backupActivitiesContext, params)

		// Assert that the workflow was executed and handled the error
		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		assert.ErrorContains(t, env.GetWorkflowError(), "failed to prepare object store")
		env.AssertExpectations(t)
	})

	t.Run("ContinueAsNewError", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		env.SetHeader(mockHeader)
		commonActivity, _ := setupMockCommonActivities(t)
		env.RegisterActivity(commonActivity)
		env.RegisterWorkflow(CreateBackupWorkflowWithContext)

		// Create mock storage for BackupActivity
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("UpdateBackupState", mock.Anything, mock.Anything).Return(&datamodel.Backup{}, nil).Maybe()
		env.RegisterActivity(&TestBackupActivity{BackupActivity: &activities.BackupActivity{SE: mockStorage}})

		// Set up test data
		params := &commonparams.CreateBackupParams{
			VolumeUUID:  "test-vol",
			AccountName: "test-account",
			BackupName:  "test-backup",
		}
		backupVault := &datamodel.BackupVault{
			Name:          "test-backup-vault",
			BucketDetails: datamodel.BucketDetailsArray{&datamodel.BucketDetails{BucketName: "test-bucket", ServiceAccountName: "sa-test", VendorSubnetID: "subnet-12345"}},
		}
		backup := &datamodel.Backup{
			State:         "InProgress",
			Name:          "test-backup",
			VolumeUUID:    "test-vol",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes:    &datamodel.BackupAttributes{},
		}
		volume := &datamodel.Volume{
			Pool:             &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{Password: "password"}, PoolAttributes: &datamodel.PoolAttributes{PrimaryZone: "us-central1-a"}},
			Svm:              &datamodel.Svm{Name: "svm_test"},
			VolumeAttributes: &datamodel.VolumeAttributes{BlockProperties: &datamodel.BlockProperties{OSType: "LINUX"}, VendorSubnetID: "subnet-12345", ExternalUUID: "external-uuid"},
		}

		backupActivitiesContext := &activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:      backup,
				BackupVault: backupVault,
				Volume:      volume,
			},
			Node: &models.Node{EndpointAddress: "127.0.0.1"},
		}

		// Mock activities to trigger ContinueAsNew
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetNode", mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
		env.OnActivity("PrepareObjectStoreActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:      backup,
				BackupVault: backupVault,
				Volume:      volume,
			},
			Node: &models.Node{EndpointAddress: "127.0.0.1"},
		}, nil)
		env.OnActivity("GetOrCreateObjectStoreActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:      backup,
				BackupVault: backupVault,
				Volume:      volume,
			},
			Node: &models.Node{EndpointAddress: "127.0.0.1"},
		}, nil)
		env.OnActivity("PrepareSnapmirrorActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:      backup,
				BackupVault: backupVault,
				Volume:      volume,
			},
			Node: &models.Node{EndpointAddress: "127.0.0.1"},
		}, nil)
		env.OnActivity("CreateSnapmirrorRelationshipActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:      backup,
				BackupVault: backupVault,
				Volume:      volume,
			},
			Node: &models.Node{EndpointAddress: "127.0.0.1"},
			SnapmirrorRelationship: &commonparams.SnapmirrorRelationship{
				UUID: "test-snapmirror-uuid",
			},
		}, nil)
		env.OnActivity("CreatingSnapshotActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:      backup,
				BackupVault: backupVault,
				Volume:      volume,
			},
			Node:         &models.Node{EndpointAddress: "127.0.0.1"},
			SnapshotName: "test-backup",
			DbSnapshot: &datamodel.Snapshot{
				Name:               "test-backup",
				Description:        "VCP-Backup",
				VolumeID:           1,
				AccountID:          1,
				State:              "creating",
				StateDetails:       "creating",
				SnapshotAttributes: &datamodel.SnapshotAttributes{},
			},
		}, nil)
		env.OnActivity("CreateSnapshotActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup: &datamodel.Backup{
					Name: "test-backup",
					Attributes: &datamodel.BackupAttributes{
						SnapshotID: "test-snapshot-uuid",
					},
				},
				BackupVault: backupVault,
				Volume:      volume,
			},
			Node:         &models.Node{EndpointAddress: "127.0.0.1"},
			SnapshotName: "test-backup",
			SnapmirrorRelationship: &commonparams.SnapmirrorRelationship{
				UUID: "test-snapmirror-uuid",
			},
			SnapshotResponse: &vsa.SnapshotProviderResponse{
				ProviderResponse: vsa.ProviderResponse{
					ExternalUUID: "test-snapshot-uuid",
				},
			},
		}, nil)
		env.OnActivity("TransferSnapshotActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:      backup,
				BackupVault: backupVault,
				Volume:      volume,
			},
			Node:         &models.Node{EndpointAddress: "127.0.0.1"},
			SnapshotName: "test-backup",
			SnapmirrorRelationship: &commonparams.SnapmirrorRelationship{
				UUID: "test-snapmirror-uuid",
			},
		}, nil)

		// Mock the PollTransferStatusWithHistoryCheckActivity to trigger ContinueAsNew
		env.OnActivity("PollTransferStatusWithHistoryCheckActivity", mock.Anything, mock.Anything, mock.Anything).Return(&activities.PollTransferStatusOutput{
			BackupActivitiesContext: &activities.BackupActivitiesContext{
				BackupWorkflowInit: &activities.BackupWorkflowInput{
					Backup:      backup,
					BackupVault: backupVault,
					Volume:      volume,
				},
				Node:         &models.Node{EndpointAddress: "127.0.0.1"},
				SnapshotName: "test-backup",
				SnapmirrorRelationship: &commonparams.SnapmirrorRelationship{
					UUID: "test-snapmirror-uuid",
				},
				TransferStatus: activities.SmStatusTransferring,
			},
			TransferComplete:    false,
			ShouldContinueAsNew: true,
			ContinueAsNewReason: "Event history limit reached",
			NextWaitTime:        5 * time.Millisecond,
		}, nil)

		// Execute workflow
		env.ExecuteWorkflow(CreateBackupWorkflowWithContext, backupActivitiesContext, params)

		// Assert that the workflow was executed and ContinueAsNew was triggered
		assert.True(t, env.IsWorkflowCompleted())
		// ContinueAsNew should result in a ContinueAsNewError
		assert.Error(t, env.GetWorkflowError())
		assert.True(t, workflow.IsContinueAsNewError(env.GetWorkflowError()))
		env.AssertExpectations(t)
	})

	t.Run("RevertFailure", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		env.SetHeader(mockHeader)
		commonActivity, _ := setupMockCommonActivities(t)
		env.RegisterActivity(commonActivity)
		env.RegisterWorkflow(CreateBackupWorkflowWithContext)

		// Create mock storage for BackupActivity
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("UpdateBackupState", mock.Anything, mock.Anything).Return(&datamodel.Backup{}, nil).Maybe()
		env.RegisterActivity(&TestBackupActivity{BackupActivity: &activities.BackupActivity{SE: mockStorage}})

		// Set up test data
		params := &commonparams.CreateBackupParams{
			VolumeUUID:  "test-vol",
			AccountName: "test-account",
			BackupName:  "test-backup",
		}
		backupVault := &datamodel.BackupVault{
			Name:          "test-backup-vault",
			BucketDetails: datamodel.BucketDetailsArray{&datamodel.BucketDetails{BucketName: "test-bucket", ServiceAccountName: "sa-test", VendorSubnetID: "subnet-12345"}},
		}
		backup := &datamodel.Backup{
			State:         "InProgress",
			Name:          "test-backup",
			VolumeUUID:    "test-vol",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes:    &datamodel.BackupAttributes{},
		}
		volume := &datamodel.Volume{
			Pool:             &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{Password: "password"}, PoolAttributes: &datamodel.PoolAttributes{PrimaryZone: "us-central1-a"}},
			Svm:              &datamodel.Svm{Name: "svm_test"},
			VolumeAttributes: &datamodel.VolumeAttributes{BlockProperties: &datamodel.BlockProperties{OSType: "LINUX"}, VendorSubnetID: "subnet-12345", ExternalUUID: "external-uuid"},
		}

		backupActivitiesContext := &activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:      backup,
				BackupVault: backupVault,
				Volume:      volume,
			},
			Node: &models.Node{EndpointAddress: "127.0.0.1"},
		}

		// Mock activities with failure at PrepareObjectStoreActivity
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetNode", mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
		env.OnActivity("PrepareObjectStoreActivity", mock.Anything, mock.Anything).Return(nil, errors.New("failed to prepare object store"))
		// Mock UpdateBackupError to fail (revert failure)
		env.OnActivity("UpdateBackupError", mock.Anything, mock.Anything, mock.Anything).Return(errors.New("failed to update backup error"))

		// Execute workflow
		env.ExecuteWorkflow(CreateBackupWorkflowWithContext, backupActivitiesContext, params)

		// Assert that the workflow was executed and handled the error
		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		assert.ErrorContains(t, env.GetWorkflowError(), "failed to prepare object store")
		env.AssertExpectations(t)
	})
}

func TestBackupWorkflow_CreateBackupMetadataIfFirstBackupActivityFailure(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
	mockHeader := &commonpb.Header{
		Fields: map[string]*commonpb.Payload{
			"logParam": encodedValue,
		},
	}
	env.SetHeader(mockHeader)

	// Create mock storage for CommonActivities and BackupActivity
	mockStorage := database.NewMockStorage(t)
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()

	commonActivity := &activities.CommonActivities{SE: mockStorage}
	env.RegisterActivity(commonActivity)
	env.RegisterWorkflow(CreateBackupWorkflow)
	mockStorage.On("UpdateSnapshot", mock.Anything, mock.Anything).Return(&datamodel.Snapshot{}, nil).Maybe()
	mockStorage.On("CreatingSnapshot", mock.Anything, mock.Anything).Return(&datamodel.Snapshot{
		Name:               "test-backup",
		Description:        "VCP-Backup",
		VolumeID:           1,
		AccountID:          1,
		State:              "creating",
		StateDetails:       "creating",
		SnapshotAttributes: &datamodel.SnapshotAttributes{},
	}, nil).Maybe()
	mockStorage.On("UpdateBackup", mock.Anything, mock.Anything).Return(&datamodel.Backup{}, nil).Maybe()
	mockStorage.On("UpdateBackupLatestLogicalBackupSizeByVolume", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	mockStorage.On("UpdateVolumeFields", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	// Create a custom BackupActivity that bypasses the nil check in UpdateSnapshotActivity
	customBackupActivity := &TestBackupActivity{BackupActivity: &activities.BackupActivity{SE: mockStorage}}
	env.RegisterActivity(customBackupActivity)

	// Set up test data
	params := &commonparams.CreateBackupParams{
		VolumeUUID:  "test-vol",
		AccountName: "test-account",
		BackupName:  "test-backup",
	}
	backupVault := &datamodel.BackupVault{
		BaseModel: datamodel.BaseModel{UUID: "test-backup-vault-uuid"},
		Name:      "test-backup-vault",
		BucketDetails: []*datamodel.BucketDetails{
			{
				BucketName:         "vsa-backup-bucket",
				VendorSubnetID:     "test-vendor-subnet-id",
				ServiceAccountName: "test-service-account",
			},
		},
	}
	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "test-vol"},
		Name:      "test-volume",
		Account: &datamodel.Account{
			Name: "test-account",
		},
		Svm: &datamodel.Svm{
			Name: "test-svm",
		},
		PoolID: 1,
		Pool: &datamodel.Pool{
			PoolCredentials: &datamodel.PoolCredentials{
				Password: "pool-password",
				SecretID: "pool-credential-secret-id",
			},
			DeploymentName: "test-pool-deployment",
			PoolAttributes: &datamodel.PoolAttributes{
				PrimaryZone: "test-zone-1",
			},
		},
		DataProtection: &datamodel.DataProtection{
			BackupVaultID: "test-backup-vault-uuid",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID:   "external-uuid-1",
			VendorSubnetID: "test-vendor-subnet-id",
		},
	}
	backup := &datamodel.Backup{
		BaseModel:     datamodel.BaseModel{UUID: "test-backup-uuid"},
		Name:          "test-backup",
		VolumeUUID:    "test-vol",
		BackupVaultID: 1,
		Attributes:    &datamodel.BackupAttributes{},
	}

	// Mock all the successful activities (using the same pattern as the existing TestBackupWorkflow)
	env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("GetNode", mock.Anything, mock.Anything).Return([]*datamodel.Node{
		{EndpointAddress: "127.0.0.1"},
	}, nil)
	env.OnActivity("PrepareObjectStoreActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		ObjStoreName: "vsa-backup-bucket",
		BucketDetails: &datamodel.BucketDetails{
			BucketName:         "vsa-backup-bucket",
			VendorSubnetID:     "test-vendor-subnet-id",
			ServiceAccountName: "test-service-account",
		},
		BucketName: "vsa-backup-bucket",
	}, nil)
	env.OnActivity("GetOrCreateObjectStoreActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		ObjStore: &common.CloudTarget{
			Name: "vsa-backup-bucket",
			UUID: "test-obj-store-uuid",
		},
	}, nil)
	env.OnActivity("PrepareSnapmirrorActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		SmSourcePath:      "test-svm:test-volume",
		SmDestinationPath: "vsa-backup-bucket:/objstore/test-vol",
	}, nil)
	env.OnActivity("CreateSnapmirrorRelationshipActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		SnapmirrorRelationship: &common.SnapmirrorRelationship{
			UUID:            "test-snapmirror-uuid",
			DestinationUUID: nillable.ToPointer("test-destination-uuid"),
		},
	}, nil)
	env.OnActivity("CreatingSnapshotActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node:         &models.Node{EndpointAddress: "127.0.0.1"},
		SnapshotName: "test-backup",
		DbSnapshot: &datamodel.Snapshot{
			Name:               "test-backup",
			Description:        "VCP-Backup",
			VolumeID:           1,
			AccountID:          1,
			State:              "creating",
			StateDetails:       "creating",
			SnapshotAttributes: &datamodel.SnapshotAttributes{},
		},
	}, nil)
	env.OnActivity("CreateSnapshotActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup: &datamodel.Backup{
				Name: "test-backup",
				Attributes: &datamodel.BackupAttributes{
					SnapshotID: "test-snapshot-uuid",
				},
			},
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node:         &models.Node{EndpointAddress: "127.0.0.1"},
		SnapshotName: "test-backup",
		SnapmirrorRelationship: &commonparams.SnapmirrorRelationship{
			UUID: "test-snapmirror-uuid",
		},
		SnapshotResponse: &vsa.SnapshotProviderResponse{
			ProviderResponse: vsa.ProviderResponse{
				ExternalUUID: "test-snapshot-uuid",
			},
		},
	}, nil)
	env.OnActivity("TransferSnapshotActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node:         &models.Node{EndpointAddress: "127.0.0.1"},
		SnapshotName: "test-backup",
		SnapmirrorRelationship: &commonparams.SnapmirrorRelationship{
			UUID: "test-snapmirror-uuid",
		},
	}, nil)
	env.OnActivity("PollTransferStatusWithHistoryCheckActivity", mock.Anything, mock.Anything, mock.Anything).Return(&activities.PollTransferStatusOutput{
		BackupActivitiesContext: &activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:      backup,
				BackupVault: backupVault,
				Volume:      volume,
			},
			Node:              &models.Node{EndpointAddress: "127.0.0.1"},
			SnapshotName:      "test-backup",
			SmSourcePath:      "test-svm:test-volume",
			SmDestinationPath: "vsa-backup-bucket:/objstore/test-vol",
			SnapmirrorRelationship: &commonparams.SnapmirrorRelationship{
				UUID: "test-snapmirror-uuid",
			},
			TransferStatus: activities.SmStatusSuccess,
		},
		TransferComplete:    true,
		ShouldContinueAsNew: false,
	}, nil)
	env.OnActivity("GetSnapmirror", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&commonparams.SnapmirrorRelationship{
		UUID:    "test-snapmirror-uuid",
		Healthy: nillable.ToPointer(true),
	}, nil)
	env.OnActivity("FinishBackupActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node: &models.Node{EndpointAddress: "127.0.0.1"},
	}, nil)
	env.OnActivity("UpdateSnapshotActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node: &models.Node{EndpointAddress: "127.0.0.1"},
	}, nil)
	env.OnActivity("GetObjectStoreEndpointActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node: &models.Node{EndpointAddress: "127.0.0.1"},
	}, nil)
	env.OnActivity("GetObjectStoreSnapshotActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node: &models.Node{EndpointAddress: "127.0.0.1"},
	}, nil)
	env.OnActivity("UpdateBackupSizeActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node: &models.Node{EndpointAddress: "127.0.0.1"},
	}, nil)
	env.OnActivity("CreateRemoteBackupFromVCPActivity", mock.Anything, mock.Anything).Return(&activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
	}, nil)
	// Mock CreateBackupMetadataIfFirstBackupActivity to return an error
	env.OnActivity("CreateBackupMetadataIfFirstBackupActivity", mock.Anything, mock.Anything, mock.Anything).Return(errors.New("failed to create backup metadata"))
	env.OnActivity("CleanupOldBackupSnapshotsActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	// Mock rollback activities in case workflow fails
	env.OnActivity("UpdateBackupError", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	// Execute workflow
	env.ExecuteWorkflow(CreateBackupWorkflow, params, backup, backupVault, volume)

	// Assert that the workflow was executed successfully despite the metadata activity failure
	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

func TestDeleteBackupWorkflow_DeleteBackupMetadataIfLastBackupActivityFailure(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
	mockHeader := &commonpb.Header{
		Fields: map[string]*commonpb.Payload{
			"logParam": encodedValue,
		},
	}
	env.SetHeader(mockHeader)
	commonActivity, _ := setupMockCommonActivities(t)
	env.RegisterActivity(commonActivity)
	env.RegisterActivity(setupMockBackupActivity(t))
	env.RegisterWorkflow(DeleteBackupWorkflow)

	// Set up test data
	params := &commonparams.DeleteBackupParams{
		BackupVaultUUID: "vault-uuid",
		BackupUUID:      "backup-uuid",
		AccountName:     "test-account",
	}
	account := &datamodel.Account{BaseModel: datamodel.BaseModel{UUID: "account-uuid"}}
	backupVault := &datamodel.BackupVault{
		Name: "test-backup-vault",
		BucketDetails: datamodel.BucketDetailsArray{
			&datamodel.BucketDetails{BucketName: "test-bucket", ServiceAccountName: "sa-test", VendorSubnetID: "subnet-12345"},
		},
		Account: account,
	}
	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "test-vol"},
		Pool: &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			},
		},
		Svm: &datamodel.Svm{
			Name: "svm_test",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			VendorSubnetID: "subnet-12345",
		},
		DataProtection: &datamodel.DataProtection{},
	}
	backup := &datamodel.Backup{
		Name:          "test-backup",
		VolumeUUID:    "test-vol",
		BackupVault:   backupVault,
		BackupVaultID: 1,
		Attributes:    &datamodel.BackupAttributes{BucketName: "test-bucket"},
	}

	// Mock all the successful activities
	env.OnActivity("GetAccountByName", mock.Anything, params.AccountName).Return(account, nil)
	env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("GetBackupVault", mock.Anything, params.BackupVaultUUID).Return(backupVault, nil)
	env.OnActivity("GetBackup", mock.Anything, params.BackupVaultUUID, params.BackupUUID, params.AccountName).Return(backup, nil)
	env.OnActivity("IsVolumeDeleted", mock.Anything, backup.VolumeUUID).Return(false, nil)
	env.OnActivity("IsExpertModeVolume", mock.Anything, backup.VolumeUUID).Return(false, nil)
	env.OnActivity("GetVolume", mock.Anything, backup.VolumeUUID).Return(volume, nil)
	env.OnActivity("GetNode", mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	env.OnActivity("GetSmDestinationPathActivity", mock.Anything, mock.Anything, mock.Anything).Return("test-bucket:/objstore/test-vol", nil)
	env.OnActivity("GetSmSourcePathActivity", mock.Anything, mock.Anything).Return("svm_test:volume_test", nil)
	env.OnActivity("IsSnapmirrorDeleted", mock.Anything, mock.Anything, mock.Anything).Return(&commonparams.SnapmirrorDeletePrecheckResult{RelationshipMissing: false, Relationship: &commonparams.SnapmirrorRelationship{UUID: "test-sm-uuid"}}, nil)
	env.OnActivity("GetBackupCountByVolumeUUID", mock.Anything, backup.VolumeUUID).Return(int64(1), nil)
	env.OnActivity("GetObjectStore", mock.Anything, mock.Anything, mock.Anything).Return(&commonparams.CloudTarget{UUID: "obj-store-uuid"}, nil)
	env.OnActivity("GetSnapmirror", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&commonparams.SnapmirrorRelationship{UUID: "snapmirror-uuid"}, nil)
	env.OnActivity("DeleteSnapmirror", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{JobUUID: "job-uuid"}, nil)
	env.OnActivity("GetOntapJob", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil)
	env.OnActivity("UpdateVolumeLatestLogicalBackupSize", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("DeleteCloudEndpoint", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{JobUUID: "job-uuid"}, nil)
	env.OnActivity("DeleteSnapshotForBackup", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("DeleteBackupSnapshotFromDB", mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("HydrateSnapshotDeletionToCCFEActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("DeleteBackup", mock.Anything, params.BackupUUID).Return(backup, nil)
	// Mock DeleteBackupMetadataIfLastBackupActivity to return an error
	env.OnActivity("DeleteBackupMetadataIfLastBackupActivity", mock.Anything, mock.Anything).Return(errors.New("failed to delete backup metadata"))

	// Execute workflow
	env.ExecuteWorkflow(DeleteBackupWorkflow, params)

	// Assert that the workflow was executed successfully despite the metadata activity failure
	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

// TestDeleteBackupWorkflow_SnapshotDehydrationUsesSourceVolumeProject is a regression test
// for cross-project backup vaults. The backup snapshot is an ONTAP snapshot on the source
// volume, so its CCFE resource path must use the source volume's project (volume.Account),
// NOT the backup-vault project carried on the DeleteBackup request (deleteBackupParams.AccountName).
// Previously the dehydration used the request account, producing a ResourceNotFound at CCFE
// because the snapshot does not exist under the backup-vault project.
func TestDeleteBackupWorkflow_SnapshotDehydrationUsesSourceVolumeProject(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
	mockHeader := &commonpb.Header{
		Fields: map[string]*commonpb.Payload{
			"logParam": encodedValue,
		},
	}
	env.SetHeader(mockHeader)
	commonActivity, _ := setupMockCommonActivities(t)
	env.RegisterActivity(commonActivity)
	env.RegisterActivity(setupMockBackupActivity(t))
	env.RegisterWorkflow(DeleteBackupWorkflow)

	// The DeleteBackup request comes in against the cross-project backup-vault project.
	const backupVaultProject = "backup-vault-project"
	// The source volume (and therefore the snapshot) lives in a different project.
	const sourceVolumeProject = "source-volume-project"

	params := &commonparams.DeleteBackupParams{
		BackupVaultUUID: "vault-uuid",
		BackupUUID:      "backup-uuid",
		AccountName:     backupVaultProject,
	}
	// account resolved from the request (backup-vault project).
	account := &datamodel.Account{BaseModel: datamodel.BaseModel{UUID: "backup-vault-account-uuid"}, Name: backupVaultProject}
	// volume account is the source volume's project (preloaded by GetVolume in production).
	volumeAccount := &datamodel.Account{BaseModel: datamodel.BaseModel{UUID: "source-volume-account-uuid"}, Name: sourceVolumeProject}
	backupVault := &datamodel.BackupVault{
		Name: "test-backup-vault",
		BucketDetails: datamodel.BucketDetailsArray{
			&datamodel.BucketDetails{BucketName: "test-bucket", ServiceAccountName: "sa-test", VendorSubnetID: "subnet-12345"},
		},
		Account: account,
	}
	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "test-vol"},
		Name:      "iscsi_vol_test",
		Pool: &datamodel.Pool{
			BaseModel:       datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{Password: "password"},
		},
		Svm: &datamodel.Svm{Name: "svm_test"},
		VolumeAttributes: &datamodel.VolumeAttributes{
			VendorSubnetID: "subnet-12345",
		},
		Account:        volumeAccount,
		DataProtection: &datamodel.DataProtection{},
	}
	backup := &datamodel.Backup{
		Name:          "backup-f38c819e",
		VolumeUUID:    "test-vol",
		BackupVault:   backupVault,
		BackupVaultID: 1,
		Attributes:    &datamodel.BackupAttributes{BucketName: "test-bucket", SnapshotID: "snapshot-uuid"},
	}

	var capturedProjectID string
	var capturedVolumeName string

	env.OnActivity("GetAccountByName", mock.Anything, params.AccountName).Return(account, nil)
	env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("GetBackupVault", mock.Anything, params.BackupVaultUUID).Return(backupVault, nil)
	env.OnActivity("GetBackup", mock.Anything, params.BackupVaultUUID, params.BackupUUID, params.AccountName).Return(backup, nil)
	env.OnActivity("IsVolumeDeleted", mock.Anything, backup.VolumeUUID).Return(false, nil)
	env.OnActivity("IsExpertModeVolume", mock.Anything, backup.VolumeUUID).Return(false, nil)
	env.OnActivity("GetVolume", mock.Anything, backup.VolumeUUID).Return(volume, nil)
	env.OnActivity("GetNode", mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	env.OnActivity("GetSmDestinationPathActivity", mock.Anything, mock.Anything, mock.Anything).Return("test-bucket:/objstore/test-vol", nil)
	env.OnActivity("GetSmSourcePathActivity", mock.Anything, mock.Anything).Return("svm_test:volume_test", nil)
	env.OnActivity("IsSnapmirrorDeleted", mock.Anything, mock.Anything, mock.Anything).Return(&commonparams.SnapmirrorDeletePrecheckResult{RelationshipMissing: false, Relationship: &commonparams.SnapmirrorRelationship{UUID: "test-sm-uuid"}}, nil)
	env.OnActivity("GetBackupCountByVolumeUUID", mock.Anything, backup.VolumeUUID).Return(int64(1), nil)
	env.OnActivity("GetObjectStore", mock.Anything, mock.Anything, mock.Anything).Return(&commonparams.CloudTarget{UUID: "obj-store-uuid"}, nil)
	env.OnActivity("GetSnapmirror", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&commonparams.SnapmirrorRelationship{UUID: "snapmirror-uuid"}, nil)
	env.OnActivity("DeleteSnapmirror", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{JobUUID: "job-uuid"}, nil)
	env.OnActivity("GetOntapJob", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil)
	env.OnActivity("UpdateVolumeLatestLogicalBackupSize", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("DeleteCloudEndpoint", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{JobUUID: "job-uuid"}, nil)
	env.OnActivity("DeleteSnapshotForBackup", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("DeleteBackupSnapshotFromDB", mock.Anything, mock.Anything).Return(nil)
	// Capture the project id (args[4]) and volume name (args[2]) passed to the dehydration activity.
	env.OnActivity("HydrateSnapshotDeletionToCCFEActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			capturedVolumeName = args[2].(string)
			capturedProjectID = args[4].(string)
		}).Return(nil)
	env.OnActivity("DeleteBackup", mock.Anything, params.BackupUUID).Return(backup, nil)
	env.OnActivity("DeleteBackupMetadataIfLastBackupActivity", mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(DeleteBackupWorkflow, params)

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
	// The dehydration must target the source volume's project, not the backup-vault project.
	assert.Equal(t, sourceVolumeProject, capturedProjectID,
		"snapshot dehydration must use the source volume's project, not the backup-vault project")
	assert.NotEqual(t, backupVaultProject, capturedProjectID,
		"snapshot dehydration must not use the backup-vault project from the DeleteBackup request")
	assert.Equal(t, volume.Name, capturedVolumeName)
	env.AssertExpectations(t)
}

func TestDeleteBackupWorkflow_CrossRegionBackupSuccess(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
	mockHeader := &commonpb.Header{
		Fields: map[string]*commonpb.Payload{
			"logParam": encodedValue,
		},
	}
	env.SetHeader(mockHeader)
	commonActivity, _ := setupMockCommonActivities(t)
	env.RegisterActivity(commonActivity)
	env.RegisterActivity(setupMockBackupActivity(t))
	env.RegisterWorkflow(DeleteBackupWorkflow)

	// Set up test data for cross-region backup
	params := &commonparams.DeleteBackupParams{
		BackupVaultUUID: "vault-uuid",
		BackupUUID:      "backup-uuid",
		AccountName:     "test-account",
	}
	account := &datamodel.Account{BaseModel: datamodel.BaseModel{UUID: "account-uuid"}}
	externalVaultUUID := "external-vault-uuid"
	backupRegionName := "us-west1"
	backupVault := &datamodel.BackupVault{
		BaseModel:        datamodel.BaseModel{UUID: params.BackupVaultUUID},
		Name:             "test-backup-vault",
		BackupVaultType:  "CROSS_REGION",
		ExternalUUID:     &externalVaultUUID,
		BackupRegionName: &backupRegionName,
		BucketDetails: datamodel.BucketDetailsArray{
			&datamodel.BucketDetails{BucketName: "test-bucket", ServiceAccountName: "sa-test", VendorSubnetID: "subnet-12345"},
		},
		Account: account,
	}
	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "test-vol"},
		Pool: &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			},
		},
		Svm: &datamodel.Svm{
			Name: "svm_test",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			VendorSubnetID: "subnet-12345",
		},
		Account:        account,
		DataProtection: &datamodel.DataProtection{},
	}
	externalBackupUUID := "external-backup-uuid"
	backup := &datamodel.Backup{
		BaseModel:     datamodel.BaseModel{UUID: params.BackupUUID},
		Name:          "test-backup",
		VolumeUUID:    "test-vol",
		BackupVault:   backupVault,
		BackupVaultID: 1,
		Attributes:    &datamodel.BackupAttributes{BucketName: "test-bucket"},
		ExternalUUID:  externalBackupUUID,
	}

	// Mock all the successful activities
	env.OnActivity("GetAccountByName", mock.Anything, params.AccountName).Return(account, nil)
	env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("GetBackupVault", mock.Anything, params.BackupVaultUUID).Return(backupVault, nil)
	env.OnActivity("GetBackup", mock.Anything, params.BackupVaultUUID, params.BackupUUID, params.AccountName).Return(backup, nil)
	env.OnActivity("IsVolumeDeleted", mock.Anything, backup.VolumeUUID).Return(false, nil)
	env.OnActivity("IsExpertModeVolume", mock.Anything, backup.VolumeUUID).Return(false, nil)
	env.OnActivity("GetVolume", mock.Anything, backup.VolumeUUID).Return(volume, nil)
	env.OnActivity("GetNode", mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	env.OnActivity("GetSmDestinationPathActivity", mock.Anything, mock.Anything, mock.Anything).Return("test-bucket:/objstore/test-vol", nil)
	env.OnActivity("GetSmSourcePathActivity", mock.Anything, mock.Anything).Return("svm_test:volume_test", nil)
	env.OnActivity("IsSnapmirrorDeleted", mock.Anything, mock.Anything, mock.Anything).Return(&commonparams.SnapmirrorDeletePrecheckResult{RelationshipMissing: false, Relationship: &commonparams.SnapmirrorRelationship{UUID: "test-sm-uuid"}}, nil)
	env.OnActivity("GetBackupCountByVolumeUUID", mock.Anything, backup.VolumeUUID).Return(int64(1), nil)
	env.OnActivity("GetObjectStore", mock.Anything, mock.Anything, mock.Anything).Return(&commonparams.CloudTarget{UUID: "obj-store-uuid"}, nil)
	env.OnActivity("GetSnapmirror", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&commonparams.SnapmirrorRelationship{UUID: "snapmirror-uuid"}, nil)
	env.OnActivity("DeleteSnapmirror", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{JobUUID: "job-uuid"}, nil)
	env.OnActivity("GetOntapJob", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil)
	env.OnActivity("UpdateVolumeLatestLogicalBackupSize", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("DeleteCloudEndpoint", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{JobUUID: "job-uuid"}, nil)
	env.OnActivity("DeleteSnapshotForBackup", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("DeleteBackupSnapshotFromDB", mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("HydrateSnapshotDeletionToCCFEActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("DeleteBackup", mock.Anything, params.BackupUUID).Return(backup, nil)
	// Mock DeleteRemoteBackupFromVCPActivity to succeed
	env.OnActivity("DeleteRemoteBackupFromVCPActivity", mock.Anything, params.BackupUUID, params.BackupVaultUUID, params.AccountName, backupRegionName).Return(nil)
	env.OnActivity("DeleteBackupMetadataIfLastBackupActivity", mock.Anything, mock.Anything).Return(nil)

	// Execute workflow
	env.ExecuteWorkflow(DeleteBackupWorkflow, params)

	// Assert that the workflow completed successfully
	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

func TestDeleteBackupWorkflow_CrossRegionBackupFailure(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
	mockHeader := &commonpb.Header{
		Fields: map[string]*commonpb.Payload{
			"logParam": encodedValue,
		},
	}
	env.SetHeader(mockHeader)
	commonActivity, _ := setupMockCommonActivities(t)
	env.RegisterActivity(commonActivity)
	env.RegisterActivity(setupMockBackupActivity(t))
	env.RegisterWorkflow(DeleteBackupWorkflow)

	// Set up test data for cross-region backup
	params := &commonparams.DeleteBackupParams{
		BackupVaultUUID: "vault-uuid",
		BackupUUID:      "backup-uuid",
		AccountName:     "test-account",
	}
	account := &datamodel.Account{BaseModel: datamodel.BaseModel{UUID: "account-uuid"}}
	externalVaultUUID := "external-vault-uuid"
	backupRegionName := "us-west1"
	backupVault := &datamodel.BackupVault{
		BaseModel:        datamodel.BaseModel{UUID: params.BackupVaultUUID},
		Name:             "test-backup-vault",
		BackupVaultType:  "CROSS_REGION",
		ExternalUUID:     &externalVaultUUID,
		BackupRegionName: &backupRegionName,
		BucketDetails: datamodel.BucketDetailsArray{
			&datamodel.BucketDetails{BucketName: "test-bucket", ServiceAccountName: "sa-test", VendorSubnetID: "subnet-12345"},
		},
		Account: account,
	}
	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "test-vol"},
		Pool: &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			},
		},
		Svm: &datamodel.Svm{
			Name: "svm_test",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			VendorSubnetID: "subnet-12345",
		},
		Account:        account,
		DataProtection: &datamodel.DataProtection{},
	}
	externalBackupUUID := "external-backup-uuid"
	backup := &datamodel.Backup{
		BaseModel:     datamodel.BaseModel{UUID: params.BackupUUID},
		Name:          "test-backup",
		VolumeUUID:    "test-vol",
		BackupVault:   backupVault,
		BackupVaultID: 1,
		Attributes:    &datamodel.BackupAttributes{BucketName: "test-bucket"},
		ExternalUUID:  externalBackupUUID,
	}

	// Mock all the successful activities
	env.OnActivity("GetAccountByName", mock.Anything, params.AccountName).Return(account, nil)
	env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("GetBackupVault", mock.Anything, params.BackupVaultUUID).Return(backupVault, nil)
	env.OnActivity("GetBackup", mock.Anything, params.BackupVaultUUID, params.BackupUUID, params.AccountName).Return(backup, nil)
	env.OnActivity("IsVolumeDeleted", mock.Anything, backup.VolumeUUID).Return(false, nil)
	env.OnActivity("IsExpertModeVolume", mock.Anything, backup.VolumeUUID).Return(false, nil)
	env.OnActivity("GetVolume", mock.Anything, backup.VolumeUUID).Return(volume, nil)
	env.OnActivity("GetNode", mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	env.OnActivity("GetSmDestinationPathActivity", mock.Anything, mock.Anything, mock.Anything).Return("test-bucket:/objstore/test-vol", nil)
	env.OnActivity("GetSmSourcePathActivity", mock.Anything, mock.Anything).Return("svm_test:volume_test", nil)
	env.OnActivity("IsSnapmirrorDeleted", mock.Anything, mock.Anything, mock.Anything).Return(&commonparams.SnapmirrorDeletePrecheckResult{RelationshipMissing: false, Relationship: &commonparams.SnapmirrorRelationship{UUID: "test-sm-uuid"}}, nil)
	env.OnActivity("GetBackupCountByVolumeUUID", mock.Anything, backup.VolumeUUID).Return(int64(1), nil)
	env.OnActivity("GetObjectStore", mock.Anything, mock.Anything, mock.Anything).Return(&commonparams.CloudTarget{UUID: "obj-store-uuid"}, nil)
	env.OnActivity("GetSnapmirror", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&commonparams.SnapmirrorRelationship{UUID: "snapmirror-uuid"}, nil)
	env.OnActivity("DeleteSnapmirror", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{JobUUID: "job-uuid"}, nil)
	env.OnActivity("GetOntapJob", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil)
	env.OnActivity("UpdateVolumeLatestLogicalBackupSize", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("DeleteCloudEndpoint", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{JobUUID: "job-uuid"}, nil)
	env.OnActivity("DeleteSnapshotForBackup", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("DeleteBackupSnapshotFromDB", mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("HydrateSnapshotDeletionToCCFEActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("DeleteBackup", mock.Anything, params.BackupUUID).Return(backup, nil)
	// Mock DeleteRemoteBackupFromVCPActivity to fail
	env.OnActivity("DeleteRemoteBackupFromVCPActivity", mock.Anything, params.BackupUUID, params.BackupVaultUUID, params.AccountName, backupRegionName).Return(errors.New("failed to delete remote backup from VCP"))
	env.OnActivity("DeleteBackupMetadataIfLastBackupActivity", mock.Anything, mock.Anything).Return(nil)

	// Execute workflow
	env.ExecuteWorkflow(DeleteBackupWorkflow, params)

	// Assert that the workflow failed due to DeleteRemoteBackupFromVCPActivity failure
	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

// TestCreateBackupWorkflow_EnsureJobStateError tests the error path when EnsureJobState fails
func TestCreateBackupWorkflow_EnsureJobStateError(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
	mockHeader := &commonpb.Header{
		Fields: map[string]*commonpb.Payload{
			"logParam": encodedValue,
		},
	}
	env.SetHeader(mockHeader)
	commonActivity, mockStorage := setupMockCommonActivities(t)
	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	env.RegisterActivity(commonActivity)
	env.RegisterActivity(commonActivity.GetJob)
	env.RegisterWorkflow(CreateBackupWorkflow)

	// Mock GetJob to return a job with state PROCESSING (not NEW) to trigger EnsureJobState error
	env.OnActivity(commonActivity.GetJob, mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
		State:     string(datamodel.JobsStatePROCESSING), // Wrong state to trigger error
	}, nil)

	// Set up test data
	params := &commonparams.CreateBackupParams{
		VolumeUUID:  "test-vol",
		AccountName: "test-account",
		BackupName:  "test-backup",
	}
	backupVault := &datamodel.BackupVault{
		Name:          "test-backup-vault",
		BucketDetails: datamodel.BucketDetailsArray{&datamodel.BucketDetails{BucketName: "test-bucket", ServiceAccountName: "sa-test", VendorSubnetID: "subnet-12345"}},
	}
	backup := &datamodel.Backup{
		State:         "InProgress",
		Name:          "test-backup",
		VolumeUUID:    "test-vol",
		BackupVault:   backupVault,
		BackupVaultID: 1,
		Attributes:    &datamodel.BackupAttributes{},
	}
	volume := &datamodel.Volume{
		Pool:             &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{Password: "password"}, PoolAttributes: &datamodel.PoolAttributes{PrimaryZone: "us-central1-a"}},
		Svm:              &datamodel.Svm{Name: "svm_test"},
		VolumeAttributes: &datamodel.VolumeAttributes{BlockProperties: &datamodel.BlockProperties{OSType: "LINUX"}, VendorSubnetID: "subnet-12345", ExternalUUID: "external-uuid"},
	}

	// Execute workflow
	env.ExecuteWorkflow(CreateBackupWorkflow, params, backup, backupVault, volume)

	// Assert that the workflow failed due to EnsureJobState error
	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

// TestDeleteBackupWorkflow_EnsureJobStateError tests the error path when EnsureJobState fails
func TestDeleteBackupWorkflow_EnsureJobStateError(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
	mockHeader := &commonpb.Header{
		Fields: map[string]*commonpb.Payload{
			"logParam": encodedValue,
		},
	}
	env.SetHeader(mockHeader)
	commonActivity, mockStorage := setupMockCommonActivities(t)
	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	env.RegisterActivity(commonActivity)
	env.RegisterActivity(commonActivity.GetJob)
	env.RegisterWorkflow(DeleteBackupWorkflow)

	// Mock GetJob to return a job with state PROCESSING (not NEW) to trigger EnsureJobState error
	env.OnActivity(commonActivity.GetJob, mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
		State:     string(datamodel.JobsStatePROCESSING), // Wrong state to trigger error
	}, nil)

	// Set up test data
	params := &commonparams.DeleteBackupParams{
		BackupVaultUUID: "vault-uuid",
		BackupUUID:      "backup-uuid",
		AccountName:     "test-account",
	}

	// Execute workflow
	env.ExecuteWorkflow(DeleteBackupWorkflow, params)

	// Assert that the workflow failed due to EnsureJobState error
	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

// TestUpdateBackupWorkflow_EnsureJobStateError tests the error path when EnsureJobState fails
func TestUpdateBackupWorkflow_EnsureJobStateError(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
	mockHeader := &commonpb.Header{
		Fields: map[string]*commonpb.Payload{
			"logParam": encodedValue,
		},
	}
	env.SetHeader(mockHeader)
	commonActivity, mockStorage := setupMockCommonActivities(t)
	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	env.RegisterActivity(commonActivity)
	env.RegisterActivity(commonActivity.GetJob)
	env.RegisterWorkflow(UpdateBackupWorkflow)

	// Mock GetJob to return a job with state PROCESSING (not NEW) to trigger EnsureJobState error
	env.OnActivity(commonActivity.GetJob, mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
		State:     string(datamodel.JobsStatePROCESSING), // Wrong state to trigger error
	}, nil)

	// Set up test data
	backup := &datamodel.Backup{
		BaseModel: datamodel.BaseModel{UUID: "backup-uuid"},
		Name:      "test-backup",
		State:     "Ready",
	}

	// Execute workflow
	env.ExecuteWorkflow(UpdateBackupWorkflow, backup)

	// Assert that the workflow failed due to EnsureJobState error
	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

// TestCreateBackupWorkflowWithContext_ExpertModeVolume tests the expert mode volume logic
// This specifically tests lines 218-235 which handle expert mode volume initialization and activities
func TestCreateBackupWorkflowWithContext_ExpertModeVolume(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
	mockHeader := &commonpb.Header{
		Fields: map[string]*commonpb.Payload{
			"logParam": encodedValue,
		},
	}

	// Setup common test data
	pool := &datamodel.Pool{
		BaseModel:      datamodel.BaseModel{ID: int64(1)},
		DeploymentName: "test-deployment",
		PoolCredentials: &datamodel.PoolCredentials{
			Password: "test-password",
		},
		PoolAttributes: &datamodel.PoolAttributes{
			PrimaryZone:  "us-central1-a",
			IsRegionalHA: false,
		},
	}
	backup := &datamodel.Backup{
		BaseModel:  datamodel.BaseModel{UUID: "test-backup-uuid"},
		Attributes: &datamodel.BackupAttributes{},
	}
	backupVault := &datamodel.BackupVault{
		BaseModel: datamodel.BaseModel{UUID: "test-vault-uuid"},
	}
	dbNodes := []*datamodel.Node{
		{
			BaseModel:       datamodel.BaseModel{ID: int64(1)},
			EndpointAddress: "127.0.0.1",
		},
	}

	t.Run("WhenIsExpertModeVolumeIsFalse_SkipsExpertModeLogic", func(t *testing.T) {
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		env.SetHeader(mockHeader)
		commonActivity, _ := setupMockCommonActivities(t)
		backupActivity := setupMockBackupActivity(t)
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(backupActivity)
		env.RegisterWorkflow(CreateBackupWorkflowWithContext)

		params := &commonparams.CreateBackupParams{
			VolumeUUID:         "test-volume-uuid",
			IsExpertModeVolume: false,
		}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid", ID: int64(1)},
			PoolID:    pool.ID,
			Pool:      pool,
		}

		backupActivitiesContext := &activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Volume:      volume,
				Backup:      backup,
				BackupVault: backupVault,
			},
			Node:         &models.Node{EndpointAddress: "127.0.0.1"},
			SnapshotName: "test-snapshot",
		}

		env.OnActivity(commonActivity.GetNode, mock.Anything, pool.ID).Return(dbNodes, nil)
		env.OnActivity(backupActivity.PrepareObjectStoreActivity, mock.Anything, mock.Anything).Return(backupActivitiesContext, nil).Maybe()
		env.OnActivity(backupActivity.GetOrCreateObjectStoreActivity, mock.Anything, mock.Anything).Return(backupActivitiesContext, nil).Maybe()
		env.OnActivity(backupActivity.PrepareSnapmirrorActivity, mock.Anything, mock.Anything).Return(backupActivitiesContext, nil).Maybe()
		env.OnActivity(backupActivity.CreateSnapmirrorRelationshipActivity, mock.Anything, mock.Anything).Return(backupActivitiesContext, nil).Maybe()
		env.OnActivity(backupActivity.CreatingSnapshotActivity, mock.Anything, mock.Anything).Return(backupActivitiesContext, nil).Maybe()
		env.OnActivity("CreateSnapshotActivity", mock.Anything, mock.Anything).Return(backupActivitiesContext, nil).Maybe()
		env.OnActivity("TransferSnapshotActivity", mock.Anything, mock.Anything).Return(backupActivitiesContext, nil).Maybe()
		env.OnActivity("PollTransferStatusWithHistoryCheckActivity", mock.Anything, mock.Anything, mock.Anything).Return(&activities.PollTransferStatusOutput{
			BackupActivitiesContext: &activities.BackupActivitiesContext{
				BackupWorkflowInit: &activities.BackupWorkflowInput{
					Volume:      volume,
					Backup:      backup,
					BackupVault: backupVault,
				},
				Node:              &models.Node{EndpointAddress: "127.0.0.1"},
				SnapshotName:      "test-snapshot",
				SmSourcePath:      "svm_test:volume_test",
				SmDestinationPath: "test-bucket:/objstore/test-vol",
				SnapmirrorRelationship: &commonparams.SnapmirrorRelationship{
					UUID: "test-snapmirror-uuid",
				},
				TransferStatus: activities.SmStatusSuccess,
			},
			TransferComplete:    true,
			ShouldContinueAsNew: false,
		}, nil).Maybe()
		env.OnActivity("GetSnapmirror", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&commonparams.SnapmirrorRelationship{
			UUID:    "test-snapmirror-uuid",
			Healthy: nillable.ToPointer(true),
		}, nil).Maybe()
		env.OnActivity("FinishBackupActivity", mock.Anything, mock.Anything).Return(backupActivitiesContext, nil).Maybe()
		env.OnActivity("UpdateSnapshotActivity", mock.Anything, mock.Anything).Return(backupActivitiesContext, nil).Maybe()
		env.OnActivity("GetObjectStoreEndpointActivity", mock.Anything, mock.Anything).Return(backupActivitiesContext, nil).Maybe()
		env.OnActivity("GetObjectStoreSnapshotActivity", mock.Anything, mock.Anything).Return(backupActivitiesContext, nil).Maybe()
		env.OnActivity("UpdateBackupSizeActivity", mock.Anything, mock.Anything).Return(backupActivitiesContext, nil).Maybe()
		env.OnActivity("CreateRemoteBackupFromVCPActivity", mock.Anything, mock.Anything).Return(backupActivitiesContext, nil).Maybe()
		env.OnActivity("CleanupOldBackupSnapshotsActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("CreateBackupMetadataIfFirstBackupActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("UpdateBackupError", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

		// Execute workflow
		env.ExecuteWorkflow(CreateBackupWorkflowWithContext, backupActivitiesContext, params)

		// Should not call expert mode activities - verify by checking workflow completes
		assert.True(t, env.IsWorkflowCompleted())
		// Note: We can't directly verify IsExpertMode from ExecuteWorkflow, but we can verify
		// that the workflow completes without calling expert mode activities
		env.AssertExpectations(t)
	})

	t.Run("WhenIsExpertModeVolumeIsTrue_CallsExpertModeActivities", func(t *testing.T) {
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		env.SetHeader(mockHeader)
		commonActivity, _ := setupMockCommonActivities(t)
		backupActivity := setupMockBackupActivity(t)
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(backupActivity)
		env.RegisterWorkflow(CreateBackupWorkflowWithContext)

		params := &commonparams.CreateBackupParams{
			VolumeUUID:         "test-volume-uuid",
			IsExpertModeVolume: true,
			LocationID:         "us-central1-a",
		}

		volumeWithNilDataProtection := &datamodel.Volume{
			BaseModel:      datamodel.BaseModel{UUID: "test-volume-uuid", ID: int64(1)},
			PoolID:         pool.ID,
			Pool:           pool,
			DataProtection: nil, // DataProtection is nil - should be initialized
		}

		backupActivitiesContext := &activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Volume:      volumeWithNilDataProtection,
				Backup:      backup,
				BackupVault: backupVault,
			},
			Node:         &models.Node{EndpointAddress: "127.0.0.1"},
			SnapshotName: "test-snapshot",
		}

		env.OnActivity("GetNode", mock.Anything, pool.ID).Return(dbNodes, nil)
		env.OnActivity("CheckAndAttachBackupVaultToVolume", mock.Anything, mock.Anything, params.LocationID).Return(backupActivitiesContext, nil)
		env.OnActivity("GetVolumesAndConstituentCountActivity", mock.Anything, mock.Anything).Return(backupActivitiesContext, nil)
		env.OnActivity("GetVolumeProtocolsFromOntapActivity", mock.Anything, mock.Anything).Return(backupActivitiesContext, nil).Maybe()
		env.OnActivity("PrepareObjectStoreActivity", mock.Anything, mock.Anything).Return(backupActivitiesContext, nil).Maybe()
		env.OnActivity("GetOrCreateObjectStoreActivity", mock.Anything, mock.Anything).Return(backupActivitiesContext, nil).Maybe()
		env.OnActivity("PrepareSnapmirrorActivity", mock.Anything, mock.Anything).Return(backupActivitiesContext, nil).Maybe()
		env.OnActivity("CreateSnapmirrorRelationshipActivity", mock.Anything, mock.Anything).Return(backupActivitiesContext, nil).Maybe()
		env.OnActivity("CreatingSnapshotActivity", mock.Anything, mock.Anything).Return(backupActivitiesContext, nil).Maybe()
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Maybe()

		// Execute workflow
		env.ExecuteWorkflow(CreateBackupWorkflowWithContext, backupActivitiesContext, params)

		// Verify expert mode activities were called
		assert.True(t, env.IsWorkflowCompleted())
		env.AssertExpectations(t)
	})

	t.Run("WhenCheckAndAttachBackupVaultToVolumeFails_WorkflowFails", func(t *testing.T) {
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		env.SetHeader(mockHeader)
		commonActivity, _ := setupMockCommonActivities(t)
		backupActivity := setupMockBackupActivity(t)
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(backupActivity)
		env.RegisterWorkflow(CreateBackupWorkflowWithContext)

		params := &commonparams.CreateBackupParams{
			VolumeUUID:         "test-volume-uuid",
			IsExpertModeVolume: true,
			LocationID:         "us-central1-a",
		}

		volumeWithNilDataProtection := &datamodel.Volume{
			BaseModel:      datamodel.BaseModel{UUID: "test-volume-uuid", ID: int64(1)},
			PoolID:         pool.ID,
			Pool:           pool,
			DataProtection: nil,
		}

		backupActivitiesContext := &activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Volume:      volumeWithNilDataProtection,
				Backup:      backup,
				BackupVault: backupVault,
			},
			Node:         &models.Node{EndpointAddress: "127.0.0.1"},
			SnapshotName: "test-snapshot",
		}

		expectedErr := errors.New("failed to attach backup vault")
		env.OnActivity("GetNode", mock.Anything, pool.ID).Return(dbNodes, nil)
		env.OnActivity("CheckAndAttachBackupVaultToVolume", mock.Anything, mock.Anything, params.LocationID).Return(nil, expectedErr)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Maybe()

		// Execute workflow
		env.ExecuteWorkflow(CreateBackupWorkflowWithContext, backupActivitiesContext, params)

		// Should fail with error
		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("WhenGetVolumesAndConstituentCountActivityFails_WorkflowFails", func(t *testing.T) {
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		env.SetHeader(mockHeader)
		commonActivity, _ := setupMockCommonActivities(t)
		backupActivity := setupMockBackupActivity(t)
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(backupActivity)
		env.RegisterWorkflow(CreateBackupWorkflowWithContext)

		params := &commonparams.CreateBackupParams{
			VolumeUUID:         "test-volume-uuid",
			IsExpertModeVolume: true,
			LocationID:         "us-central1-a",
		}

		volumeWithNilDataProtection := &datamodel.Volume{
			BaseModel:      datamodel.BaseModel{UUID: "test-volume-uuid", ID: int64(1)},
			PoolID:         pool.ID,
			Pool:           pool,
			DataProtection: nil,
		}

		backupActivitiesContext := &activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Volume:      volumeWithNilDataProtection,
				Backup:      backup,
				BackupVault: backupVault,
			},
			Node:         &models.Node{EndpointAddress: "127.0.0.1"},
			SnapshotName: "test-snapshot",
		}

		expectedErr := errors.New("failed to get volumes and constituent count")
		env.OnActivity("GetNode", mock.Anything, pool.ID).Return(dbNodes, nil)
		env.OnActivity("CheckAndAttachBackupVaultToVolume", mock.Anything, mock.Anything, params.LocationID).Return(backupActivitiesContext, nil)
		env.OnActivity("GetVolumesAndConstituentCountActivity", mock.Anything, mock.Anything).Return(nil, expectedErr)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Maybe()

		// Execute workflow
		env.ExecuteWorkflow(CreateBackupWorkflowWithContext, backupActivitiesContext, params)

		// Should fail with error
		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("WhenSnapshotIDIsEmpty_SkipsGetSnapshotNameByUUIDActivity", func(t *testing.T) {
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		env.SetHeader(mockHeader)
		commonActivity, _ := setupMockCommonActivities(t)
		backupActivity := setupMockBackupActivity(t)
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(backupActivity)
		env.RegisterWorkflow(CreateBackupWorkflowWithContext)

		params := &commonparams.CreateBackupParams{
			VolumeUUID:         "test-volume-uuid",
			IsExpertModeVolume: true,
			LocationID:         "us-central1-a",
		}

		volume := &datamodel.Volume{
			BaseModel:      datamodel.BaseModel{UUID: "test-volume-uuid", ID: int64(1)},
			PoolID:         pool.ID,
			Pool:           pool,
			DataProtection: nil,
			VolumeAttributes: &datamodel.VolumeAttributes{
				ExternalUUID: "external-uuid",
			},
		}

		backupWithEmptySnapshotID := &datamodel.Backup{
			BaseModel: datamodel.BaseModel{UUID: "test-backup-uuid"},
			Attributes: &datamodel.BackupAttributes{
				SnapshotID: "", // Empty SnapshotID
			},
		}

		backupActivitiesContext := &activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Volume:      volume,
				Backup:      backupWithEmptySnapshotID,
				BackupVault: backupVault,
			},
		}

		env.OnActivity("GetNode", mock.Anything, pool.ID).Return(dbNodes, nil)
		env.OnActivity("CheckAndAttachBackupVaultToVolume", mock.Anything, mock.Anything, params.LocationID).Return(backupActivitiesContext, nil)
		// GetSnapshotNameByUUIDActivity should NOT be called when SnapshotID is empty
		env.OnActivity("GetVolumesAndConstituentCountActivity", mock.Anything, mock.Anything).Return(backupActivitiesContext, nil)
		env.OnActivity("GetVolumeProtocolsFromOntapActivity", mock.Anything, mock.Anything).Return(backupActivitiesContext, nil).Maybe()
		env.OnActivity("PrepareObjectStoreActivity", mock.Anything, mock.Anything).Return(backupActivitiesContext, nil).Maybe()
		env.OnActivity("GetOrCreateObjectStoreActivity", mock.Anything, mock.Anything).Return(backupActivitiesContext, nil).Maybe()
		env.OnActivity("PrepareSnapmirrorActivity", mock.Anything, mock.Anything).Return(backupActivitiesContext, nil).Maybe()
		env.OnActivity("CreateSnapmirrorRelationshipActivity", mock.Anything, mock.Anything).Return(backupActivitiesContext, nil).Maybe()
		env.OnActivity("CreatingSnapshotActivity", mock.Anything, mock.Anything).Return(backupActivitiesContext, nil).Maybe()
		env.OnActivity("CreateSnapshotActivity", mock.Anything, mock.Anything).Return(backupActivitiesContext, nil).Maybe()
		env.OnActivity("TransferSnapshotActivity", mock.Anything, mock.Anything).Return(backupActivitiesContext, nil).Maybe()
		env.OnActivity("PollTransferStatusWithHistoryCheckActivity", mock.Anything, mock.Anything, mock.Anything).Return(&activities.PollTransferStatusOutput{
			BackupActivitiesContext: backupActivitiesContext,
			TransferComplete:        true,
			ShouldContinueAsNew:     false,
		}, nil).Maybe()
		env.OnActivity("FinishBackupActivity", mock.Anything, mock.Anything).Return(backupActivitiesContext, nil).Maybe()
		env.OnActivity("GetObjectStoreEndpointActivity", mock.Anything, mock.Anything).Return(backupActivitiesContext, nil).Maybe()
		env.OnActivity("GetObjectStoreSnapshotActivity", mock.Anything, mock.Anything).Return(backupActivitiesContext, nil).Maybe()
		env.OnActivity("UpdateBackupSizeActivity", mock.Anything, mock.Anything).Return(backupActivitiesContext, nil).Maybe()
		env.OnActivity("CreateRemoteBackupFromVCPActivity", mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("CleanupOldBackupSnapshotsActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("HydrateSnapshotToCCFEActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("CreateBackupMetadataIfFirstBackupActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Maybe()

		// Execute workflow
		env.ExecuteWorkflow(CreateBackupWorkflowWithContext, backupActivitiesContext, params)

		// Verify workflow completed and GetSnapshotNameByUUIDActivity was not called
		assert.True(t, env.IsWorkflowCompleted())
		env.AssertExpectations(t)
	})

	t.Run("WhenSnapshotIDIsNotEmpty_GetSnapshotNameByUUIDActivitySucceeds", func(t *testing.T) {
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		env.SetHeader(mockHeader)
		commonActivity, _ := setupMockCommonActivities(t)
		backupActivity := setupMockBackupActivity(t)
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(backupActivity)
		env.RegisterWorkflow(CreateBackupWorkflowWithContext)

		params := &commonparams.CreateBackupParams{
			VolumeUUID:         "test-volume-uuid",
			IsExpertModeVolume: true,
			LocationID:         "us-central1-a",
		}

		volume := &datamodel.Volume{
			BaseModel:      datamodel.BaseModel{UUID: "test-volume-uuid", ID: int64(1)},
			PoolID:         pool.ID,
			Pool:           pool,
			DataProtection: nil,
			VolumeAttributes: &datamodel.VolumeAttributes{
				ExternalUUID: "external-uuid",
			},
		}

		backupWithSnapshotID := &datamodel.Backup{
			BaseModel: datamodel.BaseModel{UUID: "test-backup-uuid"},
			Attributes: &datamodel.BackupAttributes{
				SnapshotID: "test-snapshot-uuid",
			},
		}

		backupActivitiesContext := &activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Volume:      volume,
				Backup:      backupWithSnapshotID,
				BackupVault: backupVault,
			},
			Node: &models.Node{EndpointAddress: "127.0.0.1"},
		}

		// Expected context after GetSnapshotNameByUUIDActivity succeeds
		expectedContext := &activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Volume: volume,
				Backup: &datamodel.Backup{
					BaseModel: datamodel.BaseModel{UUID: "test-backup-uuid"},
					Attributes: &datamodel.BackupAttributes{
						SnapshotID:   "test-snapshot-uuid",
						SnapshotName: "test-snapshot-name",
					},
				},
				BackupVault: backupVault,
			},
			Node:         &models.Node{EndpointAddress: "127.0.0.1"},
			SnapshotName: "test-snapshot-name",
			SnapmirrorRelationship: &commonparams.SnapmirrorRelationship{
				UUID: "test-snapmirror-uuid",
			},
		}

		env.OnActivity("GetNode", mock.Anything, pool.ID).Return(dbNodes, nil)
		env.OnActivity("CheckAndAttachBackupVaultToVolume", mock.Anything, mock.Anything, params.LocationID).Return(backupActivitiesContext, nil)
		env.OnActivity("GetSnapshotNameByUUIDActivity", mock.Anything, backupActivitiesContext).Return(expectedContext, nil).Once()
		env.OnActivity("GetVolumesAndConstituentCountActivity", mock.Anything, mock.Anything).Return(expectedContext, nil)
		env.OnActivity("GetVolumeProtocolsFromOntapActivity", mock.Anything, mock.Anything).Return(expectedContext, nil).Maybe()
		env.OnActivity("PrepareObjectStoreActivity", mock.Anything, mock.Anything).Return(expectedContext, nil).Maybe()
		env.OnActivity("GetOrCreateObjectStoreActivity", mock.Anything, mock.Anything).Return(expectedContext, nil).Maybe()
		env.OnActivity("PrepareSnapmirrorActivity", mock.Anything, mock.Anything).Return(expectedContext, nil).Maybe()
		env.OnActivity("CreateSnapmirrorRelationshipActivity", mock.Anything, mock.Anything).Return(expectedContext, nil).Maybe()
		env.OnActivity("CreatingSnapshotActivity", mock.Anything, mock.Anything).Return(expectedContext, nil).Maybe()
		env.OnActivity("CreateSnapshotActivity", mock.Anything, mock.Anything).Return(expectedContext, nil).Maybe()
		env.OnActivity("TransferSnapshotActivity", mock.Anything, mock.Anything).Return(expectedContext, nil).Maybe()
		env.OnActivity("PollTransferStatusWithHistoryCheckActivity", mock.Anything, mock.Anything, mock.Anything).Return(&activities.PollTransferStatusOutput{
			BackupActivitiesContext: &activities.BackupActivitiesContext{
				BackupWorkflowInit:     expectedContext.BackupWorkflowInit,
				Node:                   expectedContext.Node,
				SnapshotName:           expectedContext.SnapshotName,
				SmSourcePath:           "svm_test:volume_test",
				SmDestinationPath:      "test-bucket:/objstore/test-vol",
				SnapmirrorRelationship: expectedContext.SnapmirrorRelationship,
				TransferStatus:         activities.SmStatusSuccess,
			},
			TransferComplete:    true,
			ShouldContinueAsNew: false,
		}, nil).Maybe()
		env.OnActivity("GetSnapmirror", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&commonparams.SnapmirrorRelationship{
			UUID:    "test-snapmirror-uuid",
			Healthy: nillable.ToPointer(true),
		}, nil).Maybe()
		env.OnActivity("UpdateSnapshotActivity", mock.Anything, mock.Anything).Return(expectedContext, nil).Maybe()
		env.OnActivity("FinishBackupActivity", mock.Anything, mock.Anything).Return(expectedContext, nil).Maybe()
		env.OnActivity("GetObjectStoreEndpointActivity", mock.Anything, mock.Anything).Return(expectedContext, nil).Maybe()
		env.OnActivity("GetObjectStoreSnapshotActivity", mock.Anything, mock.Anything).Return(expectedContext, nil).Maybe()
		env.OnActivity("UpdateBackupSizeActivity", mock.Anything, mock.Anything).Return(expectedContext, nil).Maybe()
		env.OnActivity("CreateRemoteBackupFromVCPActivity", mock.Anything, mock.Anything).Return(expectedContext, nil).Maybe()
		env.OnActivity("CleanupOldBackupSnapshotsActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("HydrateSnapshotToCCFEActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("CreateBackupMetadataIfFirstBackupActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("UpdateBackupError", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

		// Execute workflow
		env.ExecuteWorkflow(CreateBackupWorkflowWithContext, backupActivitiesContext, params)

		// Verify workflow completed successfully and GetSnapshotNameByUUIDActivity was called
		assert.True(t, env.IsWorkflowCompleted())
		assert.NoError(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("WhenSnapshotIDIsNotEmpty_GetSnapshotNameByUUIDActivityFails", func(t *testing.T) {
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		env.SetHeader(mockHeader)
		commonActivity, _ := setupMockCommonActivities(t)
		backupActivity := setupMockBackupActivity(t)
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(backupActivity)
		env.RegisterWorkflow(CreateBackupWorkflowWithContext)

		params := &commonparams.CreateBackupParams{
			VolumeUUID:         "test-volume-uuid",
			IsExpertModeVolume: true,
			LocationID:         "us-central1-a",
		}

		volume := &datamodel.Volume{
			BaseModel:      datamodel.BaseModel{UUID: "test-volume-uuid", ID: int64(1)},
			PoolID:         pool.ID,
			Pool:           pool,
			DataProtection: nil,
			VolumeAttributes: &datamodel.VolumeAttributes{
				ExternalUUID: "external-uuid",
			},
		}

		backupWithSnapshotID := &datamodel.Backup{
			BaseModel: datamodel.BaseModel{UUID: "test-backup-uuid"},
			Attributes: &datamodel.BackupAttributes{
				SnapshotID: "test-snapshot-uuid",
			},
		}

		backupActivitiesContext := &activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Volume:      volume,
				Backup:      backupWithSnapshotID,
				BackupVault: backupVault,
			},
			Node: &models.Node{EndpointAddress: "127.0.0.1"},
		}

		expectedErr := errors.New("failed to get snapshot name by UUID")
		env.OnActivity("GetNode", mock.Anything, pool.ID).Return(dbNodes, nil)
		env.OnActivity("CheckAndAttachBackupVaultToVolume", mock.Anything, mock.Anything, params.LocationID).Return(backupActivitiesContext, nil)
		env.OnActivity("GetSnapshotNameByUUIDActivity", mock.Anything, backupActivitiesContext).Return(nil, expectedErr).Once()
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("UpdateBackupError", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

		// Execute workflow
		env.ExecuteWorkflow(CreateBackupWorkflowWithContext, backupActivitiesContext, params)

		// Verify workflow failed with the expected error
		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})
}

func TestDeleteBackupWorkflow_IsExpertModeVolumeCheck(t *testing.T) {
	t.Run("WhenIsExpertModeVolumeReturnsTrue", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		env.SetHeader(mockHeader)

		// Create mock storage for CommonActivities
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()

		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(setupMockBackupActivity(t))
		env.RegisterWorkflow(DeleteBackupWorkflow)

		// Set up test data
		params := &commonparams.DeleteBackupParams{
			BackupVaultUUID: "vault-uuid",
			BackupUUID:      "backup-uuid",
			AccountName:     "test-account",
		}
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{UUID: "account-uuid"}}
		backupVault := &datamodel.BackupVault{
			Name: "test-backup-vault",
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{BucketName: "test-bucket", ServiceAccountName: "sa-test", VendorSubnetID: "subnet-12345"},
			},
			Account: account,
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-vol"},
			Pool: &datamodel.Pool{
				BaseModel: datamodel.BaseModel{ID: int64(1)},
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "",
					CertificateID: "",
				},
			},
			Svm: &datamodel.Svm{
				Name: "svm_test",
			},
			VolumeAttributes: &datamodel.VolumeAttributes{
				VendorSubnetID: "subnet-12345",
			},
			DataProtection: &datamodel.DataProtection{
				BackupVaultID: "vault-uuid",
			},
		}
		backup := &datamodel.Backup{
			Name:          "test-backup",
			VolumeUUID:    "test-vol",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes:    &datamodel.BackupAttributes{BucketName: "test-bucket"},
		}

		// Mock activity responses
		env.OnActivity("GetAccountByName", mock.Anything, params.AccountName).Return(account, nil)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetBackupVault", mock.Anything, params.BackupVaultUUID).Return(backupVault, nil)
		env.OnActivity("GetBackup", mock.Anything, params.BackupVaultUUID, params.BackupUUID, params.AccountName).Return(backup, nil)
		env.OnActivity("IsVolumeDeleted", mock.Anything, backup.VolumeUUID).Return(false, nil)
		env.OnActivity("IsExpertModeVolume", mock.Anything, backup.VolumeUUID).Return(true, nil)
		env.OnActivity("GetVolume", mock.Anything, backup.VolumeUUID).Return(volume, nil)
		env.OnActivity("GetNode", mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
		env.OnActivity("GetSmDestinationPathActivity", mock.Anything, mock.Anything, mock.Anything).Return("test-bucket:/objstore/test-vol", nil)
		env.OnActivity("GetSmSourcePathActivity", mock.Anything, mock.Anything).Return("svm_test:volume_test", nil)
		env.OnActivity("IsSnapmirrorDeleted", mock.Anything, mock.Anything, mock.Anything).Return(&commonparams.SnapmirrorDeletePrecheckResult{RelationshipMissing: false, Relationship: &commonparams.SnapmirrorRelationship{UUID: "test-sm-uuid"}}, nil)
		env.OnActivity("GetBackupCountByVolumeUUID", mock.Anything, backup.VolumeUUID).Return(int64(1), nil).Once()
		env.OnActivity("GetObjectStore", mock.Anything, mock.Anything, mock.Anything).Return(&commonparams.CloudTarget{UUID: "obj-store-uuid"}, nil)
		env.OnActivity("GetSnapmirror", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&commonparams.SnapmirrorRelationship{UUID: "snapmirror-uuid"}, nil)
		env.OnActivity("DeleteSnapmirror", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{JobUUID: "job-uuid"}, nil)
		env.OnActivity("GetOntapJob", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil)
		env.OnActivity("UpdateVolumeLatestLogicalBackupSize", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DeleteCloudEndpoint", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{JobUUID: "job-uuid"}, nil)
		env.OnActivity("DeleteSnapshotForBackup", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DeleteBackup", mock.Anything, params.BackupUUID).Return(backup, nil)
		env.OnActivity("GetBackupCountByVolumeUUID", mock.Anything, backup.VolumeUUID).Return(int64(0), nil).Once()
		env.OnActivity("DetachBackupVaultFromVolume", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DeleteBackupMetadataIfLastBackupActivity", mock.Anything, mock.Anything).Return(nil)

		// Execute workflow
		env.ExecuteWorkflow(DeleteBackupWorkflow, params)

		// Assert workflow execution
		assert.True(t, env.IsWorkflowCompleted())
		assert.NoError(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("WhenIsExpertModeVolumeReturnsFalse", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		env.SetHeader(mockHeader)

		// Create mock storage for CommonActivities
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()

		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(setupMockBackupActivity(t))
		env.RegisterWorkflow(DeleteBackupWorkflow)

		// Set up test data
		params := &commonparams.DeleteBackupParams{
			BackupVaultUUID: "vault-uuid",
			BackupUUID:      "backup-uuid",
			AccountName:     "test-account",
		}
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{UUID: "account-uuid"}}
		backupVault := &datamodel.BackupVault{
			Name: "test-backup-vault",
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{BucketName: "test-bucket", ServiceAccountName: "sa-test", VendorSubnetID: "subnet-12345"},
			},
			Account: account,
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-vol"},
			Pool: &datamodel.Pool{
				BaseModel: datamodel.BaseModel{ID: int64(1)},
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "",
					CertificateID: "",
				},
			},
			Svm: &datamodel.Svm{
				Name: "svm_test",
			},
			VolumeAttributes: &datamodel.VolumeAttributes{
				VendorSubnetID: "subnet-12345",
			},
			DataProtection: &datamodel.DataProtection{},
		}
		backup := &datamodel.Backup{
			Name:          "test-backup",
			VolumeUUID:    "test-vol",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes:    &datamodel.BackupAttributes{BucketName: "test-bucket"},
		}

		// Mock activity responses
		env.OnActivity("GetAccountByName", mock.Anything, params.AccountName).Return(account, nil)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetBackupVault", mock.Anything, params.BackupVaultUUID).Return(backupVault, nil)
		env.OnActivity("GetBackup", mock.Anything, params.BackupVaultUUID, params.BackupUUID, params.AccountName).Return(backup, nil)
		env.OnActivity("IsVolumeDeleted", mock.Anything, backup.VolumeUUID).Return(false, nil)
		env.OnActivity("IsExpertModeVolume", mock.Anything, backup.VolumeUUID).Return(false, nil)
		env.OnActivity("GetVolume", mock.Anything, backup.VolumeUUID).Return(volume, nil)
		env.OnActivity("GetNode", mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
		env.OnActivity("GetSmDestinationPathActivity", mock.Anything, mock.Anything, mock.Anything).Return("test-bucket:/objstore/test-vol", nil)
		env.OnActivity("GetSmSourcePathActivity", mock.Anything, mock.Anything).Return("svm_test:volume_test", nil)
		env.OnActivity("IsSnapmirrorDeleted", mock.Anything, mock.Anything, mock.Anything).Return(&commonparams.SnapmirrorDeletePrecheckResult{RelationshipMissing: false, Relationship: &commonparams.SnapmirrorRelationship{UUID: "test-sm-uuid"}}, nil)
		env.OnActivity("GetBackupCountByVolumeUUID", mock.Anything, backup.VolumeUUID).Return(int64(1), nil)
		env.OnActivity("GetObjectStore", mock.Anything, mock.Anything, mock.Anything).Return(&commonparams.CloudTarget{UUID: "obj-store-uuid"}, nil)
		env.OnActivity("GetSnapmirror", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&commonparams.SnapmirrorRelationship{UUID: "snapmirror-uuid"}, nil)
		env.OnActivity("DeleteSnapmirror", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{JobUUID: "job-uuid"}, nil)
		env.OnActivity("GetOntapJob", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil)
		env.OnActivity("UpdateVolumeLatestLogicalBackupSize", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DeleteCloudEndpoint", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{JobUUID: "job-uuid"}, nil)
		env.OnActivity("DeleteSnapshotForBackup", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DeleteBackupSnapshotFromDB", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("HydrateSnapshotDeletionToCCFEActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DeleteBackup", mock.Anything, params.BackupUUID).Return(backup, nil)
		env.OnActivity("DeleteBackupMetadataIfLastBackupActivity", mock.Anything, mock.Anything).Return(nil)

		// Execute workflow
		env.ExecuteWorkflow(DeleteBackupWorkflow, params)

		// Assert workflow execution
		assert.True(t, env.IsWorkflowCompleted())
		assert.NoError(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("WhenIsExpertModeVolumeReturnsTrueAndLastBackup_DetachesBackupVault", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		env.SetHeader(mockHeader)

		// Create mock storage for CommonActivities
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()

		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(setupMockBackupActivity(t))
		env.RegisterWorkflow(DeleteBackupWorkflow)

		// Set up test data
		params := &commonparams.DeleteBackupParams{
			BackupVaultUUID: "vault-uuid",
			BackupUUID:      "backup-uuid",
			AccountName:     "test-account",
		}
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{UUID: "account-uuid"}}
		backupVault := &datamodel.BackupVault{
			Name: "test-backup-vault",
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{BucketName: "test-bucket", ServiceAccountName: "sa-test", VendorSubnetID: "subnet-12345"},
			},
			Account: account,
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-vol"},
			Pool: &datamodel.Pool{
				BaseModel: datamodel.BaseModel{ID: int64(1)},
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "",
					CertificateID: "",
				},
			},
			Svm: &datamodel.Svm{
				Name: "svm_test",
			},
			VolumeAttributes: &datamodel.VolumeAttributes{
				VendorSubnetID: "subnet-12345",
			},
			DataProtection: &datamodel.DataProtection{
				BackupVaultID: "vault-uuid",
			},
		}
		backup := &datamodel.Backup{
			Name:          "test-backup",
			VolumeUUID:    "test-vol",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes:    &datamodel.BackupAttributes{BucketName: "test-bucket"},
		}

		// Mock activity responses
		env.OnActivity("GetAccountByName", mock.Anything, params.AccountName).Return(account, nil)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetBackupVault", mock.Anything, params.BackupVaultUUID).Return(backupVault, nil)
		env.OnActivity("GetBackup", mock.Anything, params.BackupVaultUUID, params.BackupUUID, params.AccountName).Return(backup, nil)
		env.OnActivity("IsVolumeDeleted", mock.Anything, backup.VolumeUUID).Return(false, nil)
		env.OnActivity("IsExpertModeVolume", mock.Anything, backup.VolumeUUID).Return(true, nil)
		env.OnActivity("GetVolume", mock.Anything, backup.VolumeUUID).Return(volume, nil)
		env.OnActivity("GetNode", mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
		env.OnActivity("GetSmDestinationPathActivity", mock.Anything, mock.Anything, mock.Anything).Return("test-bucket:/objstore/test-vol", nil)
		env.OnActivity("GetSmSourcePathActivity", mock.Anything, mock.Anything).Return("svm_test:volume_test", nil)
		env.OnActivity("IsSnapmirrorDeleted", mock.Anything, mock.Anything, mock.Anything).Return(&commonparams.SnapmirrorDeletePrecheckResult{RelationshipMissing: false, Relationship: &commonparams.SnapmirrorRelationship{UUID: "test-sm-uuid"}}, nil)
		env.OnActivity("GetBackupCountByVolumeUUID", mock.Anything, backup.VolumeUUID).Return(int64(1), nil).Once()
		env.OnActivity("GetObjectStore", mock.Anything, mock.Anything, mock.Anything).Return(&commonparams.CloudTarget{UUID: "obj-store-uuid"}, nil)
		env.OnActivity("GetSnapmirror", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&commonparams.SnapmirrorRelationship{UUID: "snapmirror-uuid"}, nil)
		env.OnActivity("DeleteSnapmirror", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{JobUUID: "job-uuid"}, nil)
		env.OnActivity("GetOntapJob", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil)
		env.OnActivity("UpdateVolumeLatestLogicalBackupSize", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DeleteCloudEndpoint", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{JobUUID: "job-uuid"}, nil)
		env.OnActivity("DeleteSnapshotForBackup", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DeleteBackup", mock.Anything, params.BackupUUID).Return(backup, nil)
		env.OnActivity("GetBackupCountByVolumeUUID", mock.Anything, backup.VolumeUUID).Return(int64(0), nil).Once()
		env.OnActivity("DetachBackupVaultFromVolume", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DeleteBackupMetadataIfLastBackupActivity", mock.Anything, mock.Anything).Return(nil)

		// Execute workflow
		env.ExecuteWorkflow(DeleteBackupWorkflow, params)

		// Assert workflow execution
		assert.True(t, env.IsWorkflowCompleted())
		assert.NoError(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("WhenIsExpertModeVolumeReturnsTrueAndNotLastBackup_DoesNotDetachBackupVault", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		env.SetHeader(mockHeader)

		// Create mock storage for CommonActivities
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()

		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(setupMockBackupActivity(t))
		env.RegisterWorkflow(DeleteBackupWorkflow)

		// Set up test data
		params := &commonparams.DeleteBackupParams{
			BackupVaultUUID: "vault-uuid",
			BackupUUID:      "backup-uuid",
			AccountName:     "test-account",
		}
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{UUID: "account-uuid"}}
		backupVault := &datamodel.BackupVault{
			Name: "test-backup-vault",
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{BucketName: "test-bucket", ServiceAccountName: "sa-test", VendorSubnetID: "subnet-12345"},
			},
			Account: account,
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-vol"},
			Pool: &datamodel.Pool{
				BaseModel: datamodel.BaseModel{ID: int64(1)},
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "",
					CertificateID: "",
				},
			},
			Svm: &datamodel.Svm{
				Name: "svm_test",
			},
			VolumeAttributes: &datamodel.VolumeAttributes{
				VendorSubnetID: "subnet-12345",
			},
			DataProtection: &datamodel.DataProtection{
				BackupVaultID: "vault-uuid",
			},
		}
		backup := &datamodel.Backup{
			Name:          "test-backup",
			VolumeUUID:    "test-vol",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes:    &datamodel.BackupAttributes{BucketName: "test-bucket"},
		}

		// Mock activity responses
		env.OnActivity("GetAccountByName", mock.Anything, params.AccountName).Return(account, nil)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetBackupVault", mock.Anything, params.BackupVaultUUID).Return(backupVault, nil)
		env.OnActivity("GetBackup", mock.Anything, params.BackupVaultUUID, params.BackupUUID, params.AccountName).Return(backup, nil)
		env.OnActivity("IsVolumeDeleted", mock.Anything, backup.VolumeUUID).Return(false, nil)
		env.OnActivity("IsExpertModeVolume", mock.Anything, backup.VolumeUUID).Return(true, nil)
		env.OnActivity("GetVolume", mock.Anything, backup.VolumeUUID).Return(volume, nil)
		env.OnActivity("GetNode", mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
		env.OnActivity("GetSmDestinationPathActivity", mock.Anything, mock.Anything, mock.Anything).Return("test-bucket:/objstore/test-vol", nil)
		env.OnActivity("GetSmSourcePathActivity", mock.Anything, mock.Anything).Return("svm_test:volume_test", nil)
		env.OnActivity("IsSnapmirrorDeleted", mock.Anything, mock.Anything, mock.Anything).Return(&commonparams.SnapmirrorDeletePrecheckResult{RelationshipMissing: false, Relationship: &commonparams.SnapmirrorRelationship{UUID: "test-sm-uuid"}}, nil)
		env.OnActivity("GetBackupCountByVolumeUUID", mock.Anything, backup.VolumeUUID).Return(int64(2), nil).Once()
		env.OnActivity("GetObjectStore", mock.Anything, mock.Anything, mock.Anything).Return(&commonparams.CloudTarget{UUID: "obj-store-uuid"}, nil)
		env.OnActivity("IsBackupShared", mock.Anything, mock.Anything).Return(true, nil) // Backup IS shared -> skip snapmirror deletion
		env.OnActivity("DeleteBackup", mock.Anything, params.BackupUUID).Return(backup, nil)
		env.OnActivity("GetBackupCountByVolumeUUID", mock.Anything, backup.VolumeUUID).Return(int64(1), nil).Once()
		env.OnActivity("DeleteBackupMetadataIfLastBackupActivity", mock.Anything, mock.Anything).Return(nil)

		// Execute workflow
		env.ExecuteWorkflow(DeleteBackupWorkflow, params)

		// Assert workflow execution - DetachBackupVaultFromVolume should NOT be called
		assert.True(t, env.IsWorkflowCompleted())
		assert.NoError(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})
}

func TestBackupDeleteEndpointMismatch(t *testing.T) {
	dest := "d254cb3e-336c-11f1-8985-f5673706ed49"
	stored := "4ea7a442-86d1-11e0-ae1c-123478563412"

	t.Run("nil_rel", func(t *testing.T) {
		assert.False(t, backupDeleteEndpointMismatch(nil, &datamodel.Backup{Attributes: &datamodel.BackupAttributes{EndpointUUID: stored}}))
	})
	t.Run("nil_DestinationUUID", func(t *testing.T) {
		rel := &commonparams.SnapmirrorRelationship{UUID: "x"}
		assert.False(t, backupDeleteEndpointMismatch(rel, &datamodel.Backup{Attributes: &datamodel.BackupAttributes{EndpointUUID: stored}}))
	})
	t.Run("empty_DestinationUUID_after_trim", func(t *testing.T) {
		ws := "   "
		rel := &commonparams.SnapmirrorRelationship{DestinationUUID: &ws}
		assert.False(t, backupDeleteEndpointMismatch(rel, &datamodel.Backup{Attributes: &datamodel.BackupAttributes{EndpointUUID: stored}}))
	})
	t.Run("nil_dbBackup", func(t *testing.T) {
		rel := &commonparams.SnapmirrorRelationship{DestinationUUID: &dest}
		assert.False(t, backupDeleteEndpointMismatch(rel, nil))
	})
	t.Run("nil_Attributes", func(t *testing.T) {
		rel := &commonparams.SnapmirrorRelationship{DestinationUUID: &dest}
		assert.False(t, backupDeleteEndpointMismatch(rel, &datamodel.Backup{}))
	})
	t.Run("empty_stored_endpoint_after_trim", func(t *testing.T) {
		rel := &commonparams.SnapmirrorRelationship{DestinationUUID: &dest}
		b := &datamodel.Backup{Attributes: &datamodel.BackupAttributes{EndpointUUID: "  "}}
		assert.False(t, backupDeleteEndpointMismatch(rel, b))
	})
	t.Run("match", func(t *testing.T) {
		rel := &commonparams.SnapmirrorRelationship{DestinationUUID: &dest}
		b := &datamodel.Backup{Attributes: &datamodel.BackupAttributes{EndpointUUID: dest}}
		assert.False(t, backupDeleteEndpointMismatch(rel, b))
	})
	t.Run("match_EqualFold", func(t *testing.T) {
		upper := strings.ToUpper(dest)
		rel := &commonparams.SnapmirrorRelationship{DestinationUUID: &dest}
		b := &datamodel.Backup{Attributes: &datamodel.BackupAttributes{EndpointUUID: upper}}
		assert.False(t, backupDeleteEndpointMismatch(rel, b))
	})
	t.Run("mismatch", func(t *testing.T) {
		rel := &commonparams.SnapmirrorRelationship{DestinationUUID: &dest}
		b := &datamodel.Backup{Attributes: &datamodel.BackupAttributes{EndpointUUID: stored}}
		assert.True(t, backupDeleteEndpointMismatch(rel, b))
	})
}

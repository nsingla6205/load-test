package common

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	errs "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
)

func TestHydrateReplicationCreate(t *testing.T) {
	mockLogger := log.NewLogger()
	ctx := context.Background()
	replication := models.ReplicationHydrateObject{
		ResourceId:       "replication-name",
		ReplicationState: "CREATING",
	}
	region := "mocked-region"
	projectId := "mocked-project"
	volumeResourceID := "mocked-volume-id"
	token := "mocked-token"

	// Save and mock hydrateToCffe
	originalHydrateToCffe := hydrateToCffe
	defer func() { hydrateToCffe = originalHydrateToCffe }()
	t.Run("WhenHydrateToCffeReturnError", func(tt *testing.T) {
		expectedErr := &errs.CustomError{
			OriginalErr: errors.New("some error"),
		}
		hydrateToCffe = func(ctx context.Context, logger log.Logger, v any, url string, method string, token string) error {
			return expectedErr
		}
		err := _hydrateReplicationCreate(ctx, mockLogger, replication, region, projectId, volumeResourceID, token)
		assert.Error(tt, err.(*errs.CustomError).Unwrap())
		assert.Equal(tt, expectedErr, err)
	})
	t.Run("WhenSuccessful", func(tt *testing.T) {
		hydrateToCffe = func(ctx context.Context, logger log.Logger, v any, url string, method string, token string) error {
			return nil
		}
		err := _hydrateReplicationCreate(ctx, mockLogger, replication, region, projectId, volumeResourceID, token)
		assert.NoError(tt, err, nil)
	})
}

func TestHydrateVolumeCreate(t *testing.T) {
	mockLogger := log.NewLogger()
	ctx := context.Background()
	volume := models.VolumeHydrateObject{
		ResourceId: "vol-1",
		VolumeId:   "uuid-1",
		PoolId:     "pool-1",
		Protocols:  []string{"NFS"},
		State:      "READY",
		QuotaInGib: 10,
	}
	location := "mocked-location"
	projectId := "mocked-project"
	token := "mocked-token"
	originalHydrateToCffe := hydrateToCffe
	defer func() { hydrateToCffe = originalHydrateToCffe }()
	t.Run("WhenHydrateToCffeReturnError", func(tt *testing.T) {
		expectedErr := &errs.CustomError{
			OriginalErr: errors.New("some error"),
		}
		hydrateToCffe = func(ctx context.Context, logger log.Logger, v any, url string, method string, token string) error {
			return expectedErr
		}
		err := _hydrateVolumeCreate(ctx, mockLogger, volume, location, projectId, token)
		assert.Error(tt, err.(*errs.CustomError).Unwrap())
		assert.Equal(tt, expectedErr, err)
	})
	t.Run("WhenSuccessful", func(tt *testing.T) {
		hydrateToCffe = func(ctx context.Context, logger log.Logger, v any, url string, method string, token string) error {
			return nil
		}
		err := _hydrateVolumeCreate(ctx, mockLogger, volume, location, projectId, token)
		assert.NoError(tt, err, nil)
	})
}

func TestHydrateUpdatedVolume(t *testing.T) {
	ctx := context.Background()
	volume := models.VolumeUpdateCCFERequest{
		CloneDetails: &models.VolumeCloneDetailsUpdateMaskRequest{
			ParentVolumeId:       "",
			ParentSnapshotId:     "",
			SharedBytes:          0,
			SplitCompletePercent: 0,
		},
	}
	region := "mocked-region"
	projectID := "mocked-project"
	volumeResourceID := "mocked-volume"
	token := "mocked-token"

	originalHydrateToCffe := hydrateToCffe
	defer func() { hydrateToCffe = originalHydrateToCffe }()

	t.Run("WhenHydrateToCffeReturnError", func(tt *testing.T) {
		expectedErr := &errs.CustomError{
			OriginalErr: errors.New("some error"),
		}
		hydrateToCffe = func(ctx context.Context, logger log.Logger, v any, url string, method string, token string) error {
			return expectedErr
		}
		err := _hydrateUpdatedVolume(ctx, volume, region, projectID, volumeResourceID, token)
		assert.Error(tt, err.(*errs.CustomError).Unwrap())
		assert.Equal(tt, expectedErr, err)
	})

	t.Run("WhenSuccessful_UsesPatchAndUpdateMask", func(tt *testing.T) {
		var gotURL, gotMethod string
		var gotPayload models.VolumeUpdateCCFERequest
		hydrateToCffe = func(ctx context.Context, logger log.Logger, v any, url string, method string, token string) error {
			gotURL = url
			gotMethod = method
			gotPayload = v.(models.VolumeUpdateCCFERequest)
			return nil
		}

		err := _hydrateUpdatedVolume(ctx, volume, region, projectID, volumeResourceID, token)
		assert.NoError(tt, err)
		assert.Equal(tt, http.MethodPatch, gotMethod)
		assert.Equal(tt, volume, gotPayload)
		assert.Equal(
			tt,
			fmt.Sprintf("%s/v1internal/projects/%s/locations/%s/volumes/%s?update_mask=cloneDetails", baseUri, projectID, region, volumeResourceID),
			gotURL,
		)
	})

	t.Run("WhenCloneDetailsNil_SendsNullAndParentUpdateMask", func(tt *testing.T) {
		clearVol := models.VolumeUpdateCCFERequest{
			CloneDetails: nil,
		}
		var gotURL, gotMethod string
		var gotBody []byte
		hydrateToCffe = func(ctx context.Context, logger log.Logger, v any, url string, method string, token string) error {
			gotURL = url
			gotMethod = method
			var err error
			gotBody, err = json.Marshal(v)
			return err
		}

		err := _hydrateUpdatedVolume(ctx, clearVol, region, projectID, volumeResourceID, token)
		assert.NoError(tt, err)
		assert.Equal(tt, http.MethodPatch, gotMethod)
		assert.JSONEq(tt, `{"cloneDetails":null}`, string(gotBody))
		assert.Equal(
			tt,
			fmt.Sprintf("%s/v1internal/projects/%s/locations/%s/volumes/%s?update_mask=cloneDetails", baseUri, projectID, region, volumeResourceID),
			gotURL,
		)
	})
}

func TestHydrateVolumeDelete(t *testing.T) {
	mockLogger := log.NewLogger()
	ctx := context.Background()
	volumeResourceID := "vol-1"
	region := "mocked-region"
	projectId := "mocked-project"
	token := "mocked-token"
	originalHydrateToCffe := hydrateToCffe
	defer func() { hydrateToCffe = originalHydrateToCffe }()
	t.Run("WhenHydrateToCffeReturnError", func(tt *testing.T) {
		expectedErr := &errs.CustomError{
			OriginalErr: errors.New("some error"),
		}
		hydrateToCffe = func(ctx context.Context, logger log.Logger, v any, url string, method string, token string) error {
			return expectedErr
		}
		err := _hydrateVolumeDelete(ctx, mockLogger, volumeResourceID, region, projectId, token)
		assert.Error(tt, err.(*errs.CustomError).Unwrap())
		assert.Equal(tt, expectedErr, err)
	})
	t.Run("WhenSuccessful", func(tt *testing.T) {
		hydrateToCffe = func(ctx context.Context, logger log.Logger, v any, url string, method string, token string) error {
			return nil
		}
		err := _hydrateVolumeDelete(ctx, mockLogger, volumeResourceID, region, projectId, token)
		assert.NoError(tt, err, nil)
	})
}

func TestBatchHydrateCreatedSnapshots(t *testing.T) {
	mockLogger := log.NewLogger()
	ctx := context.Background()
	currVolumeName := "mock-volume"
	location := "mock-location"
	projectId := "mock-project"
	token := "mock-token"

	// Save and mock hydrateToCffe
	originalHydrateToCffe := hydrateToCffe
	defer func() { hydrateToCffe = originalHydrateToCffe }()

	t.Run("HandlesEmptyResourcesArray", func(tt *testing.T) {
		hydrateToCffe = func(ctx context.Context, logger log.Logger, v any, url string, method string, token string) error {
			return nil
		}
		err := _batchHydrateCreatedSnapshots(ctx, mockLogger, []models.Request{}, currVolumeName, location, projectId, token)
		assert.NoError(tt, err)
	})

	t.Run("HandlesSingleResource", func(tt *testing.T) {
		hydrateToCffe = func(ctx context.Context, logger log.Logger, v any, url string, method string, token string) error {
			return nil
		}
		resources := []models.Request{
			{Snapshot: &models.HydrateSnapshot{SnapshotId: "uuid-1", ResourceId: "snap-1"}},
		}
		err := _batchHydrateCreatedSnapshots(ctx, mockLogger, resources, currVolumeName, location, projectId, token)
		assert.NoError(tt, err)
	})

	t.Run("HandlesMultipleResources", func(tt *testing.T) {
		hydrateToCffe = func(ctx context.Context, logger log.Logger, v any, url string, method string, token string) error {
			return nil
		}
		resources := []models.Request{
			{Snapshot: &models.HydrateSnapshot{SnapshotId: "uuid-1", ResourceId: "snap-1"}},
			{Snapshot: &models.HydrateSnapshot{SnapshotId: "uuid-2", ResourceId: "snap-2"}},
		}
		err := _batchHydrateCreatedSnapshots(ctx, mockLogger, resources, currVolumeName, location, projectId, token)
		assert.NoError(tt, err)
	})

	t.Run("HandlesHydrateToCffeError", func(tt *testing.T) {
		hydrateToCffe = func(ctx context.Context, logger log.Logger, v any, url string, method string, token string) error {
			return errors.New("mock error")
		}
		resources := []models.Request{
			{Snapshot: &models.HydrateSnapshot{SnapshotId: "uuid-1", ResourceId: "snap-1"}},
		}
		err := _batchHydrateCreatedSnapshots(ctx, mockLogger, resources, currVolumeName, location, projectId, token)
		assert.NoError(tt, err)
	})

	t.Run("HandlesBatchSizeLimit", func(tt *testing.T) {
		hydrateToCffe = func(ctx context.Context, logger log.Logger, v any, url string, method string, token string) error {
			return nil
		}
		resources := []models.Request{}
		for i := 0; i < batchSize+1; i++ {
			resources = append(resources, models.Request{Snapshot: &models.HydrateSnapshot{SnapshotId: fmt.Sprintf("uuid-%d", i), ResourceId: fmt.Sprintf("snap-%d", i)}})
		}
		err := _batchHydrateCreatedSnapshots(ctx, mockLogger, resources, currVolumeName, location, projectId, token)
		assert.NoError(tt, err)
	})
}

func TestBatchHydrateDeletedSnapshots(t *testing.T) {
	mockLogger := log.NewLogger()
	ctx := context.Background()
	currVolumeName := "mock-volume"
	region := "mock-region"
	projectId := "mock-project"
	token := "mock-token"

	// Save and mock hydrateToCffe
	originalHydrateToCffe := hydrateToCffe
	defer func() { hydrateToCffe = originalHydrateToCffe }()

	t.Run("HandlesEmptyHydrateSnapshot", func(tt *testing.T) {
		hydrateToCffe = func(ctx context.Context, logger log.Logger, v any, url string, method string, token string) error {
			return nil
		}
		err := _batchHydrateDeletedSnapshots(ctx, mockLogger, []models.Request{}, currVolumeName, region, projectId, token)
		assert.Nil(tt, err)
	})

	t.Run("HandlesEmptyNamesError", func(tt *testing.T) {
		hydrateToCffe = func(ctx context.Context, logger log.Logger, v any, url string, method string, token string) error {
			return nil
		}
		hydrateSnapshot := []models.Request{
			{Snapshot: &models.HydrateSnapshot{SnapshotId: "uuid-1", ResourceId: ""}},
		}
		err := _batchHydrateDeletedSnapshots(ctx, mockLogger, hydrateSnapshot, currVolumeName, region, projectId, token)
		assert.Nil(tt, err)
	})

	t.Run("HandlesSingleHydrateSnapshot", func(tt *testing.T) {
		hydrateToCffe = func(ctx context.Context, logger log.Logger, v any, url string, method string, token string) error {
			return nil
		}
		hydrateSnapshot := []models.Request{
			{Snapshot: &models.HydrateSnapshot{SnapshotId: "uuid-1", ResourceId: "snap-1"}},
		}
		err := _batchHydrateDeletedSnapshots(ctx, mockLogger, hydrateSnapshot, currVolumeName, region, projectId, token)
		assert.NoError(tt, err)
	})

	t.Run("HandlesMultipleHydrateSnapshots", func(tt *testing.T) {
		hydrateToCffe = func(ctx context.Context, logger log.Logger, v any, url string, method string, token string) error {
			return nil
		}
		hydrateSnapshot := []models.Request{
			{Snapshot: &models.HydrateSnapshot{SnapshotId: "uuid-1", ResourceId: "snap-1"}},
			{Snapshot: &models.HydrateSnapshot{SnapshotId: "uuid-2", ResourceId: "snap-2"}},
		}
		err := _batchHydrateDeletedSnapshots(ctx, mockLogger, hydrateSnapshot, currVolumeName, region, projectId, token)
		assert.NoError(tt, err)
	})

	t.Run("HandlesHydrateToCffeError", func(tt *testing.T) {
		hydrateToCffe = func(ctx context.Context, logger log.Logger, v any, url string, method string, token string) error {
			return errors.New("mock error")
		}
		hydrateSnapshot := []models.Request{
			{Snapshot: &models.HydrateSnapshot{SnapshotId: "uuid-1", ResourceId: "snap-1"}},
		}
		err := _batchHydrateDeletedSnapshots(ctx, mockLogger, hydrateSnapshot, currVolumeName, region, projectId, token)
		assert.Error(tt, err)
		assert.Equal(tt, "mock error", err.Error())
	})

	t.Run("HandlesBatchSizeLimit", func(tt *testing.T) {
		hydrateToCffe = func(ctx context.Context, logger log.Logger, v any, url string, method string, token string) error {
			return nil
		}
		hydrateSnapshot := []models.Request{}
		for i := 0; i < batchSize+1; i++ {
			hydrateSnapshot = append(hydrateSnapshot, models.Request{Snapshot: &models.HydrateSnapshot{SnapshotId: fmt.Sprintf("uuid-%d", i), ResourceId: fmt.Sprintf("snap-%d", i)}})
		}
		err := _batchHydrateDeletedSnapshots(ctx, mockLogger, hydrateSnapshot, currVolumeName, region, projectId, token)
		assert.NoError(tt, err)
	})
}

func TestHydrateCreatedScheduledBackups(t *testing.T) {
	ctx := context.Background()
	mockLogger := log.NewMockLogger(t)
	projectId := "mocked-project"
	location := "mocked-location"
	backupVaultName := "mocked-backup-vault"
	token := "mocked-token"

	resources := []models.Request{
		{
			Backup: &models.HydrateBackup{
				ResourceId:       "mock-backup",
				BackupId:         "mock-uuid",
				VolumeUsageBytes: nil,
			},
		},
	}

	originalHydrateToCcfe := hydrateToCffe
	defer func() { hydrateToCffe = originalHydrateToCcfe }()
	t.Run("WhenHydrateToCcfeSucceeds", func(tt *testing.T) {
		hydrateToCffe = func(ctx context.Context, logger log.Logger, v any, url string, method string, token string) error {
			return nil
		}
		mockLogger.On("Infof", mock.Anything, mock.Anything).Return(nil)

		err := HydrateCreatedBackups(ctx, mockLogger, resources, backupVaultName, location, projectId, token)
		assert.NoError(tt, err)
	})

	t.Run("WhenHydrateToCcfeErrors", func(tt *testing.T) {
		hydrateToCffe = func(ctx context.Context, logger log.Logger, v any, url string, method string, token string) error {
			return errors.New("could not hydrate backups to ccfe")
		}
		mockLogger.On("Errorf", mock.Anything, mock.Anything, mock.Anything).Return(nil)

		err := HydrateCreatedBackups(ctx, mockLogger, resources, backupVaultName, location, projectId, token)
		assert.Error(tt, err)
		assert.Equal(tt, "could not hydrate backups to ccfe", err.Error())
	})
}

func TestHydrateDeletedScheduledBackups(t *testing.T) {
	ctx := context.Background()
	mockLogger := log.NewMockLogger(t)
	projectId := "mocked-project"
	location := "mocked-location"
	backupVaultName := "mocked-backup-vault"
	token := "mocked-token"
	names := []string{"mock-backup-1", "mock-backup-2"}

	originalHydrateToCcfe := hydrateToCffe
	defer func() { hydrateToCffe = originalHydrateToCcfe }()
	t.Run("WhenHydrateToCcfeSucceeds", func(tt *testing.T) {
		hydrateToCffe = func(ctx context.Context, logger log.Logger, v any, url string, method string, token string) error {
			return nil
		}
		mockLogger.On("Infof", mock.Anything, mock.Anything).Return(nil)

		err := HydrateDeletedBackups(ctx, mockLogger, names, backupVaultName, location, projectId, token)
		assert.NoError(tt, err)
	})

	t.Run("WhenHydrateToCcfeErrors", func(tt *testing.T) {
		hydrateToCffe = func(ctx context.Context, logger log.Logger, v any, url string, method string, token string) error {
			return errors.New("could not hydrate backups to ccfe")
		}
		mockLogger.On("Errorf", mock.Anything, mock.Anything, mock.Anything).Return(nil)

		err := HydrateDeletedBackups(ctx, mockLogger, names, backupVaultName, location, projectId, token)
		assert.Error(tt, err)
		assert.Equal(tt, "could not hydrate backups to ccfe", err.Error())
	})
}

func TestHydrateCreatedBackupVaults(t *testing.T) {
	ctx := context.Background()
	mockLogger := log.NewLogger()
	projectId := "mocked-project"
	location := "mocked-location"
	backupVaultName := "mocked-backup-vault"
	token := "mocked-token"
	resources := []models.Request{
		{
			BackupVault: &models.HydrateBackupVault{
				ResourceId:    "backup-vault-1",
				BackupVaultId: "uuid-1",
			},
		},
	}

	originalHydrateToCcfe := hydrateToCffe
	defer func() { hydrateToCffe = originalHydrateToCcfe }()

	t.Run("WhenHydrateToCcfeSucceeds", func(tt *testing.T) {
		expectedURL := fmt.Sprintf("%s/v1internal/projects/%s/locations/%s/resources:%s", baseUri, projectId, location, Create)
		var capturedBody models.GcpHydrateCreate
		var capturedURL string
		var capturedMethod string
		var capturedToken string

		hydrateToCffe = func(ctx context.Context, logger log.Logger, v any, url string, method string, token string) error {
			capturedBody = v.(models.GcpHydrateCreate)
			capturedURL = url
			capturedMethod = method
			capturedToken = token
			return nil
		}

		err := _hydrateCreatedBackupVaults(ctx, mockLogger, resources, backupVaultName, location, projectId, token)
		assert.NoError(tt, err)
		assert.Equal(tt, models.GcpHydrateCreate{Requests: resources}, capturedBody)
		assert.Equal(tt, expectedURL, capturedURL)
		assert.Equal(tt, http.MethodPost, capturedMethod)
		assert.Equal(tt, token, capturedToken)
	})

	t.Run("WhenHydrateToCcfeErrors", func(tt *testing.T) {
		expectedErr := errors.New("could not hydrate backup vaults to ccfe")
		hydrateToCffe = func(ctx context.Context, logger log.Logger, v any, url string, method string, token string) error {
			return expectedErr
		}

		err := _hydrateCreatedBackupVaults(ctx, mockLogger, resources, backupVaultName, location, projectId, token)
		assert.Error(tt, err)
		assert.Equal(tt, expectedErr, err)
	})
}

func TestHydrateDeletedBackupVaults(t *testing.T) {
	ctx := context.Background()
	mockLogger := log.NewLogger()
	projectId := "mocked-project"
	location := "mocked-location"
	backupVaultName := "mocked-backup-vault"
	token := "mocked-token"
	names := []string{"backupVaults/vault-1", "backupVaults/vault-2"}

	originalHydrateToCcfe := hydrateToCffe
	defer func() { hydrateToCffe = originalHydrateToCcfe }()

	t.Run("WhenHydrateToCcfeSucceeds", func(tt *testing.T) {
		expectedURL := fmt.Sprintf("%s/v1internal/projects/%s/locations/%s/resources:%s", baseUri, projectId, location, Delete)
		var capturedBody models.GcpHydrateDelete
		var capturedURL string
		var capturedMethod string
		var capturedToken string

		hydrateToCffe = func(ctx context.Context, logger log.Logger, v any, url string, method string, token string) error {
			capturedBody = v.(models.GcpHydrateDelete)
			capturedURL = url
			capturedMethod = method
			capturedToken = token
			return nil
		}

		err := _hydrateDeletedBackupVaults(ctx, mockLogger, names, backupVaultName, location, projectId, token)
		assert.NoError(tt, err)
		assert.Equal(tt, models.GcpHydrateDelete{Names: names}, capturedBody)
		assert.Equal(tt, expectedURL, capturedURL)
		assert.Equal(tt, http.MethodPost, capturedMethod)
		assert.Equal(tt, token, capturedToken)
	})

	t.Run("WhenHydrateToCcfeErrors", func(tt *testing.T) {
		expectedErr := errors.New("could not delete hydrate backup vaults to ccfe")
		hydrateToCffe = func(ctx context.Context, logger log.Logger, v any, url string, method string, token string) error {
			return expectedErr
		}

		err := _hydrateDeletedBackupVaults(ctx, mockLogger, names, backupVaultName, location, projectId, token)
		assert.Error(tt, err)
		assert.Equal(tt, expectedErr, err)
	})
}

func TestGetAllUUIDs(t *testing.T) {
	t.Run("ReturnsAllUUIDsAndSnapshotType", func(tt *testing.T) {
		requestArr := []models.Request{
			{Snapshot: &models.HydrateSnapshot{SnapshotId: "uuid-1", ResourceId: "snap-1"}},
			{Snapshot: &models.HydrateSnapshot{SnapshotId: "uuid-2", ResourceId: "snap-2"}},
		}
		expectedUUIDs := ", uuid-1, uuid-2"
		expectedType := "snapshot"
		allUuids, resourceType := getAllUUIDs(requestArr)
		assert.Equal(tt, expectedUUIDs, allUuids)
		assert.Equal(tt, expectedType, resourceType)
	})

	t.Run("HandlesEmptyResourceId", func(tt *testing.T) {
		requestArr := []models.Request{
			{Snapshot: &models.HydrateSnapshot{SnapshotId: "uuid-1", ResourceId: ""}},
		}
		expectedUUIDs := ""
		expectedType := ""
		allUuids, resourceType := getAllUUIDs(requestArr)
		assert.Equal(tt, expectedUUIDs, allUuids)
		assert.Equal(tt, expectedType, resourceType)
	})

	t.Run("HandlesNilSnapshot", func(tt *testing.T) {
		requestArr := []models.Request{
			{Snapshot: nil},
		}
		expectedUUIDs := ""
		expectedType := ""
		allUuids, resourceType := getAllUUIDs(requestArr)
		assert.Equal(tt, expectedUUIDs, allUuids)
		assert.Equal(tt, expectedType, resourceType)
	})

	t.Run("HandlesEmptyRequestArray", func(tt *testing.T) {
		requestArr := []models.Request{}
		expectedUUIDs := ""
		expectedType := ""
		allUuids, resourceType := getAllUUIDs(requestArr)
		assert.Equal(tt, expectedUUIDs, allUuids)
		assert.Equal(tt, expectedType, resourceType)
	})
}

func TestConvertDeleteResource(tt *testing.T) {
	tt.Run("HandlesValidSnapshot", func(t *testing.T) {
		requestArr := []models.Request{
			{Snapshot: &models.HydrateSnapshot{ResourceId: "snap-1"}},
		}
		expected := models.GcpHydrateDelete{Names: []string{"snapshots/snap-1"}}
		result := convertDeleteResource(requestArr)
		assert.Equal(t, expected, result)
	})

	tt.Run("HandlesEmptyResourceId", func(t *testing.T) {
		requestArr := []models.Request{
			{Snapshot: &models.HydrateSnapshot{ResourceId: ""}},
		}
		expected := models.GcpHydrateDelete{}
		result := convertDeleteResource(requestArr)
		assert.Equal(t, expected, result)
	})

	tt.Run("HandlesNilSnapshot", func(t *testing.T) {
		requestArr := []models.Request{
			{Snapshot: nil},
		}
		expected := models.GcpHydrateDelete{}
		result := convertDeleteResource(requestArr)
		assert.Equal(t, expected, result)
	})

	tt.Run("HandlesEmptyRequestArray", func(t *testing.T) {
		requestArr := []models.Request{}
		expected := models.GcpHydrateDelete{}
		result := convertDeleteResource(requestArr)
		assert.Equal(t, expected, result)
	})
}

func TestHydrateReplicationDelete(t *testing.T) {
	mockLogger := log.NewLogger()
	ctx := context.Background()
	replicationResourceId := "replication-1"
	volumeResourceID := "volume-1"
	region := "mocked-region"
	projectId := "mocked-project"
	token := "mocked-token"
	originalHydrateToCffe := hydrateToCffe
	defer func() { hydrateToCffe = originalHydrateToCffe }()
	t.Run("WhenHydrateToCffeReturnError", func(tt *testing.T) {
		expectedErr := &errs.CustomError{
			OriginalErr: errors.New("some error"),
		}
		hydrateToCffe = func(ctx context.Context, logger log.Logger, v any, url string, method string, token string) error {
			return expectedErr
		}
		err := _hydrateReplicationDelete(ctx, mockLogger, replicationResourceId, volumeResourceID, region, projectId, token)
		assert.Error(tt, err.(*errs.CustomError).Unwrap())
		assert.Equal(tt, expectedErr, err)
	})
	t.Run("WhenSuccessful", func(tt *testing.T) {
		hydrateToCffe = func(ctx context.Context, logger log.Logger, v any, url string, method string, token string) error {
			return nil
		}
		err := _hydrateReplicationDelete(ctx, mockLogger, replicationResourceId, volumeResourceID, region, projectId, token)
		assert.NoError(tt, err, nil)
	})
}

func TestHydrateFlexCacheState(t *testing.T) {
	mockLogger := log.NewMockLogger(t)
	ctx := context.Background()
	region := "mocked-region"
	projectId := "mocked-project"
	volumeResourceID := "mocked-volume-id"
	token := "mocked-token"
	cacheState := "PENDING_CLUSTER_PEERING"
	state := "PREPARING"
	t.Run("WhenHydrateToCffeReturnError", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		expectedErr := &errs.CustomError{
			OriginalErr: errors.New("some error"),
		}
		request := &models.FlexCacheVolumeUpdateMaskRequest{
			CacheState: models.FlexCacheVolumeHydrateCacheState(cacheState),
			State:      models.FlexCacheVolumeHydrateState(state),
		}

		url := fmt.Sprintf("%s/v1internal/projects/%s/locations/%s/volumes/%s?update_mask=cacheState,state", baseUri, projectId, region, volumeResourceID)
		mockLogger.EXPECT().Infof("Hydrating FlexCache state to callbackApi, resourceId:: %+v", volumeResourceID)
		mm.EXPECT().hydrateToCffe(ctx, mockLogger, request, url, http.MethodPatch, token).Return(expectedErr)
		err := _hydrateFlexCacheState(ctx, mockLogger, region, projectId, volumeResourceID, cacheState, state, token)
		assert.Error(tt, err.(*errs.CustomError).Unwrap())
		assert.Equal(tt, expectedErr, err)
	})

	t.Run("WhenSuccessful", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		request := &models.FlexCacheVolumeUpdateMaskRequest{
			CacheState: models.FlexCacheVolumeHydrateCacheState(cacheState),
			State:      models.FlexCacheVolumeHydrateState(state),
		}
		url := fmt.Sprintf("%s/v1internal/projects/%s/locations/%s/volumes/%s?update_mask=cacheState,state", baseUri, projectId, region, volumeResourceID)
		mockLogger.EXPECT().Infof("Hydrating FlexCache state to callbackApi, resourceId:: %+v", volumeResourceID)
		mm.EXPECT().hydrateToCffe(ctx, mockLogger, request, url, http.MethodPatch, token).Return(nil)

		err := _hydrateFlexCacheState(ctx, mockLogger, region, projectId, volumeResourceID, "PENDING_CLUSTER_PEERING", "PREPARING", token)
		assert.NoError(tt, err)
	})
}

func TestHydrateReplicationStateFunc(t *testing.T) {
	mockLogger := log.NewLogger()
	ctx := context.Background()
	region := "mocked-region"
	projectId := "mocked-project"
	volumeResourceID := "mocked-volume-id"
	replicationId := "mocked-replication-id"
	state := models.VolumeReplicationHydrateState("READY")
	token := "mocked-token"
	originalHydrateToCffe := hydrateToCffe
	defer func() { hydrateToCffe = originalHydrateToCffe }()
	t.Run("WhenHydrateToCffeReturnError", func(tt *testing.T) {
		expectedErr := &errs.CustomError{
			OriginalErr: errors.New("some error"),
		}
		hydrateToCffe = func(ctx context.Context, logger log.Logger, v any, url string, method string, token string) error {
			return expectedErr
		}
		err := _hydrateReplicationState(ctx, mockLogger, region, projectId, volumeResourceID, replicationId, state, token)
		assert.Error(tt, err.(*errs.CustomError).Unwrap())
		assert.Equal(tt, expectedErr, err)
	})
	t.Run("WhenSuccessful", func(tt *testing.T) {
		hydrateToCffe = func(ctx context.Context, logger log.Logger, v any, url string, method string, token string) error {
			return nil
		}
		err := _hydrateReplicationState(ctx, mockLogger, region, projectId, volumeResourceID, replicationId, state, token)
		assert.NoError(tt, err, nil)
	})
}

func TestHydrateReplicationStateAndTypeFunc(t *testing.T) {
	mockLogger := log.NewLogger()
	ctx := context.Background()
	region := "mocked-region"
	projectId := "mocked-project"
	volumeResourceID := "mocked-volume-id"
	replicationId := "mocked-replication-id"
	state := models.VolumeReplicationHydrateState("READY")
	hybridReplicationType := datamodel.HybridReplicationParametersReplicationType("cres")
	token := "mocked-token"
	originalHydrateToCffe := hydrateToCffe
	defer func() { hydrateToCffe = originalHydrateToCffe }()
	t.Run("WhenHydrateToCffeReturnError", func(tt *testing.T) {
		expectedErr := &errs.CustomError{
			OriginalErr: errors.New("some error"),
		}
		hydrateToCffe = func(ctx context.Context, logger log.Logger, v any, url string, method string, token string) error {
			return expectedErr
		}
		err := _hydrateReplicationStateAndType(ctx, mockLogger, region, projectId, volumeResourceID, replicationId, state, hybridReplicationType, token)
		assert.Error(tt, err.(*errs.CustomError).Unwrap())
		assert.Equal(tt, expectedErr, err)
	})
	t.Run("WhenSuccessful", func(tt *testing.T) {
		hydrateToCffe = func(ctx context.Context, logger log.Logger, v any, url string, method string, token string) error {
			return nil
		}
		err := _hydrateReplicationStateAndType(ctx, mockLogger, region, projectId, volumeResourceID, replicationId, state, hybridReplicationType, token)
		assert.NoError(tt, err, nil)
	})
}

func Test_doHydrateToCffe(t *testing.T) {
	mockLogger := log.NewLogger()
	ctx := context.Background()
	testToken := "test-token"
	testURL := "http://example.com"
	testMethod := "POST"
	testBody := map[string]string{"foo": "bar"}

	// Save and mock dependencies
	originalJsonMarshal := jsonMarshal
	originalHttpNewRequest := httpNewRequest
	originalHttpClientDo := httpClientDo
	originalIoReadAll := ioReadAll
	originalJsonUnmarshal := jsonUnmarshal
	defer func() {
		jsonMarshal = originalJsonMarshal
		httpNewRequest = originalHttpNewRequest
		httpClientDo = originalHttpClientDo
		ioReadAll = originalIoReadAll
		jsonUnmarshal = originalJsonUnmarshal
	}()
	t.Run("WhenJsonMarshalFails", func(tt *testing.T) {
		jsonMarshal = func(v any) ([]byte, error) {
			return nil, errors.New("marshal error")
		}
		err := _doHydrateToCffe(ctx, mockLogger, testBody, testURL, testMethod, testToken)
		assert.Error(tt, err.(*errs.CustomError).Unwrap())
		assert.Equal(tt, err.(*errs.CustomError).Unwrap(), errors.New("marshal error"))
	})

	t.Run("WhenHttpNewRequestFails", func(tt *testing.T) {
		jsonMarshal = func(v any) ([]byte, error) {
			return []byte("{}"), nil
		}
		httpNewRequest = func(method, url string, body io.Reader) (*http.Request, error) {
			return nil, errors.New("request error")
		}
		err := _doHydrateToCffe(ctx, mockLogger, testBody, testURL, testMethod, testToken)
		assert.Error(tt, err.(*errs.CustomError).Unwrap())
		assert.Equal(tt, err.(*errs.CustomError).Unwrap(), errors.New("request error"))
	})

	t.Run("WhenHttpClientDoFails", func(tt *testing.T) {
		jsonMarshal = func(v any) ([]byte, error) {
			return []byte("{}"), nil
		}
		httpNewRequest = func(method, url string, body io.Reader) (*http.Request, error) {
			req, _ := http.NewRequest(method, url, body)
			return req, nil
		}
		httpClientDo = func(req *http.Request) (*http.Response, error) {
			return nil, errors.New("client do error")
		}
		err := _doHydrateToCffe(ctx, mockLogger, testBody, testURL, testMethod, testToken)
		assert.Error(tt, err.(*errs.CustomError).Unwrap())
		assert.Equal(tt, err.(*errs.CustomError).Unwrap(), errors.New("client do error"))
	})

	t.Run("WhenResponseBodyReadFails", func(tt *testing.T) {
		jsonMarshal = func(v any) ([]byte, error) {
			return []byte("{}"), nil
		}
		httpNewRequest = func(method, url string, body io.Reader) (*http.Request, error) {
			req, _ := http.NewRequest(method, url, body)
			return req, nil
		}
		httpClientDo = func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: 400,
				Body:       io.NopCloser(bytes.NewReader([]byte("body"))),
			}, nil
		}
		ioReadAll = func(r io.Reader) ([]byte, error) {
			return nil, errors.New("read error")
		}
		err := _doHydrateToCffe(ctx, mockLogger, testBody, testURL, testMethod, testToken)
		assert.Error(tt, err.(*errs.CustomError).Unwrap())
		assert.Equal(tt, err.(*errs.CustomError).Unwrap(), errors.New("read error"))
	})

	t.Run("WhenJsonUnmarshalFails", func(tt *testing.T) {
		jsonMarshal = func(v any) ([]byte, error) {
			return []byte("{}"), nil
		}
		httpNewRequest = func(method, url string, body io.Reader) (*http.Request, error) {
			req, _ := http.NewRequest(method, url, body)
			return req, nil
		}
		httpClientDo = func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				Body: io.NopCloser(bytes.NewReader([]byte("body"))),
			}, nil
		}
		ioReadAll = func(r io.Reader) ([]byte, error) {
			return []byte("body"), nil
		}
		jsonUnmarshal = func(data []byte, v any) error {
			return errors.New("unmarshal error")
		}
		err := _doHydrateToCffe(ctx, mockLogger, testBody, testURL, testMethod, testToken)
		assert.Error(tt, err.(*errs.CustomError).Unwrap())
		assert.Equal(tt, err.(*errs.CustomError).Unwrap(), errors.New("unmarshal error"))
	})

	t.Run("WhenSuccess", func(tt *testing.T) {
		jsonMarshal = func(v any) ([]byte, error) {
			return []byte("{}"), nil
		}
		httpNewRequest = func(method, url string, body io.Reader) (*http.Request, error) {
			req, _ := http.NewRequest(method, url, body)
			return req, nil
		}
		httpClientDo = func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				Body:       io.NopCloser(bytes.NewReader([]byte("{}"))),
				StatusCode: 200,
			}, nil
		}
		ioReadAll = func(r io.Reader) ([]byte, error) {
			return []byte("{}"), nil
		}
		jsonUnmarshal = func(data []byte, v any) error {
			return nil
		}
		err := _doHydrateToCffe(ctx, mockLogger, testBody, testURL, testMethod, testToken)
		assert.NoError(tt, err, nil)
	})
}

func TestHydrateToCffe(t *testing.T) {
	mockLogger := log.NewLogger()
	ctx := context.Background()
	testToken := "test-token"
	testURL := "http://example.com"
	testMethod := "POST"
	testBody := map[string]string{"foo": "bar"}

	// Save and mock dependencies
	originalDoHydrateToCffe := doHydrateToCffe
	defer func() { doHydrateToCffe = originalDoHydrateToCffe }()
	t.Run("RetriesOn429WithQuotaLimit", func(tt *testing.T) {
		retryCount := 0
		doHydrateToCffe = func(ctx context.Context, logger log.Logger, v any, url string, method string, token string) error {
			retryCount++
			httpCode := 429
			return &errs.CustomError{
				OriginalErr: errors.New("Quota limit exceeded"),
				HttpCode:    &httpCode,
				Message:     "Quota limit exceeded",
			}
		}
		_ = _hydrateToCffe(ctx, mockLogger, testBody, testURL, testMethod, testToken)
		assert.GreaterOrEqual(tt, retryCount, 1)
	})
	t.Run("Getting400Error", func(tt *testing.T) {
		retryCount := 0
		doHydrateToCffe = func(ctx context.Context, logger log.Logger, v any, url string, method string, token string) error {
			retryCount++
			httpCode := 400
			return &errs.CustomError{
				OriginalErr: errors.New("some error"),
				HttpCode:    &httpCode,
			}
		}
		err := _hydrateToCffe(ctx, mockLogger, testBody, testURL, testMethod, testToken)
		assert.GreaterOrEqual(tt, retryCount, 1)
		assert.Equal(tt, "some error", err.(*errs.CustomError).OriginalErr.Error())
	})

	t.Run("WhenDoHydrateToCffeReturnsNil", func(tt *testing.T) {
		doHydrateToCffe = func(ctx context.Context, logger log.Logger, v any, url string, method string, token string) error {
			return nil
		}
		err := _hydrateToCffe(ctx, mockLogger, testBody, testURL, testMethod, testToken)
		assert.NoError(tt, err)
	})
}

func Test_getResourceQuotaType(t *testing.T) {
	assert.Equal(t, FlexVolumesPerRegion, getResourceQuotaType(ResourceTypeVolume))
	assert.Equal(t, FlexReplicationVolumesLimit, getResourceQuotaType(ResourceTypeReplication))
	assert.Equal(t, FlexIRRReplicationVolumesLimit, getResourceQuotaType(ResourceTypeInRegionReplication))
	assert.Equal(t, ResourceQuotaTypeEmpty, getResourceQuotaType(ResourceType("UNKNOWN")))
}

func Test_getQuotaLimit(t *testing.T) {
	mockLogger := log.NewLogger()
	ctx := context.Background()
	region := "mock-region"
	projectId := "mock-project"
	token := "mock-token"
	resourceType := ResourceTypeVolume
	originalHydrateToCffe := getQuotaLimitsForResource
	defer func() { getQuotaLimitsForResource = originalHydrateToCffe }()

	t.Run("WhenHydrateToCffeReturnsError", func(tt *testing.T) {
		expectedErr := errors.New("some error")

		getQuotaLimitsForResource = func(ctx context.Context, projectId string, region string, quotaType QuotaType, token string, logger log.Logger) (int, error) {
			return 0, errors.New("some error")
		}
		_, err := _getQuotaLimit(ctx, mockLogger, region, projectId, token, resourceType)
		assert.Equal(tt, expectedErr, err)
	})

	t.Run("WhenHydrateToCffeReturnsNil", func(tt *testing.T) {
		getQuotaLimitsForResource = func(ctx context.Context, projectId string, region string, quotaType QuotaType, token string, logger log.Logger) (int, error) {
			return 0, nil
		}
		_, err := _getQuotaLimit(ctx, mockLogger, region, projectId, token, resourceType)

		assert.Equal(tt, err, nil)
	})
}

func TestGetQuotaLimitsForResource(t *testing.T) {
	mockLogger := log.NewLogger()
	ctx := context.Background()
	projectId := "mock-project"
	region := "mock-region"
	quotaType := FlexVolumesPerRegion
	token := "mock-token"

	originalHttpNewRequest := httpNewRequest
	originalHttpClientDo := httpClientDo
	originalIoReadAll := ioReadAll
	originalJsonUnmarshal := jsonUnmarshal
	originalStringConvAtoi := stringConvAtoi
	defer func() {
		httpNewRequest = originalHttpNewRequest
		httpClientDo = originalHttpClientDo
		ioReadAll = originalIoReadAll
		jsonUnmarshal = originalJsonUnmarshal
		stringConvAtoi = originalStringConvAtoi
	}()

	t.Run("WhenHttpNewRequestFails", func(tt *testing.T) {
		expectedErr := errors.New("request error")
		httpNewRequest = func(method, url string, body io.Reader) (*http.Request, error) {
			return nil, errors.New("request error")
		}
		quota, err := _getQuotaLimitsForResource(ctx, projectId, region, quotaType, token, mockLogger)
		assert.Equal(tt, 0, quota)
		assert.Equal(tt, expectedErr, err.(*errs.CustomError).OriginalErr)
	})

	t.Run("WhenHttpClientDoFails", func(tt *testing.T) {
		expectedErr := errors.New("client do error")
		httpNewRequest = originalHttpNewRequest
		httpClientDo = func(req *http.Request) (*http.Response, error) {
			return nil, expectedErr
		}
		quota, err := _getQuotaLimitsForResource(ctx, projectId, region, quotaType, token, mockLogger)
		assert.Equal(tt, 0, quota)
		assert.Equal(tt, expectedErr, err.(*errs.CustomError).OriginalErr)
	})

	t.Run("WhenIoReadAllFails", func(tt *testing.T) {
		expectedErr := errors.New("client do error")
		httpNewRequest = originalHttpNewRequest
		httpClientDo = func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: 200,
				Body:       io.NopCloser(bytes.NewReader([]byte("{}"))),
			}, nil
		}
		ioReadAll = func(r io.Reader) ([]byte, error) {
			return nil, expectedErr
		}
		quota, err := _getQuotaLimitsForResource(ctx, projectId, region, quotaType, token, mockLogger)
		assert.Equal(tt, 0, quota)
		assert.Equal(tt, expectedErr, err.(*errs.CustomError).OriginalErr)
	})

	t.Run("WhenJsonUnmarshalFailsOnSuccess", func(tt *testing.T) {
		expectedErr := errors.New("unmarshal error")
		httpNewRequest = originalHttpNewRequest
		httpClientDo = func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: 200,
				Body:       io.NopCloser(bytes.NewReader([]byte("{}"))),
			}, nil
		}
		ioReadAll = func(r io.Reader) ([]byte, error) {
			return []byte("{}"), nil
		}
		jsonUnmarshal = func(data []byte, v any) error {
			return errors.New("unmarshal error")
		}
		quota, err := _getQuotaLimitsForResource(ctx, projectId, region, quotaType, token, mockLogger)
		assert.Equal(tt, 0, quota)
		assert.Equal(tt, expectedErr, err.(*errs.CustomError).OriginalErr)
	})

	t.Run("WhenStringConvAtoiFails", func(tt *testing.T) {
		expectedErr := errors.New("atoi error")
		httpNewRequest = originalHttpNewRequest
		httpClientDo = func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: 200,
				Body:       io.NopCloser(bytes.NewReader([]byte(`{"type":"quota","value":"notanint"}`))),
			}, nil
		}
		ioReadAll = func(r io.Reader) ([]byte, error) {
			return []byte(`{"type":"quota","value":"notanint"}`), nil
		}
		jsonUnmarshal = originalJsonUnmarshal
		stringConvAtoi = func(s string) (int, error) {
			return 0, errors.New("atoi error")
		}
		quota, err := _getQuotaLimitsForResource(ctx, projectId, region, quotaType, token, mockLogger)
		assert.Equal(tt, 0, quota)
		assert.Equal(tt, expectedErr, err.(*errs.CustomError).OriginalErr)
	})

	t.Run("WhenSuccess", func(tt *testing.T) {
		httpNewRequest = originalHttpNewRequest
		httpClientDo = func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: 200,
				Body:       io.NopCloser(bytes.NewReader([]byte(`{"type":"quota","value":"42"}`))),
			}, nil
		}
		ioReadAll = func(r io.Reader) ([]byte, error) {
			return []byte(`{"type":"quota","value":"42"}`), nil
		}
		jsonUnmarshal = originalJsonUnmarshal
		stringConvAtoi = strconv.Atoi
		quota, err := _getQuotaLimitsForResource(ctx, projectId, region, quotaType, token, mockLogger)
		assert.Equal(tt, 42, quota)
		assert.NoError(tt, err, nil)
	})

	t.Run("WhenStatusCodeNot200AndJsonUnmarshalFails", func(tt *testing.T) {
		httpNewRequest = originalHttpNewRequest
		httpClientDo = func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: 400,
				Body:       io.NopCloser(bytes.NewReader([]byte(`{}`))),
			}, nil
		}
		ioReadAll = func(r io.Reader) ([]byte, error) {
			return []byte(`{}`), nil
		}
		jsonUnmarshal = func(data []byte, v any) error {
			return errors.New("unmarshal error")
		}
		quota, err := _getQuotaLimitsForResource(ctx, projectId, region, quotaType, token, mockLogger)
		assert.Equal(tt, 0, quota)
		assert.Error(tt, err.(*errs.CustomError).Unwrap())
	})

	t.Run("WhenStatusCodeNot200AndSuccess", func(tt *testing.T) {
		httpNewRequest = originalHttpNewRequest
		httpClientDo = func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: 400,
				Body:       io.NopCloser(bytes.NewReader([]byte(`{"code":400,"message":"quota error","status":"FAILED"}`))),
			}, nil
		}
		ioReadAll = func(r io.Reader) ([]byte, error) {
			return []byte(`{"code":400,"message":"quota error","status":"FAILED"}`), nil
		}
		jsonUnmarshal = originalJsonUnmarshal
		quota, err := _getQuotaLimitsForResource(ctx, projectId, region, quotaType, token, mockLogger)
		assert.Equal(tt, 0, quota)
		assert.Equal(tt, "quota error", err.(*errs.CustomError).GetMessage())
	})
}

func TestMapStateToGcpState(t *testing.T) {
	t.Run("ReturnsDeletedState", func(tt *testing.T) {
		state := datamodel.LifeCycleStateDeleted
		expectedState := deletedGcp
		result := _mapStateToGcpState(state)
		assert.Equal(tt, expectedState, result)
	})

	t.Run("ReturnsAvailableState", func(tt *testing.T) {
		state := datamodel.LifeCycleStateAvailable
		expectedState := datamodel.LifeCycleStateREADY
		result := _mapStateToGcpState(state)
		assert.Equal(tt, expectedState, result)
	})

	t.Run("ReturnsDefaultStateForEmptyInput", func(tt *testing.T) {
		state := ""
		expectedState := defaultGcp
		result := _mapStateToGcpState(state)
		assert.Equal(tt, expectedState, result)
	})

	t.Run("ReturnsInputStateForUnknownState", func(tt *testing.T) {
		state := "unknownState"
		expectedState := "unknownState"
		result := _mapStateToGcpState(state)
		assert.Equal(tt, expectedState, result)
	})
}

func TestMapToGcpBulkSnapshotDeleteTests(tt *testing.T) {
	tt.Run("HandlesEmptyResourceId", func(tt *testing.T) {
		reqArray := []models.Request{
			{Snapshot: &models.HydrateSnapshot{ResourceId: ""}},
		}
		expected := models.GcpHydrateDelete{Names: []string{}}
		result := mapToGcpBulkSnapshotDelete(reqArray)
		assert.Equal(tt, expected, result)
	})

	tt.Run("HandlesMultipleSnapshots", func(tt *testing.T) {
		reqArray := []models.Request{
			{Snapshot: &models.HydrateSnapshot{ResourceId: "snap-1"}},
			{Snapshot: &models.HydrateSnapshot{ResourceId: "snap-2"}},
		}
		expected := models.GcpHydrateDelete{Names: []string{"snapshots/snap-1", "snapshots/snap-2"}}
		result := mapToGcpBulkSnapshotDelete(reqArray)
		assert.Equal(tt, expected, result)
	})

	tt.Run("HandlesNilSnapshot", func(tt *testing.T) {
		reqArray := []models.Request{
			{Snapshot: nil},
		}
		expected := models.GcpHydrateDelete{Names: []string{}}
		result := mapToGcpBulkSnapshotDelete(reqArray)
		assert.Equal(tt, expected, result)
	})

	tt.Run("HandlesEmptyRequestArray", func(tt *testing.T) {
		reqArray := []models.Request{}
		expected := models.GcpHydrateDelete{Names: []string{}}
		result := mapToGcpBulkSnapshotDelete(reqArray)
		assert.Equal(tt, expected, result)
	})
}

func TestHydrateUpdatedPool(t *testing.T) {
	ctx := context.Background()
	mockToken := "mock-token"

	// Save original hydrateToCffe function and restore after tests
	originalHydrateToCffe := hydrateToCffe
	defer func() { hydrateToCffe = originalHydrateToCffe }()

	// Save original baseUri and restore after tests
	originalBaseUri := baseUri
	defer func() { baseUri = originalBaseUri }()
	baseUri = "https://mock-base-uri.com"

	t.Run("Success_StateOnly", func(tt *testing.T) {
		poolHydrateObj := models.PoolHydrateObject{
			OwnerID:        "test-project",
			PoolID:         "test-pool-id",
			Name:           "test-pool",
			State:          "AVAILABLE",
			Region:         "us-central1-a",
			HotTierSizeGib: 0, // Zero to test state-only update
		}

		var capturedPayload models.PoolUpdateCCFERequest
		var capturedURL string
		var capturedMethod string

		hydrateToCffe = func(ctx context.Context, logger log.Logger, v any, url string, method string, token string) error {
			capturedPayload = v.(models.PoolUpdateCCFERequest)
			capturedURL = url
			capturedMethod = method
			return nil
		}

		err := _hydrateUpdatedPool(ctx, poolHydrateObj, mockToken)

		assert.NoError(tt, err)
		assert.Equal(tt, "AVAILABLE", capturedPayload.State)
		assert.Equal(tt, nil, capturedPayload.HotTierSizeGib) // Should be nil for state-only update
		assert.Equal(tt, "PATCH", capturedMethod)
		expectedURL := "https://mock-base-uri.com/v1internal/projects/test-project/locations/us-central1-a/storagePools/test-pool?update_mask=state"
		assert.Equal(tt, expectedURL, capturedURL)
	})

	t.Run("Success_StateAndHotTierSize", func(tt *testing.T) {
		poolHydrateObj := models.PoolHydrateObject{
			OwnerID:        "test-project",
			PoolID:         "test-pool-id",
			Name:           "test-pool",
			State:          "READY",
			Region:         "us-central1-b",
			HotTierSizeGib: 100, // Non-zero to test both state and hot tier update
		}

		var capturedPayload models.PoolUpdateCCFERequest
		var capturedURL string

		hydrateToCffe = func(ctx context.Context, logger log.Logger, v any, url string, method string, token string) error {
			capturedPayload = v.(models.PoolUpdateCCFERequest)
			capturedURL = url
			return nil
		}

		err := _hydrateUpdatedPool(ctx, poolHydrateObj, mockToken)

		assert.NoError(tt, err)
		assert.Equal(tt, "READY", capturedPayload.State)
		assert.Equal(tt, int64(100), capturedPayload.HotTierSizeGib)
		expectedURL := "https://mock-base-uri.com/v1internal/projects/test-project/locations/us-central1-b/storagePools/test-pool?update_mask=state,hot_tier_size_gib"
		assert.Equal(tt, expectedURL, capturedURL)
	})

	t.Run("Success_LargeHotTierSize", func(tt *testing.T) {
		poolHydrateObj := models.PoolHydrateObject{
			OwnerID:        "test-project",
			PoolID:         "test-pool-id",
			Name:           "test-pool",
			State:          "UPDATING",
			Region:         "europe-west1-a",
			HotTierSizeGib: 9999, // Large value
		}

		var capturedPayload models.PoolUpdateCCFERequest

		hydrateToCffe = func(ctx context.Context, logger log.Logger, v any, url string, method string, token string) error {
			capturedPayload = v.(models.PoolUpdateCCFERequest)
			return nil
		}

		err := _hydrateUpdatedPool(ctx, poolHydrateObj, mockToken)

		assert.NoError(tt, err)
		assert.Equal(tt, "UPDATING", capturedPayload.State)
		assert.Equal(tt, int64(9999), capturedPayload.HotTierSizeGib)
	})

	t.Run("Success_SpecialCharactersInNames", func(tt *testing.T) {
		poolHydrateObj := models.PoolHydrateObject{
			OwnerID:        "test-project-123",
			PoolID:         "test-pool-id-456",
			Name:           "test-pool-with-dashes",
			State:          "CREATING",
			Region:         "asia-east1-c",
			HotTierSizeGib: 50,
		}

		var capturedURL string

		hydrateToCffe = func(ctx context.Context, logger log.Logger, v any, url string, method string, token string) error {
			capturedURL = url
			return nil
		}

		err := _hydrateUpdatedPool(ctx, poolHydrateObj, mockToken)

		assert.NoError(tt, err)
		expectedURL := "https://mock-base-uri.com/v1internal/projects/test-project-123/locations/asia-east1-c/storagePools/test-pool-with-dashes?update_mask=state,hot_tier_size_gib"
		assert.Equal(tt, expectedURL, capturedURL)
	})

	t.Run("Error_HydrateToCffeError", func(tt *testing.T) {
		poolHydrateObj := models.PoolHydrateObject{
			OwnerID:        "test-project",
			PoolID:         "test-pool-id",
			Name:           "test-pool",
			State:          "AVAILABLE",
			Region:         "us-central1-a",
			HotTierSizeGib: 100,
		}

		expectedError := &errs.CustomError{
			Message:     "Failed to hydrate to CCFE",
			OriginalErr: errors.New("CCFE service unavailable"),
		}

		hydrateToCffe = func(ctx context.Context, logger log.Logger, v any, url string, method string, token string) error {
			return expectedError
		}

		err := _hydrateUpdatedPool(ctx, poolHydrateObj, mockToken)

		assert.Error(tt, err)
		assert.Equal(tt, expectedError, err)
	})

	t.Run("Error_NetworkError", func(tt *testing.T) {
		poolHydrateObj := models.PoolHydrateObject{
			OwnerID:        "test-project",
			PoolID:         "test-pool-id",
			Name:           "test-pool",
			State:          "AVAILABLE",
			Region:         "us-central1-a",
			HotTierSizeGib: 0,
		}

		expectedError := errors.New("network timeout")

		hydrateToCffe = func(ctx context.Context, logger log.Logger, v any, url string, method string, token string) error {
			return expectedError
		}

		err := _hydrateUpdatedPool(ctx, poolHydrateObj, mockToken)

		assert.Error(tt, err)
		assert.Equal(tt, expectedError, err)
	})

	t.Run("Error_AuthenticationError", func(tt *testing.T) {
		poolHydrateObj := models.PoolHydrateObject{
			OwnerID:        "test-project",
			PoolID:         "test-pool-id",
			Name:           "test-pool",
			State:          "AVAILABLE",
			Region:         "us-central1-a",
			HotTierSizeGib: 25,
		}

		httpCode := 401
		expectedError := &errs.CustomError{
			Message:  "Unauthorized",
			HttpCode: &httpCode,
		}

		hydrateToCffe = func(ctx context.Context, logger log.Logger, v any, url string, method string, token string) error {
			return expectedError
		}

		err := _hydrateUpdatedPool(ctx, poolHydrateObj, mockToken)

		assert.Error(tt, err)
		assert.Equal(tt, expectedError, err)
	})

	t.Run("EdgeCase_EmptyToken", func(tt *testing.T) {
		poolHydrateObj := models.PoolHydrateObject{
			OwnerID:        "test-project",
			PoolID:         "test-pool-id",
			Name:           "test-pool",
			State:          "AVAILABLE",
			Region:         "us-central1-a",
			HotTierSizeGib: 0,
		}

		var capturedToken string

		hydrateToCffe = func(ctx context.Context, logger log.Logger, v any, url string, method string, token string) error {
			capturedToken = token
			return nil
		}

		err := _hydrateUpdatedPool(ctx, poolHydrateObj, "")

		assert.NoError(tt, err)
		assert.Equal(tt, "", capturedToken)
	})

	t.Run("EdgeCase_EmptyPoolFields", func(tt *testing.T) {
		poolHydrateObj := models.PoolHydrateObject{
			OwnerID:        "",
			PoolID:         "",
			Name:           "",
			State:          "",
			Region:         "",
			HotTierSizeGib: 0,
		}

		var capturedURL string
		var capturedPayload models.PoolUpdateCCFERequest

		hydrateToCffe = func(ctx context.Context, logger log.Logger, v any, url string, method string, token string) error {
			capturedURL = url
			capturedPayload = v.(models.PoolUpdateCCFERequest)
			return nil
		}

		err := _hydrateUpdatedPool(ctx, poolHydrateObj, mockToken)

		assert.NoError(tt, err)
		assert.Equal(tt, "", capturedPayload.State)
		assert.Equal(tt, nil, capturedPayload.HotTierSizeGib)
		expectedURL := "https://mock-base-uri.com/v1internal/projects//locations//storagePools/?update_mask=state"
		assert.Equal(tt, expectedURL, capturedURL)
	})

	t.Run("EdgeCase_NegativeHotTierSize", func(tt *testing.T) {
		poolHydrateObj := models.PoolHydrateObject{
			OwnerID:        "test-project",
			PoolID:         "test-pool-id",
			Name:           "test-pool",
			State:          "AVAILABLE",
			Region:         "us-central1-a",
			HotTierSizeGib: -10, // Negative value
		}

		var capturedPayload models.PoolUpdateCCFERequest
		var capturedURL string

		hydrateToCffe = func(ctx context.Context, logger log.Logger, v any, url string, method string, token string) error {
			capturedPayload = v.(models.PoolUpdateCCFERequest)
			capturedURL = url
			return nil
		}

		err := _hydrateUpdatedPool(ctx, poolHydrateObj, mockToken)

		assert.NoError(tt, err)
		assert.Equal(tt, "AVAILABLE", capturedPayload.State)
		assert.Equal(tt, nil, capturedPayload.HotTierSizeGib) // Should be nil since -10 is not > 0
		expectedURL := "https://mock-base-uri.com/v1internal/projects/test-project/locations/us-central1-a/storagePools/test-pool?update_mask=state"
		assert.Equal(tt, expectedURL, capturedURL)
	})

	t.Run("EdgeCase_HotTierSizeEqualsOne", func(tt *testing.T) {
		poolHydrateObj := models.PoolHydrateObject{
			OwnerID:        "test-project",
			PoolID:         "test-pool-id",
			Name:           "test-pool",
			State:          "AVAILABLE",
			Region:         "us-central1-a",
			HotTierSizeGib: 1, // Minimum positive value
		}

		var capturedPayload models.PoolUpdateCCFERequest
		var capturedURL string

		hydrateToCffe = func(ctx context.Context, logger log.Logger, v any, url string, method string, token string) error {
			capturedPayload = v.(models.PoolUpdateCCFERequest)
			capturedURL = url
			return nil
		}

		err := _hydrateUpdatedPool(ctx, poolHydrateObj, mockToken)

		assert.NoError(tt, err)
		assert.Equal(tt, "AVAILABLE", capturedPayload.State)
		assert.Equal(tt, int64(1), capturedPayload.HotTierSizeGib)
		expectedURL := "https://mock-base-uri.com/v1internal/projects/test-project/locations/us-central1-a/storagePools/test-pool?update_mask=state,hot_tier_size_gib"
		assert.Equal(tt, expectedURL, capturedURL)
	})

	t.Run("ParameterValidation_CorrectParametersPassedToHydrateToCffe", func(tt *testing.T) {
		poolHydrateObj := models.PoolHydrateObject{
			OwnerID:        "validation-project",
			PoolID:         "validation-pool-id",
			Name:           "validation-pool",
			State:          "VALIDATING",
			Region:         "us-west1-a",
			HotTierSizeGib: 200,
		}

		var capturedCtx context.Context
		var capturedLogger log.Logger
		var capturedPayload models.PoolUpdateCCFERequest
		var capturedURL string
		var capturedMethod string
		var capturedToken string

		hydrateToCffe = func(ctx context.Context, logger log.Logger, v any, url string, method string, token string) error {
			capturedCtx = ctx
			capturedLogger = logger
			capturedPayload = v.(models.PoolUpdateCCFERequest)
			capturedURL = url
			capturedMethod = method
			capturedToken = token
			return nil
		}

		err := _hydrateUpdatedPool(ctx, poolHydrateObj, "validation-token")

		assert.NoError(tt, err)
		assert.Equal(tt, ctx, capturedCtx)
		assert.NotNil(tt, capturedLogger)
		assert.Equal(tt, "VALIDATING", capturedPayload.State)
		assert.Equal(tt, int64(200), capturedPayload.HotTierSizeGib)
		assert.Equal(tt, "PATCH", capturedMethod)
		assert.Equal(tt, "validation-token", capturedToken)
		expectedURL := "https://mock-base-uri.com/v1internal/projects/validation-project/locations/us-west1-a/storagePools/validation-pool?update_mask=state,hot_tier_size_gib"
		assert.Equal(tt, expectedURL, capturedURL)
	})
}

func TestConvertToGCPHydrateBackupCreateRequests(t *testing.T) {
	backups := []*datamodel.Backup{
		{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "uuid1",
			},
			Name:        "backup1",
			SizeInBytes: 12345,
			Attributes: &datamodel.BackupAttributes{
				BucketName:       "bucket1",
				SourceVolumeZone: "us-central1-a",
			},
		},
		{
			BaseModel: datamodel.BaseModel{
				ID:   2,
				UUID: "uuid2",
			},
			Name:        "backup2",
			SizeInBytes: 67890,
			Attributes: &datamodel.BackupAttributes{
				BucketName: "bucket2",
			},
			BackupVault: &datamodel.BackupVault{
				SourceRegionName: nillable.ToPointer("us-central1"),
			},
		},
	}
	result := ConvertToGCPHydrateBackupCreateRequests(backups, models.BackupHydrationModeDefault, "")
	require := assert.New(t)
	require.Len(result, 2)
	require.Equal("backup1", result[0].Backup.ResourceId)
	require.Equal("uuid1", result[0].Backup.BackupId)
	require.NotNil(result[0].Backup.VolumeUsageBytes)
	require.Equal(uint64(12345), *result[0].Backup.VolumeUsageBytes)

	// Validate AssetLocationMetadata for backup1
	require.NotNil(result[0].Backup.AssetLocationMetadata)
	require.Len(result[0].Backup.AssetLocationMetadata.ChildAssets, 1)
	require.Equal("storage.googleapis.com/Bucket", result[0].Backup.AssetLocationMetadata.ChildAssets[0].AssetType)
	require.Len(result[0].Backup.AssetLocationMetadata.ChildAssets[0].AssetNames, 1)
	require.Equal("//storage.googleapis.com/bucket1", result[0].Backup.AssetLocationMetadata.ChildAssets[0].AssetNames[0])

	require.Equal("backup2", result[1].Backup.ResourceId)
	require.Equal("uuid2", result[1].Backup.BackupId)
	require.NotNil(result[1].Backup.VolumeUsageBytes)
	require.Equal(uint64(67890), *result[1].Backup.VolumeUsageBytes)

	// Validate AssetLocationMetadata for backup2
	require.NotNil(result[1].Backup.AssetLocationMetadata)
	require.Len(result[1].Backup.AssetLocationMetadata.ChildAssets, 1)
	require.Equal("storage.googleapis.com/Bucket", result[1].Backup.AssetLocationMetadata.ChildAssets[0].AssetType)
	require.Len(result[1].Backup.AssetLocationMetadata.ChildAssets[0].AssetNames, 1)
	require.Equal("//storage.googleapis.com/bucket2", result[1].Backup.AssetLocationMetadata.ChildAssets[0].AssetNames[0])

	result = ConvertToGCPHydrateBackupCreateRequests([]*datamodel.Backup{}, models.BackupHydrationModeDefault, "")
	require.Empty(result)

	result = ConvertToGCPHydrateBackupCreateRequests(nil, models.BackupHydrationModeDefault, "")
	require.Empty(result)
}

func TestConvertToGCPHydrateBackupCreateRequests_SourceVolumeDetailsAndMode(t *testing.T) {
	// Helper to mock GetSourceVolumePathFromBackup for tests that don't care about path logic.
	mockPath := func() func() {
		orig := utils.GetSourceVolumePathFromBackup
		utils.GetSourceVolumePathFromBackup = func(*datamodel.Backup) string { return "projects/acct/locations/zone/volumes/vol" }
		return func() { utils.GetSourceVolumePathFromBackup = orig }
	}

	t.Run("DefaultModeIsSetInResult", func(t *testing.T) {
		defer mockPath()()
		backup := &datamodel.Backup{
			BaseModel:   datamodel.BaseModel{UUID: "uuid1"},
			Name:        "backup1",
			SizeInBytes: 100,
			Attributes: &datamodel.BackupAttributes{
				VolumeName: "vol-name",
				Protocols:  []string{"NFSV3"},
			},
		}
		result := ConvertToGCPHydrateBackupCreateRequests([]*datamodel.Backup{backup}, models.BackupHydrationModeDefault, "")
		assert.Len(t, result, 1)
		assert.Equal(t, models.BackupHydrationModeDefault, result[0].Backup.Mode)
		assert.Empty(t, result[0].Backup.SourceStoragePool)
	})

	t.Run("OntapModeIsSetInResult", func(t *testing.T) {
		defer mockPath()()
		backup := &datamodel.Backup{
			BaseModel:   datamodel.BaseModel{UUID: "uuid1"},
			Name:        "backup1",
			SizeInBytes: 100,
			Attributes: &datamodel.BackupAttributes{
				VolumeName: "vol-name",
				Protocols:  []string{"NFSV3"},
			},
		}
		result := ConvertToGCPHydrateBackupCreateRequests([]*datamodel.Backup{backup}, models.BackupHydrationModeONTAP, "")
		assert.Len(t, result, 1)
		assert.Equal(t, models.BackupHydrationModeONTAP, result[0].Backup.Mode)
		assert.Empty(t, result[0].Backup.SourceStoragePool)
	})

	t.Run("SourceVolumeDetailsAlwaysPopulated", func(t *testing.T) {
		defer mockPath()()
		backup := &datamodel.Backup{
			BaseModel:   datamodel.BaseModel{UUID: "uuid1"},
			Name:        "backup1",
			SizeInBytes: 100,
			Attributes: &datamodel.BackupAttributes{
				VolumeName: "vol-name",
				Protocols:  []string{"NFSV3", "SMB"},
			},
		}
		result := ConvertToGCPHydrateBackupCreateRequests([]*datamodel.Backup{backup}, models.BackupHydrationModeDefault, "")
		assert.Len(t, result, 1)
		assert.NotNil(t, result[0].Backup.SourceVolumeDetails)
		assert.Equal(t, []string{"NFSV3", "SMB"}, result[0].Backup.SourceVolumeDetails.VolumeProtocols)
	})

	t.Run("SourceVolumeDetailsEmptyProtocolsWhenAttributesNil", func(t *testing.T) {
		defer mockPath()()
		backup := &datamodel.Backup{
			BaseModel:   datamodel.BaseModel{UUID: "uuid1"},
			Name:        "backup1",
			SizeInBytes: 100,
			Attributes:  nil,
		}
		result := ConvertToGCPHydrateBackupCreateRequests([]*datamodel.Backup{backup}, models.BackupHydrationModeDefault, "")
		assert.Len(t, result, 1)
		assert.NotNil(t, result[0].Backup.SourceVolumeDetails)
		assert.Empty(t, result[0].Backup.SourceVolumeDetails.VolumeProtocols)
	})

	t.Run("OntapBackupSourceVolumePathReplacesVolumeNameWithUUID", func(t *testing.T) {
		originalGetSourceVolumePath := utils.GetSourceVolumePathFromBackup
		defer func() { utils.GetSourceVolumePathFromBackup = originalGetSourceVolumePath }()

		const volumeName = "ontap-vol-name"
		const volumeUUID = "vol-uuid-abc123"
		utils.GetSourceVolumePathFromBackup = func(b *datamodel.Backup) string {
			return "projects/account/locations/us-east1/volumes/" + volumeName
		}

		backup := &datamodel.Backup{
			BaseModel:   datamodel.BaseModel{UUID: volumeUUID},
			Name:        "backup1",
			VolumeUUID:  volumeUUID,
			SizeInBytes: 100,
			Attributes: &datamodel.BackupAttributes{
				VolumeName:         volumeName,
				IsExpertModeBackup: true,
			},
		}
		result := ConvertToGCPHydrateBackupCreateRequests([]*datamodel.Backup{backup}, models.BackupHydrationModeONTAP, "")
		assert.Len(t, result, 1)
		assert.Equal(t, "projects/account/locations/us-east1/volumes/"+volumeUUID, result[0].Backup.SourceVolume)
	})

	t.Run("NonOntapBackupSourceVolumePathUnchanged", func(t *testing.T) {
		originalGetSourceVolumePath := utils.GetSourceVolumePathFromBackup
		defer func() { utils.GetSourceVolumePathFromBackup = originalGetSourceVolumePath }()

		const volumeName = "regular-vol-name"
		const expectedPath = "projects/account/locations/us-east1/volumes/" + volumeName
		utils.GetSourceVolumePathFromBackup = func(b *datamodel.Backup) string {
			return expectedPath
		}

		backup := &datamodel.Backup{
			BaseModel:   datamodel.BaseModel{UUID: "vol-uuid"},
			Name:        "backup1",
			VolumeUUID:  "vol-uuid",
			SizeInBytes: 100,
			Attributes: &datamodel.BackupAttributes{
				VolumeName:         volumeName,
				IsExpertModeBackup: false,
			},
		}
		result := ConvertToGCPHydrateBackupCreateRequests([]*datamodel.Backup{backup}, models.BackupHydrationModeDefault, "")
		assert.Len(t, result, 1)
		assert.Equal(t, expectedPath, result[0].Backup.SourceVolume)
	})

	t.Run("OntapBackupWithEmptyVolumeNameSkipsPathRewrite", func(t *testing.T) {
		originalGetSourceVolumePath := utils.GetSourceVolumePathFromBackup
		defer func() { utils.GetSourceVolumePathFromBackup = originalGetSourceVolumePath }()

		const expectedPath = "projects/account/locations/us-east1/volumes/"
		utils.GetSourceVolumePathFromBackup = func(b *datamodel.Backup) string {
			return expectedPath
		}

		backup := &datamodel.Backup{
			BaseModel:   datamodel.BaseModel{UUID: "vol-uuid"},
			Name:        "backup1",
			VolumeUUID:  "vol-uuid",
			SizeInBytes: 100,
			Attributes: &datamodel.BackupAttributes{
				VolumeName:         "", // empty — rewrite should be skipped
				IsExpertModeBackup: true,
			},
		}
		result := ConvertToGCPHydrateBackupCreateRequests([]*datamodel.Backup{backup}, models.BackupHydrationModeONTAP, "")
		assert.Len(t, result, 1)
		assert.Equal(t, expectedPath, result[0].Backup.SourceVolume)
	})

	t.Run("SourceStoragePoolSetForOntapMode", func(t *testing.T) {
		defer mockPath()()
		backup := &datamodel.Backup{
			BaseModel:   datamodel.BaseModel{UUID: "uuid1"},
			Name:        "backup1",
			SizeInBytes: 100,
			Attributes: &datamodel.BackupAttributes{
				VolumeName: "vol-name",
			},
		}
		const poolPath = "projects/537984359821/locations/us-central1/storagePools/sp"
		result := ConvertToGCPHydrateBackupCreateRequests([]*datamodel.Backup{backup}, models.BackupHydrationModeONTAP, poolPath)
		assert.Len(t, result, 1)
		assert.Equal(t, poolPath, result[0].Backup.SourceStoragePool)
	})

	t.Run("SourceStoragePoolNotSetForDefaultMode", func(t *testing.T) {
		defer mockPath()()
		backup := &datamodel.Backup{
			BaseModel:   datamodel.BaseModel{UUID: "uuid1"},
			Name:        "backup1",
			SizeInBytes: 100,
			Attributes: &datamodel.BackupAttributes{
				VolumeName: "vol-name",
			},
		}
		const poolPath = "projects/537984359821/locations/us-central1/storagePools/sp"
		result := ConvertToGCPHydrateBackupCreateRequests([]*datamodel.Backup{backup}, models.BackupHydrationModeDefault, poolPath)
		assert.Len(t, result, 1)
		assert.Empty(t, result[0].Backup.SourceStoragePool)
	})

	t.Run("SourceStoragePoolNotSetWhenEmptyEvenForOntapMode", func(t *testing.T) {
		defer mockPath()()
		backup := &datamodel.Backup{
			BaseModel:   datamodel.BaseModel{UUID: "uuid1"},
			Name:        "backup1",
			SizeInBytes: 100,
			Attributes: &datamodel.BackupAttributes{
				VolumeName: "vol-name",
			},
		}
		result := ConvertToGCPHydrateBackupCreateRequests([]*datamodel.Backup{backup}, models.BackupHydrationModeONTAP, "")
		assert.Len(t, result, 1)
		assert.Empty(t, result[0].Backup.SourceStoragePool)
	})
}

func TestConvertToGCPHydrateBackupDeleteRequests(t *testing.T) {
	backups := []*datamodel.Backup{
		{Name: "backup1"},
		{Name: "backup2"},
		{Name: "backup3"},
	}
	expected := []string{"backups/backup1", "backups/backup2", "backups/backup3"}
	result := ConvertToGCPHydrateBackupDeleteRequests(backups)
	assert.Equal(t, expected, result)

	// Test with empty slice
	result = ConvertToGCPHydrateBackupDeleteRequests([]*datamodel.Backup{})
	assert.Empty(t, result)

	// Test with nil input
	result = ConvertToGCPHydrateBackupDeleteRequests(nil)
	assert.Empty(t, result)
}

func TestConvertToGCPHydrateBackupVaultCreateRequests(t *testing.T) {
	backupRegion := "us-central1"
	kmsKeyVersion := "kms-key-version-1"
	kmsConfigPath := "projects/p1/locations/us-central1/keyRings/r1/cryptoKeys/k1"

	backupVaults := []*datamodel.BackupVault{
		{
			BaseModel:        datamodel.BaseModel{UUID: "vault-uuid-1"},
			Name:             "vault-1",
			BackupVaultType:  "STANDARD",
			BackupRegionName: &backupRegion,
			CmekAttributes:   nil, // explicitly validate nil-safe handling
		},
		{
			BaseModel:        datamodel.BaseModel{UUID: "vault-uuid-2"},
			Name:             "vault-2",
			BackupVaultType:  "STANDARD",
			BackupRegionName: &backupRegion,
			CmekAttributes: &datamodel.CmekAttributes{
				BackupsPrimaryKeyVersion: &kmsKeyVersion,
				KmsConfigResourcePath:    &kmsConfigPath,
			},
		},
	}

	result := ConvertToGCPHydrateBackupVaultCreateRequests(backupVaults)
	require := assert.New(t)
	require.Len(result, 2)

	require.NotNil(result[0].BackupVault)
	require.Equal("vault-1", result[0].BackupVault.ResourceId)
	require.Equal("vault-uuid-1", result[0].BackupVault.BackupVaultId)
	require.Equal("STANDARD", result[0].BackupVault.BackupVaultType)
	require.Equal(backupRegion, result[0].BackupVault.BackupRegion)
	require.Nil(result[0].BackupVault.BackupsKmsKey)
	require.Nil(result[0].BackupVault.KmsConfigResourcePath)

	require.NotNil(result[1].BackupVault)
	require.Equal("vault-2", result[1].BackupVault.ResourceId)
	require.Equal("vault-uuid-2", result[1].BackupVault.BackupVaultId)
	require.Equal("STANDARD", result[1].BackupVault.BackupVaultType)
	require.Equal(backupRegion, result[1].BackupVault.BackupRegion)
	require.NotNil(result[1].BackupVault.BackupsKmsKey)
	require.NotNil(result[1].BackupVault.KmsConfigResourcePath)
	require.Equal(kmsKeyVersion, *result[1].BackupVault.BackupsKmsKey)
	require.Equal(kmsConfigPath, *result[1].BackupVault.KmsConfigResourcePath)

	result = ConvertToGCPHydrateBackupVaultCreateRequests([]*datamodel.BackupVault{})
	require.Empty(result)

	result = ConvertToGCPHydrateBackupVaultCreateRequests(nil)
	require.Empty(result)
}

func TestConvertToGCPHydrateBackupVaultDeleteRequests(t *testing.T) {
	t.Run("ReturnsFormattedBackupVaultNames", func(tt *testing.T) {
		backupVaults := []*datamodel.BackupVault{
			{Name: "vault-1"},
			{Name: "vault-2"},
		}
		expected := []string{"backupVaults/vault-1", "backupVaults/vault-2"}
		result := ConvertToGCPHydrateBackupVaultDeleteRequests(backupVaults)
		assert.Equal(tt, expected, result)
	})

	t.Run("ReturnsEmptySliceForEmptyInput", func(tt *testing.T) {
		result := ConvertToGCPHydrateBackupVaultDeleteRequests([]*datamodel.BackupVault{})
		assert.Empty(tt, result)
	})

	t.Run("ReturnsEmptySliceForNilInput", func(tt *testing.T) {
		result := ConvertToGCPHydrateBackupVaultDeleteRequests(nil)
		assert.Empty(tt, result)
	})
}

func TestHydrateQuotaRuleDelete(t *testing.T) {
	mockLogger := log.NewLogger()
	ctx := context.Background()
	quotaRuleId := "quota-rule-uuid-1"
	volumeId := "volume-uuid-1"
	region := "mocked-region"
	projectId := "mocked-project"
	token := "mocked-token"

	// Save and mock hydrateToCffe
	originalHydrateToCffe := hydrateToCffe
	defer func() { hydrateToCffe = originalHydrateToCffe }()

	t.Run("WhenHydrateToCffeReturnError", func(tt *testing.T) {
		expectedErr := &errs.CustomError{
			OriginalErr: errors.New("some error"),
		}
		hydrateToCffe = func(ctx context.Context, logger log.Logger, v any, url string, method string, token string) error {
			return expectedErr
		}
		err := _hydrateQuotaRuleDelete(ctx, mockLogger, quotaRuleId, volumeId, region, projectId, token)
		assert.Error(tt, err.(*errs.CustomError).Unwrap())
		assert.Equal(tt, expectedErr, err)
	})

	t.Run("WhenSuccessful", func(tt *testing.T) {
		hydrateToCffe = func(ctx context.Context, logger log.Logger, v any, url string, method string, token string) error {
			return nil
		}
		err := _hydrateQuotaRuleDelete(ctx, mockLogger, quotaRuleId, volumeId, region, projectId, token)
		assert.NoError(tt, err, nil)
	})
}

func TestHydrateQuotaRuleCreate(t *testing.T) {
	mockLogger := log.NewLogger()
	ctx := context.Background()
	quotaRule := models.QuotaRuleHydrateObject{
		ResourceId:  "quota-rule-1",
		QuotaRuleId: "quota-rule-uuid-1",
	}
	volumeResourceID := "volume-uuid-1"
	location := "mocked-location"
	projectId := "mocked-project"
	token := "mocked-token"

	// Save and mock hydrateToCffe
	originalHydrateToCffe := hydrateToCffe
	defer func() { hydrateToCffe = originalHydrateToCffe }()

	t.Run("WhenHydrateToCffeReturnError", func(tt *testing.T) {
		expectedErr := &errs.CustomError{
			OriginalErr: errors.New("some error"),
		}
		hydrateToCffe = func(ctx context.Context, logger log.Logger, v any, url string, method string, token string) error {
			return expectedErr
		}
		err := _hydrateQuotaRuleCreate(ctx, mockLogger, quotaRule, volumeResourceID, location, projectId, token)
		assert.Error(tt, err.(*errs.CustomError).Unwrap())
		assert.Equal(tt, expectedErr, err)
	})

	t.Run("WhenSuccessful", func(tt *testing.T) {
		hydrateToCffe = func(ctx context.Context, logger log.Logger, v any, url string, method string, token string) error {
			return nil
		}
		err := _hydrateQuotaRuleCreate(ctx, mockLogger, quotaRule, volumeResourceID, location, projectId, token)
		assert.NoError(tt, err, nil)
	})
}

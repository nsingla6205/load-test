package vsa

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/go-openapi/strfmt"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/priv/client/snapmirror"
	models2 "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/priv/models"
	ontaprest "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/ontap-rest"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
)

func TestCreateVolumeReplicationSchedule(t *testing.T) {
	originalgetOntapClientFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = originalgetOntapClientFunc }()
	t.Run("WhenGetScheduleReturnsError", func(tt *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockClusterClient := new(ontaprest.MockClusterClient)

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		ontapProvider := &OntapRestProvider{}
		returnedError := fmt.Errorf("some Error")
		schedule := VolumeReplicationSchedule10Minutely
		params := &ontaprest.ScheduleCollectionGetParams{
			Name: schedule,
		}
		mockClient.On("Cluster").Return(mockClusterClient)
		mockClusterClient.On("ScheduleCollectionGet", params, mock.Anything).Return(returnedError)

		err := createVolumeReplicationSchedule(ontapProvider, schedule)
		assert.Equal(tt, returnedError, err)
	})
	t.Run("WhenCreate10minutelyScheduleReturnsError", func(tt *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockClusterClient := new(ontaprest.MockClusterClient)

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		ontapProvider := &OntapRestProvider{}
		returnedError := fmt.Errorf("some Error")
		schedule := VolumeReplicationSchedule10Minutely
		params := &ontaprest.ScheduleCollectionGetParams{
			Name: schedule,
		}
		mockClient.On("Cluster").Return(mockClusterClient)
		mockClusterClient.On("ScheduleCollectionGet", params, mock.Anything).Return(nil)
		mockClusterClient.On("ScheduleCreate", mock.Anything).Return(returnedError)

		err := createVolumeReplicationSchedule(ontapProvider, schedule)
		assert.Equal(tt, returnedError, err)
	})
	t.Run("WhenCreateHourlyScheduleReturnsError", func(tt *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockClusterClient := new(ontaprest.MockClusterClient)

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		ontapProvider := &OntapRestProvider{}
		returnedError := fmt.Errorf("some Error")
		schedule := VolumeReplicationScheduleHourly
		params := &ontaprest.ScheduleCollectionGetParams{
			Name: schedule,
		}
		mockClient.On("Cluster").Return(mockClusterClient)
		mockClusterClient.On("ScheduleCollectionGet", params, mock.Anything).Return(nil)
		mockClusterClient.On("ScheduleCreate", mock.Anything).Return(returnedError)

		err := createVolumeReplicationSchedule(ontapProvider, schedule)
		assert.Equal(tt, returnedError, err)
	})
	t.Run("WhenCreateDailyScheduleReturnsError", func(tt *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockClusterClient := new(ontaprest.MockClusterClient)

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		ontapProvider := &OntapRestProvider{}
		returnedError := fmt.Errorf("some Error")
		schedule := VolumeReplicationScheduleDaily
		params := &ontaprest.ScheduleCollectionGetParams{
			Name: schedule,
		}
		mockClient.On("Cluster").Return(mockClusterClient)
		mockClusterClient.On("ScheduleCollectionGet", params, mock.Anything).Return(nil)
		mockClusterClient.On("ScheduleCreate", mock.Anything).Return(returnedError)

		err := createVolumeReplicationSchedule(ontapProvider, schedule)
		assert.Equal(tt, returnedError, err)
	})
	t.Run("WhenInvalidSchedule", func(tt *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockClusterClient := new(ontaprest.MockClusterClient)

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		ontapProvider := &OntapRestProvider{}
		schedule := "InvalidSchedule"
		params := &ontaprest.ScheduleCollectionGetParams{
			Name: schedule,
		}
		expectedError := errors.NewUserInputValidationErr("Unknown replication schedule")
		mockClient.On("Cluster").Return(mockClusterClient)
		mockClusterClient.On("ScheduleCollectionGet", params, mock.Anything).Return(nil)

		err := createVolumeReplicationSchedule(ontapProvider, schedule)
		assert.Equal(tt, expectedError, err)
	})
	t.Run("WhenScheduleExists", func(tt *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockClusterClient := new(ontaprest.MockClusterClient)

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		ontapProvider := &OntapRestProvider{}
		scheduleName := VolumeReplicationScheduleDaily
		params := &ontaprest.ScheduleCollectionGetParams{
			Name: scheduleName,
		}
		mockClient.On("Cluster").Return(mockClusterClient)
		mockClusterClient.On("ScheduleCollectionGet", params, mock.Anything).Return(nil)
		mockClusterClient.On("ScheduleCreate", mock.Anything).Return(nil)
		err := createVolumeReplicationSchedule(ontapProvider, scheduleName)
		assert.NoError(tt, err)
	})
}

func TestCreateVolumeReplication(t *testing.T) {
	srcVolumeUUID := "src-uuid"
	dstVolumeUUID := "dst-uuid"
	srcVolume := ontaprest.Volume{
		Volume: models.Volume{
			UUID: &srcVolumeUUID,
			Type: nillable.ToPointer(models.VolumeTypeRw),
			Name: nillable.ToPointer("srcvol"),
			Svm: &models.VolumeInlineSvm{
				Name: nillable.ToPointer("srcsvm"),
			},
		},
	}
	dstVolume := ontaprest.Volume{
		Volume: models.Volume{
			UUID: &dstVolumeUUID,
			Type: nillable.ToPointer(models.VolumeTypeDp),
			Name: nillable.ToPointer("dstvol"),
			Svm: &models.VolumeInlineSvm{
				Name: nillable.ToPointer("dstsvm"),
			},
		},
	}
	volume1 := &Volume{
		ExternalUUID: dstVolumeUUID,
	}
	volumeGetParams := &ontaprest.VolumeGetParams{UUID: *dstVolume.UUID}
	volumeReplicationCreateParams := CreateVolumeReplicationParams{
		VolumeReplication: &VolumeReplication{
			Volume:                volume1,
			ReplicationPolicy:     "MirrorAllSnapshots",
			ReplicationSchedule:   VolumeReplicationScheduleDaily,
			SourceSVMName:         *srcVolume.Svm.Name,
			SourceVolumeName:      *srcVolume.Name,
			DestinationSVMName:    *dstVolume.Svm.Name,
			DestinationVolumeName: *dstVolume.Name,
		},
	}
	var snapmirrorEmptyList []*ontaprest.SnapmirrorRelationship

	createParams := &ontaprest.SnapmirrorRelationshipCreateParams{
		SourcePath:      volumeReplicationCreateParams.VolumeReplication.SourcePath(),
		DestinationPath: volumeReplicationCreateParams.VolumeReplication.DestinationPath(),
		Policy:          volumeReplicationCreateParams.VolumeReplication.ReplicationPolicy,
		Schedule:        &volumeReplicationCreateParams.VolumeReplication.ReplicationSchedule,
	}

	sourcePath := nillable.GetString(srcVolume.Svm.Name, "") + ":" + nillable.GetString(srcVolume.Name, "")
	destinationPath := nillable.GetString(dstVolume.Svm.Name, "") + ":" + nillable.GetString(dstVolume.Name, "")
	snapmirror := &ontaprest.SnapmirrorRelationship{
		SnapmirrorRelationship: models.SnapmirrorRelationship{
			Source:                &models.SnapmirrorSourceEndpoint{Path: &sourcePath},
			Destination:           &models.SnapmirrorEndpoint{Path: &destinationPath},
			UUID:                  nillable.ToPointer(strfmt.UUID("uuid")),
			State:                 nillable.ToPointer(models.SnapmirrorRelationshipStateSnapmirrored),
			TotalTransferDuration: nillable.ToPointer("PT2M34S"),
			Transfer: &models.SnapmirrorRelationshipInlineTransfer{
				TotalDuration:    nillable.ToPointer("PT4M50S"),
				State:            nillable.ToPointer(models.SnapmirrorRelationshipInlineTransferStateSuccess),
				BytesTransferred: nillable.ToPointer(int64(500)),
				EndTime:          nillable.ToPointer(strfmt.DateTime(time.Now())),
			},
			Policy: &models.SnapmirrorRelationshipInlinePolicy{
				Name: nillable.ToPointer("policy"),
			},
			LagTime: nillable.ToPointer("PT20S"),
			SnapmirrorRelationshipInlineUnhealthyReason: []*models.SnapmirrorError{
				{
					Message: nillable.ToPointer("error"),
				},
			},
			TransferSchedule: &models.SnapmirrorRelationshipInlineTransferSchedule{
				Name: nillable.ToPointer("le schedule"),
			},
			Healthy:            nillable.GetBoolPtr(true),
			TotalTransferBytes: nillable.ToPointer(int64(1000)),
		},
	}
	getParams := &ontaprest.SnapmirrorRelationshipGetParams{UUID: snapmirror.UUID.String()}
	jobAccepted := &ontaprest.JobAccepted{JobUUID: "jobUUID"}
	doEnsureSvmPeering = func(provider *OntapRestProvider, params *CreateVolumeReplicationParams) error {
		return nil
	}
	doCreateVolumeReplicationScheduleIfNeeded = func(provider *OntapRestProvider, schedule string) (err error) {
		return nil
	}
	originalgetOntapClientFunc := getOntapClientFunc

	defer func() {
		doEnsureSvmPeering = ensureSvmPeering
		getOntapClientFunc = originalgetOntapClientFunc
		doCreateVolumeReplicationScheduleIfNeeded = createVolumeReplicationSchedule
	}()

	t.Run("WhenSnapmirrorCreateReturnsError", func(tt *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockStorageClient := new(ontaprest.MockStorageClient)
		mockSnapmirrorClient := new(ontaprest.MockSnapmirrorClient)
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		ontapProvider := &OntapRestProvider{}
		expectedError := errors.New("Faceplanting")

		mockClient.On("Storage").Return(mockStorageClient)
		mockClient.On("Snapmirror").Return(mockSnapmirrorClient)
		mockStorageClient.On("VolumeGet", volumeGetParams).Return(&dstVolume, nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipListDestinations", mock.Anything).Return(snapmirrorEmptyList, nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipList", mock.Anything).Return(snapmirrorEmptyList, nil).Times(2)
		mockSnapmirrorClient.On("SnapmirrorRelationshipCreate", createParams).Return(nil, nil, expectedError).Times(1)
		volumeReplication, err := ontapProvider.CreateVolumeReplication(&volumeReplicationCreateParams)
		assert.Equal(tt, expectedError, err)
		assert.Empty(tt, volumeReplication)
	})
	t.Run("WhenSnapmirrorCreatePollReturnsError", func(tt *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockStorageClient := new(ontaprest.MockStorageClient)
		mockSnapmirrorClient := new(ontaprest.MockSnapmirrorClient)
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		provider := &OntapRestProvider{}
		expectedError := errors.New("Faceplanting")

		mockClient.On("Storage").Return(mockStorageClient)
		mockClient.On("Snapmirror").Return(mockSnapmirrorClient)
		mockStorageClient.On("VolumeGet", volumeGetParams).Return(&dstVolume, nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipListDestinations", mock.Anything).Return(snapmirrorEmptyList, nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipList", mock.Anything).Return(snapmirrorEmptyList, nil).Times(2)
		mockSnapmirrorClient.On("SnapmirrorRelationshipCreate", createParams).Return(nil, jobAccepted, nil).Times(1)
		mockClient.On("Poll", jobAccepted.JobUUID).Return(expectedError).Times(1)

		volumeReplication, err := provider.CreateVolumeReplication(&volumeReplicationCreateParams)
		assert.Equal(tt, expectedError, err)
		assert.Empty(tt, volumeReplication)
	})
	t.Run("WhenSnapmirrorResyncOrInitializeReturnsError", func(tt *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockStorageClient := new(ontaprest.MockStorageClient)
		mockSnapmirrorClient := new(ontaprest.MockSnapmirrorClient)
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		provider := &OntapRestProvider{}
		expectedError := errors.New("Faceplanting")

		mockClient.On("Storage").Return(mockStorageClient)
		mockClient.On("Snapmirror").Return(mockSnapmirrorClient)
		mockStorageClient.On("VolumeGet", volumeGetParams).Return(&dstVolume, nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipListDestinations", mock.Anything).Return(snapmirrorEmptyList, nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipList", mock.Anything).Return(snapmirrorEmptyList, nil).Times(2)
		mockSnapmirrorClient.On("SnapmirrorRelationshipCreate", createParams).Return(snapmirror, jobAccepted, nil).Times(1)
		mockClient.On("Poll", jobAccepted.JobUUID).Return(nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipGet", getParams).Return(snapmirror, nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipResyncOrInitializeOrResume", snapmirror.UUID.String()).Return(nil, nil, expectedError).Times(1)

		volumeReplication, err := provider.CreateVolumeReplication(&volumeReplicationCreateParams)
		assert.Equal(tt, expectedError, err)
		assert.Empty(tt, volumeReplication)
	})
	t.Run("WhenSnapmirrorResyncOrInitializePollReturnsError", func(tt *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockStorageClient := new(ontaprest.MockStorageClient)
		mockSnapmirrorClient := new(ontaprest.MockSnapmirrorClient)
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		provider := &OntapRestProvider{}
		expectedError := errors.New("Faceplanting")

		mockClient.On("Storage").Return(mockStorageClient)
		mockClient.On("Snapmirror").Return(mockSnapmirrorClient)
		mockStorageClient.On("VolumeGet", volumeGetParams).Return(&dstVolume, nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipListDestinations", mock.Anything).Return(snapmirrorEmptyList, nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipList", mock.Anything).Return(snapmirrorEmptyList, nil).Times(2)
		mockSnapmirrorClient.On("SnapmirrorRelationshipCreate", createParams).Return(snapmirror, jobAccepted, nil).Times(1)
		mockClient.On("Poll", jobAccepted.JobUUID).Return(nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipGet", getParams).Return(snapmirror, nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipResyncOrInitializeOrResume", snapmirror.UUID.String()).Return(nil, jobAccepted, nil).Times(1)
		mockClient.On("Poll", jobAccepted.JobUUID).Return(expectedError).Times(1)

		volumeReplication, err := provider.CreateVolumeReplication(&volumeReplicationCreateParams)
		assert.Equal(tt, expectedError, err)
		assert.Empty(tt, volumeReplication)
	})
	t.Run("WhenSnapmirrorGetReturnsError", func(tt *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockStorageClient := new(ontaprest.MockStorageClient)
		mockSnapmirrorClient := new(ontaprest.MockSnapmirrorClient)
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		provider := &OntapRestProvider{}
		expectedError := errors.New("Faceplanting")

		mockClient.On("Storage").Return(mockStorageClient)
		mockClient.On("Snapmirror").Return(mockSnapmirrorClient)
		mockStorageClient.On("VolumeGet", volumeGetParams).Return(&dstVolume, nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipListDestinations", mock.Anything).Return(snapmirrorEmptyList, nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipList", mock.Anything).Return(snapmirrorEmptyList, nil).Times(2)
		mockSnapmirrorClient.On("SnapmirrorRelationshipCreate", createParams).Return(snapmirror, jobAccepted, nil).Times(1)
		mockClient.On("Poll", jobAccepted.JobUUID).Return(nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipResyncOrInitializeOrResume", snapmirror.UUID.String()).Return(nil, jobAccepted, nil).Times(1)
		mockClient.On("Poll", jobAccepted.JobUUID).Return(nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipGet", getParams).Return(nil, expectedError).Times(1)

		volumeReplication, err := provider.CreateVolumeReplication(&volumeReplicationCreateParams)
		assert.Equal(tt, expectedError, err)
		assert.Empty(tt, volumeReplication)
	})
	t.Run("WhenSnapmirrorWaitForMirrorStateReturnsError", func(tt *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockStorageClient := new(ontaprest.MockStorageClient)
		mockSnapmirrorClient := new(ontaprest.MockSnapmirrorClient)
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		provider := &OntapRestProvider{}
		expectedError := errors.New("Faceplanting")
		volumeReplicationCreateParams.ReverseResync = true

		mockClient.On("Storage").Return(mockStorageClient)
		mockClient.On("Snapmirror").Return(mockSnapmirrorClient)
		mockStorageClient.On("VolumeGet", volumeGetParams).Return(&dstVolume, nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipListDestinations", mock.Anything).Return(snapmirrorEmptyList, nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipList", mock.Anything).Return(snapmirrorEmptyList, nil).Times(2)
		mockSnapmirrorClient.On("SnapmirrorRelationshipCreate", createParams).Return(snapmirror, jobAccepted, nil).Times(1)
		mockClient.On("Poll", jobAccepted.JobUUID).Return(nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipResyncOrInitializeOrResume", snapmirror.UUID.String()).Return(nil, jobAccepted, nil).Times(1)
		mockClient.On("Poll", jobAccepted.JobUUID).Return(nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipGet", getParams).Return(snapmirror, nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipGet", getParams).Return(nil, expectedError).Times(1)

		volumeReplication, err := provider.CreateVolumeReplication(&volumeReplicationCreateParams)
		assert.Equal(tt, expectedError, err)
		assert.Empty(tt, volumeReplication)

		volumeReplicationCreateParams.ReverseResync = false
	})
	t.Run("WhenSnapmirrorGetVolumeForLanguageReturnsError", func(tt *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockStorageClient := new(ontaprest.MockStorageClient)
		mockSnapmirrorClient := new(ontaprest.MockSnapmirrorClient)
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		provider := &OntapRestProvider{}
		expectedError := errors.New("Faceplanting")

		mockClient.On("Storage").Return(mockStorageClient)
		mockClient.On("Snapmirror").Return(mockSnapmirrorClient)
		mockStorageClient.On("VolumeGet", volumeGetParams).Return(&dstVolume, nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipListDestinations", mock.Anything).Return(snapmirrorEmptyList, nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipList", mock.Anything).Return(snapmirrorEmptyList, nil).Times(2)
		mockSnapmirrorClient.On("SnapmirrorRelationshipCreate", createParams).Return(snapmirror, jobAccepted, nil).Times(1)
		mockClient.On("Poll", jobAccepted.JobUUID).Return(nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipResyncOrInitializeOrResume", snapmirror.UUID.String()).Return(nil, jobAccepted, nil).Times(1)
		mockClient.On("Poll", jobAccepted.JobUUID).Return(nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipGet", getParams).Return(snapmirror, nil).Times(2)
		mockStorageClient.On("VolumeGet", &ontaprest.VolumeGetParams{BaseParams: ontaprest.BaseParams{Fields: []string{"language"}}, UUID: *dstVolume.UUID}).Return(nil, expectedError).Times(1)

		volumeReplication, err := provider.CreateVolumeReplication(&volumeReplicationCreateParams)
		assert.Equal(tt, expectedError, err)
		assert.Empty(tt, volumeReplication)
	})
	t.Run("WhenSuccessful", func(tt *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockStorageClient := new(ontaprest.MockStorageClient)
		mockSnapmirrorClient := new(ontaprest.MockSnapmirrorClient)
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		provider := &OntapRestProvider{}
		volumeReplicationCreateParams.ReverseResync = true

		mockClient.On("Storage").Return(mockStorageClient)
		mockClient.On("Snapmirror").Return(mockSnapmirrorClient)
		mockStorageClient.On("VolumeGet", volumeGetParams).Return(&dstVolume, nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipListDestinations", mock.Anything).Return(snapmirrorEmptyList, nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipList", mock.Anything).Return(snapmirrorEmptyList, nil).Times(2)
		mockSnapmirrorClient.On("SnapmirrorRelationshipCreate", createParams).Return(snapmirror, jobAccepted, nil).Times(1)
		mockClient.On("Poll", jobAccepted.JobUUID).Return(nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipResyncOrInitializeOrResume", snapmirror.UUID.String()).Return(nil, jobAccepted, nil).Times(1)
		mockClient.On("Poll", jobAccepted.JobUUID).Return(nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipGet", getParams).Return(snapmirror, nil)

		volumeReplication, err := provider.CreateVolumeReplication(&volumeReplicationCreateParams)
		assert.NoError(tt, err)
		assert.NotEmpty(tt, volumeReplication)

		volumeReplicationCreateParams.ReverseResync = false
	})
	t.Run("WhenSuccessfulWithVolumeLanguage", func(tt *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockStorageClient := new(ontaprest.MockStorageClient)
		mockSnapmirrorClient := new(ontaprest.MockSnapmirrorClient)
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		provider := &OntapRestProvider{}
		languageCode := "c.utf-8"
		dstVolume.Language = &languageCode

		mockClient.On("Storage").Return(mockStorageClient)
		mockClient.On("Snapmirror").Return(mockSnapmirrorClient)
		mockStorageClient.On("VolumeGet", volumeGetParams).Return(&dstVolume, nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipListDestinations", mock.Anything).Return(snapmirrorEmptyList, nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipList", mock.Anything).Return(snapmirrorEmptyList, nil).Times(2)
		mockSnapmirrorClient.On("SnapmirrorRelationshipCreate", createParams).Return(snapmirror, jobAccepted, nil).Times(1)
		mockClient.On("Poll", jobAccepted.JobUUID).Return(nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipResyncOrInitializeOrResume", snapmirror.UUID.String()).Return(nil, jobAccepted, nil).Times(1)
		mockClient.On("Poll", jobAccepted.JobUUID).Return(nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipGet", getParams).Return(snapmirror, nil).Times(2)
		mockStorageClient.On("VolumeGet", &ontaprest.VolumeGetParams{BaseParams: ontaprest.BaseParams{Fields: []string{"language"}}, UUID: *dstVolume.UUID}).Return(&dstVolume, nil).Times(1)

		volumeReplication, err := provider.CreateVolumeReplication(&volumeReplicationCreateParams)
		assert.NoError(tt, err)
		assert.NotNil(tt, volumeReplication.Volume.Language)
		assert.Equal(tt, "c.utf-8", *volumeReplication.Volume.Language)
		assert.NotEmpty(tt, volumeReplication)

		dstVolume.Language = nil
	})

	t.Run("WhenValidateCreateSnapmirrorFails", func(tt *testing.T) {
		peeringErr := errors.New("svm peering failed")
		doEnsureSvmPeering = func(provider *OntapRestProvider, params *CreateVolumeReplicationParams) error {
			return peeringErr
		}
		defer func() { doEnsureSvmPeering = ensureSvmPeering }()

		mockClient := new(ontaprest.MockRESTClient)
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		provider := &OntapRestProvider{}

		volumeReplication, err := provider.CreateVolumeReplication(&volumeReplicationCreateParams)
		assert.Error(tt, err)
		assert.Nil(tt, volumeReplication)
		assert.Contains(tt, err.Error(), "svm peering failed")
	})

	t.Run("WhenCreateVsaVolumeReplicationReturnsFlexGroupGeometryMismatch", func(tt *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockStorageClient := new(ontaprest.MockStorageClient)
		mockSnapmirrorClient := new(ontaprest.MockSnapmirrorClient)
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		doEnsureSvmPeering = func(provider *OntapRestProvider, params *CreateVolumeReplicationParams) error {
			return nil
		}
		provider := &OntapRestProvider{}
		flexGroupErr := errors.New(flexGroupGeometryMismatchMsg)

		mockClient.On("Storage").Return(mockStorageClient)
		mockClient.On("Snapmirror").Return(mockSnapmirrorClient)
		mockStorageClient.On("VolumeGet", volumeGetParams).Return(&dstVolume, nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipListDestinations", mock.Anything).Return(snapmirrorEmptyList, nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipList", mock.Anything).Return(snapmirrorEmptyList, nil).Times(2)
		mockSnapmirrorClient.On("SnapmirrorRelationshipList", mock.MatchedBy(func(p *ontaprest.SnapmirrorRelationshipListParams) bool {
			return p != nil && p.SourcePath == volumeReplicationCreateParams.VolumeReplication.SourcePath() && p.DestinationPath == volumeReplicationCreateParams.VolumeReplication.DestinationPath()
		})).Return(snapmirrorEmptyList, nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipCreate", createParams).Return(nil, nil, flexGroupErr).Times(1)

		volumeReplication, err := provider.CreateVolumeReplication(&volumeReplicationCreateParams)
		assert.Error(tt, err)
		assert.Nil(tt, volumeReplication)
		customErr := vsaerrors.ExtractCustomError(err)
		assert.NotNil(tt, customErr, "expected CustomError (FlexGroup geometry mismatch)")
		assert.True(tt, customErr.IsError(vsaerrors.ErrFlexGroupGeometryMismatchForReplication), "expected ErrFlexGroupGeometryMismatchForReplication")
	})

	t.Run("WhenCreateVsaVolumeReplicationReturnsOtherError", func(tt *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockStorageClient := new(ontaprest.MockStorageClient)
		mockSnapmirrorClient := new(ontaprest.MockSnapmirrorClient)
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		doEnsureSvmPeering = func(provider *OntapRestProvider, params *CreateVolumeReplicationParams) error {
			return nil
		}
		provider := &OntapRestProvider{}
		underlyingErr := errors.New("snapmirror create failed")

		mockClient.On("Storage").Return(mockStorageClient)
		mockClient.On("Snapmirror").Return(mockSnapmirrorClient)
		mockStorageClient.On("VolumeGet", volumeGetParams).Return(&dstVolume, nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipListDestinations", mock.Anything).Return(snapmirrorEmptyList, nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipList", mock.Anything).Return(snapmirrorEmptyList, nil).Times(2)
		mockSnapmirrorClient.On("SnapmirrorRelationshipList", mock.MatchedBy(func(p *ontaprest.SnapmirrorRelationshipListParams) bool {
			return p != nil && p.SourcePath == volumeReplicationCreateParams.VolumeReplication.SourcePath() && p.DestinationPath == volumeReplicationCreateParams.VolumeReplication.DestinationPath()
		})).Return(snapmirrorEmptyList, nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipCreate", createParams).Return(nil, nil, underlyingErr).Times(1)

		volumeReplication, err := provider.CreateVolumeReplication(&volumeReplicationCreateParams)
		assert.Error(tt, err)
		assert.Nil(tt, volumeReplication)
		assert.Contains(tt, err.Error(), "snapmirror create failed")
	})

	t.Run("WhenExistingRelationship_UsesExistingAndResyncs", func(tt *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockStorageClient := new(ontaprest.MockStorageClient)
		mockSnapmirrorClient := new(ontaprest.MockSnapmirrorClient)
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		doEnsureSvmPeering = func(provider *OntapRestProvider, params *CreateVolumeReplicationParams) error {
			return nil
		}
		provider := &OntapRestProvider{}
		volumeReplicationCreateParams.ReverseResync = true

		existingList := []*ontaprest.SnapmirrorRelationship{snapmirror}
		mockClient.On("Storage").Return(mockStorageClient)
		mockClient.On("Snapmirror").Return(mockSnapmirrorClient)
		mockStorageClient.On("VolumeGet", volumeGetParams).Return(&dstVolume, nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipListDestinations", mock.Anything).Return(snapmirrorEmptyList, nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipList", mock.Anything).Return(snapmirrorEmptyList, nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipList", mock.MatchedBy(func(p *ontaprest.SnapmirrorRelationshipListParams) bool {
			return p != nil && p.SourcePath == volumeReplicationCreateParams.VolumeReplication.SourcePath() && p.DestinationPath == volumeReplicationCreateParams.VolumeReplication.DestinationPath()
		})).Return(existingList, nil).Times(1)
		// No SnapmirrorRelationshipCreate - use existing relationship
		mockSnapmirrorClient.On("SnapmirrorRelationshipResyncOrInitializeOrResume", snapmirror.UUID.String()).Return(nil, jobAccepted, nil).Times(1)
		mockClient.On("Poll", jobAccepted.JobUUID).Return(nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipGet", getParams).Return(snapmirror, nil).Times(2)

		volumeReplication, err := provider.CreateVolumeReplication(&volumeReplicationCreateParams)
		assert.NoError(tt, err)
		assert.NotEmpty(tt, volumeReplication)

		volumeReplicationCreateParams.ReverseResync = false
	})
}

func TestCreateVsaVolumeReplication(t *testing.T) {
	dstVolumeUUID := "dst-uuid"
	volumeReplicationCreateParams := CreateVolumeReplicationParams{
		VolumeReplication: &VolumeReplication{
			Volume:                &Volume{ExternalUUID: dstVolumeUUID},
			ReplicationPolicy:     "MirrorAllSnapshots",
			ReplicationSchedule:   VolumeReplicationScheduleDaily,
			SourceSVMName:         "srcsvm",
			SourceVolumeName:      "srcvol",
			DestinationSVMName:    "dstsvm",
			DestinationVolumeName: "dstvol",
		},
	}
	provider := &OntapRestProvider{}
	originalGetOntapClientFunc := getOntapClientFunc
	originalDoCreateSchedule := doCreateVolumeReplicationScheduleIfNeeded
	defer func() {
		getOntapClientFunc = originalGetOntapClientFunc
		doCreateVolumeReplicationScheduleIfNeeded = originalDoCreateSchedule
	}()

	t.Run("WhenCreateVolumeReplicationScheduleIfNeededFails", func(tt *testing.T) {
		scheduleErr := errors.New("schedule creation failed")
		doCreateVolumeReplicationScheduleIfNeeded = func(p *OntapRestProvider, schedule string) error {
			return scheduleErr
		}
		replication, err := createVsaVolumeReplication(provider, &volumeReplicationCreateParams, false)
		assert.Error(tt, err)
		assert.Nil(tt, replication)
		assert.Equal(tt, scheduleErr, err)
	})

	t.Run("WhenGetOntapClientFails", func(tt *testing.T) {
		doCreateVolumeReplicationScheduleIfNeeded = func(p *OntapRestProvider, schedule string) error {
			return nil
		}
		clientErr := errors.New("client init failed")
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return nil, clientErr
		}
		replication, err := createVsaVolumeReplication(provider, &volumeReplicationCreateParams, false)
		assert.Error(tt, err)
		assert.Nil(tt, replication)
		assert.Equal(tt, clientErr, err)
	})

	t.Run("WhenSnapmirrorListFails", func(tt *testing.T) {
		doCreateVolumeReplicationScheduleIfNeeded = func(p *OntapRestProvider, schedule string) error {
			return nil
		}
		mockClient := new(ontaprest.MockRESTClient)
		mockSnapmirrorClient := new(ontaprest.MockSnapmirrorClient)
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		listErr := errors.New("list failed")
		mockClient.On("Snapmirror").Return(mockSnapmirrorClient)
		mockSnapmirrorClient.On("SnapmirrorRelationshipList", mock.Anything).Return(nil, listErr).Times(1)

		replication, err := createVsaVolumeReplication(provider, &volumeReplicationCreateParams, false)
		assert.Error(tt, err)
		assert.Nil(tt, replication)
		assert.Equal(tt, listErr, err)
	})
}

func TestConvertSnapmirrorToVolumeReplication(t *testing.T) {
	snapmirror := ontaprest.SnapmirrorRelationship{
		SnapmirrorRelationship: models.SnapmirrorRelationship{
			UUID:                  nillable.ToPointer(strfmt.UUID("uuid")),
			State:                 nillable.ToPointer(models.SnapmirrorRelationshipStateSnapmirrored),
			TotalTransferDuration: nillable.ToPointer("PT2M34S"),
			Transfer: &models.SnapmirrorRelationshipInlineTransfer{
				TotalDuration:    nillable.ToPointer("PT4M50S"),
				State:            nillable.ToPointer(models.SnapmirrorRelationshipInlineTransferStateSuccess),
				BytesTransferred: nillable.ToPointer(int64(500)),
				EndTime:          nillable.ToPointer(strfmt.DateTime(time.Now())),
			},
			Policy: &models.SnapmirrorRelationshipInlinePolicy{
				Name: nillable.ToPointer("policy"),
			},
			LagTime: nillable.ToPointer("PT20S"),
			SnapmirrorRelationshipInlineUnhealthyReason: []*models.SnapmirrorError{
				{
					Message: nillable.ToPointer("error"),
				},
			},
			TransferSchedule: &models.SnapmirrorRelationshipInlineTransferSchedule{
				Name: nillable.ToPointer("le schedule"),
			},
			Healthy:            nillable.GetBoolPtr(true),
			TotalTransferBytes: nillable.ToPointer(int64(1000)),
		},
	}
	volumeReplication := &VolumeReplication{}

	t.Run("WhenParsingISO8601Time", func(tt *testing.T) {
		volumeReplicationRes, err := convertSnapMirrorToVolumeReplication(snapmirror, volumeReplication)
		assert.NoError(tt, err)
		assert.NotEmpty(tt, volumeReplicationRes)
		assert.Equal(tt, volumeReplicationRes.TotalTransferTimeSecs, int64(154))
		assert.Equal(tt, volumeReplicationRes.LastTransferDuration, int64(290))
		assert.Equal(tt, volumeReplicationRes.LagTime, int64(20))
	})
	t.Run("WhenTotalTransferDurationReturnsError", func(tt *testing.T) {
		snapmirror.TotalTransferDuration = nillable.ToPointer("some-invalid-string")
		expectedError := errors.New("unexpected input")

		volumeReplicationRes, err := convertSnapMirrorToVolumeReplication(snapmirror, volumeReplication)
		assert.Equal(tt, expectedError, err)
		assert.Empty(tt, volumeReplicationRes)
		snapmirror.TotalTransferDuration = nillable.ToPointer("PT2M34S")
	})
	t.Run("WhenTotalDurationReturnsError", func(tt *testing.T) {
		snapmirror.Transfer.TotalDuration = nillable.ToPointer("some-invalid-string")
		expectedError := errors.New("unexpected input")

		volumeReplicationRes, err := convertSnapMirrorToVolumeReplication(snapmirror, volumeReplication)
		assert.Equal(tt, expectedError, err)
		assert.Empty(tt, volumeReplicationRes)
		snapmirror.Transfer.TotalDuration = nillable.ToPointer("PT4M50S")
	})
	t.Run("WhenLagTimeReturnsError", func(tt *testing.T) {
		snapmirror.LagTime = nillable.ToPointer("some-invalid-string")
		expectedError := errors.New("unexpected input")

		volumeReplicationRes, err := convertSnapMirrorToVolumeReplication(snapmirror, volumeReplication)
		assert.Equal(tt, expectedError, err)
		assert.Empty(tt, volumeReplicationRes)
		snapmirror.LagTime = nillable.ToPointer("PT20S")
	})
	t.Run("WhenTransferIsNil", func(tt *testing.T) {
		snapmirror.Transfer = nil
		volumeReplicationRes, err := convertSnapMirrorToVolumeReplication(snapmirror, volumeReplication)
		assert.NoError(tt, err)
		assert.NotEmpty(tt, volumeReplicationRes)
		snapmirror.Transfer = &models.SnapmirrorRelationshipInlineTransfer{
			TotalDuration:    nillable.ToPointer("PT4M50S"),
			State:            nillable.ToPointer(models.SnapmirrorRelationshipInlineTransferStateSuccess),
			BytesTransferred: nillable.ToPointer(int64(500)),
			EndTime:          nillable.ToPointer(strfmt.DateTime(time.Now())),
		}
	})
	t.Run("WhenEndTimeIsNil", func(tt *testing.T) {
		var nilEndTime *time.Time
		snapmirror.Transfer.EndTime = nil
		volumeReplicationRes, err := convertSnapMirrorToVolumeReplication(snapmirror, volumeReplication)
		assert.NoError(tt, err)
		assert.NotEmpty(tt, volumeReplicationRes)
		assert.Equal(tt, nilEndTime, volumeReplicationRes.LastTransferEndTime)
		snapmirror.Transfer.EndTime = nillable.ToPointer(strfmt.DateTime(time.Now()))
	})
	t.Run("WhenPolicyIsNil", func(tt *testing.T) {
		snapmirrorWithNilPolicy := snapmirror
		snapmirrorWithNilPolicy.Policy = nil
		volumeReplicationWithPolicy := &VolumeReplication{
			ReplicationPolicy: "MirrorAllSnapshots",
		}

		volumeReplicationRes, err := convertSnapMirrorToVolumeReplication(snapmirrorWithNilPolicy, volumeReplicationWithPolicy)
		assert.NoError(tt, err)
		assert.NotEmpty(tt, volumeReplicationRes)
		assert.Equal(tt, "MirrorAllSnapshots", volumeReplicationRes.ReplicationPolicy)
	})
	t.Run("WhenPolicyNameIsNil", func(tt *testing.T) {
		snapmirrorWithNilPolicyName := snapmirror
		snapmirrorWithNilPolicyName.Policy = &models.SnapmirrorRelationshipInlinePolicy{}
		volumeReplicationWithPolicy := &VolumeReplication{
			ReplicationPolicy: "MirrorAllSnapshots",
		}

		volumeReplicationRes, err := convertSnapMirrorToVolumeReplication(snapmirrorWithNilPolicyName, volumeReplicationWithPolicy)
		assert.NoError(tt, err)
		assert.NotEmpty(tt, volumeReplicationRes)
		assert.Equal(tt, "", volumeReplicationRes.ReplicationPolicy)
	})
	t.Run("WhenConversionSuccessful", func(tt *testing.T) {
		volumeReplicationRes, err := convertSnapMirrorToVolumeReplication(snapmirror, volumeReplication)
		assert.NoError(tt, err)
		assert.NotEmpty(tt, volumeReplicationRes)
	})
}

func TestAuthorizeVolumeReplication(t *testing.T) {
	t.Run("WhenDoEnsureSvmPeeringReturnsError", func(tt *testing.T) {
		provider := &OntapRestProvider{}
		params := &CreateVolumeReplicationParams{}
		doEnsureSvmPeering = func(provider *OntapRestProvider, params *CreateVolumeReplicationParams) error {
			return errors.New("some error")
		}
		defer func() { doEnsureSvmPeering = ensureSvmPeering }()
		volRep, err := provider.AuthorizeVolumeReplication(params)
		assert.Error(tt, err)
		assert.Nil(tt, volRep)
	})
	t.Run("WhenSuccessful", func(tt *testing.T) {
		provider := &OntapRestProvider{}
		params := &CreateVolumeReplicationParams{}
		doEnsureSvmPeering = func(provider *OntapRestProvider, params *CreateVolumeReplicationParams) error {
			return nil
		}
		defer func() { doEnsureSvmPeering = ensureSvmPeering }()
		volRep, err := provider.AuthorizeVolumeReplication(params)
		assert.NoError(tt, err)
		assert.Nil(tt, volRep)
	})
}

func TestDeleteVolumeReplication(t *testing.T) {
	testUUID := utils.RandomUUID()
	formattedUUID := strfmt.UUID(testUUID)
	delParams := &ontaprest.SnapmirrorRelationshipDeleteParams{UUID: testUUID}
	getParams := &ontaprest.SnapmirrorRelationshipGetParams{UUID: testUUID}
	listParams := &ontaprest.SnapmirrorRelationshipListParams{DestinationPath: ":", SourcePath: ":"}
	jobAccepted := &ontaprest.JobAccepted{JobUUID: "jobUUID"}
	mirrorState := "idle"
	volumeReplicationDeleteParams := DeleteVolumeReplicationParams{VolumeReplication: &VolumeReplication{RelationshipID: testUUID}}
	returnSnapmirror := ontaprest.SnapmirrorRelationship{SnapmirrorRelationship: models.SnapmirrorRelationship{UUID: &formattedUUID, State: &mirrorState}}
	originalgetOntapClientFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = originalgetOntapClientFunc }()
	t.Run("WhenGetOntapClientFunc", func(tt *testing.T) {
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return nil, errors.New("getOntapClient error")
		}
		provider := &OntapRestProvider{}
		volumeReplication, err := provider.DeleteVolumeReplication(&volumeReplicationDeleteParams)
		assert.Error(tt, err)
		assert.Nil(tt, volumeReplication)
		assert.Equal(tt, err.Error(), "getOntapClient error")
	})
	t.Run("WhenClientReturnsError", func(tt *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockStorageClient := new(ontaprest.MockStorageClient)
		mockSnapmirrorClient := new(ontaprest.MockSnapmirrorClient)
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		provider := &OntapRestProvider{}
		expectedError := errors.New("Faceplanting")

		mockClient.On("Storage").Return(mockStorageClient)
		mockClient.On("Snapmirror").Return(mockSnapmirrorClient)
		mockSnapmirrorClient.On("SnapmirrorRelationshipGet", getParams).Return(nil, expectedError).Times(1)
		volumeReplication, err := provider.DeleteVolumeReplication(&volumeReplicationDeleteParams)
		assert.Equal(tt, expectedError, err)
		assert.Empty(tt, volumeReplication)
	})
	t.Run("WhenEntryNotFound", func(tt *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockSnapmirrorClient := new(ontaprest.MockSnapmirrorClient)
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		provider := &OntapRestProvider{}
		doCleanupSvmPeering = func(provider *OntapRestProvider, params *DeleteVolumeReplicationParams) error {
			return nil
		}
		expectedError := errors.NewNotFoundErr("", nil)

		mockClient.On("Snapmirror").Return(mockSnapmirrorClient)
		mockSnapmirrorClient.On("SnapmirrorRelationshipGet", getParams).Return(nil, expectedError).Times(1)

		volumeReplication, err := provider.DeleteVolumeReplication(&volumeReplicationDeleteParams)
		assert.NoError(tt, err)
		assert.Equal(tt, volumeReplication, &VolumeReplication{RelationshipID: testUUID})
		doCleanupSvmPeering = cleanupSvmPeering
	})
	t.Run("WhenInMirroredRelationship", func(tt *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockSnapmirrorClient := new(ontaprest.MockSnapmirrorClient)
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		provider := &OntapRestProvider{}
		snapmirrorState := SnapmirrorStateMirrored

		mockClient.On("Snapmirror").Return(mockSnapmirrorClient)
		mockSnapmirrorClient.On("SnapmirrorRelationshipGet", getParams).Return(&ontaprest.SnapmirrorRelationship{
			SnapmirrorRelationship: models.SnapmirrorRelationship{
				State: &snapmirrorState,
				UUID:  &formattedUUID,
			},
		}, nil).Times(1)

		_, err := provider.DeleteVolumeReplication(&volumeReplicationDeleteParams)
		assert.Error(tt, err, "Cannot delete a relationship in the current mirror state")
	})
	t.Run("WhenDeleteReturnsAnError", func(tt *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockSnapmirrorClient := new(ontaprest.MockSnapmirrorClient)
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		provider := &OntapRestProvider{}
		doCleanupSvmPeering = func(provider *OntapRestProvider, params *DeleteVolumeReplicationParams) error {
			return nil
		}

		expectedError := errors.New("Faceplanting")

		mockClient.On("Snapmirror").Return(mockSnapmirrorClient)
		mockSnapmirrorClient.On("SnapmirrorRelationshipGet", getParams).Return(&returnSnapmirror, nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipDelete", delParams).Return(false, nil, expectedError).Times(1)
		volumeReplication, err := provider.DeleteVolumeReplication(&volumeReplicationDeleteParams)
		assert.Equal(tt, expectedError, err)
		assert.Empty(tt, volumeReplication)
		doCleanupSvmPeering = cleanupSvmPeering
	})
	t.Run("WhenDeleteReturnsEntryNotFound", func(tt *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockSnapmirrorClient := new(ontaprest.MockSnapmirrorClient)
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		provider := &OntapRestProvider{}

		doCleanupSvmPeering = func(provider *OntapRestProvider, params *DeleteVolumeReplicationParams) error {
			return nil
		}
		expectedError := errors.New("entry not found")

		mockClient.On("Snapmirror").Return(mockSnapmirrorClient)
		mockSnapmirrorClient.On("SnapmirrorRelationshipGet", getParams).Return(&returnSnapmirror, nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipDelete", delParams).Return(false, nil, expectedError).Times(1)
		volumeReplication, err := provider.DeleteVolumeReplication(&volumeReplicationDeleteParams)
		assert.NoError(tt, err)
		assert.NotNil(tt, volumeReplication)
		doCleanupSvmPeering = cleanupSvmPeering
	})
	t.Run("WhenPollingFails", func(tt *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockSnapmirrorClient := new(ontaprest.MockSnapmirrorClient)
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		provider := &OntapRestProvider{}

		doCleanupSvmPeering = func(provider *OntapRestProvider, params *DeleteVolumeReplicationParams) error {
			return nil
		}
		expectedError := errors.New("Faceplanting")

		mockClient.On("Snapmirror").Return(mockSnapmirrorClient)
		mockSnapmirrorClient.On("SnapmirrorRelationshipGet", getParams).Return(&returnSnapmirror, nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipDelete", delParams).Return(false, jobAccepted, nil).Times(1)
		mockClient.On("Poll", jobAccepted.JobUUID).Return(expectedError).Times(1)
		volumeReplication, err := provider.DeleteVolumeReplication(&volumeReplicationDeleteParams)
		assert.Equal(tt, expectedError, err)
		assert.Empty(tt, volumeReplication)
		doCleanupSvmPeering = cleanupSvmPeering
	})
	t.Run("WhenPollingReturnsNotFound", func(tt *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockSnapmirrorClient := new(ontaprest.MockSnapmirrorClient)
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		provider := &OntapRestProvider{}
		expectedError := errors.New("entry not found")

		doCleanupSvmPeering = func(provider *OntapRestProvider, params *DeleteVolumeReplicationParams) error {
			return nil
		}

		mockClient.On("Snapmirror").Return(mockSnapmirrorClient)
		mockSnapmirrorClient.On("SnapmirrorRelationshipGet", getParams).Return(&returnSnapmirror, nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipDelete", delParams).Return(false, jobAccepted, nil).Times(1)
		mockClient.On("Poll", jobAccepted.JobUUID).Return(expectedError).Times(1)

		volumeReplication, err := provider.DeleteVolumeReplication(&volumeReplicationDeleteParams)
		assert.NoError(tt, err)
		assert.NotNil(tt, volumeReplication)
		doCleanupSvmPeering = cleanupSvmPeering
	})
	t.Run("WhenSVMCleanupFails", func(tt *testing.T) {
		expectedError := errors.New("Faceplanting")
		mockClient := new(ontaprest.MockRESTClient)
		mockSnapmirrorClient := new(ontaprest.MockSnapmirrorClient)
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		provider := &OntapRestProvider{}

		doCleanupSvmPeering = func(provider *OntapRestProvider, params *DeleteVolumeReplicationParams) error {
			return expectedError
		}

		mockClient.On("Snapmirror").Return(mockSnapmirrorClient)
		mockSnapmirrorClient.On("SnapmirrorRelationshipGet", getParams).Return(&returnSnapmirror, nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipDelete", delParams).Return(false, jobAccepted, nil).Times(1)
		mockClient.On("Poll", jobAccepted.JobUUID).Return(nil).Times(1)

		volumeReplication, err := provider.DeleteVolumeReplication(&volumeReplicationDeleteParams)
		assert.Equal(tt, expectedError, err)
		assert.Empty(tt, volumeReplication)
		doCleanupSvmPeering = cleanupSvmPeering
	})
	t.Run("WhenSuccessful", func(tt *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockSnapmirrorClient := new(ontaprest.MockSnapmirrorClient)
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		provider := &OntapRestProvider{}

		doCleanupSvmPeering = func(provider *OntapRestProvider, params *DeleteVolumeReplicationParams) error {
			return nil
		}
		defer func() {
			doCleanupSvmPeering = cleanupSvmPeering
		}()

		mockClient.On("Snapmirror").Return(mockSnapmirrorClient)
		mockSnapmirrorClient.On("SnapmirrorRelationshipGet", getParams).Return(&returnSnapmirror, nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipDelete", delParams).Return(false, jobAccepted, nil).Times(1)
		mockClient.On("Poll", jobAccepted.JobUUID).Return(nil).Times(1)

		volumeReplication, err := provider.DeleteVolumeReplication(&volumeReplicationDeleteParams)
		assert.NoError(tt, err)
		assert.NotNil(tt, volumeReplication)
		doCleanupSvmPeering = cleanupSvmPeering
	})
	t.Run("WithoutRelationshipIDReturnsEmpty", func(tt *testing.T) {
		replicationDeleteParams := DeleteVolumeReplicationParams{VolumeReplication: &VolumeReplication{}}
		mockClient := new(ontaprest.MockRESTClient)
		mockSnapmirrorClient := new(ontaprest.MockSnapmirrorClient)
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		provider := &OntapRestProvider{}

		doCleanupSvmPeering = func(provider *OntapRestProvider, params *DeleteVolumeReplicationParams) error {
			return nil
		}

		mockClient.On("Snapmirror").Return(mockSnapmirrorClient)
		mockSnapmirrorClient.On("SnapmirrorRelationshipList", listParams).Return([]*ontaprest.SnapmirrorRelationship{}, nil).Times(1)
		volumeReplication, err := provider.DeleteVolumeReplication(&replicationDeleteParams)
		assert.NoError(tt, err)
		assert.Empty(tt, volumeReplication)
		doCleanupSvmPeering = cleanupSvmPeering
	})
	t.Run("WithoutRelationshipIDReturnsError", func(tt *testing.T) {
		replicationDeleteParams := DeleteVolumeReplicationParams{VolumeReplication: &VolumeReplication{}}
		mockClient := new(ontaprest.MockRESTClient)
		mockSnapmirrorClient := new(ontaprest.MockSnapmirrorClient)
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		provider := &OntapRestProvider{}

		doCleanupSvmPeering = func(provider *OntapRestProvider, params *DeleteVolumeReplicationParams) error {
			return nil
		}
		expectedError := errors.New("Faceplanting")

		mockClient.On("Snapmirror").Return(mockSnapmirrorClient)
		mockSnapmirrorClient.On("SnapmirrorRelationshipList", listParams).Return(nil, expectedError).Times(1)

		volumeReplication, err := provider.DeleteVolumeReplication(&replicationDeleteParams)
		assert.Error(tt, err, expectedError)
		assert.Empty(tt, volumeReplication)
	})
	t.Run("WithoutRelationshipIDSuccessful", func(tt *testing.T) {
		replicationDeleteParams := DeleteVolumeReplicationParams{VolumeReplication: &VolumeReplication{}}
		mockClient := new(ontaprest.MockRESTClient)
		mockSnapmirrorClient := new(ontaprest.MockSnapmirrorClient)
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		provider := &OntapRestProvider{}

		doCleanupSvmPeering = func(provider *OntapRestProvider, params *DeleteVolumeReplicationParams) error {
			return nil
		}

		mockClient.On("Snapmirror").Return(mockSnapmirrorClient)
		mockSnapmirrorClient.On("SnapmirrorRelationshipList", listParams).Return([]*ontaprest.SnapmirrorRelationship{&returnSnapmirror}, nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipDelete", delParams).Return(false, jobAccepted, nil).Times(1)
		mockClient.On("Poll", jobAccepted.JobUUID).Return(nil).Times(1)

		volumeReplication, err := provider.DeleteVolumeReplication(&replicationDeleteParams)
		assert.NoError(tt, err)
		assert.Empty(tt, volumeReplication)
		doCleanupSvmPeering = cleanupSvmPeering
	})
}

func TestUpdateVolumeReplication(t *testing.T) {
	originalgetOntapClientFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = originalgetOntapClientFunc }()
	t.Run("WhenReplicationScheduleIsEmpty", func(tt *testing.T) {
		provider := &OntapRestProvider{}
		params := VolumeReplication{ReplicationSchedule: ""}

		result, err := provider.UpdateVolumeReplication(&params)
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
	})
	t.Run("WhenCreateVolumeReplicationScheduleFails", func(tt *testing.T) {
		provider := &OntapRestProvider{}
		params := VolumeReplication{ReplicationSchedule: "daily"}

		defer func() {
			doCreateVolumeReplicationScheduleIfNeeded = createVolumeReplicationSchedule
		}()
		doCreateVolumeReplicationScheduleIfNeeded = func(provider *OntapRestProvider, schedule string) (err error) {
			return errors.New("failed to create schedule")
		}
		result, err := provider.UpdateVolumeReplication(&params)
		assert.Error(tt, err)
		assert.Nil(tt, result)
	})
	t.Run("WhenClientReturnsError", func(tt *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockSnapmirrorClient := new(ontaprest.MockSnapmirrorClient)
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		provider := &OntapRestProvider{}
		expectedErr := errors.New("RandomError")

		params := &VolumeReplication{ReplicationSchedule: "daily", RelationshipID: "123"}
		modifyParams := &ontaprest.SnapmirrorRelationshipModifyParams{TransferSchedule: &params.ReplicationSchedule, UUID: params.RelationshipID}
		defer func() {
			doCreateVolumeReplicationScheduleIfNeeded = createVolumeReplicationSchedule
		}()
		doCreateVolumeReplicationScheduleIfNeeded = func(provider *OntapRestProvider, schedule string) (err error) {
			return nil
		}

		mockClient.On("Snapmirror").Return(mockSnapmirrorClient)
		mockSnapmirrorClient.On("SnapmirrorRelationshipModify", modifyParams).Return(nil, nil, expectedErr).Times(1)
		volumeReplication, err := provider.UpdateVolumeReplication(params)
		assert.Equal(tt, expectedErr, err)
		assert.Empty(tt, volumeReplication)
	})
	t.Run("WhenPollReturnsError", func(tt *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockSnapmirrorClient := new(ontaprest.MockSnapmirrorClient)
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		provider := &OntapRestProvider{}
		expectedErr := errors.New("RandomError")

		params := &VolumeReplication{ReplicationSchedule: "daily", RelationshipID: "123"}
		jobAccepted := &ontaprest.JobAccepted{JobUUID: "jobUUID"}
		modifyParams := &ontaprest.SnapmirrorRelationshipModifyParams{TransferSchedule: &params.ReplicationSchedule, UUID: params.RelationshipID}
		defer func() {
			doCreateVolumeReplicationScheduleIfNeeded = createVolumeReplicationSchedule
		}()
		doCreateVolumeReplicationScheduleIfNeeded = func(provider *OntapRestProvider, schedule string) (err error) {
			return nil
		}

		mockClient.On("Snapmirror").Return(mockSnapmirrorClient)
		mockSnapmirrorClient.On("SnapmirrorRelationshipModify", modifyParams).Return(nil, jobAccepted, nil).Times(1)
		mockClient.On("Poll", jobAccepted.JobUUID).Return(expectedErr).Times(1)
		volumeReplication, err := provider.UpdateVolumeReplication(params)
		assert.Equal(tt, expectedErr, err)
		assert.Empty(tt, volumeReplication)
	})
	t.Run("WhenGetSnapmirrorReturnsError", func(tt *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockSnapmirrorClient := new(ontaprest.MockSnapmirrorClient)
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		provider := &OntapRestProvider{}
		expectedErr := errors.New("RandomError")

		params := &VolumeReplication{ReplicationSchedule: "daily", RelationshipID: "123"}
		jobAccepted := &ontaprest.JobAccepted{JobUUID: "jobUUID"}

		modifyParams := &ontaprest.SnapmirrorRelationshipModifyParams{TransferSchedule: &params.ReplicationSchedule, UUID: params.RelationshipID}
		getParams := &ontaprest.SnapmirrorRelationshipGetParams{UUID: params.RelationshipID}
		defer func() {
			doCreateVolumeReplicationScheduleIfNeeded = createVolumeReplicationSchedule
		}()
		doCreateVolumeReplicationScheduleIfNeeded = func(provider *OntapRestProvider, schedule string) (err error) {
			return nil
		}

		mockClient.On("Snapmirror").Return(mockSnapmirrorClient)
		mockSnapmirrorClient.On("SnapmirrorRelationshipModify", modifyParams).Return(nil, jobAccepted, nil).Times(1)
		mockClient.On("Poll", jobAccepted.JobUUID).Return(nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipGet", getParams).Return(nil, expectedErr).Times(1)
		volumeReplication, err := provider.UpdateVolumeReplication(params)
		assert.Equal(tt, expectedErr, err)
		assert.Empty(tt, volumeReplication)
	})
	t.Run("WhenSuccess", func(tt *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockSnapmirrorClient := new(ontaprest.MockSnapmirrorClient)
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		provider := &OntapRestProvider{}

		jobAccepted := &ontaprest.JobAccepted{JobUUID: "jobUUID"}
		replicationSchedule := "daily"
		snapmirrorPolicyName := "policyName"
		relationshipID := strfmt.UUID(uuid.New().String())
		boolTrue := true
		snapMirrorSchedule := models.SnapmirrorRelationshipInlineTransferSchedule{Name: &replicationSchedule}
		expectedSnapmirrorRelationship := &ontaprest.SnapmirrorRelationship{
			SnapmirrorRelationship: models.SnapmirrorRelationship{
				TransferSchedule: &snapMirrorSchedule,
				Policy:           &models.SnapmirrorRelationshipInlinePolicy{Name: &snapmirrorPolicyName},
				UUID:             &relationshipID,
				Healthy:          &boolTrue,
			},
		}

		params := &VolumeReplication{ReplicationSchedule: replicationSchedule, RelationshipID: relationshipID.String()}

		modifyParams := &ontaprest.SnapmirrorRelationshipModifyParams{TransferSchedule: &params.ReplicationSchedule, UUID: params.RelationshipID}
		getParams := &ontaprest.SnapmirrorRelationshipGetParams{UUID: params.RelationshipID}

		defer func() {
			doCreateVolumeReplicationScheduleIfNeeded = createVolumeReplicationSchedule
		}()
		doCreateVolumeReplicationScheduleIfNeeded = func(provider *OntapRestProvider, schedule string) (err error) {
			return nil
		}

		mockClient.On("Snapmirror").Return(mockSnapmirrorClient)
		mockSnapmirrorClient.On("SnapmirrorRelationshipModify", modifyParams).Return(nil, jobAccepted, nil).Times(1)
		mockClient.On("Poll", jobAccepted.JobUUID).Return(nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipGet", getParams).Return(expectedSnapmirrorRelationship, nil).Times(1)

		volumeReplication, err := provider.UpdateVolumeReplication(params)
		assert.NoError(tt, err)
		assert.Equal(tt, replicationSchedule, volumeReplication.ReplicationSchedule)
		assert.Equal(tt, relationshipID.String(), volumeReplication.RelationshipID)
		assert.Equal(tt, snapmirrorPolicyName, volumeReplication.ReplicationPolicy)
		assert.Equal(tt, true, volumeReplication.Healthy)
	})
}

func TestReleaseVolumeReplication(t *testing.T) {
	srcVolumeUUID := "src-uuid"
	dstVolumeUUID := "dst-uuid"
	srcVolume := ontaprest.Volume{
		Volume: models.Volume{
			UUID: &srcVolumeUUID,
			Type: nillable.ToPointer(models.VolumeTypeRw),
			Name: nillable.ToPointer("srcvol"),
			Svm: &models.VolumeInlineSvm{
				Name: nillable.ToPointer("srcsvm"),
			},
		},
	}
	dstVolume := ontaprest.Volume{
		Volume: models.Volume{
			UUID: &dstVolumeUUID,
			Type: nillable.ToPointer(models.VolumeTypeDp),
			Name: nillable.ToPointer("dstvol"),
			Svm: &models.VolumeInlineSvm{
				Name: nillable.ToPointer("dstsvm"),
			},
		},
	}
	online := "online"
	volume1 := &Volume{
		Volume:            models.Volume{State: &online},
		ExternalUUID:      dstVolumeUUID,
		IsOnPremMigration: true,
	}
	volumeReplicationCreateParams := ReleaseVolumeReplicationParams{
		VolumeReplication: &VolumeReplication{
			Volume:                volume1,
			ReplicationPolicy:     "MirrorAllSnapshots",
			SourceSVMName:         *srcVolume.Svm.Name,
			SourceVolumeName:      *srcVolume.Name,
			DestinationSVMName:    *dstVolume.Svm.Name,
			DestinationVolumeName: *dstVolume.Name,
			ClusterPeerID:         nillable.ToPointer(uint64(1)),
			EndpointType:          "src",
			ReplicationType:       "ONPREM_REPLICATION",
		},
	}
	listDestinationParams := &ontaprest.SnapmirrorRelationshipListDestinationsParams{
		DestinationPath: nillable.GetStringPtr(volumeReplicationCreateParams.VolumeReplication.DestinationPath()),
		SourcePath:      nillable.GetStringPtr(volumeReplicationCreateParams.VolumeReplication.SourcePath()),
	}
	uuid := strfmt.UUID("snapmirror-uuid")
	destinations := []*ontaprest.SnapmirrorRelationship{
		{
			SnapmirrorRelationship: models.SnapmirrorRelationship{
				UUID: &uuid,
			},
		},
	}
	releaseParams := &ontaprest.SnapmirrorRelationshipReleaseParams{
		UUID: destinations[0].UUID.String(),
	}
	jobAccepted := &ontaprest.JobAccepted{JobUUID: "jobUUID"}
	oldSnapmirrorErrorIntervalRetrySeconds := snapmirrorErrorIntervalRetrySeconds
	originalgetOntapClientFunc := getOntapClientFunc
	defer func() {
		snapmirrorErrorIntervalRetrySeconds = oldSnapmirrorErrorIntervalRetrySeconds
		getOntapClientFunc = originalgetOntapClientFunc
	}()

	t.Run("WhenListDestinationsReturnsError", func(tt *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockSnapmirrorClient := new(ontaprest.MockSnapmirrorClient)

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		provider := &OntapRestProvider{}
		expectedError := errors.New("faceplanting")

		mockClient.On("Snapmirror").Return(mockSnapmirrorClient)
		mockSnapmirrorClient.On("SnapmirrorRelationshipListDestinations", listDestinationParams).Return(nil, expectedError).Times(1)
		volumeReplication, err := provider.ReleaseVolumeReplication(&volumeReplicationCreateParams)
		assert.Equal(tt, expectedError, err)
		assert.Empty(tt, volumeReplication)
	})
	t.Run("WhenReleaseReturnsError", func(tt *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockSnapmirrorClient := new(ontaprest.MockSnapmirrorClient)

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		provider := &OntapRestProvider{}
		expectedError := errors.New("faceplanting")

		mockClient.On("Snapmirror").Return(mockSnapmirrorClient)
		mockSnapmirrorClient.On("SnapmirrorRelationshipListDestinations", listDestinationParams).Return(destinations, nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipRelease", releaseParams).Return(false, nil, expectedError).Times(1)
		volumeReplication, err := provider.ReleaseVolumeReplication(&volumeReplicationCreateParams)
		assert.Equal(tt, expectedError, err)
		assert.Empty(tt, volumeReplication)
	})
	t.Run("WhenPollReturnsError", func(tt *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockSnapmirrorClient := new(ontaprest.MockSnapmirrorClient)

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		provider := &OntapRestProvider{}
		expectedError := errors.New("faceplanting")

		mockClient.On("Snapmirror").Return(mockSnapmirrorClient)
		mockSnapmirrorClient.On("SnapmirrorRelationshipListDestinations", listDestinationParams).Return(destinations, nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipRelease", releaseParams).Return(false, jobAccepted, nil).Times(1)
		mockClient.On("Poll", jobAccepted.JobUUID).Return(expectedError).Times(1)
		volumeReplication, err := provider.ReleaseVolumeReplication(&volumeReplicationCreateParams)
		assert.Equal(tt, expectedError, err)
		assert.Empty(tt, volumeReplication)
	})
	t.Run("WhenReleaseReturnsAnotherOperationError", func(tt *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockSnapmirrorClient := new(ontaprest.MockSnapmirrorClient)

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		provider := &OntapRestProvider{}
		expectedError := errors.New("Another SnapMirror operation is in progress")

		snapmirrorErrorIntervalRetrySeconds = 0
		doCleanupSvmPeering = func(provider *OntapRestProvider, params *DeleteVolumeReplicationParams) error {
			return nil
		}
		defer func() {
			doCleanupSvmPeering = cleanupSvmPeering
		}()

		mockClient.On("Snapmirror").Return(mockSnapmirrorClient)
		mockSnapmirrorClient.On("SnapmirrorRelationshipListDestinations", listDestinationParams).Return(destinations, nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipRelease", releaseParams).Return(false, nil, expectedError).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipRelease", releaseParams).Return(false, jobAccepted, nil).Times(1)
		mockClient.On("Poll", jobAccepted.JobUUID).Return(nil).Times(1)
		volumeReplication, err := provider.ReleaseVolumeReplication(&volumeReplicationCreateParams)
		assert.NoError(tt, err)
		assert.NotEmpty(tt, volumeReplication)
	})
	t.Run("WhenReleaseReturnsEntryNotFoundError", func(tt *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockSnapmirrorClient := new(ontaprest.MockSnapmirrorClient)

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		provider := &OntapRestProvider{}
		expectedError := errors.New("entry not found")

		snapmirrorErrorIntervalRetrySeconds = 0

		doCleanupSvmPeering = func(provider *OntapRestProvider, params *DeleteVolumeReplicationParams) error {
			return nil
		}
		defer func() {
			doCleanupSvmPeering = cleanupSvmPeering
		}()

		mockClient.On("Snapmirror").Return(mockSnapmirrorClient)
		mockSnapmirrorClient.On("SnapmirrorRelationshipListDestinations", listDestinationParams).Return(destinations, nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipRelease", releaseParams).Return(false, nil, expectedError).Times(1)
		volumeReplication, err := provider.ReleaseVolumeReplication(&volumeReplicationCreateParams)
		assert.NoError(tt, err)
		assert.NotEmpty(tt, volumeReplication)
	})
	t.Run("WhenReleaseJobReturnsAnotherOperationError", func(tt *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockSnapmirrorClient := new(ontaprest.MockSnapmirrorClient)

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		provider := &OntapRestProvider{}
		expectedError := errors.New("Another SnapMirror operation is in progress")

		snapmirrorErrorIntervalRetrySeconds = 0

		doCleanupSvmPeering = func(provider *OntapRestProvider, params *DeleteVolumeReplicationParams) error {
			return nil
		}
		defer func() {
			doCleanupSvmPeering = cleanupSvmPeering
		}()

		mockClient.On("Snapmirror").Return(mockSnapmirrorClient)
		mockSnapmirrorClient.On("SnapmirrorRelationshipListDestinations", listDestinationParams).Return(destinations, nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipRelease", releaseParams).Return(false, jobAccepted, nil).Times(1)
		mockClient.On("Poll", jobAccepted.JobUUID).Return(expectedError).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipRelease", releaseParams).Return(false, jobAccepted, nil).Times(1)
		mockClient.On("Poll", jobAccepted.JobUUID).Return(nil).Times(1)
		volumeReplication, err := provider.ReleaseVolumeReplication(&volumeReplicationCreateParams)
		assert.NoError(tt, err)
		assert.NotEmpty(tt, volumeReplication)
	})
	t.Run("WhenCleanupSVMPeeringFails", func(tt *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockSnapmirrorClient := new(ontaprest.MockSnapmirrorClient)

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		provider := &OntapRestProvider{Logger: log.NewLogger().(*log.Slogger)}

		doCleanupSvmPeering = func(provider *OntapRestProvider, params *DeleteVolumeReplicationParams) error {
			return errors.New("SVM cleanup error")
		}
		defer func() {
			doCleanupSvmPeering = cleanupSvmPeering
		}()

		mockClient.On("Snapmirror").Return(mockSnapmirrorClient)
		mockSnapmirrorClient.On("SnapmirrorRelationshipListDestinations", listDestinationParams).Return(destinations, nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipRelease", releaseParams).Return(false, jobAccepted, nil).Times(1)
		mockClient.On("Poll", jobAccepted.JobUUID).Return(nil).Times(1)
		volumeReplication, err := provider.ReleaseVolumeReplication(&volumeReplicationCreateParams)
		assert.Equal(tt, errors.New("SVM cleanup error"), err)
		assert.Empty(tt, volumeReplication)
	})
	t.Run("WhenReplicationTypeIsNotExternal", func(tt *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockSnapmirrorClient := new(ontaprest.MockSnapmirrorClient)

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		provider := &OntapRestProvider{}

		mockClient.On("Snapmirror").Return(mockSnapmirrorClient)
		mockSnapmirrorClient.On("SnapmirrorRelationshipListDestinations", listDestinationParams).Return(destinations, nil).Times(1)

		volumeReplicationCreateParams.VolumeReplication.ClusterPeerID = nillable.ToPointer(uint64(0))
		svmPeeringCleanupCalled := false

		doCleanupSvmPeering = func(provider *OntapRestProvider, params *DeleteVolumeReplicationParams) error {
			svmPeeringCleanupCalled = true
			return nil
		}
		defer func() {
			doCleanupSvmPeering = cleanupSvmPeering
		}()
		volumeReplicationCreateParams.VolumeReplication.ReplicationType = ""
		mockClient.On("Snapmirror").Return(mockSnapmirrorClient)
		mockSnapmirrorClient.On("SnapmirrorRelationshipList", &ontaprest.SnapmirrorRelationshipListParams{}).Return(destinations, nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipRelease", releaseParams).Return(false, jobAccepted, nil).Times(1)
		mockClient.On("Poll", jobAccepted.JobUUID).Return(nil).Times(1)
		volumeReplication, err := provider.ReleaseVolumeReplication(&volumeReplicationCreateParams)
		assert.NoError(tt, err)
		assert.NotEmpty(tt, volumeReplication)
		assert.Equal(tt, false, svmPeeringCleanupCalled, "SVM Peering Cleanup should not have been called")
	})
	t.Run("WhenSuccessful", func(tt *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockSnapmirrorClient := new(ontaprest.MockSnapmirrorClient)
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		provider := &OntapRestProvider{}

		doCleanupSvmPeering = func(provider *OntapRestProvider, params *DeleteVolumeReplicationParams) error {
			return nil
		}
		defer func() {
			doCleanupSvmPeering = cleanupSvmPeering
		}()

		mockClient.On("Snapmirror").Return(mockSnapmirrorClient)
		mockSnapmirrorClient.On("SnapmirrorRelationshipListDestinations", listDestinationParams).Return(destinations, nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipRelease", releaseParams).Return(false, jobAccepted, nil).Times(1)
		mockClient.On("Poll", jobAccepted.JobUUID).Return(nil).Times(1)

		volumeReplication, err := provider.ReleaseVolumeReplication(&volumeReplicationCreateParams)
		assert.NoError(tt, err)
		assert.NotEmpty(tt, volumeReplication)
	})
	t.Run("WhenSuccessfulHybridRep", func(tt *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockSnapmirrorClient := new(ontaprest.MockSnapmirrorClient)

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		provider := &OntapRestProvider{}

		doCleanupSvmPeering = func(provider *OntapRestProvider, params *DeleteVolumeReplicationParams) error {
			return nil
		}
		defer func() {
			doCleanupSvmPeering = cleanupSvmPeering
		}()

		mockClient.On("Snapmirror").Return(mockSnapmirrorClient)
		mockSnapmirrorClient.On("SnapmirrorRelationshipListDestinations", listDestinationParams).Return(destinations, nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipRelease", releaseParams).Return(false, jobAccepted, nil).Times(1)
		mockClient.On("Poll", jobAccepted.JobUUID).Return(nil).Times(1)

		volumeReplication, err := provider.ReleaseVolumeReplication(&volumeReplicationCreateParams)
		assert.NoError(tt, err)
		assert.NotEmpty(tt, volumeReplication)
	})
}

func TestGetReplicationDetails(t *testing.T) {
	t.Run("WhenGetReplicationDetailsReturnsError", func(tt *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockSnapmirrorClient := new(ontaprest.MockSnapmirrorClient)
		defer func() {
			getOntapClientFunc = getOntapClient
		}()

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		provider := &OntapRestProvider{}

		volRep := &VolumeReplication{
			DestinationVolumeName: "yavin",
			DestinationSVMName:    "rebel-base",
			ExternalUUID:          "gold-team",
		}

		expectedError := errors.New("some error")

		mockClient.On("Snapmirror").Return(mockSnapmirrorClient)
		mockSnapmirrorClient.On("SnapmirrorGetPriv", context.TODO(), "rebel-base:yavin", "gold-team", (*string)(nil)).Return(nil, expectedError).Times(1)
		res, err := provider.GetReplicationDetails(context.TODO(), volRep)
		assert.EqualError(tt, err, expectedError.Error())
		assert.Nil(tt, res)
	})
	t.Run("WhenGetReplicationDetailsReturnsSuccessful", func(tt *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockSnapmirrorClient := new(ontaprest.MockSnapmirrorClient)
		defer func() {
			getOntapClientFunc = getOntapClient
		}()

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		provider := &OntapRestProvider{}

		volRep := &VolumeReplication{
			DestinationVolumeName: "yavin",
			DestinationSVMName:    "rebel-base",
			ExternalUUID:          "gold-team",
		}

		data1 := &models2.Data{
			State:                SnapmirrorStateMirrored,
			TotalTransferBytes:   int64(10000),
			LastTransferSize:     int64(5000),
			LastTransferDuration: "PT1D23H45M59S",
		}

		expectedResp := snapmirror.SnapmirrorGetOK{
			Payload: &models2.SnapmirrorResponse{
				Records: []*models2.Data{data1},
			},
		}

		mockClient.On("Snapmirror").Return(mockSnapmirrorClient)
		mockSnapmirrorClient.On("SnapmirrorGetPriv", context.TODO(), "rebel-base:yavin", "gold-team", (*string)(nil)).Return(&expectedResp, nil).Times(1)
		res, err := provider.GetReplicationDetails(context.TODO(), volRep)
		assert.NoError(tt, err)
		assert.NotNil(tt, res)
		assert.Equal(tt, res.MirrorState, SnapmirrorStateMirrored)
		assert.Equal(tt, res.TotalTransferBytes, int64(10000))
		assert.Equal(tt, res.LastTransferSize, int64(5000))
		assert.Equal(tt, res.LastTransferDuration, int64(171959))
	})
	t.Run("WhenNillableParseStringTimeTotimeTimeReturnsError", func(tt *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockSnapmirrorClient := new(ontaprest.MockSnapmirrorClient)
		defer func() {
			getOntapClientFunc = getOntapClient
			nillableParseStringTimeTotimeTime = nillable.ParseStringTimeTotimeTime
		}()

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		nillableParseStringTimeTotimeTime = func(s string) (*time.Time, error) {
			return nil, errors.New("error parsing time")
		}
		provider := &OntapRestProvider{}

		volRep := &VolumeReplication{
			DestinationVolumeName: "yavin",
			DestinationSVMName:    "rebel-base",
			ExternalUUID:          "gold-team",
		}

		data1 := &models2.Data{
			State:                SnapmirrorStateMirrored,
			TotalTransferBytes:   int64(10000),
			LastTransferSize:     int64(5000),
			LastTransferDuration: "PT1D23H45M59S",
		}

		expectedResp := snapmirror.SnapmirrorGetOK{
			Payload: &models2.SnapmirrorResponse{
				Records: []*models2.Data{data1},
			},
		}

		mockClient.On("Snapmirror").Return(mockSnapmirrorClient)
		mockSnapmirrorClient.On("SnapmirrorGetPriv", context.TODO(), "rebel-base:yavin", "gold-team", (*string)(nil)).Return(&expectedResp, nil).Times(1)
		res, err := provider.GetReplicationDetails(context.TODO(), volRep)
		assert.EqualError(tt, err, "error parsing time")
		assert.Nil(tt, res)
	})
	t.Run("WhenNillableParseStringTimeTotimeTimeReturnsErrorForProgressLastUpdated", func(tt *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockSnapmirrorClient := new(ontaprest.MockSnapmirrorClient)
		callCount := 0
		defer func() {
			getOntapClientFunc = getOntapClient
			nillableParseStringTimeTotimeTime = nillable.ParseStringTimeTotimeTime
		}()

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		nillableParseStringTimeTotimeTime = func(s string) (*time.Time, error) {
			callCount++
			if callCount == 2 { // Second call is for ProgressLastUpdated
				return nil, errors.New("error parsing progress time")
			}
			return &time.Time{}, nil // First call succeeds
		}
		provider := &OntapRestProvider{}

		volRep := &VolumeReplication{
			DestinationVolumeName: "yavin",
			DestinationSVMName:    "rebel-base",
			ExternalUUID:          "gold-team",
		}

		data1 := &models2.Data{
			State:                SnapmirrorStateMirrored,
			TotalTransferBytes:   int64(10000),
			LastTransferSize:     int64(5000),
			LastTransferDuration: "PT1D23H45M59S",
		}

		expectedResp := snapmirror.SnapmirrorGetOK{
			Payload: &models2.SnapmirrorResponse{
				Records: []*models2.Data{data1},
			},
		}

		mockClient.On("Snapmirror").Return(mockSnapmirrorClient)
		mockSnapmirrorClient.On("SnapmirrorGetPriv", context.TODO(), "rebel-base:yavin", "gold-team", (*string)(nil)).Return(&expectedResp, nil).Times(1)
		res, err := provider.GetReplicationDetails(context.TODO(), volRep)
		assert.EqualError(tt, err, "error parsing progress time")
		assert.Nil(tt, res)
	})
	t.Run("WhenLastTransferDurationIsEmpty", func(tt *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockSnapmirrorClient := new(ontaprest.MockSnapmirrorClient)
		defer func() {
			getOntapClientFunc = getOntapClient
		}()

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		provider := &OntapRestProvider{}

		volRep := &VolumeReplication{
			DestinationVolumeName: "yavin",
			DestinationSVMName:    "rebel-base",
			ExternalUUID:          "gold-team",
		}

		data1 := &models2.Data{
			State:                SnapmirrorStateMirrored,
			TotalTransferBytes:   int64(10000),
			LastTransferSize:     int64(5000),
			LastTransferDuration: "",      // Empty string to test the condition
			LagTime:              "PT30S", // Non-empty to ensure other parsing works
		}

		expectedResp := snapmirror.SnapmirrorGetOK{
			Payload: &models2.SnapmirrorResponse{
				Records: []*models2.Data{data1},
			},
		}

		mockClient.On("Snapmirror").Return(mockSnapmirrorClient)
		mockSnapmirrorClient.On("SnapmirrorGetPriv", context.TODO(), "rebel-base:yavin", "gold-team", (*string)(nil)).Return(&expectedResp, nil).Times(1)
		res, err := provider.GetReplicationDetails(context.TODO(), volRep)
		assert.NoError(tt, err)
		assert.NotNil(tt, res)
		assert.Equal(tt, res.MirrorState, SnapmirrorStateMirrored)
		assert.Equal(tt, res.TotalTransferBytes, int64(10000))
		assert.Equal(tt, res.LastTransferSize, int64(5000))
		assert.Equal(tt, res.LastTransferDuration, int64(0)) // Should remain 0 when empty
		assert.Equal(tt, res.LagTime, int64(30))             // Should be parsed from "PT30S"
	})
}

func TestGetVolumeReplication(t *testing.T) {
	t.Run("ReturnsVolumeReplicationWhenSnapmirrorGetSucceeds", func(tt *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockSnapmirrorClient := new(ontaprest.MockSnapmirrorClient)
		originalgetOntapClientFunc := getOntapClientFunc
		defer func() {
			getOntapClientFunc = originalgetOntapClientFunc
		}()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		provider := &OntapRestProvider{}
		replication := &VolumeReplication{ExternalUUID: "valid-uuid"}

		expectedSnapmirror := &ontaprest.SnapmirrorRelationship{
			SnapmirrorRelationship: models.SnapmirrorRelationship{
				Policy:  &models.SnapmirrorRelationshipInlinePolicy{Name: nillable.ToPointer("policy")},
				UUID:    nillable.ToPointer(strfmt.UUID("uuid")),
				Healthy: nillable.GetBoolPtr(true),
			},
		}

		mockClient.On("Snapmirror").Return(mockSnapmirrorClient)
		mockSnapmirrorClient.On("SnapmirrorRelationshipGet", &ontaprest.SnapmirrorRelationshipGetParams{UUID: replication.ExternalUUID}).Return(expectedSnapmirror, nil).Times(1)

		result, err := provider.GetVolumeReplication(replication)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		mockSnapmirrorClient.AssertExpectations(tt)
	})
	t.Run("ReturnsErrorWhenSnapmirrorGetFails", func(tt *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockSnapmirrorClient := new(ontaprest.MockSnapmirrorClient)
		originalgetOntapClientFunc := getOntapClientFunc
		defer func() {
			getOntapClientFunc = originalgetOntapClientFunc
		}()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		provider := &OntapRestProvider{}
		replication := &VolumeReplication{ExternalUUID: "invalid-uuid"}

		expectedError := errors.New("snapmirror get failed")

		mockClient.On("Snapmirror").Return(mockSnapmirrorClient)
		mockSnapmirrorClient.On("SnapmirrorRelationshipGet", &ontaprest.SnapmirrorRelationshipGetParams{UUID: replication.ExternalUUID}).Return(nil, expectedError).Times(1)

		result, err := provider.GetVolumeReplication(replication)

		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, expectedError, err)
		mockSnapmirrorClient.AssertExpectations(tt)
	})
}

func TestResyncVolumeReplicationWithGSDisabled(t *testing.T) {
	relationshipID := "volumereplication-1"

	srcVolumeUUID := "src-uuid"
	dstVolumeUUID := "dst-uuid"

	srcVolumeName := "srcvol"
	dstVolumeName := "dstvol"

	params := &VolumeReplication{
		MirrorState:    SnapmirrorStateBroken,
		RelationshipID: relationshipID,
		Volume: &Volume{
			// VolumeID:      dstVolumeUUID,
			// CreationToken: dstVolumeName,
			ExternalUUID: srcVolumeUUID,
		},
	}

	srcVolume := ontaprest.Volume{
		Volume: models.Volume{
			UUID: &srcVolumeUUID,
			Type: nillable.ToPointer(models.VolumeTypeRw),
			Name: nillable.ToPointer(srcVolumeName),
			Svm: &models.VolumeInlineSvm{
				Name: nillable.ToPointer("srcsvm"),
				UUID: nillable.ToPointer("srcsvm-uuid"),
			},
		},
	}
	dstVolume := ontaprest.Volume{
		Volume: models.Volume{
			UUID: &dstVolumeUUID,
			Type: nillable.ToPointer(models.VolumeTypeDp),
			Name: nillable.ToPointer(dstVolumeName),
			Svm: &models.VolumeInlineSvm{
				Name: nillable.ToPointer("dstsvm"),
			},
		},
	}
	sourcePath := nillable.GetString(srcVolume.Svm.Name, "") + ":" + nillable.GetString(srcVolume.Name, "")
	destinationPath := nillable.GetString(dstVolume.Svm.Name, "") + ":" + nillable.GetString(dstVolume.Name, "")
	snapmirror := &ontaprest.SnapmirrorRelationship{
		SnapmirrorRelationship: models.SnapmirrorRelationship{
			Source:                &models.SnapmirrorSourceEndpoint{Path: &sourcePath},
			Destination:           &models.SnapmirrorEndpoint{Path: &destinationPath},
			UUID:                  nillable.ToPointer(strfmt.UUID("uuid")),
			State:                 nillable.ToPointer(models.SnapmirrorRelationshipStateSnapmirrored),
			TotalTransferDuration: nillable.ToPointer("PT2M34S"),
			Transfer: &models.SnapmirrorRelationshipInlineTransfer{
				TotalDuration:    nillable.ToPointer("PT4M50S"),
				State:            nillable.ToPointer(models.SnapmirrorRelationshipInlineTransferStateSuccess),
				BytesTransferred: nillable.ToPointer(int64(500)),
				EndTime:          nillable.ToPointer(strfmt.DateTime(time.Now())),
			},
			Policy: &models.SnapmirrorRelationshipInlinePolicy{
				Name: nillable.ToPointer("policy"),
			},
			LagTime: nillable.ToPointer("PT20S"),
			SnapmirrorRelationshipInlineUnhealthyReason: []*models.SnapmirrorError{
				{
					Message: nillable.ToPointer("error"),
				},
			},
			TransferSchedule: &models.SnapmirrorRelationshipInlineTransferSchedule{
				Name: nillable.ToPointer("le schedule"),
			},
			Healthy:            nillable.GetBoolPtr(true),
			TotalTransferBytes: nillable.ToPointer(int64(1000)),
		},
	}
	setupMocks := func(tt *testing.T) (provider *OntapRestProvider, mockClient *ontaprest.MockRESTClient, mockSnapmirrorClient *ontaprest.MockSnapmirrorClient, mockStorageClient *ontaprest.MockStorageClient, mockSANClient *ontaprest.MockSANClient) {
		mockClient = new(ontaprest.MockRESTClient)
		mockStorageClient = new(ontaprest.MockStorageClient)
		mockSnapmirrorClient = new(ontaprest.MockSnapmirrorClient)
		mockSANClient = new(ontaprest.MockSANClient)
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		provider = &OntapRestProvider{}
		return
	}

	t.Run("WhenGetSnapmirrorReturnsError", func(tt *testing.T) {
		provider, mockClient, mockSnapmirrorClient, _, _ := setupMocks(tt)

		expectedError := errors.New("Faceplanting")

		getParams := &ontaprest.SnapmirrorRelationshipGetParams{UUID: relationshipID}

		mockClient.On("Snapmirror").Return(mockSnapmirrorClient)
		mockSnapmirrorClient.On("SnapmirrorRelationshipGet", getParams).Return(nil, expectedError).Times(1)
		volumeReplication, err := provider.ResyncVolumeReplication(params)

		assert.Equal(tt, expectedError, err)
		assert.Empty(tt, volumeReplication)
	})
	t.Run("WhenSnapmirrorIsNotInStateBroken", func(tt *testing.T) {
		provider, mockClient, mockSnapmirrorClient, _, _ := setupMocks(tt)

		expectedError := errors.NewConflictErr("Cannot perform a resync operation in this mirror state")

		getParams := &ontaprest.SnapmirrorRelationshipGetParams{UUID: relationshipID}

		params.MirrorState = SnapmirrorStateMirrored

		defer func() {
			params.MirrorState = SnapmirrorStateBroken
		}()

		mockClient.On("Snapmirror").Return(mockSnapmirrorClient)
		mockSnapmirrorClient.On("SnapmirrorRelationshipGet", getParams).Return(snapmirror, nil).Times(1)
		volumeReplication, err := provider.ResyncVolumeReplication(params)

		assert.Equal(tt, expectedError, err)
		assert.Empty(tt, volumeReplication)
	})
	t.Run("WhenGettingVolumeReturnsError", func(tt *testing.T) {
		provider, mockClient, mockSnapmirrorClient, mockStorageClient, _ := setupMocks(tt)

		expectedError := errors.New("Faceplanting")

		snapmirorGetParams := &ontaprest.SnapmirrorRelationshipGetParams{UUID: relationshipID}
		volumeGetParams := &ontaprest.VolumeGetParams{UUID: *srcVolume.UUID}

		mockClient.On("Snapmirror").Return(mockSnapmirrorClient)
		mockClient.On("Storage").Return(mockStorageClient)
		mockSnapmirrorClient.On("SnapmirrorRelationshipGet", snapmirorGetParams).Return(snapmirror, nil).Times(1)
		mockStorageClient.On("VolumeGet", volumeGetParams).Return(nil, expectedError).Times(1)

		volumeReplication, err := provider.ResyncVolumeReplication(params)

		assert.Equal(tt, expectedError, err)
		assert.Empty(tt, volumeReplication)
	})
	t.Run("WhenResyncOrInitializeOrResumeSnapmirrorReturnsError", func(tt *testing.T) {
		provider, mockClient, mockSnapmirrorClient, mockStorageClient, _ := setupMocks(tt)

		expectedError := errors.New("Faceplanting")

		snapmirorGetParams := &ontaprest.SnapmirrorRelationshipGetParams{UUID: relationshipID}
		volumeGetParams := &ontaprest.VolumeGetParams{UUID: *srcVolume.UUID}
		snapmirorResumeParams := snapmirror.UUID

		mockClient.On("Snapmirror").Return(mockSnapmirrorClient)
		mockClient.On("Storage").Return(mockStorageClient)
		mockSnapmirrorClient.On("SnapmirrorRelationshipGet", snapmirorGetParams).Return(snapmirror, nil).Times(1)
		mockStorageClient.On("VolumeGet", volumeGetParams).Return(&srcVolume, nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipResyncOrInitializeOrResume", snapmirorResumeParams.String()).Return(nil, nil, expectedError).Times(1)
		volumeReplication, err := provider.ResyncVolumeReplication(params)

		assert.Equal(tt, expectedError, err)
		assert.Empty(tt, volumeReplication)
	})
	t.Run("WhenGettingSnapmirrorInsideRetryLoopReturnsError", func(tt *testing.T) {
		provider, mockClient, mockSnapmirrorClient, mockStorageClient, _ := setupMocks(tt)

		expectedError := errors.New("Faceplanting")

		snapmirorGetParams := &ontaprest.SnapmirrorRelationshipGetParams{UUID: relationshipID}
		volumeGetParams := &ontaprest.VolumeGetParams{UUID: *srcVolume.UUID}
		snapmirorResumeParams := snapmirror.UUID

		mockClient.On("Snapmirror").Return(mockSnapmirrorClient)
		mockClient.On("Storage").Return(mockStorageClient)
		mockSnapmirrorClient.On("SnapmirrorRelationshipGet", snapmirorGetParams).Return(snapmirror, nil).Times(1)
		mockStorageClient.On("VolumeGet", volumeGetParams).Return(&srcVolume, nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipResyncOrInitializeOrResume", snapmirorResumeParams.String()).Return(snapmirror, nil, nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipGet", snapmirorGetParams).Return(nil, expectedError).Times(1)
		volumeReplication, err := provider.ResyncVolumeReplication(params)

		assert.Equal(tt, expectedError, err)
		assert.Empty(tt, volumeReplication)
	})
	t.Run("WhenUnmountVolumeReturnsError", func(tt *testing.T) {
		provider, mockClient, mockSnapmirrorClient, mockStorageClient, _ := setupMocks(tt)

		expectedError := errors.New("Faceplanting")

		snapmirorGetParams := &ontaprest.SnapmirrorRelationshipGetParams{UUID: relationshipID}
		volumeGetParams := &ontaprest.VolumeGetParams{UUID: *srcVolume.UUID}
		snapmirorResumeParams := snapmirror.UUID
		doUnmountVolume = func(provider *OntapRestProvider, volume *ontaprest.Volume, volRep *VolumeReplication) error {
			return expectedError
		}

		mockClient.On("Snapmirror").Return(mockSnapmirrorClient)
		mockClient.On("Storage").Return(mockStorageClient)
		mockSnapmirrorClient.On("SnapmirrorRelationshipGet", snapmirorGetParams).Return(snapmirror, nil).Times(1)
		mockStorageClient.On("VolumeGet", volumeGetParams).Return(&srcVolume, nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipResyncOrInitializeOrResume", snapmirorResumeParams.String()).Return(snapmirror, nil, nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipGet", snapmirorGetParams).Return(snapmirror, nil).Times(1)
		volumeReplication, err := provider.ResyncVolumeReplication(params)

		assert.Equal(tt, expectedError, err)
		assert.Empty(tt, volumeReplication)
	})
	t.Run("WhenFinallyGettingSnapmirrorReturnsError", func(tt *testing.T) {
		provider, mockClient, mockSnapmirrorClient, mockStorageClient, _ := setupMocks(tt)

		expectedError := errors.New("Faceplanting")

		snapmirorGetParams := &ontaprest.SnapmirrorRelationshipGetParams{UUID: relationshipID}
		volumeGetParams := &ontaprest.VolumeGetParams{UUID: *srcVolume.UUID}
		snapmirorResumeParams := snapmirror.UUID

		// unmountJobAccepted := &ontaprest.JobAccepted{JobUUID: "job-uuid"}
		doUnmountVolume = func(provider *OntapRestProvider, volume *ontaprest.Volume, volRep *VolumeReplication) error {
			return nil
		}

		mockClient.On("Snapmirror").Return(mockSnapmirrorClient)
		mockClient.On("Storage").Return(mockStorageClient)
		mockSnapmirrorClient.On("SnapmirrorRelationshipGet", snapmirorGetParams).Return(snapmirror, nil).Times(1)
		mockStorageClient.On("VolumeGet", volumeGetParams).Return(&srcVolume, nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipResyncOrInitializeOrResume", snapmirorResumeParams.String()).Return(snapmirror, nil, nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipGet", snapmirorGetParams).Return(snapmirror, nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipGet", snapmirorGetParams).Return(nil, expectedError).Times(1)
		volumeReplication, err := provider.ResyncVolumeReplication(params)

		assert.Equal(tt, expectedError, err)
		assert.Empty(tt, volumeReplication)
	})
	t.Run("WhenWaitingForJobReturnsError", func(tt *testing.T) {
		provider, mockClient, mockSnapmirrorClient, mockStorageClient, _ := setupMocks(tt)

		expectedError := errors.New("Faceplanting")

		snapmirorGetParams := &ontaprest.SnapmirrorRelationshipGetParams{UUID: relationshipID}
		volumeGetParams := &ontaprest.VolumeGetParams{UUID: *srcVolume.UUID}
		snapmirorResumeParams := snapmirror.UUID
		resyncJobAccepted := &ontaprest.JobAccepted{JobUUID: "job-uuid"}

		mockClient.On("Snapmirror").Return(mockSnapmirrorClient)
		mockClient.On("Storage").Return(mockStorageClient)
		mockSnapmirrorClient.On("SnapmirrorRelationshipGet", snapmirorGetParams).Return(snapmirror, nil).Times(1)
		mockStorageClient.On("VolumeGet", volumeGetParams).Return(&srcVolume, nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipResyncOrInitializeOrResume", snapmirorResumeParams.String()).Return(nil, resyncJobAccepted, nil).Times(1)
		mockClient.On("Poll", resyncJobAccepted.JobUUID).Return(expectedError).Times(1)
		volumeReplication, err := provider.ResyncVolumeReplication(params)

		assert.Equal(tt, expectedError, err)
		assert.Empty(tt, volumeReplication)
	})
	t.Run("WhenSuccessful", func(tt *testing.T) {
		provider, mockClient, mockSnapmirrorClient, mockStorageClient, _ := setupMocks(tt)

		snapmirorGetParams := &ontaprest.SnapmirrorRelationshipGetParams{UUID: relationshipID}
		volumeGetParams := &ontaprest.VolumeGetParams{UUID: *srcVolume.UUID}
		snapmirorResumeParams := snapmirror.UUID

		mockClient.On("Snapmirror").Return(mockSnapmirrorClient)
		mockClient.On("Storage").Return(mockStorageClient)
		mockSnapmirrorClient.On("SnapmirrorRelationshipGet", snapmirorGetParams).Return(snapmirror, nil).Times(1)
		mockStorageClient.On("VolumeGet", volumeGetParams).Return(&srcVolume, nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipResyncOrInitializeOrResume", snapmirorResumeParams.String()).Return(snapmirror, nil, nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipGet", snapmirorGetParams).Return(snapmirror, nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipGet", snapmirorGetParams).Return(snapmirror, nil).Times(1)

		volumeReplication, err := provider.ResyncVolumeReplication(params)

		assert.NoError(tt, err)
		assert.NotNil(tt, volumeReplication)
	})
	t.Run("WhenEnsureBillablePersist", func(tt *testing.T) {
		provider, mockClient, mockSnapmirrorClient, mockStorageClient, _ := setupMocks(tt)

		snapmirorGetParams := &ontaprest.SnapmirrorRelationshipGetParams{UUID: relationshipID}
		volumeGetParams := &ontaprest.VolumeGetParams{UUID: *srcVolume.UUID}
		snapmirorResumeParams := snapmirror.UUID

		mockClient.On("Snapmirror").Return(mockSnapmirrorClient)
		mockClient.On("Storage").Return(mockStorageClient)
		mockSnapmirrorClient.On("SnapmirrorRelationshipGet", snapmirorGetParams).Return(snapmirror, nil).Times(1)
		mockStorageClient.On("VolumeGet", volumeGetParams).Return(&srcVolume, nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipResyncOrInitializeOrResume", snapmirorResumeParams.String()).Return(snapmirror, nil, nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipGet", snapmirorGetParams).Return(snapmirror, nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipGet", snapmirorGetParams).Return(snapmirror, nil).Times(1)

		volumeReplication, err := provider.ResyncVolumeReplication(params)

		assert.NoError(tt, err)
		assert.NotNil(tt, volumeReplication)
	})
}

func TestListSnapmirrorDestinations(t *testing.T) {
	originalgetOntapClientFunc := getOntapClientFunc
	defer func() {
		getOntapClientFunc = originalgetOntapClientFunc
	}()
	t.Run("WhenSnapmirrorRelationshipListDestinationsReturnsError", func(tt *testing.T) {
		mrc := new(ontaprest.MockRESTClient)
		msmc := new(ontaprest.MockSnapmirrorClient)
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mrc, nil
		}
		prov := &OntapRestProvider{}

		expectedError := errors.New("some error")

		mrc.On("Snapmirror").Return(msmc)
		msmc.On("SnapmirrorRelationshipListDestinations", (*ontaprest.SnapmirrorRelationshipListDestinationsParams)(nil)).Return(nil, expectedError).Times(1)
		res, err := prov.ListSnapmirrorDestinations(nil)
		assert.EqualError(tt, err, expectedError.Error())
		assert.Nil(tt, res)
	})
	t.Run("WhenSnapmirrorDestinationsExistSuccessful", func(tt *testing.T) {
		mrc := new(ontaprest.MockRESTClient)
		msmc := new(ontaprest.MockSnapmirrorClient)
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mrc, nil
		}
		prov := &OntapRestProvider{}
		uuid := strfmt.UUID("1")
		destinations := []*ontaprest.SnapmirrorRelationship{
			{
				SnapmirrorRelationship: models.SnapmirrorRelationship{
					UUID:  &uuid,
					State: nillable.ToPointer("initialized"),
					Source: &models.SnapmirrorSourceEndpoint{
						Path: nillable.ToPointer("first-svm:first-volume"),
						Svm:  &models.SnapmirrorSourceEndpointInlineSvm{UUID: nillable.ToPointer("svm-1"), Name: nillable.ToPointer("first-svm")},
					},
					Destination: &models.SnapmirrorEndpoint{
						Path: nillable.ToPointer("first-svm:first-volume"),
						Svm:  &models.SnapmirrorEndpointInlineSvm{UUID: nillable.ToPointer("svm-1"), Name: nillable.ToPointer("first-svm")},
					},
				},
			},
		}

		mrc.On("Snapmirror").Return(msmc)
		msmc.On("SnapmirrorRelationshipListDestinations", (*ontaprest.SnapmirrorRelationshipListDestinationsParams)(nil)).Return(destinations, nil).Times(1)
		res, err := prov.ListSnapmirrorDestinations(nil)
		assert.NoError(tt, err)
		assert.Len(tt, res, 1)
		assert.Equal(tt, res[0].SourcePath, *destinations[0].Source.Path)
		assert.Equal(tt, res[0].DestinationSVMName, *destinations[0].Destination.Svm.Name)
		assert.Equal(tt, uuid.String(), res[0].RelationshipUUID)
		assert.Equal(tt, *destinations[0].Destination.Path, res[0].DestinationPath)
		assert.Equal(tt, *destinations[0].Source.Svm.Name, res[0].SourceSVMName)

		mrc.AssertExpectations(tt)
		msmc.AssertExpectations(tt)
	})
	t.Run("WhenNoSnapmirrorDestinationsExistSuccessful", func(tt *testing.T) {
		mrc := new(ontaprest.MockRESTClient)
		msmc := new(ontaprest.MockSnapmirrorClient)
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mrc, nil
		}
		prov := &OntapRestProvider{}
		var destinations []*ontaprest.SnapmirrorRelationship

		mrc.On("Snapmirror").Return(msmc)
		msmc.On("SnapmirrorRelationshipListDestinations", (*ontaprest.SnapmirrorRelationshipListDestinationsParams)(nil)).Return(destinations, nil).Times(1)
		res, err := prov.ListSnapmirrorDestinations(nil)
		assert.NoError(tt, err)
		assert.NotNil(tt, res)
		assert.Len(tt, res, 0)
	})
	t.Run("ErrorWhenGetOntapClientFails", func(tt *testing.T) {
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return nil, errors.New("client creation failed")
		}
		prov := &OntapRestProvider{}

		res, err := prov.ListSnapmirrorDestinations(nil)

		assert.Error(tt, err)
		assert.Nil(tt, res)
		assert.Contains(tt, err.Error(), "client creation failed")
	})
	t.Run("SuccessWithMultipleDestinations", func(tt *testing.T) {
		mrc := new(ontaprest.MockRESTClient)
		msmc := new(ontaprest.MockSnapmirrorClient)
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mrc, nil
		}
		prov := &OntapRestProvider{}

		uuid1 := strfmt.UUID("1")
		uuid2 := strfmt.UUID("2")
		destinations := []*ontaprest.SnapmirrorRelationship{
			{
				SnapmirrorRelationship: models.SnapmirrorRelationship{
					UUID:  &uuid1,
					State: nillable.ToPointer("initialized"),
					Source: &models.SnapmirrorSourceEndpoint{
						Path: nillable.ToPointer("src-svm:src-volume-1"),
						Svm:  &models.SnapmirrorSourceEndpointInlineSvm{UUID: nillable.ToPointer("svm-1"), Name: nillable.ToPointer("src-svm")},
					},
					Destination: &models.SnapmirrorEndpoint{
						Path: nillable.ToPointer("dst-svm:dst-volume-1"),
						Svm:  &models.SnapmirrorEndpointInlineSvm{UUID: nillable.ToPointer("svm-2"), Name: nillable.ToPointer("dst-svm")},
					},
				},
			},
			{
				SnapmirrorRelationship: models.SnapmirrorRelationship{
					UUID:  &uuid2,
					State: nillable.ToPointer("snapmirrored"),
					Source: &models.SnapmirrorSourceEndpoint{
						Path: nillable.ToPointer("src-svm:src-volume-2"),
						Svm:  &models.SnapmirrorSourceEndpointInlineSvm{UUID: nillable.ToPointer("svm-1"), Name: nillable.ToPointer("src-svm")},
					},
					Destination: &models.SnapmirrorEndpoint{
						Path: nillable.ToPointer("dst-svm:dst-volume-2"),
						Svm:  &models.SnapmirrorEndpointInlineSvm{UUID: nillable.ToPointer("svm-2"), Name: nillable.ToPointer("dst-svm")},
					},
				},
			},
		}

		mrc.On("Snapmirror").Return(msmc)
		msmc.On("SnapmirrorRelationshipListDestinations", (*ontaprest.SnapmirrorRelationshipListDestinationsParams)(nil)).Return(destinations, nil).Times(1)

		res, err := prov.ListSnapmirrorDestinations(nil)

		assert.NoError(tt, err)
		assert.NotNil(tt, res)
		assert.Len(tt, res, 2)

		// Check first destination
		assert.Equal(tt, uuid1.String(), res[0].RelationshipUUID)
		assert.Equal(tt, "src-svm:src-volume-1", res[0].SourcePath)
		assert.Equal(tt, "src-svm", res[0].SourceSVMName)
		assert.Equal(tt, "dst-svm:dst-volume-1", res[0].DestinationPath)
		assert.Equal(tt, "dst-svm", res[0].DestinationSVMName)

		// Check second destination
		assert.Equal(tt, uuid2.String(), res[1].RelationshipUUID)
		assert.Equal(tt, "src-svm:src-volume-2", res[1].SourcePath)
		assert.Equal(tt, "src-svm", res[1].SourceSVMName)
		assert.Equal(tt, "dst-svm:dst-volume-2", res[1].DestinationPath)
		assert.Equal(tt, "dst-svm", res[1].DestinationSVMName)

		mrc.AssertExpectations(tt)
		msmc.AssertExpectations(tt)
	})
	t.Run("SuccessWithNilSourceAndDestination", func(tt *testing.T) {
		mrc := new(ontaprest.MockRESTClient)
		msmc := new(ontaprest.MockSnapmirrorClient)
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mrc, nil
		}
		prov := &OntapRestProvider{}

		uuid := strfmt.UUID("1")
		destinations := []*ontaprest.SnapmirrorRelationship{
			{
				SnapmirrorRelationship: models.SnapmirrorRelationship{
					UUID:        &uuid,
					State:       nillable.ToPointer("initialized"),
					Source:      nil,
					Destination: nil,
				},
			},
		}

		mrc.On("Snapmirror").Return(msmc)
		msmc.On("SnapmirrorRelationshipListDestinations", (*ontaprest.SnapmirrorRelationshipListDestinationsParams)(nil)).Return(destinations, nil).Times(1)

		res, err := prov.ListSnapmirrorDestinations(nil)

		assert.NoError(tt, err)
		assert.NotNil(tt, res)
		assert.Len(tt, res, 1)
		assert.Equal(tt, uuid.String(), res[0].RelationshipUUID)
		assert.Equal(tt, "", res[0].SourcePath)
		assert.Equal(tt, "", res[0].SourceSVMName)
		assert.Equal(tt, "", res[0].DestinationPath)
		assert.Equal(tt, "", res[0].DestinationSVMName)

		mrc.AssertExpectations(tt)
		msmc.AssertExpectations(tt)
	})
	t.Run("SuccessWithNilSVM", func(tt *testing.T) {
		mrc := new(ontaprest.MockRESTClient)
		msmc := new(ontaprest.MockSnapmirrorClient)
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mrc, nil
		}
		prov := &OntapRestProvider{}

		uuid := strfmt.UUID("1")
		destinations := []*ontaprest.SnapmirrorRelationship{
			{
				SnapmirrorRelationship: models.SnapmirrorRelationship{
					UUID:  &uuid,
					State: nillable.ToPointer("initialized"),
					Source: &models.SnapmirrorSourceEndpoint{
						Path: nillable.ToPointer("src-svm:src-volume"),
						Svm:  nil,
					},
					Destination: &models.SnapmirrorEndpoint{
						Path: nillable.ToPointer("dst-svm:dst-volume"),
						Svm:  nil,
					},
				},
			},
		}

		mrc.On("Snapmirror").Return(msmc)
		msmc.On("SnapmirrorRelationshipListDestinations", (*ontaprest.SnapmirrorRelationshipListDestinationsParams)(nil)).Return(destinations, nil).Times(1)

		res, err := prov.ListSnapmirrorDestinations(nil)

		assert.NoError(tt, err)
		assert.NotNil(tt, res)
		assert.Len(tt, res, 1)
		assert.Equal(tt, uuid.String(), res[0].RelationshipUUID)
		assert.Equal(tt, "src-svm:src-volume", res[0].SourcePath)
		assert.Equal(tt, "", res[0].SourceSVMName)
		assert.Equal(tt, "dst-svm:dst-volume", res[0].DestinationPath)
		assert.Equal(tt, "", res[0].DestinationSVMName)

		mrc.AssertExpectations(tt)
		msmc.AssertExpectations(tt)
	})
	t.Run("SuccessWithParams", func(tt *testing.T) {
		mrc := new(ontaprest.MockRESTClient)
		msmc := new(ontaprest.MockSnapmirrorClient)
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mrc, nil
		}
		prov := &OntapRestProvider{}

		uuid := strfmt.UUID("1")
		destinations := []*ontaprest.SnapmirrorRelationship{
			{
				SnapmirrorRelationship: models.SnapmirrorRelationship{
					UUID:  &uuid,
					State: nillable.ToPointer("initialized"),
					Source: &models.SnapmirrorSourceEndpoint{
						Path: nillable.ToPointer("src-svm:src-volume"),
						Svm:  &models.SnapmirrorSourceEndpointInlineSvm{UUID: nillable.ToPointer("svm-1"), Name: nillable.ToPointer("src-svm")},
					},
					Destination: &models.SnapmirrorEndpoint{
						Path: nillable.ToPointer("dst-svm:dst-volume"),
						Svm:  &models.SnapmirrorEndpointInlineSvm{UUID: nillable.ToPointer("svm-2"), Name: nillable.ToPointer("dst-svm")},
					},
				},
			},
		}

		params := &ontaprest.SnapmirrorRelationshipListDestinationsParams{
			SourcePath: nillable.ToPointer("src-svm:src-volume"),
		}

		mrc.On("Snapmirror").Return(msmc)
		msmc.On("SnapmirrorRelationshipListDestinations", params).Return(destinations, nil).Times(1)

		res, err := prov.ListSnapmirrorDestinations(params)

		assert.NoError(tt, err)
		assert.NotNil(tt, res)
		assert.Len(tt, res, 1)
		assert.Equal(tt, uuid.String(), res[0].RelationshipUUID)
		assert.Equal(tt, "src-svm:src-volume", res[0].SourcePath)
		assert.Equal(tt, "src-svm", res[0].SourceSVMName)
		assert.Equal(tt, "dst-svm:dst-volume", res[0].DestinationPath)
		assert.Equal(tt, "dst-svm", res[0].DestinationSVMName)

		mrc.AssertExpectations(tt)
		msmc.AssertExpectations(tt)
	})
}

func TestCleanupSvmPeering(t *testing.T) {
	srcVolumeUUID := "src-uuid"
	dstVolumeUUID := "dst-uuid"
	srcVolume := ontaprest.Volume{
		Volume: models.Volume{
			UUID: &srcVolumeUUID,
			Type: nillable.ToPointer(models.VolumeTypeRw),
			Name: nillable.ToPointer("srcvol"),
			Svm: &models.VolumeInlineSvm{
				Name: nillable.ToPointer("srcSVM"),
			},
		},
	}
	dstVolume := ontaprest.Volume{
		Volume: models.Volume{
			UUID: &dstVolumeUUID,
			Type: nillable.ToPointer(models.VolumeTypeDp),
			Name: nillable.ToPointer("dstvol"),
			Svm: &models.VolumeInlineSvm{
				Name: nillable.ToPointer("dstSVM"),
			},
		},
	}
	sourcePath := nillable.GetString(srcVolume.Svm.Name, "") + ":" + nillable.GetString(srcVolume.Name, "")
	destinationPath := nillable.GetString(dstVolume.Svm.Name, "") + ":" + nillable.GetString(dstVolume.Name, "")

	snapmirror := &ontaprest.SnapmirrorRelationship{
		SnapmirrorRelationship: models.SnapmirrorRelationship{
			Source:                &models.SnapmirrorSourceEndpoint{Svm: &models.SnapmirrorSourceEndpointInlineSvm{Name: nillable.ToPointer("srcSVM")}, Path: &sourcePath},
			Destination:           &models.SnapmirrorEndpoint{Svm: &models.SnapmirrorEndpointInlineSvm{Name: nillable.ToPointer("dstSVM")}, Path: &destinationPath},
			UUID:                  nillable.ToPointer(strfmt.UUID("uuid")),
			State:                 nillable.ToPointer(models.SnapmirrorRelationshipStateSnapmirrored),
			TotalTransferDuration: nillable.ToPointer("PT2M34S"),
			Transfer: &models.SnapmirrorRelationshipInlineTransfer{
				TotalDuration:    nillable.ToPointer("PT4M50S"),
				State:            nillable.ToPointer(models.SnapmirrorRelationshipInlineTransferStateSuccess),
				BytesTransferred: nillable.ToPointer(int64(500)),
				EndTime:          nillable.ToPointer(strfmt.DateTime(time.Now())),
			},
			Policy: &models.SnapmirrorRelationshipInlinePolicy{
				Name: nillable.ToPointer("policy"),
			},
			LagTime: nillable.ToPointer("PT20S"),
			SnapmirrorRelationshipInlineUnhealthyReason: []*models.SnapmirrorError{
				{
					Message: nillable.ToPointer("error"),
				},
			},
			TransferSchedule: &models.SnapmirrorRelationshipInlineTransferSchedule{
				Name: nillable.ToPointer("le schedule"),
			},
			Healthy:            nillable.GetBoolPtr(true),
			TotalTransferBytes: nillable.ToPointer(int64(1000)),
		},
	}

	snapmirror2 := &ontaprest.SnapmirrorRelationship{
		SnapmirrorRelationship: models.SnapmirrorRelationship{
			Source:                &models.SnapmirrorSourceEndpoint{Svm: &models.SnapmirrorSourceEndpointInlineSvm{Name: nillable.ToPointer("dstSVM")}},
			Destination:           &models.SnapmirrorEndpoint{Svm: &models.SnapmirrorEndpointInlineSvm{Name: nillable.ToPointer("srcSVM")}},
			UUID:                  nillable.ToPointer(strfmt.UUID("uuid")),
			State:                 nillable.ToPointer(models.SnapmirrorRelationshipStateSnapmirrored),
			TotalTransferDuration: nillable.ToPointer("PT2M34S"),
			Transfer: &models.SnapmirrorRelationshipInlineTransfer{
				TotalDuration:    nillable.ToPointer("PT4M50S"),
				State:            nillable.ToPointer(models.SnapmirrorRelationshipInlineTransferStateSuccess),
				BytesTransferred: nillable.ToPointer(int64(500)),
				EndTime:          nillable.ToPointer(strfmt.DateTime(time.Now())),
			},
			Policy: &models.SnapmirrorRelationshipInlinePolicy{
				Name: nillable.ToPointer("policy"),
			},
			LagTime: nillable.ToPointer("PT20S"),
			SnapmirrorRelationshipInlineUnhealthyReason: []*models.SnapmirrorError{
				{
					Message: nillable.ToPointer("error"),
				},
			},
			TransferSchedule: &models.SnapmirrorRelationshipInlineTransferSchedule{
				Name: nillable.ToPointer("le schedule"),
			},
			Healthy:            nillable.GetBoolPtr(true),
			TotalTransferBytes: nillable.ToPointer(int64(1000)),
		},
	}
	snapmirrorList := []*ontaprest.SnapmirrorRelationship{snapmirror}
	snapmirrorList2 := []*ontaprest.SnapmirrorRelationship{snapmirror2}

	params := &DeleteVolumeReplicationParams{
		VolumeReplication: &VolumeReplication{
			UUID:               "",
			SourceSVMName:      "srcSVM",
			DestinationSVMName: "dstSVM",
		},
	}
	params1 := &DeleteVolumeReplicationParams{
		VolumeReplication: &VolumeReplication{
			UUID:               "",
			SourceSVMName:      "srcSVM",
			DestinationSVMName: "dstSVM",
			ClusterPeerID:      nillable.ToPointer(uint64(1)),
			EndpointType:       "src",
		},
	}
	originalgetOntapClientFunc := getOntapClientFunc
	defer func() {
		getOntapClientFunc = originalgetOntapClientFunc
	}()

	t.Run("WhenListingSnapMirrorsReturnsError", func(tt *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockSnapmirrorClient := new(ontaprest.MockSnapmirrorClient)

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		provider := &OntapRestProvider{}

		mockClient.On("Snapmirror").Return(mockSnapmirrorClient)
		mockSnapmirrorClient.On("SnapmirrorRelationshipList", &ontaprest.SnapmirrorRelationshipListParams{}).Return(nil, errors.New("Error listing snapmirrors")).Times(1)
		err := cleanupSvmPeering(provider, params)
		if err == nil {
			tt.Error("Error not returned")
		} else if err.Error() != "Error listing snapmirrors" {
			tt.Error("Wrong error returned")
		}
	})
	t.Run("WhenListingSnapMirrorDestinationsReturnsError", func(tt *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockSnapmirrorClient := new(ontaprest.MockSnapmirrorClient)

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		provider := &OntapRestProvider{}

		mockClient.On("Snapmirror").Return(mockSnapmirrorClient)
		mockSnapmirrorClient.On("SnapmirrorRelationshipList", &ontaprest.SnapmirrorRelationshipListParams{}).Return(nil, nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipListDestinations", &ontaprest.SnapmirrorRelationshipListDestinationsParams{}).Return(nil, errors.New("Error listing snapmirror destinations")).Times(1)
		err := cleanupSvmPeering(provider, params)
		if err == nil {
			tt.Error("Error not returned")
		} else if err.Error() != "Error listing snapmirror destinations" {
			tt.Error("Wrong error returned")
		}
	})
	t.Run("WhenShouldNotDeletePeerDueToExistingSnapmirror", func(tt *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockSnapmirrorClient := new(ontaprest.MockSnapmirrorClient)

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		provider := &OntapRestProvider{}

		mockClient.On("Snapmirror").Return(mockSnapmirrorClient)
		mockSnapmirrorClient.On("SnapmirrorRelationshipList", &ontaprest.SnapmirrorRelationshipListParams{}).Return(snapmirrorList, nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipListDestinations", &ontaprest.SnapmirrorRelationshipListDestinationsParams{}).Return(nil, nil).Times(1)

		err := cleanupSvmPeering(provider, params)
		if err != nil {
			tt.Error("Error unexpectedly returned")
		}
	})
	t.Run("WhenShouldNotDeletePeerDueToExistingSnapmirrorHybridRep", func(tt *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockSnapmirrorClient := new(ontaprest.MockSnapmirrorClient)

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		provider := &OntapRestProvider{}

		mockClient.On("Snapmirror").Return(mockSnapmirrorClient)
		mockSnapmirrorClient.On("SnapmirrorRelationshipList", &ontaprest.SnapmirrorRelationshipListParams{}).Return(snapmirrorList2, nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipListDestinations", &ontaprest.SnapmirrorRelationshipListDestinationsParams{}).Return(nil, nil).Times(1)

		params1.VolumeReplication.ReplicationType = string(datamodel.HybridReplicationParametersReplicationTypeONPREM)
		err := cleanupSvmPeering(provider, params1)
		if err != nil {
			tt.Error("Error unexpectedly returned")
		}
	})
	t.Run("IgnoreFlexCacheInUseErrorOnDeleteSVMPeer", func(tt *testing.T) {
		origGetOntapClient := getOntapClientFunc
		origGetSvmPeer := getSvmPeer
		origDeleteSvmPeer := deleteSvmPeer
		defer func() {
			getOntapClientFunc = origGetOntapClient
			getSvmPeer = origGetSvmPeer
			deleteSvmPeer = origDeleteSvmPeer
		}()

		mrc := new(ontaprest.MockRESTClient)
		msmc := new(ontaprest.MockSnapmirrorClient)
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mrc, nil
		}

		var empty []*ontaprest.SnapmirrorRelationship
		listParams := &ontaprest.SnapmirrorRelationshipListParams{}
		destListParams := &ontaprest.SnapmirrorRelationshipListDestinationsParams{}

		mrc.On("Snapmirror").Return(msmc)
		msmc.On("SnapmirrorRelationshipList", listParams).Return(empty, nil).Times(1)
		msmc.On("SnapmirrorRelationshipListDestinations", destListParams).Return(empty, nil).Times(1)

		getSvmPeer = func(provider *OntapRestProvider, srcSVMName, dstSVMName string) (*SvmPeer, error) {
			return &SvmPeer{UUID: "peer-uuid"}, nil
		}
		deleteSvmPeer = func(provider *OntapRestProvider, svmPeerUUID string, force bool) error {
			return fmt.Errorf("The peer relationship is in use by FlexCache")
		}

		prov := &OntapRestProvider{}
		params := &DeleteVolumeReplicationParams{
			VolumeReplication: &VolumeReplication{
				SourceSVMName:      "srcSVM",
				DestinationSVMName: "dstSVM",
				EndpointType:       "src",
			},
		}
		err := cleanupSvmPeering(prov, params)
		assert.NoError(tt, err)
		mrc.AssertExpectations(tt)
		msmc.AssertExpectations(tt)
	})
	t.Run("WhenShouldNotDeletePeerDueToExistingDestination", func(tt *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockSnapmirrorClient := new(ontaprest.MockSnapmirrorClient)

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		provider := &OntapRestProvider{}

		mockClient.On("Snapmirror").Return(mockSnapmirrorClient)
		mockSnapmirrorClient.On("SnapmirrorRelationshipList", &ontaprest.SnapmirrorRelationshipListParams{}).Return(nil, nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipListDestinations", &ontaprest.SnapmirrorRelationshipListDestinationsParams{}).Return(snapmirrorList2, nil).Times(1)

		err := cleanupSvmPeering(provider, params)
		if err != nil {
			tt.Error("Error unexpectedly returned")
		}
	})
	t.Run("WhenShouldNotDeletePeerDueToExistingDestinationHybridRep", func(tt *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockSnapmirrorClient := new(ontaprest.MockSnapmirrorClient)

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		provider := &OntapRestProvider{}

		mockClient.On("Snapmirror").Return(mockSnapmirrorClient)
		mockSnapmirrorClient.On("SnapmirrorRelationshipList", &ontaprest.SnapmirrorRelationshipListParams{}).Return(nil, nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipListDestinations", &ontaprest.SnapmirrorRelationshipListDestinationsParams{}).Return(snapmirrorList, nil).Times(1)

		err := cleanupSvmPeering(provider, params1)
		if err != nil {
			tt.Error("Error unexpectedly returned")
		}
	})
	t.Run("WhenShouldNotDeletePeerDueToReverseResyncPerformedDuringCleanupLoop", func(tt *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockSnapmirrorClient := new(ontaprest.MockSnapmirrorClient)
		mockSvmClient := new(ontaprest.MockSVMClient)

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		provider := &OntapRestProvider{}
		svmPeerUuid := "svm-peer-uuid"

		mockClient.On("Snapmirror").Return(mockSnapmirrorClient)
		mockClient.On("SVM").Return(mockSvmClient)
		mockSnapmirrorClient.On("SnapmirrorRelationshipList", &ontaprest.SnapmirrorRelationshipListParams{}).Return(nil, nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipListDestinations", &ontaprest.SnapmirrorRelationshipListDestinationsParams{}).Return(snapmirrorList, nil).Times(1)
		mockSvmClient.On("SvmPeerCollectionGet")
		getSvmPeer = func(provider *OntapRestProvider, srcSVMName, dstSVMName string) (*SvmPeer, error) {
			return &SvmPeer{UUID: svmPeerUuid}, nil
		}
		deleteSvmPeer = func(provider *OntapRestProvider, svmPeerUUID string, force bool) error {
			return errors.New("Relationship is in use by SnapMirror in local cluster. Use the \"snapmirror list-destinations\" command to view the relationships. " +
				"Use the \"snapmirror release\" command to release the application, then try the command again.")
		}
		err := cleanupSvmPeering(provider, params)
		if err != nil {
			tt.Error("Error unexpectedly returned")
		}
	})
	t.Run("WhenVserverPeerDoesntExistDuringCleanupLoop", func(tt *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockSnapmirrorClient := new(ontaprest.MockSnapmirrorClient)
		mockSvmClient := new(ontaprest.MockSVMClient)

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		provider := &OntapRestProvider{}
		svmPeerUuid := "svm-peer-uuid"

		mockClient.On("Snapmirror").Return(mockSnapmirrorClient)
		mockClient.On("SVM").Return(mockSvmClient)
		mockSnapmirrorClient.On("SnapmirrorRelationshipList", &ontaprest.SnapmirrorRelationshipListParams{}).Return(nil, nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipListDestinations", &ontaprest.SnapmirrorRelationshipListDestinationsParams{}).Return(snapmirrorList, nil).Times(1)
		getSvmPeer = func(provider *OntapRestProvider, srcSVMName, dstSVMName string) (*SvmPeer, error) {
			return &SvmPeer{UUID: svmPeerUuid}, nil
		}
		deleteSvmPeer = func(provider *OntapRestProvider, svmPeerUUID string, force bool) error {
			return errors.New("Vserver peer relationship does not exist on the local cluster")
		}

		err := cleanupSvmPeering(provider, params)
		if err != nil {
			tt.Errorf("Unexpected error returned: %s", err.Error())
		}
	})
	t.Run("WhenFailedToLoadJobDuringCleanupLoopSuccess", func(tt *testing.T) {
		svmPeerPollIntervalSeconds = 1
		defer func() { svmPeerPollIntervalSeconds = 15 }()

		mockClient := new(ontaprest.MockRESTClient)
		mockSnapmirrorClient := new(ontaprest.MockSnapmirrorClient)
		mockSvmClient := new(ontaprest.MockSVMClient)

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		provider := &OntapRestProvider{}
		svmPeerUuid := "svm-peer-uuid"

		mockClient.On("Snapmirror").Return(mockSnapmirrorClient)
		mockClient.On("SVM").Return(mockSvmClient)
		mockSnapmirrorClient.On("SnapmirrorRelationshipList", &ontaprest.SnapmirrorRelationshipListParams{}).Return(nil, nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipListDestinations", &ontaprest.SnapmirrorRelationshipListDestinationsParams{}).Return(snapmirrorList, nil).Times(1)
		getSvmPeer = func(provider *OntapRestProvider, srcSVMName, dstSVMName string) (*SvmPeer, error) {
			return &SvmPeer{UUID: svmPeerUuid}, nil
		}
		count := 0
		deleteSvmPeer = func(provider *OntapRestProvider, svmPeerUUID string, force bool) error {
			if count == 0 {
				count = 1
				return errors.New("Relationship is in use by SnapMirror in peer cluster")
			}
			return nil
		}

		err := cleanupSvmPeering(provider, params)
		if err != nil {
			tt.Errorf("Unexpected error returned: %s", err.Error())
		}
	})
	t.Run("WhenRelationshipNeedsToBeReleasedOnPeeredSVMDuringCleanupLoopSuccess", func(tt *testing.T) {
		svmPeerPollIntervalSeconds = 0
		defer func() { svmPeerPollIntervalSeconds = 15 }()

		mockClient := new(ontaprest.MockRESTClient)
		mockSnapmirrorClient := new(ontaprest.MockSnapmirrorClient)
		mockSvmClient := new(ontaprest.MockSVMClient)

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		provider := &OntapRestProvider{}
		svmPeerUuid := "svm-peer-uuid"

		mockClient.On("Snapmirror").Return(mockSnapmirrorClient)
		mockClient.On("SVM").Return(mockSvmClient)
		mockSnapmirrorClient.On("SnapmirrorRelationshipList", &ontaprest.SnapmirrorRelationshipListParams{}).Return(nil, nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipListDestinations", &ontaprest.SnapmirrorRelationshipListDestinationsParams{}).Return(snapmirrorList, nil).Times(1)
		getSvmPeer = func(provider *OntapRestProvider, srcSVMName, dstSVMName string) (*SvmPeer, error) {
			return &SvmPeer{UUID: svmPeerUuid}, nil
		}
		deleteSvmPeer = func(provider *OntapRestProvider, svmPeerUUID string, force bool) error {
			return errors.New("A relationship on the peer cluster needs to be released")
		}

		err := cleanupSvmPeering(provider, params)
		if err == nil {
			tt.Error("Expected an error")
		} else {
			if err.Error() != "A source relationship on the Vserver peer needs to be released in peer cluster" {
				tt.Errorf("Unexpected error: %s", err.Error())
			}
			if !errors.IsConflictErr(err) {
				tt.Error("Expected a ConflictError")
			}
			if errors.GetTrackingID(err) != errors.StaleSnapmirrorCleanupNeeded {
				tt.Error("Wrong tracking ID returned")
			}
		}
	})
	t.Run("WhenFailedToLoadJobDuringCleanupLoopSuccess", func(tt *testing.T) {
		svmPeerPollIntervalSeconds = 1
		defer func() { svmPeerPollIntervalSeconds = 15 }()

		mockClient := new(ontaprest.MockRESTClient)
		mockSnapmirrorClient := new(ontaprest.MockSnapmirrorClient)
		mockSvmClient := new(ontaprest.MockSVMClient)

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		provider := &OntapRestProvider{}
		svmPeerUuid := "svm-peer-uuid"

		mockClient.On("Snapmirror").Return(mockSnapmirrorClient)
		mockClient.On("SVM").Return(mockSvmClient)
		mockSnapmirrorClient.On("SnapmirrorRelationshipList", &ontaprest.SnapmirrorRelationshipListParams{}).Return(nil, nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipListDestinations", &ontaprest.SnapmirrorRelationshipListDestinationsParams{}).Return(snapmirrorList, nil).Times(1)
		getSvmPeer = func(provider *OntapRestProvider, srcSVMName, dstSVMName string) (*SvmPeer, error) {
			return &SvmPeer{UUID: svmPeerUuid}, nil
		}
		count := 0
		deleteSvmPeer = func(provider *OntapRestProvider, svmPeerUUID string, force bool) error {
			if count == 0 {
				count = 1
				return errors.New("Failed to load job for Deleting a Vserver peer relationship between svm_bob and svm_rob")
			}
			return nil
		}

		err := cleanupSvmPeering(provider, params)
		if err != nil {
			tt.Errorf("Unexpected error returned: %s", err.Error())
		}
	})
	t.Run("WhenFailedToContactPeerCluster", func(tt *testing.T) {
		defer func() { params.VolumeReplication.Volume = nil }()

		params.VolumeReplication.Volume = &Volume{
			IsOnPremMigration: true,
		}
		mockClient := new(ontaprest.MockRESTClient)
		mockSnapmirrorClient := new(ontaprest.MockSnapmirrorClient)
		mockSvmClient := new(ontaprest.MockSVMClient)

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		provider := &OntapRestProvider{}
		svmPeerUuid := "svm-peer-uuid"

		mockClient.On("Snapmirror").Return(mockSnapmirrorClient)
		mockClient.On("SVM").Return(mockSvmClient)
		mockSnapmirrorClient.On("SnapmirrorRelationshipList", &ontaprest.SnapmirrorRelationshipListParams{}).Return(nil, nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipListDestinations", &ontaprest.SnapmirrorRelationshipListDestinationsParams{}).Return(snapmirrorList, nil).Times(1)
		getSvmPeer = func(provider *OntapRestProvider, srcSVMName, dstSVMName string) (*SvmPeer, error) {
			return &SvmPeer{UUID: svmPeerUuid}, nil
		}
		count := 0
		deleteSvmPeer = func(provider *OntapRestProvider, svmPeerUUID string, force bool) error {
			if count == 0 {
				count = 1
				return errors.New("errors.New(\"Failed to contact peer cluster \\\\\\\"peer-cluster\\\\\\\" at addresses: 1.2.3.4, 1.2.4.5. RPC: Remote system error [from mgwd on node \\\\\\\"node-name\\\\\\\" (VSID: -1) to mgwd at 1.2.3.14, 1.2.3.11]\")")
			}
			return nil
		}

		err := cleanupSvmPeering(provider, params)
		if err != nil {
			tt.Errorf("Unexpected error returned: %s", err.Error())
		}
	})
	t.Run("WhenDeleteSVMPeerReturnsUnknownError", func(tt *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockSnapmirrorClient := new(ontaprest.MockSnapmirrorClient)
		mockSvmClient := new(ontaprest.MockSVMClient)

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		provider := &OntapRestProvider{}
		svmPeerUuid := "svm-peer-uuid"

		mockClient.On("Snapmirror").Return(mockSnapmirrorClient)
		mockClient.On("SVM").Return(mockSvmClient)
		mockSnapmirrorClient.On("SnapmirrorRelationshipList", &ontaprest.SnapmirrorRelationshipListParams{}).Return(nil, nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipListDestinations", &ontaprest.SnapmirrorRelationshipListDestinationsParams{}).Return(snapmirrorList, nil).Times(1)
		getSvmPeer = func(provider *OntapRestProvider, srcSVMName, dstSVMName string) (*SvmPeer, error) {
			return &SvmPeer{UUID: svmPeerUuid}, nil
		}
		deleteSvmPeer = func(provider *OntapRestProvider, svmPeerUUID string, force bool) error {
			return errors.New("Unknown error")
		}

		err := cleanupSvmPeering(provider, params)
		if err == nil {
			tt.Error("Error not returned")
		} else if err.Error() != "Unknown error" {
			tt.Error("Wrong error returned")
		}
	})
	t.Run("WhenEntryDoesntExistShouldIgnore", func(tt *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockSnapmirrorClient := new(ontaprest.MockSnapmirrorClient)
		mockSvmClient := new(ontaprest.MockSVMClient)

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		provider := &OntapRestProvider{}
		svmPeerUuid := "svm-peer-uuid"

		mockClient.On("Snapmirror").Return(mockSnapmirrorClient)
		mockClient.On("SVM").Return(mockSvmClient)
		mockSnapmirrorClient.On("SnapmirrorRelationshipList", &ontaprest.SnapmirrorRelationshipListParams{}).Return(nil, nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipListDestinations", &ontaprest.SnapmirrorRelationshipListDestinationsParams{}).Return(snapmirrorList, nil).Times(1)
		getSvmPeer = func(provider *OntapRestProvider, srcSVMName, dstSVMName string) (*SvmPeer, error) {
			return &SvmPeer{UUID: svmPeerUuid}, nil
		}
		deleteSvmPeer = func(provider *OntapRestProvider, svmPeerUUID string, force bool) error {
			return errors.New("entry doesn't exist")
		}

		err := cleanupSvmPeering(provider, params)
		if err != nil {
			tt.Error("Error not returned")
		}
	})
	t.Run("WhenSuccessful", func(tt *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockSnapmirrorClient := new(ontaprest.MockSnapmirrorClient)
		mockSvmClient := new(ontaprest.MockSVMClient)

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		provider := &OntapRestProvider{}
		svmPeerUuid := "svm-peer-uuid"

		mockClient.On("Snapmirror").Return(mockSnapmirrorClient)
		mockClient.On("SVM").Return(mockSvmClient)
		mockSnapmirrorClient.On("SnapmirrorRelationshipList", &ontaprest.SnapmirrorRelationshipListParams{}).Return(nil, nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipListDestinations", &ontaprest.SnapmirrorRelationshipListDestinationsParams{}).Return(snapmirrorList, nil).Times(1)
		getSvmPeer = func(provider *OntapRestProvider, srcSVMName, dstSVMName string) (*SvmPeer, error) {
			return &SvmPeer{UUID: svmPeerUuid}, nil
		}
		deleteSvmPeer = func(provider *OntapRestProvider, svmPeerUUID string, force bool) error {
			return nil
		}

		err := cleanupSvmPeering(provider, params)
		if err != nil {
			tt.Error("Error not returned")
		}
	})
	t.Run("WhenSuccessfulHybridRep", func(tt *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockSnapmirrorClient := new(ontaprest.MockSnapmirrorClient)
		mockSvmClient := new(ontaprest.MockSVMClient)

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		provider := &OntapRestProvider{}
		svmPeerUuid := "svm-peer-uuid"

		mockClient.On("Snapmirror").Return(mockSnapmirrorClient)
		mockClient.On("SVM").Return(mockSvmClient)
		mockSnapmirrorClient.On("SnapmirrorRelationshipList", &ontaprest.SnapmirrorRelationshipListParams{}).Return(nil, nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipListDestinations", &ontaprest.SnapmirrorRelationshipListDestinationsParams{}).Return(snapmirrorList, nil).Times(1)
		getSvmPeer = func(provider *OntapRestProvider, srcSVMName, dstSVMName string) (*SvmPeer, error) {
			return &SvmPeer{UUID: svmPeerUuid}, nil
		}
		deleteSvmPeer = func(provider *OntapRestProvider, svmPeerUUID string, force bool) error {
			return nil
		}

		err := cleanupSvmPeering(provider, params1)
		if err != nil {
			tt.Error("Error not returned")
		}
	})
}

func TestReverseVolumeReplication(t *testing.T) {
	relationshipID := "snapmirror-uuid"
	srcVolumeName := "srcvol"
	dstVolumeName := "dstvol"
	srcSVMName := "srcsvm"
	dstSVMName := "dstsvm"
	snapmirrorState := "broken_off"

	volRep := &VolumeReplication{
		RelationshipID:        relationshipID,
		SourceVolumeName:      srcVolumeName,
		DestinationVolumeName: dstVolumeName,
		SourceSVMName:         srcSVMName,
		DestinationSVMName:    dstSVMName,
	}

	snapmirror := &ontaprest.SnapmirrorRelationship{
		SnapmirrorRelationship: models.SnapmirrorRelationship{
			State: &snapmirrorState,
			UUID:  nillable.ToPointer(strfmt.UUID(relationshipID)),
			Source: &models.SnapmirrorSourceEndpoint{
				Path: nillable.GetStringPtr(srcSVMName + ":" + srcVolumeName),
				Svm: &models.SnapmirrorSourceEndpointInlineSvm{
					Name: nillable.GetStringPtr(srcSVMName),
				},
			},
			Destination: &models.SnapmirrorEndpoint{
				Path: nillable.GetStringPtr(dstSVMName + ":" + dstVolumeName),
				Svm: &models.SnapmirrorEndpointInlineSvm{
					Name: nillable.GetStringPtr(dstSVMName),
				},
			},
		},
	}

	getParams := &ontaprest.SnapmirrorRelationshipGetParams{UUID: relationshipID}

	setupMocks := func(tt *testing.T) (provider *OntapRestProvider, mockClient *ontaprest.MockRESTClient, mockSnapmirrorClient *ontaprest.MockSnapmirrorClient) {
		mockClient = new(ontaprest.MockRESTClient)
		mockSnapmirrorClient = new(ontaprest.MockSnapmirrorClient)

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}

		provider = &OntapRestProvider{
			ClientParams: ontaprest.RESTClientParams{},
			Logger:       log.NewLogger(),
		}

		mockClient.On("Snapmirror").Return(mockSnapmirrorClient)

		return provider, mockClient, mockSnapmirrorClient
	}

	originalgetOntapClientFunc := getOntapClientFunc
	defer func() {
		getOntapClientFunc = originalgetOntapClientFunc
	}()

	t.Run("WhenGetOntapClientReturnsError", func(tt *testing.T) {
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return nil, errors.New("Failed to get client")
		}

		provider := &OntapRestProvider{}

		result, err := provider.ReverseVolumeReplication(volRep)

		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.EqualError(tt, err, "Failed to get client")
	})

	t.Run("WhenSnapmirrorGetReturnsError", func(tt *testing.T) {
		provider, _, mockSnapmirrorClient := setupMocks(tt)

		mockSnapmirrorClient.On("SnapmirrorRelationshipGet", getParams).Return(nil, errors.New("Failed to get snapmirror")).Times(1)

		result, err := provider.ReverseVolumeReplication(volRep)

		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.EqualError(tt, err, "Failed to get snapmirror")
		mockSnapmirrorClient.AssertExpectations(tt)
	})

	t.Run("WhenSnapmirrorGetReturnsNil", func(tt *testing.T) {
		provider, _, mockSnapmirrorClient := setupMocks(tt)

		mockSnapmirrorClient.On("SnapmirrorRelationshipGet", getParams).Return(nil, nil).Times(1)

		result, err := provider.ReverseVolumeReplication(volRep)

		assert.Error(tt, err)
		assert.Nil(tt, result)
		mockSnapmirrorClient.AssertExpectations(tt)
	})

	t.Run("WhenSuccessful", func(tt *testing.T) {
		provider, _, mockSnapmirrorClient := setupMocks(tt)

		reverseParams := &ontaprest.SnapmirrorRelationshipReverseParams{
			UUID:            relationshipID,
			SourcePath:      dstSVMName + ":" + dstVolumeName, // Current destination becomes new source
			DestinationPath: srcSVMName + ":" + srcVolumeName, // Current source becomes new destination
		}

		mockSnapmirrorClient.On("SnapmirrorRelationshipGet", getParams).Return(snapmirror, nil).Times(1)
		mockSnapmirrorClient.On("SnapmirrorRelationshipReverse", reverseParams).Return(nil, nil, nil).Times(1)

		result, err := provider.ReverseVolumeReplication(volRep)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		// Implementation currently only populates RelationshipUUID
		mockSnapmirrorClient.AssertExpectations(tt)
	})
}

func TestAbortVolumeReplication(t *testing.T) {
	originalgetOntapClientFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = originalgetOntapClientFunc }()

	// Set poll interval to 0 for faster test execution
	originalPollInterval := waitForMirrorStatePollInterval
	waitForMirrorStatePollInterval = 0
	defer func() { waitForMirrorStatePollInterval = originalPollInterval }()

	// Test data setup
	relationshipID := "test-relationship-id"
	transferUUID := "test-transfer-uuid"
	volRep := &VolumeReplication{
		RelationshipID:     relationshipID,
		TransferUUID:       transferUUID,
		RelationshipStatus: SnapMirrorRelationshipStatusTransferring,
	}

	t.Run("WhenGetOntapClientFuncReturnsError", func(tt *testing.T) {
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return nil, errors.New("getOntapClient error")
		}
		provider := &OntapRestProvider{}

		result, err := provider.AbortVolumeReplication(volRep)

		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, "getOntapClient error", err.Error())
	})

	t.Run("WhenSnapmirrorRelationshipTransferModifyReturnsError", func(tt *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockSnapmirrorClient := new(ontaprest.MockSnapmirrorClient)
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		provider := &OntapRestProvider{
			Logger: log.NewLogger(),
		}
		expectedError := errors.New("failed to modify transfer")

		modifyTransferParams := &ontaprest.SnapmirrorRelationshipTransferModifyParams{
			UUID:         relationshipID,
			TransferUUID: transferUUID,
			State:        &volRep.RelationshipStatus,
		}

		mockClient.On("Snapmirror").Return(mockSnapmirrorClient)
		mockSnapmirrorClient.On("SnapmirrorRelationshipTransferModify", modifyTransferParams).Return(expectedError)

		result, err := provider.AbortVolumeReplication(volRep)

		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, expectedError, err)
		mockClient.AssertExpectations(tt)
		mockSnapmirrorClient.AssertExpectations(tt)
	})

	t.Run("WhenSnapmirrorRelationshipTransferModifyReturnsNotFound", func(tt *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockSnapmirrorClient := new(ontaprest.MockSnapmirrorClient)
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		provider := &OntapRestProvider{
			Logger: log.NewLogger(),
		}
		notFoundError := errors.New("snapmirror relationship not found")

		modifyTransferParams := &ontaprest.SnapmirrorRelationshipTransferModifyParams{
			UUID:         relationshipID,
			TransferUUID: transferUUID,
			State:        &volRep.RelationshipStatus,
		}

		mockClient.On("Snapmirror").Return(mockSnapmirrorClient)
		mockSnapmirrorClient.On("SnapmirrorRelationshipTransferModify", modifyTransferParams).Return(notFoundError)

		result, err := provider.AbortVolumeReplication(volRep)

		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, notFoundError, err)
		mockClient.AssertExpectations(tt)
		mockSnapmirrorClient.AssertExpectations(tt)
	})

	t.Run("WhenSnapmirrorRelationshipGetReturnsError", func(tt *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockSnapmirrorClient := new(ontaprest.MockSnapmirrorClient)
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		provider := &OntapRestProvider{
			Logger: log.NewLogger(),
		}
		expectedError := errors.New("failed to get snapmirror")

		modifyTransferParams := &ontaprest.SnapmirrorRelationshipTransferModifyParams{
			UUID:         relationshipID,
			TransferUUID: transferUUID,
			State:        &volRep.RelationshipStatus,
		}
		getParams := &ontaprest.SnapmirrorRelationshipGetParams{UUID: relationshipID}

		mockClient.On("Snapmirror").Return(mockSnapmirrorClient)
		mockSnapmirrorClient.On("SnapmirrorRelationshipTransferModify", modifyTransferParams).Return(nil)
		mockSnapmirrorClient.On("SnapmirrorRelationshipGet", getParams).Return(nil, expectedError)

		result, err := provider.AbortVolumeReplication(volRep)

		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, expectedError, err)
		mockClient.AssertExpectations(tt)
		mockSnapmirrorClient.AssertExpectations(tt)
	})

	t.Run("WhenTransferStateIsNotTransferringAfterFirstCheck", func(tt *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockSnapmirrorClient := new(ontaprest.MockSnapmirrorClient)
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		provider := &OntapRestProvider{
			Logger: log.NewLogger(),
		}

		modifyTransferParams := &ontaprest.SnapmirrorRelationshipTransferModifyParams{
			UUID:         relationshipID,
			TransferUUID: transferUUID,
			State:        &volRep.RelationshipStatus,
		}
		getParams := &ontaprest.SnapmirrorRelationshipGetParams{UUID: relationshipID}

		// Mock snapmirror with non-transferring state
		snapmirror := &ontaprest.SnapmirrorRelationship{
			SnapmirrorRelationship: models.SnapmirrorRelationship{
				Transfer: &models.SnapmirrorRelationshipInlineTransfer{
					State: nillable.ToPointer(models.SnapmirrorRelationshipInlineTransferStateSuccess),
				},
			},
		}

		mockClient.On("Snapmirror").Return(mockSnapmirrorClient)
		mockSnapmirrorClient.On("SnapmirrorRelationshipTransferModify", modifyTransferParams).Return(nil)
		mockSnapmirrorClient.On("SnapmirrorRelationshipGet", getParams).Return(snapmirror, nil)

		result, err := provider.AbortVolumeReplication(volRep)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, volRep, result)
		mockClient.AssertExpectations(tt)
		mockSnapmirrorClient.AssertExpectations(tt)
	})

	t.Run("WhenTransferStateRemainsTransferringAndTimesOut", func(tt *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockSnapmirrorClient := new(ontaprest.MockSnapmirrorClient)
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		provider := &OntapRestProvider{
			Logger: log.NewLogger(),
		}

		modifyTransferParams := &ontaprest.SnapmirrorRelationshipTransferModifyParams{
			UUID:         relationshipID,
			TransferUUID: transferUUID,
			State:        &volRep.RelationshipStatus,
		}
		getParams := &ontaprest.SnapmirrorRelationshipGetParams{UUID: relationshipID}

		// Mock snapmirror with transferring state that never changes
		snapmirror := &ontaprest.SnapmirrorRelationship{
			SnapmirrorRelationship: models.SnapmirrorRelationship{
				Transfer: &models.SnapmirrorRelationshipInlineTransfer{
					State: nillable.ToPointer(models.SnapmirrorRelationshipInlineTransferStateTransferring),
				},
			},
		}

		mockClient.On("Snapmirror").Return(mockSnapmirrorClient)
		mockSnapmirrorClient.On("SnapmirrorRelationshipTransferModify", modifyTransferParams).Return(nil)
		// Mock the method to be called waitForReplicationStateAbortMaxRetries times (abort loop uses this constant)
		mockSnapmirrorClient.On("SnapmirrorRelationshipGet", getParams).Return(snapmirror, nil).Times(waitForReplicationStateAbortMaxRetries)

		result, err := provider.AbortVolumeReplication(volRep)

		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, "Transfer abort did not finish in time", err.Error())
		mockClient.AssertExpectations(tt)
		mockSnapmirrorClient.AssertExpectations(tt)
	})

	t.Run("WhenTransferStateChangesFromTransferringToAborted", func(tt *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockSnapmirrorClient := new(ontaprest.MockSnapmirrorClient)
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		provider := &OntapRestProvider{
			Logger: log.NewLogger(),
		}

		modifyTransferParams := &ontaprest.SnapmirrorRelationshipTransferModifyParams{
			UUID:         relationshipID,
			TransferUUID: transferUUID,
			State:        &volRep.RelationshipStatus,
		}
		getParams := &ontaprest.SnapmirrorRelationshipGetParams{UUID: relationshipID}

		// Mock first call returns transferring, second call returns aborted
		snapmirrorTransferring := &ontaprest.SnapmirrorRelationship{
			SnapmirrorRelationship: models.SnapmirrorRelationship{
				Transfer: &models.SnapmirrorRelationshipInlineTransfer{
					State: nillable.ToPointer(models.SnapmirrorRelationshipInlineTransferStateTransferring),
				},
			},
		}
		snapmirrorAborted := &ontaprest.SnapmirrorRelationship{
			SnapmirrorRelationship: models.SnapmirrorRelationship{
				Transfer: &models.SnapmirrorRelationshipInlineTransfer{
					State: nillable.ToPointer(models.SnapmirrorRelationshipInlineTransferStateAborted),
				},
			},
		}

		mockClient.On("Snapmirror").Return(mockSnapmirrorClient)
		mockSnapmirrorClient.On("SnapmirrorRelationshipTransferModify", modifyTransferParams).Return(nil)
		mockSnapmirrorClient.On("SnapmirrorRelationshipGet", getParams).Return(snapmirrorTransferring, nil).Once()
		mockSnapmirrorClient.On("SnapmirrorRelationshipGet", getParams).Return(snapmirrorAborted, nil).Once()

		result, err := provider.AbortVolumeReplication(volRep)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, volRep, result)
		mockClient.AssertExpectations(tt)
		mockSnapmirrorClient.AssertExpectations(tt)
	})

	t.Run("WhenSnapmirrorRelationshipGetErrorOnSecondCall", func(tt *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockSnapmirrorClient := new(ontaprest.MockSnapmirrorClient)
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		provider := &OntapRestProvider{
			Logger: log.NewLogger(),
		}
		expectedError := errors.New("second get call failed")

		modifyTransferParams := &ontaprest.SnapmirrorRelationshipTransferModifyParams{
			UUID:         relationshipID,
			TransferUUID: transferUUID,
			State:        &volRep.RelationshipStatus,
		}
		getParams := &ontaprest.SnapmirrorRelationshipGetParams{UUID: relationshipID}

		// Mock first call succeeds with transferring state, second call fails
		snapmirrorTransferring := &ontaprest.SnapmirrorRelationship{
			SnapmirrorRelationship: models.SnapmirrorRelationship{
				Transfer: &models.SnapmirrorRelationshipInlineTransfer{
					State: nillable.ToPointer(models.SnapmirrorRelationshipInlineTransferStateTransferring),
				},
			},
		}

		mockClient.On("Snapmirror").Return(mockSnapmirrorClient)
		mockSnapmirrorClient.On("SnapmirrorRelationshipTransferModify", modifyTransferParams).Return(nil)
		mockSnapmirrorClient.On("SnapmirrorRelationshipGet", getParams).Return(snapmirrorTransferring, nil).Once()
		mockSnapmirrorClient.On("SnapmirrorRelationshipGet", getParams).Return(nil, expectedError).Once()

		result, err := provider.AbortVolumeReplication(volRep)

		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, expectedError, err)
		mockClient.AssertExpectations(tt)
		mockSnapmirrorClient.AssertExpectations(tt)
	})

	t.Run("WhenTransferStateIsNilAfterFirstCheck", func(tt *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockSnapmirrorClient := new(ontaprest.MockSnapmirrorClient)
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		provider := &OntapRestProvider{
			Logger: log.NewLogger(),
		}

		modifyTransferParams := &ontaprest.SnapmirrorRelationshipTransferModifyParams{
			UUID:         relationshipID,
			TransferUUID: transferUUID,
			State:        &volRep.RelationshipStatus,
		}
		getParams := &ontaprest.SnapmirrorRelationshipGetParams{UUID: relationshipID}

		// Mock snapmirror with nil transfer state
		snapmirror := &ontaprest.SnapmirrorRelationship{
			SnapmirrorRelationship: models.SnapmirrorRelationship{
				Transfer: nil,
			},
		}

		mockClient.On("Snapmirror").Return(mockSnapmirrorClient)
		mockSnapmirrorClient.On("SnapmirrorRelationshipTransferModify", modifyTransferParams).Return(nil)
		mockSnapmirrorClient.On("SnapmirrorRelationshipGet", getParams).Return(snapmirror, nil)

		result, err := provider.AbortVolumeReplication(volRep)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, volRep, result)
		mockClient.AssertExpectations(tt)
		mockSnapmirrorClient.AssertExpectations(tt)
	})

	t.Run("WhenEmptyRelationshipID", func(tt *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockSnapmirrorClient := new(ontaprest.MockSnapmirrorClient)
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		provider := &OntapRestProvider{
			Logger: log.NewLogger(),
		}

		volRepEmptyID := &VolumeReplication{
			RelationshipID:     "", // Empty relationship ID
			TransferUUID:       transferUUID,
			RelationshipStatus: SnapMirrorRelationshipStatusTransferring,
		}

		modifyTransferParams := &ontaprest.SnapmirrorRelationshipTransferModifyParams{
			UUID:         "",
			TransferUUID: transferUUID,
			State:        &volRepEmptyID.RelationshipStatus,
		}

		mockClient.On("Snapmirror").Return(mockSnapmirrorClient)
		mockSnapmirrorClient.On("SnapmirrorRelationshipTransferModify", modifyTransferParams).Return(errors.New("invalid UUID"))

		result, err := provider.AbortVolumeReplication(volRepEmptyID)

		assert.Error(tt, err)
		assert.Nil(tt, result)
		mockClient.AssertExpectations(tt)
		mockSnapmirrorClient.AssertExpectations(tt)
	})

	t.Run("WhenEmptyTransferUUID", func(tt *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockSnapmirrorClient := new(ontaprest.MockSnapmirrorClient)
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		provider := &OntapRestProvider{
			Logger: log.NewLogger(),
		}

		volRepEmptyTransferID := &VolumeReplication{
			RelationshipID:     relationshipID,
			TransferUUID:       "", // Empty transfer UUID
			RelationshipStatus: SnapMirrorRelationshipStatusTransferring,
		}

		modifyTransferParams := &ontaprest.SnapmirrorRelationshipTransferModifyParams{
			UUID:         relationshipID,
			TransferUUID: "",
			State:        &volRepEmptyTransferID.RelationshipStatus,
		}
		getParams := &ontaprest.SnapmirrorRelationshipGetParams{UUID: relationshipID}

		snapmirror := &ontaprest.SnapmirrorRelationship{
			SnapmirrorRelationship: models.SnapmirrorRelationship{
				Transfer: &models.SnapmirrorRelationshipInlineTransfer{
					State: nillable.ToPointer(models.SnapmirrorRelationshipInlineTransferStateSuccess),
				},
			},
		}

		mockClient.On("Snapmirror").Return(mockSnapmirrorClient)
		mockSnapmirrorClient.On("SnapmirrorRelationshipTransferModify", modifyTransferParams).Return(nil)
		mockSnapmirrorClient.On("SnapmirrorRelationshipGet", getParams).Return(snapmirror, nil)

		result, err := provider.AbortVolumeReplication(volRepEmptyTransferID)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, volRepEmptyTransferID, result)
		mockClient.AssertExpectations(tt)
		mockSnapmirrorClient.AssertExpectations(tt)
	})
}

func Test_unmountVolume(t *testing.T) {
	originalGetOntapClientFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = originalGetOntapClientFunc }()

	t.Run("Success_UnmountsVolumeWithJobCompletion", func(tt *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockStorageClient := new(ontaprest.MockStorageClient)

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}

		provider := &OntapRestProvider{
			ClientParams: ontaprest.RESTClientParams{},
			Logger:       log.NewLogger(),
		}

		volumeUUID := "test-volume-uuid-123"
		securityStyle := "unix"
		volume := ontaprest.Volume{
			Volume: models.Volume{
				UUID: &volumeUUID,
				Name: nillable.GetStringPtr("test-volume"),
				Nas: &models.VolumeInlineNas{
					Path:          nillable.GetStringPtr("/test/junction/path"),
					SecurityStyle: &securityStyle,
				},
			},
		}

		volRep := &VolumeReplication{
			SourceSVMName:      "source-svm",
			DestinationSVMName: "dest-svm",
			Volume: &Volume{
				ProtocolTypes: []string{"NFS"},
			},
		}

		// Setup mocks for the VolumeUnmount call inside _unmountVolume
		jobAccepted := &ontaprest.JobAccepted{JobUUID: "unmount-job-123"}

		mockClient.On("Storage").Return(mockStorageClient)
		mockStorageClient.On("VolumeUnmount", mock.Anything).Return(jobAccepted, nil)

		// Execute test
		err := _unmountVolume(provider, &volume, volRep)

		// Verify results
		assert.NoError(tt, err)
		mockClient.AssertExpectations(tt)
		mockStorageClient.AssertExpectations(tt)
	})
	t.Run("UnmountsVolumeForIscsi", func(tt *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockStorageClient := new(ontaprest.MockStorageClient)

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}

		provider := &OntapRestProvider{
			ClientParams: ontaprest.RESTClientParams{},
			Logger:       log.NewLogger(),
		}

		volumeUUID := "test-volume-uuid-123"
		securityStyle := "unix"
		volume := ontaprest.Volume{
			Volume: models.Volume{
				UUID: &volumeUUID,
				Name: nillable.GetStringPtr("test-volume"),
				Nas: &models.VolumeInlineNas{
					Path:          nillable.GetStringPtr("/test/junction/path"),
					SecurityStyle: &securityStyle,
				},
			},
		}

		volRep := &VolumeReplication{
			SourceSVMName:      "source-svm",
			DestinationSVMName: "dest-svm",
			Volume: &Volume{
				ProtocolTypes: []string{"ISCSI"},
			},
		}

		// Execute test
		err := _unmountVolume(provider, &volume, volRep)

		// Verify results
		assert.NoError(tt, err)
		mockClient.AssertExpectations(tt)
		mockStorageClient.AssertExpectations(tt)
	})

	t.Run("Success_UnmountsVolumeWithoutJob", func(tt *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockStorageClient := new(ontaprest.MockStorageClient)

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}

		provider := &OntapRestProvider{
			ClientParams: ontaprest.RESTClientParams{},
			Logger:       log.NewLogger(),
		}

		volumeUUID := "test-volume-uuid-456"
		securityStyle := "unix"
		volume := ontaprest.Volume{
			Volume: models.Volume{
				UUID: &volumeUUID,
				Name: nillable.GetStringPtr("test-volume-2"),
				Nas: &models.VolumeInlineNas{
					Path:          nillable.GetStringPtr("/another/junction/path"),
					SecurityStyle: &securityStyle,
				},
			},
		}

		volRep := &VolumeReplication{
			SourceSVMName:      "source-svm-2",
			DestinationSVMName: "dest-svm-2",
			Volume: &Volume{
				ProtocolTypes: []string{"NFS"},
			},
		}

		// Setup mocks for VolumeUnmount call - returns success without job
		mockClient.On("Storage").Return(mockStorageClient)
		mockStorageClient.On("VolumeUnmount", mock.Anything).Return((*ontaprest.JobAccepted)(nil), nil)

		// Execute test
		err := _unmountVolume(provider, &volume, volRep)

		// Verify results
		assert.NoError(tt, err)
		mockClient.AssertExpectations(tt)
		mockStorageClient.AssertExpectations(tt)
	})

	t.Run("Error_WhenVolumeIsNil", func(tt *testing.T) {
		provider := &OntapRestProvider{
			ClientParams: ontaprest.RESTClientParams{},
		}

		volRep := &VolumeReplication{
			SourceSVMName:      "source-svm",
			DestinationSVMName: "dest-svm",
		}

		// Execute test with nil volume - should panic or return error
		defer func() {
			if r := recover(); r != nil {
				// Expected panic due to nil pointer dereference
				assert.Contains(tt, fmt.Sprintf("%v", r), "nil pointer dereference")
			}
		}()

		err := _unmountVolume(provider, nil, volRep)

		// If it doesn't panic, it should return an error
		assert.Error(tt, err)
	})

	t.Run("Error_WhenVolumeNasIsNil", func(tt *testing.T) {
		provider := &OntapRestProvider{
			ClientParams: ontaprest.RESTClientParams{},
		}

		volumeUUID := "test-volume-uuid-789"
		volumeName := "test-volume-name"
		volume := &ontaprest.Volume{
			Volume: models.Volume{
				UUID: &volumeUUID,
				Name: &volumeName,
				Nas:  nil, // Nas is nil
			},
		}

		volRep := &VolumeReplication{
			SourceSVMName:      "source-svm",
			DestinationSVMName: "dest-svm",
			Volume: &Volume{
				ProtocolTypes: []string{"NFS"},
			},
		}

		// Execute test with volume that has nil Nas - should panic
		defer func() {
			if r := recover(); r != nil {
				// Expected panic due to nil pointer dereference
				assert.Contains(tt, fmt.Sprintf("%v", r), "nil pointer dereference")
			}
		}()

		err := _unmountVolume(provider, volume, volRep)

		// If it doesn't panic, it should return an error
		assert.Error(tt, err)
	})

	t.Run("Error_WhenGetOntapClientFails", func(tt *testing.T) {
		expectedError := errors.New("failed to create ONTAP client")

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return nil, expectedError
		}

		provider := &OntapRestProvider{
			ClientParams: ontaprest.RESTClientParams{},
			Logger:       log.NewLogger(),
		}

		volumeUUID := "test-volume-uuid-error"
		securityStyle := "unix"
		volume := ontaprest.Volume{
			Volume: models.Volume{
				UUID: &volumeUUID,
				Name: nillable.GetStringPtr("test-volume-error"),
				Nas: &models.VolumeInlineNas{
					Path:          nillable.GetStringPtr("/error/junction/path"),
					SecurityStyle: &securityStyle,
				},
			},
		}

		volRep := &VolumeReplication{
			SourceSVMName:      "source-svm",
			DestinationSVMName: "dest-svm",
			Volume: &Volume{
				ProtocolTypes: []string{"NFS"},
			},
		}

		// Execute test
		err := _unmountVolume(provider, &volume, volRep)

		// Verify results
		assert.Error(tt, err)
		assert.Equal(tt, expectedError, err)
	})

	t.Run("Error_WhenVolumeModifyFails", func(tt *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockStorageClient := new(ontaprest.MockStorageClient)
		mockNASClient := new(ontaprest.MockNASClient)

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}

		provider := &OntapRestProvider{
			ClientParams: ontaprest.RESTClientParams{},
			Logger:       log.NewLogger(),
		}

		volumeUUID := "test-volume-uuid-modify-error"
		svmUUID := "test-svm-uuid"
		securityStyle := "unix"
		volume := ontaprest.Volume{
			Volume: models.Volume{
				UUID: &volumeUUID,
				Name: nillable.GetStringPtr("test-volume-modify-error"),
				Nas: &models.VolumeInlineNas{
					Path:          nillable.GetStringPtr("/modify/error/path"),
					SecurityStyle: &securityStyle,
				},
				Svm: &models.VolumeInlineSvm{
					UUID: &svmUUID,
				},
			},
		}

		volRep := &VolumeReplication{
			SourceSVMName:      "source-svm",
			DestinationSVMName: "dest-svm",
			Volume: &Volume{
				ProtocolTypes: []string{"SMB"},
			},
		}

		modifyError := errors.New("volume unmount operation failed")

		// Setup mocks for CIFS share deletion (SMB volume)
		mockClient.On("NAS").Return(mockNASClient)
		mockNASClient.On("CifsShareDelete", mock.Anything).Return(nil)

		// Setup mocks for volume unmount
		mockClient.On("Storage").Return(mockStorageClient)
		mockStorageClient.On("VolumeUnmount", mock.Anything).Return((*ontaprest.JobAccepted)(nil), modifyError)

		// Execute test
		err := _unmountVolume(provider, &volume, volRep)

		// Verify results
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "volume unmount operation failed")
		mockClient.AssertExpectations(tt)
		mockNASClient.AssertExpectations(tt)
		mockStorageClient.AssertExpectations(tt)
	})

	t.Run("Success_WhenJobIsReturned", func(tt *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockStorageClient := new(ontaprest.MockStorageClient)

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}

		provider := &OntapRestProvider{
			ClientParams: ontaprest.RESTClientParams{},
			Logger:       log.NewLogger(),
		}

		volumeUUID := "test-volume-uuid-poll-error"
		securityStyle := "unix"
		volume := ontaprest.Volume{
			Volume: models.Volume{
				UUID: &volumeUUID,
				Name: nillable.GetStringPtr("test-volume-poll-error"),
				Nas: &models.VolumeInlineNas{
					Path:          nillable.GetStringPtr("/poll/error/path"),
					SecurityStyle: &securityStyle,
				},
			},
		}

		volRep := &VolumeReplication{
			SourceSVMName:      "source-svm",
			DestinationSVMName: "dest-svm",
			Volume: &Volume{
				ProtocolTypes: []string{"NFS"},
			},
		}

		jobUUID := "test-job-uuid"
		jobResponse := &ontaprest.JobAccepted{
			JobUUID: jobUUID,
		}

		// Setup mocks - function doesn't poll for jobs
		mockClient.On("Storage").Return(mockStorageClient)
		mockStorageClient.On("VolumeUnmount", mock.Anything).Return(jobResponse, nil)

		// Execute test
		err := _unmountVolume(provider, &volume, volRep)

		// Verify results - should succeed even when job is returned (no polling)
		assert.NoError(tt, err)
		mockClient.AssertExpectations(tt)
		mockStorageClient.AssertExpectations(tt)
	})

	t.Run("Success_SMBVolume_DeletesCifsShareAndUnmounts", func(tt *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockStorageClient := new(ontaprest.MockStorageClient)
		mockNASClient := new(ontaprest.MockNASClient)

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}

		provider := &OntapRestProvider{
			ClientParams: ontaprest.RESTClientParams{},
			Logger:       log.NewLogger(),
		}

		volumeUUID := "test-volume-uuid-smb"
		svmUUID := "test-svm-uuid"
		junctionPath := "/test-creation-token"
		shareName := "test-creation-token"
		securityStyle := "unix"
		volume := ontaprest.Volume{
			Volume: models.Volume{
				UUID: &volumeUUID,
				Name: nillable.GetStringPtr("test-volume-smb"),
				Nas: &models.VolumeInlineNas{
					Path:          nillable.GetStringPtr(junctionPath),
					SecurityStyle: &securityStyle,
				},
				Svm: &models.VolumeInlineSvm{
					UUID: &svmUUID,
				},
			},
		}

		volRep := &VolumeReplication{
			SourceSVMName:      "source-svm",
			DestinationSVMName: "dest-svm",
			Volume: &Volume{
				ProtocolTypes: []string{"SMB"},
				JunctionPath:  junctionPath,
			},
		}

		// Setup mocks for CIFS share deletion
		mockClient.On("NAS").Return(mockNASClient)
		mockNASClient.On("CifsShareDelete", mock.MatchedBy(func(params *ontaprest.CifsShareDeleteParams) bool {
			return params.ShareName == shareName && params.SvmUUID == svmUUID
		})).Return(nil)

		// Setup mocks for volume unmount
		jobAccepted := &ontaprest.JobAccepted{JobUUID: "unmount-job-123"}
		mockClient.On("Storage").Return(mockStorageClient)
		mockStorageClient.On("VolumeUnmount", mock.Anything).Return(jobAccepted, nil)

		// Execute test
		err := _unmountVolume(provider, &volume, volRep)

		// Verify results
		assert.NoError(tt, err)
		mockClient.AssertExpectations(tt)
		mockNASClient.AssertExpectations(tt)
		mockStorageClient.AssertExpectations(tt)
	})

	t.Run("Success_SMBVolume_CifsShareNotFound_ContinuesWithUnmount", func(tt *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockStorageClient := new(ontaprest.MockStorageClient)
		mockNASClient := new(ontaprest.MockNASClient)

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}

		provider := &OntapRestProvider{
			ClientParams: ontaprest.RESTClientParams{},
			Logger:       log.NewLogger(),
		}

		volumeUUID := "test-volume-uuid-smb-notfound"
		svmUUID := "test-svm-uuid"
		junctionPath := "/test-creation-token"
		shareName := "test-creation-token"
		securityStyle := "unix"
		volume := ontaprest.Volume{
			Volume: models.Volume{
				UUID: &volumeUUID,
				Name: nillable.GetStringPtr("test-volume-smb-notfound"),
				Nas: &models.VolumeInlineNas{
					Path:          nillable.GetStringPtr(junctionPath),
					SecurityStyle: &securityStyle,
				},
				Svm: &models.VolumeInlineSvm{
					UUID: &svmUUID,
				},
			},
		}

		volRep := &VolumeReplication{
			SourceSVMName:      "source-svm",
			DestinationSVMName: "dest-svm",
			Volume: &Volume{
				ProtocolTypes: []string{"SMB"},
				JunctionPath:  junctionPath,
			},
		}

		// Setup mocks for CIFS share deletion - share not found
		notFoundError := errors.NewNotFoundErr("CIFS share", &shareName)
		mockClient.On("NAS").Return(mockNASClient)
		mockNASClient.On("CifsShareDelete", mock.MatchedBy(func(params *ontaprest.CifsShareDeleteParams) bool {
			return params.ShareName == shareName && params.SvmUUID == svmUUID
		})).Return(notFoundError)

		// Setup mocks for volume unmount
		jobAccepted := &ontaprest.JobAccepted{JobUUID: "unmount-job-123"}
		mockClient.On("Storage").Return(mockStorageClient)
		mockStorageClient.On("VolumeUnmount", mock.Anything).Return(jobAccepted, nil)

		// Execute test
		err := _unmountVolume(provider, &volume, volRep)

		// Verify results - should succeed even if share not found
		assert.NoError(tt, err)
		mockClient.AssertExpectations(tt)
		mockNASClient.AssertExpectations(tt)
		mockStorageClient.AssertExpectations(tt)
	})

	t.Run("Error_SMBVolume_CifsShareDeleteFails_NonNotFoundError", func(tt *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockNASClient := new(ontaprest.MockNASClient)

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}

		provider := &OntapRestProvider{
			ClientParams: ontaprest.RESTClientParams{},
			Logger:       log.NewLogger(),
		}

		volumeUUID := "test-volume-uuid-smb-error"
		svmUUID := "test-svm-uuid"
		junctionPath := "/test-creation-token"
		shareName := "test-creation-token"
		securityStyle := "unix"
		volume := ontaprest.Volume{
			Volume: models.Volume{
				UUID: &volumeUUID,
				Name: nillable.GetStringPtr("test-volume-smb-error"),
				Nas: &models.VolumeInlineNas{
					Path:          nillable.GetStringPtr(junctionPath),
					SecurityStyle: &securityStyle,
				},
				Svm: &models.VolumeInlineSvm{
					UUID: &svmUUID,
				},
			},
		}

		volRep := &VolumeReplication{
			SourceSVMName:      "source-svm",
			DestinationSVMName: "dest-svm",
			Volume: &Volume{
				ProtocolTypes: []string{"SMB"},
				JunctionPath:  junctionPath,
			},
		}

		// Setup mocks for CIFS share deletion - returns non-not-found error
		deleteError := errors.New("failed to delete CIFS share")
		mockClient.On("NAS").Return(mockNASClient)
		mockNASClient.On("CifsShareDelete", mock.MatchedBy(func(params *ontaprest.CifsShareDeleteParams) bool {
			return params.ShareName == shareName && params.SvmUUID == svmUUID
		})).Return(deleteError)

		// Execute test
		err := _unmountVolume(provider, &volume, volRep)

		// Verify results - should fail with error
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "failed to delete CIFS share")
		mockClient.AssertExpectations(tt)
		mockNASClient.AssertExpectations(tt)
	})
}

func TestVolume_HasProtocolType(t *testing.T) {
	vol := &Volume{
		ProtocolTypes: []string{"NFS", "ISCSI", "SMB"},
	}

	assert.True(t, vol.HasProtocolType("ISCSI"), "Should match protocol with exact case")
	assert.False(t, vol.HasProtocolType("http"), "Should not match non-existent protocol")
	assert.False(t, vol.HasProtocolType(""), "Should not match empty protocol")
}

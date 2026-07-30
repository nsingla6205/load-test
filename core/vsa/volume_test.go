package vsa

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/models"
	ontaprest "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/ontap-rest"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
)

func TestCreateVolume_Success(t *testing.T) {
	t.Run("TestCreateVolumesSuccess_WithoutAutoTieringConfig", func(t *testing.T) {
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("Storage").Return(mockStorage)
		originalGetOntapClientFunc := getOntapClientFunc
		defer func() {
			getOntapClientFunc = originalGetOntapClientFunc
		}()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		volumeName := "testVolume"
		volSpace := int64(1024)
		params := CreateVolumeParams{
			VolumeName:        volumeName,
			SvmName:           "testSVM",
			Aggregates:        []string{"testAggregate"},
			Size:              volSpace,
			VolumeType:        "rw",
			SnapshotDirectory: true,
		}

		mockJob := &ontaprest.JobAccepted{
			JobUUID:      "testJobUUID",
			ResourceUUID: "testResourceUUID",
		}
		mockVolume := &ontaprest.Volume{
			Volume: models.Volume{
				UUID: nillable.ToPointer("testUUID"),
				Name: &volumeName,
				Space: &models.VolumeInlineSpace{
					Available:                 &volSpace,
					SizeAvailableForSnapshots: nillable.GetInt64Ptr(1029202020),
				},
				Size:  nillable.GetInt64Ptr(1029202020),
				State: nillable.ToPointer(models.VolumeStateOnline),
			},
		}

		mockStorage.On("VolumeCreate", mock.Anything).Return(mockVolume, mockJob, nil)
		mockClient.On("Poll", mockJob.JobUUID).Return(nil)

		resp, err := rc.CreateVolume(params)

		assert.NoError(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, volumeName, resp.Name)
		assert.Equal(t, "testUUID", resp.ExternalUUID)
		assert.Equal(t, volSpace, resp.AvailableSpace)

		mockStorage.AssertExpectations(t)
		mockClient.AssertExpectations(t)
	})

	t.Run("TestCreateVolumesSuccess_WithQosPolicy", func(t *testing.T) {
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("Storage").Return(mockStorage)
		originalGetOntapClientFunc := getOntapClientFunc
		defer func() {
			getOntapClientFunc = originalGetOntapClientFunc
		}()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		volumeName := "testVolume"
		volSpace := int64(1024)
		qosPolicy := "qos-policy-1"
		params := CreateVolumeParams{
			VolumeName:        volumeName,
			SvmName:           "testSVM",
			Aggregates:        []string{"testAggregate"},
			Size:              volSpace,
			VolumeType:        "rw",
			SnapshotDirectory: true,
			QosPolicy:         &qosPolicy,
		}

		mockJob := &ontaprest.JobAccepted{
			JobUUID:      "testJobUUID",
			ResourceUUID: "testResourceUUID",
		}
		mockVolume := &ontaprest.Volume{
			Volume: models.Volume{
				UUID: nillable.ToPointer("testUUID"),
				Name: &volumeName,
				Space: &models.VolumeInlineSpace{
					Available: &volSpace,
				},
				Size:  nillable.GetInt64Ptr(1029202020),
				State: nillable.ToPointer(models.VolumeStateOnline),
			},
		}

		mockStorage.On("VolumeCreate", mock.MatchedBy(func(createParams *ontaprest.VolumeCreateParams) bool {
			return createParams != nil && createParams.QosPolicy == qosPolicy
		})).Return(mockVolume, mockJob, nil)
		mockClient.On("Poll", mockJob.JobUUID).Return(nil)

		resp, err := rc.CreateVolume(params)

		assert.NoError(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, volumeName, resp.Name)

		mockStorage.AssertExpectations(t)
		mockClient.AssertExpectations(t)
	})

	t.Run("TestCreateVolumesSuccess_WithAutoTieringConfig", func(t *testing.T) {
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("Storage").Return(mockStorage)
		originalGetOntapClientFunc := getOntapClientFunc
		defer func() {
			getOntapClientFunc = originalGetOntapClientFunc
		}()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		volumeName := "testVolume"
		volSpace := int64(1024)
		tieringPolicy := TieringPolicy{
			CoolnessPeriod:            30,
			CoolAccessRetrievalPolicy: "Default",
			CoolAccessTieringPolicy:   "auto",
		}
		params := CreateVolumeParams{
			VolumeName:    volumeName,
			SvmName:       "testSVM",
			Aggregates:    []string{"testAggregate"},
			Size:          volSpace,
			VolumeType:    "rw",
			TieringPolicy: &tieringPolicy,
		}

		mockJob := &ontaprest.JobAccepted{
			JobUUID:      "testJobUUID",
			ResourceUUID: "testResourceUUID",
		}
		mockVolume := &ontaprest.Volume{
			Volume: models.Volume{
				UUID: nillable.ToPointer("testUUID"),
				Name: &volumeName,
				Space: &models.VolumeInlineSpace{
					Available:                 &volSpace,
					SizeAvailableForSnapshots: nillable.GetInt64Ptr(1029202020),
				},
				Size:  nillable.GetInt64Ptr(1029202020),
				State: nillable.ToPointer(models.VolumeStateOnline),
			},
		}

		mockStorage.On("VolumeCreate", mock.Anything).Return(mockVolume, mockJob, nil)
		mockClient.On("Poll", mockJob.JobUUID).Return(nil)

		resp, err := rc.CreateVolume(params)

		assert.NoError(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, volumeName, resp.Name)
		assert.Equal(t, "testUUID", resp.ExternalUUID)
		assert.Equal(t, volSpace, resp.AvailableSpace)

		mockStorage.AssertExpectations(t)
		mockClient.AssertExpectations(t)
	})

	t.Run("TestCreateVolumesSuccess_WithSecurityStyle", func(t *testing.T) {
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("Storage").Return(mockStorage)
		originalGetOntapClientFunc := getOntapClientFunc
		defer func() {
			getOntapClientFunc = originalGetOntapClientFunc
		}()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		volumeName := "testVolume"
		volSpace := int64(1024)
		securityStyle := "unix"
		params := CreateVolumeParams{
			VolumeName:    volumeName,
			SvmName:       "testSVM",
			Aggregates:    []string{"testAggregate"},
			Size:          volSpace,
			VolumeType:    "rw",
			SecurityStyle: &securityStyle,
		}

		mockJob := &ontaprest.JobAccepted{
			JobUUID:      "testJobUUID",
			ResourceUUID: "testResourceUUID",
		}
		mockVolume := &ontaprest.Volume{
			Volume: models.Volume{
				UUID: nillable.ToPointer("testUUID"),
				Name: &volumeName,
				Space: &models.VolumeInlineSpace{
					Available:                 &volSpace,
					SizeAvailableForSnapshots: nillable.GetInt64Ptr(1029202020),
				},
				Size:  nillable.GetInt64Ptr(1029202020),
				State: nillable.ToPointer(models.VolumeStateOnline),
			},
		}

		mockStorage.On("VolumeCreate", mock.MatchedBy(func(p *ontaprest.VolumeCreateParams) bool {
			return p.SecurityStyle == securityStyle
		})).Return(mockVolume, mockJob, nil)
		mockClient.On("Poll", mockJob.JobUUID).Return(nil)

		resp, err := rc.CreateVolume(params)

		assert.NoError(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, volumeName, resp.Name)
		assert.Equal(t, "testUUID", resp.ExternalUUID)
		assert.Equal(t, volSpace, resp.AvailableSpace)

		mockStorage.AssertExpectations(t)
		mockClient.AssertExpectations(t)
	})

	t.Run("TestCreateVolumesSuccess_WithEmptySecurityStyle_ShouldNotSetSecurityStyle", func(t *testing.T) {
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("Storage").Return(mockStorage)
		originalGetOntapClientFunc := getOntapClientFunc
		defer func() {
			getOntapClientFunc = originalGetOntapClientFunc
		}()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		volumeName := "testVolume"
		volSpace := int64(1024)
		emptySecurityStyle := ""
		params := CreateVolumeParams{
			VolumeName:    volumeName,
			SvmName:       "testSVM",
			Aggregates:    []string{"testAggregate"},
			Size:          volSpace,
			VolumeType:    "rw",
			SecurityStyle: &emptySecurityStyle,
		}

		mockJob := &ontaprest.JobAccepted{
			JobUUID:      "testJobUUID",
			ResourceUUID: "testResourceUUID",
		}
		mockVolume := &ontaprest.Volume{
			Volume: models.Volume{
				UUID: nillable.ToPointer("testUUID"),
				Name: &volumeName,
				Space: &models.VolumeInlineSpace{
					Available:                 &volSpace,
					SizeAvailableForSnapshots: nillable.GetInt64Ptr(1029202020),
				},
				Size:  nillable.GetInt64Ptr(1029202020),
				State: nillable.ToPointer(models.VolumeStateOnline),
			},
		}

		// Verify that SecurityStyle is NOT set when it's an empty string (should remain at default empty value)
		mockStorage.On("VolumeCreate", mock.MatchedBy(func(p *ontaprest.VolumeCreateParams) bool {
			// SecurityStyle should be empty string (default) when params.SecurityStyle is a pointer to empty string
			return p.SecurityStyle == ""
		})).Return(mockVolume, mockJob, nil)
		mockClient.On("Poll", mockJob.JobUUID).Return(nil)

		resp, err := rc.CreateVolume(params)

		assert.NoError(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, volumeName, resp.Name)
		assert.Equal(t, "testUUID", resp.ExternalUUID)
		assert.Equal(t, volSpace, resp.AvailableSpace)

		mockStorage.AssertExpectations(t)
		mockClient.AssertExpectations(t)
	})

	t.Run("TestCreateVolumesSuccess_WithAfsTotalAndMetadata", func(t *testing.T) {
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("Storage").Return(mockStorage)
		originalGetOntapClientFunc := getOntapClientFunc
		defer func() {
			getOntapClientFunc = originalGetOntapClientFunc
		}()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		volumeName := "testVolume"
		volSpace := int64(1024)
		afsTotal := int64(2048)
		metadataSize := int64(512)
		params := CreateVolumeParams{
			VolumeName:        volumeName,
			SvmName:           "testSVM",
			Aggregates:        []string{"testAggregate"},
			Size:              volSpace,
			VolumeType:        "rw",
			SnapshotDirectory: true,
		}

		mockJob := &ontaprest.JobAccepted{
			JobUUID:      "testJobUUID",
			ResourceUUID: "testResourceUUID",
		}
		mockVolume := &ontaprest.Volume{
			Volume: models.Volume{
				UUID: nillable.ToPointer("testUUID"),
				Name: &volumeName,
				Space: &models.VolumeInlineSpace{
					Available:                 &volSpace,
					SizeAvailableForSnapshots: nillable.GetInt64Ptr(1029202020),
					AfsTotal:                  &afsTotal,
					Metadata:                  &metadataSize,
				},
				Size:  nillable.GetInt64Ptr(1029202020),
				State: nillable.ToPointer(models.VolumeStateOnline),
			},
		}

		mockStorage.On("VolumeCreate", mock.Anything).Return(mockVolume, mockJob, nil)
		mockClient.On("Poll", mockJob.JobUUID).Return(nil)

		resp, err := rc.CreateVolume(params)

		assert.NoError(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, volumeName, resp.Name)
		assert.Equal(t, "testUUID", resp.ExternalUUID)
		assert.Equal(t, volSpace, resp.AvailableSpace)
		assert.Equal(t, afsTotal, resp.AFSSize)
		assert.Equal(t, metadataSize, resp.MetadataSize)

		mockStorage.AssertExpectations(t)
		mockClient.AssertExpectations(t)
	})
}

func TestCreateVolume_ErrorOnCreate(t *testing.T) {
	mockStorage := new(ontaprest.MockStorageClient)
	mockClient := new(ontaprest.MockRESTClient)
	mockClient.On("Storage").Return(mockStorage)
	originalGetOntapClientFunc := getOntapClientFunc
	defer func() {
		getOntapClientFunc = originalGetOntapClientFunc
	}()
	getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
		return mockClient, nil
	}
	rc := &OntapRestProvider{}

	volumeName := "testVolume"
	params := CreateVolumeParams{
		VolumeName: volumeName,
		SvmName:    "testSVM",
		Aggregates: []string{"testAggregate"},
		Size:       int64(1024),
		VolumeType: "rw",
	}

	mockStorage.On("VolumeCreate", mock.Anything).Return(nil, nil, errors.New("creation error"))

	resp, err := rc.CreateVolume(params)

	assert.Error(t, err)
	assert.Nil(t, resp)

	mockStorage.AssertExpectations(t)
	mockClient.AssertExpectations(t)
}

func TestCreateVolume_DuplicateErrorOnCreate(t *testing.T) {
	mockStorage := new(ontaprest.MockStorageClient)
	mockClient := new(ontaprest.MockRESTClient)
	mockClient.On("Storage").Return(mockStorage)
	originalGetOntapClientFunc := getOntapClientFunc
	defer func() {
		getOntapClientFunc = originalGetOntapClientFunc
	}()
	getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
		return mockClient, nil
	}
	rc := &OntapRestProvider{}

	volumeName := "testVolume"
	params := CreateVolumeParams{
		VolumeName: volumeName,
		SvmName:    "testSVM",
		Aggregates: []string{"testAggregate"},
		Size:       int64(1024),
		VolumeType: "rw",
	}

	mockStorage.On("VolumeCreate", mock.Anything).Return(nil, nil, errors.New("Duplicate volume name"))

	resp, err := rc.CreateVolume(params)

	assert.Error(t, err)
	assert.Nil(t, resp)

	mockStorage.AssertExpectations(t)
	mockClient.AssertExpectations(t)
}

func TestCreateVolume_MaximumCloneHierarchyError(t *testing.T) {
	mockStorage := new(ontaprest.MockStorageClient)
	mockClient := new(ontaprest.MockRESTClient)
	mockClient.On("Storage").Return(mockStorage)
	originalGetOntapClientFunc := getOntapClientFunc
	defer func() {
		getOntapClientFunc = originalGetOntapClientFunc
	}()
	getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
		return mockClient, nil
	}
	rc := &OntapRestProvider{}

	volumeName := "testVolume"
	params := CreateVolumeParams{
		VolumeName: volumeName,
		SvmName:    "testSVM",
		Aggregates: []string{"testAggregate"},
		Size:       int64(1024),
		VolumeType: "rw",
	}

	mockStorage.On("VolumeCreate", mock.Anything).Return(nil, nil, errors.New("Maximum clone hierarchy limit exceeded"))

	resp, err := rc.CreateVolume(params)

	assert.Error(t, err)
	assert.Nil(t, resp)
	// Verify that the error is wrapped as a TemporalApplicationError with ErrNestedCloneLimitExceeded
	// Extract the CustomError from the TemporalApplicationError
	customErr := vsaerrors.ExtractCustomError(err)
	require.NotNil(t, customErr, "Expected CustomError to be extracted from TemporalApplicationError")
	assert.Equal(t, vsaerrors.ErrNestedCloneLimitExceeded, customErr.TrackingID)

	mockStorage.AssertExpectations(t)
	mockClient.AssertExpectations(t)
}

func TestCreateVolume_MaximumVolumeCountErrorOnCreate(t *testing.T) {
	mockStorage := new(ontaprest.MockStorageClient)
	mockClient := new(ontaprest.MockRESTClient)
	mockClient.On("Storage").Return(mockStorage)
	originalGetOntapClientFunc := getOntapClientFunc
	defer func() {
		getOntapClientFunc = originalGetOntapClientFunc
	}()
	getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
		return mockClient, nil
	}
	rc := &OntapRestProvider{}

	params := CreateVolumeParams{
		VolumeName: "testVolume",
		SvmName:    "testSVM",
		Aggregates: []string{"testAggregate"},
		Size:       int64(1024),
		VolumeType: "rw",
	}

	mockStorage.On("VolumeCreate", mock.Anything).Return(nil, nil, errors.New(
		`Failed to create the clone volume. Reason: MAXIMUM number of VOLUMES reached on NODE "gcnv-b8d3b61ed8432af-01"`))

	resp, err := rc.CreateVolume(params)

	assert.Error(t, err)
	assert.Nil(t, resp)
	customErr := vsaerrors.ExtractCustomError(err)
	require.NotNil(t, customErr, "Expected CustomError to be extracted from TemporalApplicationError")
	assert.Equal(t, vsaerrors.ErrMaximumVolumeCount, customErr.TrackingID)

	mockStorage.AssertExpectations(t)
	mockClient.AssertExpectations(t)
}

func TestCreateVolume_MaximumVolumeCountErrorOnPoll(t *testing.T) {
	mockStorage := new(ontaprest.MockStorageClient)
	mockClient := new(ontaprest.MockRESTClient)
	mockClient.On("Storage").Return(mockStorage)
	originalGetOntapClientFunc := getOntapClientFunc
	defer func() {
		getOntapClientFunc = originalGetOntapClientFunc
	}()
	getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
		return mockClient, nil
	}
	rc := &OntapRestProvider{}

	volumeName := "testVolume"
	params := CreateVolumeParams{
		VolumeName: volumeName,
		SvmName:    "testSVM",
		Aggregates: []string{"testAggregate"},
		Size:       int64(1024),
		VolumeType: "rw",
	}

	mockJob := &ontaprest.JobAccepted{
		JobUUID:      "testJobUUID",
		ResourceUUID: "testResourceUUID",
	}
	mockVolume := &ontaprest.Volume{
		Volume: models.Volume{
			UUID:  nillable.ToPointer("testUUID"),
			Name:  &volumeName,
			State: nillable.ToPointer("online"),
		},
	}

	mockStorage.On("VolumeCreate", mock.Anything).Return(mockVolume, mockJob, nil)
	mockClient.On("Poll", mockJob.JobUUID).Return(errors.New(
		`Job failed: Failed to create the clone volume. Reason: maximum NUMBER of volumes REACHED on node "gcnv-b8d3b61ed8432af-01"`))

	resp, err := rc.CreateVolume(params)

	assert.Error(t, err)
	assert.Nil(t, resp)
	customErr := vsaerrors.ExtractCustomError(err)
	require.NotNil(t, customErr, "Expected CustomError to be extracted from TemporalApplicationError")
	assert.Equal(t, vsaerrors.ErrMaximumVolumeCount, customErr.TrackingID)

	mockStorage.AssertExpectations(t)
	mockClient.AssertExpectations(t)
}

func TestCreateVolume_ParentVolumeNotFoundError(t *testing.T) {
	mockStorage := new(ontaprest.MockStorageClient)
	mockClient := new(ontaprest.MockRESTClient)
	mockClient.On("Storage").Return(mockStorage)
	originalgetOntapClientFunc := getOntapClientFunc
	defer func() {
		getOntapClientFunc = originalgetOntapClientFunc
	}()
	getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
		return mockClient, nil
	}
	rc := &OntapRestProvider{}

	volumeName := "testVolume"
	params := CreateVolumeParams{
		VolumeName: volumeName,
		SvmName:    "testSVM",
		Aggregates: []string{"testAggregate"},
		Size:       int64(1024),
		VolumeType: "rw",
		RestoreFromSnapshot: &RestoreFromSnapshotParams{
			ParentVolumeExternalUUID: "parent-vol-uuid",
			ParentVolumeName:         "parentvol2",
			SnapshotUUID:             "snapshot-uuid",
			SnapshotName:             "snapshot-name",
			ParentVolumeSvmName:      "gcnv-96a69cb72228dbd-svm-01",
		},
	}

	// ONTAP error format: "Volume \"parentvol2\" in SVM \"gcnv-96a69cb72228dbd-svm-01\" does not exist."
	mockStorage.On("VolumeCreate", mock.Anything).Return(nil, nil, errors.New("Volume \"parentvol2\" in SVM \"gcnv-96a69cb72228dbd-svm-01\" does not exist."))

	resp, err := rc.CreateVolume(params)

	assert.Error(t, err)
	assert.Nil(t, resp)
	// Verify that the error is wrapped as a TemporalApplicationError with ErrParentVolumeNotFound
	// Extract the CustomError from the TemporalApplicationError
	customErr := vsaerrors.ExtractCustomError(err)
	require.NotNil(t, customErr, "Expected CustomError to be extracted from TemporalApplicationError")
	assert.Equal(t, vsaerrors.ErrParentVolumeNotFound, customErr.TrackingID)
	// Verify the error message contains the expected user-friendly message
	assert.Contains(t, err.Error(), "Cannot create volume from snapshot: parent volume does not exist")

	mockStorage.AssertExpectations(t)
	mockClient.AssertExpectations(t)
}

func TestCreateVolume_ParentVolumeNotFoundError_WithoutRestoreFromSnapshot(t *testing.T) {
	mockStorage := new(ontaprest.MockStorageClient)
	mockClient := new(ontaprest.MockRESTClient)
	mockClient.On("Storage").Return(mockStorage)
	originalgetOntapClientFunc := getOntapClientFunc
	defer func() {
		getOntapClientFunc = originalgetOntapClientFunc
	}()
	getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
		return mockClient, nil
	}
	rc := &OntapRestProvider{}

	volumeName := "testVolume"
	params := CreateVolumeParams{
		VolumeName: volumeName,
		SvmName:    "testSVM",
		Aggregates: []string{"testAggregate"},
		Size:       int64(1024),
		VolumeType: "rw",
		// RestoreFromSnapshot is nil - should not trigger the error wrapping
	}

	// Even if ONTAP returns "Volume does not exist" error, it should not be wrapped
	// as ErrParentVolumeNotFound if RestoreFromSnapshot is nil
	mockStorage.On("VolumeCreate", mock.Anything).Return(nil, nil, errors.New("Volume \"somevol\" in SVM \"testSVM\" does not exist."))

	resp, err := rc.CreateVolume(params)

	assert.Error(t, err)
	assert.Nil(t, resp)
	// Verify that the error is NOT wrapped as ErrParentVolumeNotFound
	customErr := vsaerrors.ExtractCustomError(err)
	if customErr != nil {
		assert.NotEqual(t, vsaerrors.ErrParentVolumeNotFound, customErr.TrackingID, "Error should not be wrapped as ErrParentVolumeNotFound when RestoreFromSnapshot is nil")
	}

	mockStorage.AssertExpectations(t)
	mockClient.AssertExpectations(t)
}

func TestCreateVolume_HotTierCapacityExhaustedErrorOnCreate(t *testing.T) {
	mockStorage := new(ontaprest.MockStorageClient)
	mockClient := new(ontaprest.MockRESTClient)
	mockClient.On("Storage").Return(mockStorage)
	originalGetOntapClientFunc := getOntapClientFunc
	defer func() {
		getOntapClientFunc = originalGetOntapClientFunc
	}()
	getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
		return mockClient, nil
	}
	rc := &OntapRestProvider{}

	volumeName := "testVolume"
	params := CreateVolumeParams{
		VolumeName: volumeName,
		SvmName:    "testSVM",
		Aggregates: []string{"testAggregate"},
		Size:       int64(1024),
		VolumeType: "rw",
	}

	// ONTAP error format: "Request to create volume ... failed because there is not enough space in aggregate ..."
	mockStorage.On("VolumeCreate", mock.Anything).Return(nil, nil, errors.New("Request to create volume \"testVolume\" failed because there is not enough space in aggregate \"aggr1\". Either create 104MB of free space in the aggregate or select a size of at most 176MB for the new volume."))

	resp, err := rc.CreateVolume(params)

	assert.Error(t, err)
	assert.Nil(t, resp)
	// Verify that the error is wrapped as a TemporalApplicationError with ErrHotTierCapacityExhausted
	customErr := vsaerrors.ExtractCustomError(err)
	require.NotNil(t, customErr, "Expected CustomError to be extracted from TemporalApplicationError")
	assert.Equal(t, vsaerrors.ErrHotTierCapacityExhausted, customErr.TrackingID)
	// Verify the error message contains the expected user-friendly message
	assert.Contains(t, err.Error(), "hot tier")

	mockStorage.AssertExpectations(t)
	mockClient.AssertExpectations(t)
}

func TestCreateVolume_HotTierCapacityExhaustedErrorOnPoll(t *testing.T) {
	mockStorage := new(ontaprest.MockStorageClient)
	mockClient := new(ontaprest.MockRESTClient)
	mockClient.On("Storage").Return(mockStorage)
	originalGetOntapClientFunc := getOntapClientFunc
	defer func() {
		getOntapClientFunc = originalGetOntapClientFunc
	}()
	getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
		return mockClient, nil
	}
	rc := &OntapRestProvider{}

	volumeName := "testVolume"
	params := CreateVolumeParams{
		VolumeName: volumeName,
		SvmName:    "testSVM",
		Aggregates: []string{"testAggregate"},
		Size:       int64(1024),
		VolumeType: "rw",
	}

	mockJob := &ontaprest.JobAccepted{
		JobUUID:      "testJobUUID",
		ResourceUUID: "testResourceUUID",
	}
	mockVolume := &ontaprest.Volume{
		Volume: models.Volume{
			UUID: nillable.ToPointer("testUUID"),
			Name: &volumeName,
		},
	}

	// ONTAP error format from job: "Job completed with Error: Failed to create the volume on node ... Reason: Request to create volume ... failed because there is not enough space in aggregate ..."
	mockStorage.On("VolumeCreate", mock.Anything).Return(mockVolume, mockJob, nil)
	mockClient.On("Poll", mockJob.JobUUID).Return(errors.New("Job completed with Error: Failed to create the volume on node \"gcnv-10bf922142bb715-01\". Reason: Request to create volume \"testVolume\" failed because there is not enough space in aggregate \"aggr1\". Either create 104MB of free space in the aggregate or select a size of at most 176MB for the new volume."))

	resp, err := rc.CreateVolume(params)

	assert.Error(t, err)
	assert.Nil(t, resp)
	// Verify that the error is wrapped as a TemporalApplicationError with ErrHotTierCapacityExhausted
	customErr := vsaerrors.ExtractCustomError(err)
	require.NotNil(t, customErr, "Expected CustomError to be extracted from TemporalApplicationError")
	assert.Equal(t, vsaerrors.ErrHotTierCapacityExhausted, customErr.TrackingID)
	// Verify the error message contains the expected user-friendly message
	assert.Contains(t, err.Error(), "hot tier")

	mockStorage.AssertExpectations(t)
	mockClient.AssertExpectations(t)
}

func TestCreateVolume_HotTierCapacityExhaustedErrorWithInsufficientSpace(t *testing.T) {
	mockStorage := new(ontaprest.MockStorageClient)
	mockClient := new(ontaprest.MockRESTClient)
	mockClient.On("Storage").Return(mockStorage)
	originalGetOntapClientFunc := getOntapClientFunc
	defer func() {
		getOntapClientFunc = originalGetOntapClientFunc
	}()
	getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
		return mockClient, nil
	}
	rc := &OntapRestProvider{}

	volumeName := "testVolume"
	params := CreateVolumeParams{
		VolumeName: volumeName,
		SvmName:    "testSVM",
		Aggregates: []string{"testAggregate"},
		Size:       int64(1024),
		VolumeType: "rw",
	}

	// Test with "insufficient space" variant
	mockStorage.On("VolumeCreate", mock.Anything).Return(nil, nil, errors.New("insufficient space in aggregate to create volume"))

	resp, err := rc.CreateVolume(params)

	assert.Error(t, err)
	assert.Nil(t, resp)
	// Verify that the error is wrapped as a TemporalApplicationError with ErrHotTierCapacityExhausted
	customErr := vsaerrors.ExtractCustomError(err)
	require.NotNil(t, customErr, "Expected CustomError to be extracted from TemporalApplicationError")
	assert.Equal(t, vsaerrors.ErrHotTierCapacityExhausted, customErr.TrackingID)
	// Verify the error message contains the expected user-friendly message
	assert.Contains(t, err.Error(), "hot tier")

	mockStorage.AssertExpectations(t)
	mockClient.AssertExpectations(t)
}

func TestCreateVolume_ErrorOnNilResponse(t *testing.T) {
	mockStorage := new(ontaprest.MockStorageClient)
	mockClient := new(ontaprest.MockRESTClient)
	mockClient.On("Storage").Return(mockStorage)
	originalGetOntapClientFunc := getOntapClientFunc
	defer func() {
		getOntapClientFunc = originalGetOntapClientFunc
	}()
	getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
		return mockClient, nil
	}
	rc := &OntapRestProvider{}

	volumeName := "testVolume"
	params := CreateVolumeParams{
		VolumeName: volumeName,
		SvmName:    "testSVM",
		Aggregates: []string{"testAggregate"},
		Size:       int64(1024),
		VolumeType: "rw",
	}

	mockStorage.On("VolumeCreate", mock.Anything).Return(nil, nil, nil)

	resp, err := rc.CreateVolume(params)

	assert.Error(t, err)
	assert.Nil(t, resp)
	assert.Equal(t, "invalid Volume response from API: volume is nil", err.Error())

	mockStorage.AssertExpectations(t)
	mockClient.AssertExpectations(t)
}

func TestCreateVolume_ErrorOnPoll(t *testing.T) {
	mockStorage := new(ontaprest.MockStorageClient)
	mockClient := new(ontaprest.MockRESTClient)
	mockClient.On("Storage").Return(mockStorage)
	originalGetOntapClientFunc := getOntapClientFunc
	defer func() {
		getOntapClientFunc = originalGetOntapClientFunc
	}()
	getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
		return mockClient, nil
	}
	rc := &OntapRestProvider{}

	volumeName := "testVolume"
	params := CreateVolumeParams{
		VolumeName: volumeName,
		SvmName:    "testSVM",
		Aggregates: []string{"testAggregate"},
		Size:       int64(1024),
		VolumeType: "rw",
	}

	mockJob := &ontaprest.JobAccepted{
		JobUUID:      "testJobUUID",
		ResourceUUID: "testResourceUUID",
	}
	mockVolume := &ontaprest.Volume{
		Volume: models.Volume{
			UUID: nillable.ToPointer("testUUID"),
			Name: &volumeName,
		},
	}

	mockStorage.On("VolumeCreate", mock.Anything).Return(mockVolume, mockJob, nil)
	mockClient.On("Poll", mockJob.JobUUID).Return(errors.New("polling error"))

	resp, err := rc.CreateVolume(params)

	assert.Error(t, err)
	assert.Nil(t, resp)

	mockStorage.AssertExpectations(t)
	mockClient.AssertExpectations(t)
}

func TestCreateVolumesFailure_getOntapClientFuncError(t *testing.T) {
	originalGetOntapClientFunc := getOntapClientFunc
	defer func() {
		getOntapClientFunc = originalGetOntapClientFunc
	}()
	getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
		return nil, errors.New("getOntapClientFunc error")
	}
	rc := &OntapRestProvider{}

	volumeName := "testVolume"
	volSpace := int64(1024)
	tieringPolicy := TieringPolicy{
		CoolnessPeriod:            30,
		CoolAccessRetrievalPolicy: "Default",
		CoolAccessTieringPolicy:   "auto",
	}
	params := CreateVolumeParams{
		VolumeName:    volumeName,
		SvmName:       "testSVM",
		Aggregates:    []string{"testAggregate"},
		Size:          volSpace,
		VolumeType:    "rw",
		TieringPolicy: &tieringPolicy,
	}

	resp, err := rc.CreateVolume(params)

	assert.Error(t, err)
	assert.Nil(t, resp)
	assert.Equal(t, "getOntapClientFunc error", err.Error())
}

func TestCreateVolume_NilSpaceHandling(t *testing.T) {
	t.Run("TestCreateVolume_WithNilSpace", func(t *testing.T) {
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("Storage").Return(mockStorage)
		originalGetOntapClientFunc := getOntapClientFunc
		defer func() {
			getOntapClientFunc = originalGetOntapClientFunc
		}()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		volumeName := "testVolume"
		params := CreateVolumeParams{
			VolumeName: volumeName,
			SvmName:    "testSVM",
			Aggregates: []string{"testAggregate"},
			Size:       int64(1024),
			VolumeType: "rw",
		}

		mockJob := &ontaprest.JobAccepted{
			JobUUID:      "testJobUUID",
			ResourceUUID: "testResourceUUID",
		}
		// Mock volume with nil Space (like FlexGroup volumes with large number of constituents)
		mockVolume := &ontaprest.Volume{
			Volume: models.Volume{
				UUID:  nillable.ToPointer("testUUID"),
				Name:  &volumeName,
				Space: nil, // Nil space to test the nil pointer check
				State: nillable.ToPointer(models.VolumeStateOnline),
			},
		}

		mockStorage.On("VolumeCreate", mock.Anything).Return(mockVolume, mockJob, nil)
		mockClient.On("Poll", mockJob.JobUUID).Return(nil)

		resp, err := rc.CreateVolume(params)

		assert.NoError(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, volumeName, resp.Name)
		assert.Equal(t, "testUUID", resp.ExternalUUID)
		assert.Equal(t, int64(0), resp.AvailableSpace) // Should be 0 when Space is nil

		mockStorage.AssertExpectations(t)
		mockClient.AssertExpectations(t)
	})

	t.Run("TestCreateVolume_WithNilAvailableSpace", func(t *testing.T) {
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("Storage").Return(mockStorage)
		originalGetOntapClientFunc := getOntapClientFunc
		defer func() {
			getOntapClientFunc = originalGetOntapClientFunc
		}()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		volumeName := "testVolume"
		params := CreateVolumeParams{
			VolumeName: volumeName,
			SvmName:    "testSVM",
			Aggregates: []string{"testAggregate"},
			Size:       int64(1024),
			VolumeType: "rw",
		}

		mockJob := &ontaprest.JobAccepted{
			JobUUID:      "testJobUUID",
			ResourceUUID: "testResourceUUID",
		}
		// Mock volume with Space but nil Available
		mockVolume := &ontaprest.Volume{
			Volume: models.Volume{
				UUID: nillable.ToPointer("testUUID"),
				Name: &volumeName,
				Space: &models.VolumeInlineSpace{
					Available: nil, // Nil Available to test the nil pointer check
				},
				State: nillable.ToPointer(models.VolumeStateOnline),
			},
		}

		mockStorage.On("VolumeCreate", mock.Anything).Return(mockVolume, mockJob, nil)
		mockClient.On("Poll", mockJob.JobUUID).Return(nil)

		resp, err := rc.CreateVolume(params)

		assert.NoError(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, volumeName, resp.Name)
		assert.Equal(t, "testUUID", resp.ExternalUUID)
		assert.Equal(t, int64(0), resp.AvailableSpace) // Should be 0 when Available is nil

		mockStorage.AssertExpectations(t)
		mockClient.AssertExpectations(t)
	})
}

func TestCreateVolume_ResponseValidation(t *testing.T) {
	t.Run("WhenVolumeResponseIsNil_ThenReturnError", func(t *testing.T) {
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("Storage").Return(mockStorage)
		originalgetOntapClientFunc := getOntapClientFunc
		defer func() {
			getOntapClientFunc = originalgetOntapClientFunc
		}()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		volumeName := "testVolume"
		params := CreateVolumeParams{
			VolumeName: volumeName,
			SvmName:    "testSVM",
			Aggregates: []string{"testAggregate"},
			Size:       int64(1024),
			VolumeType: "rw",
		}

		mockJob := &ontaprest.JobAccepted{
			JobUUID:      "testJobUUID",
			ResourceUUID: "testResourceUUID",
		}

		mockStorage.On("VolumeCreate", mock.Anything).Return(nil, mockJob, nil)
		mockClient.On("Poll", mockJob.JobUUID).Return(nil)

		resp, err := rc.CreateVolume(params)

		assert.Error(t, err)
		assert.Nil(t, resp)
		assert.Equal(t, "invalid Volume response from API: volume is nil", err.Error())

		mockStorage.AssertExpectations(t)
		mockClient.AssertExpectations(t)
	})

	t.Run("WhenVolumeNameIsNil_ThenReturnError", func(t *testing.T) {
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("Storage").Return(mockStorage)
		originalgetOntapClientFunc := getOntapClientFunc
		defer func() {
			getOntapClientFunc = originalgetOntapClientFunc
		}()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		volumeName := "testVolume"
		params := CreateVolumeParams{
			VolumeName: volumeName,
			SvmName:    "testSVM",
			Aggregates: []string{"testAggregate"},
			Size:       int64(1024),
			VolumeType: "rw",
		}

		mockJob := &ontaprest.JobAccepted{
			JobUUID:      "testJobUUID",
			ResourceUUID: "testResourceUUID",
		}

		// Mock volume with nil Name
		mockVolume := &ontaprest.Volume{
			Volume: models.Volume{
				UUID:  nillable.ToPointer("testUUID"),
				Name:  nil, // Nil Name to test the nil pointer check
				State: nillable.ToPointer(models.VolumeStateOnline),
			},
		}

		mockStorage.On("VolumeCreate", mock.Anything).Return(mockVolume, mockJob, nil)
		mockClient.On("Poll", mockJob.JobUUID).Return(nil)

		resp, err := rc.CreateVolume(params)

		assert.Error(t, err)
		assert.Nil(t, resp)
		assert.Equal(t, "invalid Volume response from API: name is nil", err.Error())

		mockStorage.AssertExpectations(t)
		mockClient.AssertExpectations(t)
	})

	t.Run("WhenVolumeUUIDIsNil_ThenReturnError", func(t *testing.T) {
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("Storage").Return(mockStorage)
		originalgetOntapClientFunc := getOntapClientFunc
		defer func() {
			getOntapClientFunc = originalgetOntapClientFunc
		}()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		volumeName := "testVolume"
		params := CreateVolumeParams{
			VolumeName: volumeName,
			SvmName:    "testSVM",
			Aggregates: []string{"testAggregate"},
			Size:       int64(1024),
			VolumeType: "rw",
		}

		mockJob := &ontaprest.JobAccepted{
			JobUUID:      "testJobUUID",
			ResourceUUID: "testResourceUUID",
		}

		// Mock volume with nil UUID
		mockVolume := &ontaprest.Volume{
			Volume: models.Volume{
				UUID:  nil, // Nil UUID to test the nil pointer check
				Name:  &volumeName,
				State: nillable.ToPointer(models.VolumeStateOnline),
			},
		}

		mockStorage.On("VolumeCreate", mock.Anything).Return(mockVolume, mockJob, nil)
		mockClient.On("Poll", mockJob.JobUUID).Return(nil)

		resp, err := rc.CreateVolume(params)

		assert.Error(t, err)
		assert.Nil(t, resp)
		assert.Equal(t, "invalid Volume response from API: UUID is nil", err.Error())

		mockStorage.AssertExpectations(t)
		mockClient.AssertExpectations(t)
	})

	t.Run("WhenVolumeStateIsNil_ThenRetryWithVolumeGet", func(t *testing.T) {
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("Storage").Return(mockStorage)
		originalgetOntapClientFunc := getOntapClientFunc
		defer func() {
			getOntapClientFunc = originalgetOntapClientFunc
		}()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		volumeName := "testVolume"
		volumeUUID := "testUUID"
		volSpace := int64(1024)
		params := CreateVolumeParams{
			VolumeName: volumeName,
			SvmName:    "testSVM",
			Aggregates: []string{"testAggregate"},
			Size:       volSpace,
			VolumeType: "rw",
		}

		mockJob := &ontaprest.JobAccepted{
			JobUUID:      "testJobUUID",
			ResourceUUID: "testResourceUUID",
		}

		// Mock volume with nil State (typical for large FlexGroup volumes with many constituents)
		mockVolumeCreate := &ontaprest.Volume{
			Volume: models.Volume{
				UUID: nillable.ToPointer(volumeUUID),
				Name: &volumeName,
				Space: &models.VolumeInlineSpace{
					Available: &volSpace,
				},
				Size:  nillable.GetInt64Ptr(volSpace),
				State: nil, // Nil State triggers the redundant GET call
			},
		}

		// Mock the successful VolumeGet response with state populated
		mockVolumeGet := &ontaprest.Volume{
			Volume: models.Volume{
				UUID: nillable.ToPointer(volumeUUID),
				Name: &volumeName,
				Space: &models.VolumeInlineSpace{
					Available: &volSpace,
				},
				Size:  nillable.GetInt64Ptr(volSpace),
				State: nillable.ToPointer(models.VolumeStateOnline),
			},
		}

		mockStorage.On("VolumeCreate", mock.Anything).Return(mockVolumeCreate, mockJob, nil)
		mockClient.On("Poll", mockJob.JobUUID).Return(nil)
		mockStorage.On("VolumeGet", mock.MatchedBy(func(p *ontaprest.VolumeGetParams) bool {
			return p.UUID == volumeUUID && p.Name == volumeName
		})).Return(mockVolumeGet, nil)

		resp, err := rc.CreateVolume(params)

		assert.NoError(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, volumeName, resp.Name)
		assert.Equal(t, volumeUUID, resp.ExternalUUID)
		assert.Equal(t, models.VolumeStateOnline, resp.State)

		mockStorage.AssertExpectations(t)
		mockClient.AssertExpectations(t)
	})

	t.Run("WhenVolumeStateIsNil_AndVolumeGetReturnsError_ThenReturnError", func(t *testing.T) {
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("Storage").Return(mockStorage)
		originalgetOntapClientFunc := getOntapClientFunc
		defer func() {
			getOntapClientFunc = originalgetOntapClientFunc
		}()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		volumeName := "testVolume"
		volumeUUID := "testUUID"
		volSpace := int64(1024)
		params := CreateVolumeParams{
			VolumeName: volumeName,
			SvmName:    "testSVM",
			Aggregates: []string{"testAggregate"},
			Size:       volSpace,
			VolumeType: "rw",
		}

		mockJob := &ontaprest.JobAccepted{
			JobUUID:      "testJobUUID",
			ResourceUUID: "testResourceUUID",
		}

		// Mock volume with nil State
		mockVolumeCreate := &ontaprest.Volume{
			Volume: models.Volume{
				UUID: nillable.ToPointer(volumeUUID),
				Name: &volumeName,
				Space: &models.VolumeInlineSpace{
					Available: &volSpace,
				},
				Size:  nillable.GetInt64Ptr(volSpace),
				State: nil, // Nil State triggers the redundant GET call
			},
		}

		mockStorage.On("VolumeCreate", mock.Anything).Return(mockVolumeCreate, mockJob, nil)
		mockClient.On("Poll", mockJob.JobUUID).Return(nil)
		mockStorage.On("VolumeGet", mock.MatchedBy(func(p *ontaprest.VolumeGetParams) bool {
			return p.UUID == volumeUUID && p.Name == volumeName
		})).Return(nil, errors.New("VolumeGet failed"))

		resp, err := rc.CreateVolume(params)

		assert.Error(t, err)
		assert.Nil(t, resp)
		assert.Equal(t, "VolumeGet failed", err.Error())

		mockStorage.AssertExpectations(t)
		mockClient.AssertExpectations(t)
	})

	t.Run("WhenVolumeStateIsNil_AndVolumeGetReturnsNilVolume_ThenReturnError", func(t *testing.T) {
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("Storage").Return(mockStorage)
		originalgetOntapClientFunc := getOntapClientFunc
		defer func() {
			getOntapClientFunc = originalgetOntapClientFunc
		}()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		volumeName := "testVolume"
		volumeUUID := "testUUID"
		volSpace := int64(1024)
		params := CreateVolumeParams{
			VolumeName: volumeName,
			SvmName:    "testSVM",
			Aggregates: []string{"testAggregate"},
			Size:       volSpace,
			VolumeType: "rw",
		}

		mockJob := &ontaprest.JobAccepted{
			JobUUID:      "testJobUUID",
			ResourceUUID: "testResourceUUID",
		}

		// Mock volume with nil State
		mockVolumeCreate := &ontaprest.Volume{
			Volume: models.Volume{
				UUID: nillable.ToPointer(volumeUUID),
				Name: &volumeName,
				Space: &models.VolumeInlineSpace{
					Available: &volSpace,
				},
				Size:  nillable.GetInt64Ptr(volSpace),
				State: nil, // Nil State triggers the redundant GET call
			},
		}

		mockStorage.On("VolumeCreate", mock.Anything).Return(mockVolumeCreate, mockJob, nil)
		mockClient.On("Poll", mockJob.JobUUID).Return(nil)
		mockStorage.On("VolumeGet", mock.MatchedBy(func(p *ontaprest.VolumeGetParams) bool {
			return p.UUID == volumeUUID && p.Name == volumeName
		})).Return(nil, nil)

		resp, err := rc.CreateVolume(params)

		assert.Error(t, err)
		assert.Nil(t, resp)
		assert.Equal(t, "invalid Volume response from API: state is nil", err.Error())

		mockStorage.AssertExpectations(t)
		mockClient.AssertExpectations(t)
	})

	t.Run("WhenVolumeStateIsNil_AndVolumeGetReturnsNilState_ThenReturnError", func(t *testing.T) {
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("Storage").Return(mockStorage)
		originalgetOntapClientFunc := getOntapClientFunc
		defer func() {
			getOntapClientFunc = originalgetOntapClientFunc
		}()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		volumeName := "testVolume"
		volumeUUID := "testUUID"
		volSpace := int64(1024)
		params := CreateVolumeParams{
			VolumeName: volumeName,
			SvmName:    "testSVM",
			Aggregates: []string{"testAggregate"},
			Size:       volSpace,
			VolumeType: "rw",
		}

		mockJob := &ontaprest.JobAccepted{
			JobUUID:      "testJobUUID",
			ResourceUUID: "testResourceUUID",
		}

		// Mock volume with nil State
		mockVolumeCreate := &ontaprest.Volume{
			Volume: models.Volume{
				UUID: nillable.ToPointer(volumeUUID),
				Name: &volumeName,
				Space: &models.VolumeInlineSpace{
					Available: &volSpace,
				},
				Size:  nillable.GetInt64Ptr(volSpace),
				State: nil, // Nil State triggers the redundant GET call
			},
		}

		// Mock VolumeGet returning volume with nil State
		mockVolumeGet := &ontaprest.Volume{
			Volume: models.Volume{
				UUID: nillable.ToPointer(volumeUUID),
				Name: &volumeName,
				Space: &models.VolumeInlineSpace{
					Available: &volSpace,
				},
				Size:  nillable.GetInt64Ptr(volSpace),
				State: nil, // Still nil State
			},
		}

		mockStorage.On("VolumeCreate", mock.Anything).Return(mockVolumeCreate, mockJob, nil)
		mockClient.On("Poll", mockJob.JobUUID).Return(nil)
		mockStorage.On("VolumeGet", mock.MatchedBy(func(p *ontaprest.VolumeGetParams) bool {
			return p.UUID == volumeUUID && p.Name == volumeName
		})).Return(mockVolumeGet, nil)

		resp, err := rc.CreateVolume(params)

		assert.Error(t, err)
		assert.Nil(t, resp)
		assert.Equal(t, "invalid Volume response from API: state is nil", err.Error())

		mockStorage.AssertExpectations(t)
		mockClient.AssertExpectations(t)
	})
}

func TestDeleteVolume_Success(t *testing.T) {
	mockStorage := new(ontaprest.MockStorageClient)
	mockClient := new(ontaprest.MockRESTClient)
	mockClient.On("Storage").Return(mockStorage)
	originalGetOntapClientFunc := getOntapClientFunc
	defer func() {
		getOntapClientFunc = originalGetOntapClientFunc
	}()
	getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
		return mockClient, nil
	}
	rc := &OntapRestProvider{}

	volumeUUID := "testUUID"
	volumeName := "testVolume"

	// Mock VolumeGet to return a volume with no clones or split state
	mockVolume := &ontaprest.Volume{
		Volume: models.Volume{
			UUID: &volumeUUID,
			Name: &volumeName,
		},
	}
	// The Clone and Snapmirror fields are nil by default, which means no clones or snapmirror protection
	mockStorage.On("VolumeGet", mock.Anything).Return(mockVolume, nil)

	// Mock VolumeDelete to succeed (synchronous - no job returned)
	mockStorage.On("VolumeDelete", mock.Anything).Return(nil, nil)

	err := rc.DeleteVolume(volumeUUID, volumeName)

	assert.NoError(t, err)

	mockStorage.AssertExpectations(t)
	mockClient.AssertExpectations(t)
}

func TestDeleteVolume_Success_Async(t *testing.T) {
	mockStorage := new(ontaprest.MockStorageClient)
	mockClient := new(ontaprest.MockRESTClient)
	mockClient.On("Storage").Return(mockStorage)
	originalGetOntapClientFunc := getOntapClientFunc
	defer func() {
		getOntapClientFunc = originalGetOntapClientFunc
	}()
	getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
		return mockClient, nil
	}
	rc := &OntapRestProvider{}

	volumeUUID := "testUUID"
	volumeName := "testVolume"

	// Mock VolumeGet to return a volume with no clones or split state
	mockVolume := &ontaprest.Volume{
		Volume: models.Volume{
			UUID: &volumeUUID,
			Name: &volumeName,
		},
	}
	mockStorage.On("VolumeGet", mock.Anything).Return(mockVolume, nil)

	// Mock VolumeDelete to return a job (async deletion)
	mockJob := &ontaprest.JobAccepted{JobUUID: "job-uuid-123"}
	mockStorage.On("VolumeDelete", mock.Anything).Return(mockJob, nil)
	mockClient.On("Poll", "job-uuid-123").Return(nil)

	err := rc.DeleteVolume(volumeUUID, volumeName)

	assert.NoError(t, err)

	mockStorage.AssertExpectations(t)
	mockClient.AssertExpectations(t)
}

func TestDeleteVolume_Error(t *testing.T) {
	mockStorage := new(ontaprest.MockStorageClient)
	mockClient := new(ontaprest.MockRESTClient)
	mockClient.On("Storage").Return(mockStorage)
	originalGetOntapClientFunc := getOntapClientFunc
	defer func() {
		getOntapClientFunc = originalGetOntapClientFunc
	}()
	getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
		return mockClient, nil
	}
	rc := &OntapRestProvider{}

	volumeUUID := "testUUID"
	volumeName := "testVolume"

	// Mock VolumeGet to return a volume with no clones or split state
	mockVolume := &ontaprest.Volume{
		Volume: models.Volume{
			UUID: &volumeUUID,
			Name: &volumeName,
		},
	}
	mockStorage.On("VolumeGet", mock.Anything).Return(mockVolume, nil)

	// Mock VolumeDelete to fail
	mockStorage.On("VolumeDelete", mock.Anything).Return(nil, errors.New("deletion error"))

	err := rc.DeleteVolume(volumeUUID, volumeName)

	assert.Error(t, err)

	mockStorage.AssertExpectations(t)
	mockClient.AssertExpectations(t)
}

func TestDeleteVolume_Error_getOntapClientFuncError(t *testing.T) {
	originalGetOntapClientFunc := getOntapClientFunc
	defer func() {
		getOntapClientFunc = originalGetOntapClientFunc
	}()
	getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
		return nil, errors.New("getOntapClientFunc error")
	}
	rc := &OntapRestProvider{}

	volumeUUID := "testUUID"
	volumeName := "testVolume"

	err := rc.DeleteVolume(volumeUUID, volumeName)

	assert.Error(t, err)
	assert.Equal(t, "getOntapClientFunc error", err.Error())
}

func TestDeleteVolume_ErrorWhenVolumeHasClones(t *testing.T) {
	mockStorage := new(ontaprest.MockStorageClient)
	mockClient := new(ontaprest.MockRESTClient)
	mockClient.On("Storage").Return(mockStorage)
	originalGetOntapClientFunc := getOntapClientFunc
	defer func() {
		getOntapClientFunc = originalGetOntapClientFunc
	}()
	getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
		return mockClient, nil
	}
	rc := &OntapRestProvider{}

	volumeUUID := "testUUID"
	volumeName := "testVolume"

	// Mock VolumeGet to return a volume with clones
	hasFlexclone := true
	mockVolume := &ontaprest.Volume{
		Volume: models.Volume{
			UUID: &volumeUUID,
			Name: &volumeName,
			Clone: &models.VolumeInlineClone{
				HasFlexclone: &hasFlexclone,
			},
		},
	}
	mockStorage.On("VolumeGet", mock.Anything).Return(mockVolume, nil)

	err := rc.DeleteVolume(volumeUUID, volumeName)

	assert.Error(t, err)
	assertErrContains(t, err, "Cannot delete a volume that is being actively used in a Volume Replication relationship or a file clone split triggered by Snapshot RestoreFiles operation or used as a reference snapshot for a backup")
}

func TestDeleteVolume_ErrorWhenVolumeHasSplitInitiated(t *testing.T) {
	mockStorage := new(ontaprest.MockStorageClient)
	mockClient := new(ontaprest.MockRESTClient)
	mockClient.On("Storage").Return(mockStorage)
	originalGetOntapClientFunc := getOntapClientFunc
	defer func() {
		getOntapClientFunc = originalGetOntapClientFunc
	}()
	getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
		return mockClient, nil
	}
	rc := &OntapRestProvider{}

	volumeUUID := "testUUID"
	volumeName := "testVolume"

	// Mock VolumeGet to return a volume with split initiated
	splitInitiated := true
	mockVolume := &ontaprest.Volume{
		Volume: models.Volume{
			UUID: &volumeUUID,
			Name: &volumeName,
			Clone: &models.VolumeInlineClone{
				SplitInitiated: &splitInitiated,
			},
		},
	}
	mockStorage.On("VolumeGet", mock.Anything).Return(mockVolume, nil)

	err := rc.DeleteVolume(volumeUUID, volumeName)

	assert.Error(t, err)
	assertErrContains(t, err, "Cannot delete a volume that is being actively used in a Volume Replication relationship or a file clone split triggered by Snapshot RestoreFiles operation or used as a reference snapshot for a backup")
}

func TestDeleteVolume_ErrorWhenVolumeGetFails(t *testing.T) {
	mockStorage := new(ontaprest.MockStorageClient)
	mockClient := new(ontaprest.MockRESTClient)
	mockClient.On("Storage").Return(mockStorage)
	originalGetOntapClientFunc := getOntapClientFunc
	defer func() {
		getOntapClientFunc = originalGetOntapClientFunc
	}()
	getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
		return mockClient, nil
	}
	rc := &OntapRestProvider{}

	volumeUUID := "testUUID"
	volumeName := "testVolume"

	// Mock VolumeGet to fail
	mockStorage.On("VolumeGet", mock.Anything).Return(nil, errors.New("volume get error"))

	err := rc.DeleteVolume(volumeUUID, volumeName)

	assert.Error(t, err)
	assert.Equal(t, "volume get error", err.Error())
}

func TestGetVolume_WhenVolumeIsFound_ThenReturnVolumeResponse(t *testing.T) {
	mockStorage := new(ontaprest.MockStorageClient)
	mockClient := new(ontaprest.MockRESTClient)
	mockClient.On("Storage").Return(mockStorage)
	originalGetOntapClientFunc := getOntapClientFunc
	defer func() {
		getOntapClientFunc = originalGetOntapClientFunc
	}()
	getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
		return mockClient, nil
	}
	rc := &OntapRestProvider{}

	volumeName := "testVolume"
	volumeUUID := "testUUID"
	svmName := "testSVM"
	mockVolume := &ontaprest.Volume{
		Volume: models.Volume{
			UUID: nillable.ToPointer(volumeUUID),
			Name: &volumeName,
			Space: &models.VolumeInlineSpace{
				Available: nillable.GetInt64Ptr(100000), // Example available space
				Size:      nillable.GetInt64Ptr(200000), // Example total size
				LogicalSpace: &models.VolumeInlineSpaceInlineLogicalSpace{
					Used: nillable.GetInt64Ptr(100000),
				},
			},
			State: nillable.ToPointer(models.VolumeStateOnline),
			SnapshotPolicy: &models.VolumeInlineSnapshotPolicy{
				Name: nillable.GetStringPtr("none"), // Example snapshot policy
			},
			SnapshotDirectoryAccessEnabled: nillable.GetBoolPtr(false), // Add this field
		},
	}
	mockStorage.On("VolumeGet", mock.Anything).Return(mockVolume, nil)

	params := GetVolumeParams{
		UUID:       volumeUUID,
		VolumeName: volumeName,
		SvmName:    svmName,
	}
	resp, err := rc.GetVolume(params)

	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, volumeName, resp.Name)
	assert.Equal(t, volumeUUID, resp.ExternalUUID)

	mockStorage.AssertExpectations(t)
	mockClient.AssertExpectations(t)
}

func TestGetVolume_WhenVolumeGetReturnsError_ThenReturnError(t *testing.T) {
	mockStorage := new(ontaprest.MockStorageClient)
	mockClient := new(ontaprest.MockRESTClient)
	mockClient.On("Storage").Return(mockStorage)
	originalGetOntapClientFunc := getOntapClientFunc
	defer func() {
		getOntapClientFunc = originalGetOntapClientFunc
	}()
	getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
		return mockClient, nil
	}
	rc := &OntapRestProvider{}

	volumeName := "testVolume"
	volumeUUID := "testUUID"
	svmName := "testSVM"
	mockStorage.On("VolumeGet", mock.Anything).Return(nil, errors.New("get error"))

	params := GetVolumeParams{
		UUID:       volumeUUID,
		VolumeName: volumeName,
		SvmName:    svmName,
		IsRestore:  false,
	}
	resp, err := rc.GetVolume(params)

	assert.Error(t, err)
	assert.Nil(t, resp)
	assert.Equal(t, "get error", err.Error())

	mockStorage.AssertExpectations(t)
	mockClient.AssertExpectations(t)
}

func TestGetVolume_WhenVolumeGetReturnsNilVolume_ThenReturnNotFoundError(t *testing.T) {
	mockStorage := new(ontaprest.MockStorageClient)
	mockClient := new(ontaprest.MockRESTClient)
	mockClient.On("Storage").Return(mockStorage)
	originalGetOntapClientFunc := getOntapClientFunc
	defer func() {
		getOntapClientFunc = originalGetOntapClientFunc
	}()
	getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
		return mockClient, nil
	}
	rc := &OntapRestProvider{}

	volumeName := "testVolume"
	volumeUUID := "testUUID"
	svmName := "testSVM"
	mockStorage.On("VolumeGet", mock.Anything).Return(nil, nil)

	params := GetVolumeParams{
		UUID:       volumeUUID,
		VolumeName: volumeName,
		SvmName:    svmName,
		IsRestore:  false,
	}
	resp, err := rc.GetVolume(params)

	assert.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "not found")

	mockStorage.AssertExpectations(t)
	mockClient.AssertExpectations(t)
}

func TestGetVolume_WhenVolumeGetReturnsVolumeWithNilNameOrUUID_ThenReturnNotFoundError(t *testing.T) {
	mockStorage := new(ontaprest.MockStorageClient)
	mockClient := new(ontaprest.MockRESTClient)
	mockClient.On("Storage").Return(mockStorage)
	originalGetOntapClientFunc := getOntapClientFunc
	defer func() {
		getOntapClientFunc = originalGetOntapClientFunc
	}()
	getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
		return mockClient, nil
	}
	rc := &OntapRestProvider{}

	volumeName := "testVolume"
	volumeUUID := "testUUID"
	svmName := "testSVM"
	// Case 1: Name is nil
	mockVolume1 := &ontaprest.Volume{
		Volume: models.Volume{
			UUID: nillable.ToPointer(volumeUUID),
			Name: nil,
		},
	}
	mockStorage.On("VolumeGet", mock.Anything).Return(mockVolume1, nil)

	params := GetVolumeParams{
		UUID:       volumeUUID,
		VolumeName: volumeName,
		SvmName:    svmName,
		IsRestore:  false,
	}
	resp, err := rc.GetVolume(params)
	assert.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "not found")

	// Case 2: UUID is nil
	mockVolume2 := &ontaprest.Volume{
		Volume: models.Volume{
			UUID: nil,
			Name: &volumeName,
		},
	}
	mockStorage.On("VolumeGet", mock.Anything).Return(mockVolume2, nil)

	resp, err = rc.GetVolume(params)
	assert.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "not found")

	mockStorage.AssertExpectations(t)
	mockClient.AssertExpectations(t)
}

func TestGetVolume_WhenVolumeGetReturnsError_getOntapClientFuncError(t *testing.T) {
	originalGetOntapClientFunc := getOntapClientFunc
	defer func() {
		getOntapClientFunc = originalGetOntapClientFunc
	}()
	getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
		return nil, errors.New("getOntapClientFunc error")
	}
	rc := &OntapRestProvider{}

	volumeName := "testVolume"
	volumeUUID := "testUUID"
	svmName := "testSVM"

	params := GetVolumeParams{
		UUID:       volumeUUID,
		VolumeName: volumeName,
		SvmName:    svmName,
		IsRestore:  false,
	}
	resp, err := rc.GetVolume(params)

	assert.Error(t, err)
	assert.Nil(t, resp)
	assertErrContains(t, err, "getOntapClientFunc error")
}

// TestGetVolume_ConstituentCountHandling tests the constituent count extraction logic in GetVolume
func TestGetVolume_ConstituentCountHandling(t *testing.T) {
	tests := []struct {
		name                     string
		volumeInlineConstituents []*models.VolumeInlineConstituentsInlineArrayItem
		expectedConstituentCount *int32
		description              string
	}{
		{
			name: "VolumeInlineConstituents with multiple items",
			volumeInlineConstituents: []*models.VolumeInlineConstituentsInlineArrayItem{
				{Name: nillable.ToPointer("constituent1")},
				{Name: nillable.ToPointer("constituent2")},
				{Name: nillable.ToPointer("constituent3")},
			},
			expectedConstituentCount: nillable.GetInt32Ptr(3),
			description:              "Should return count of 3 when VolumeInlineConstituents has 3 items",
		},
		{
			name:                     "VolumeInlineConstituents with empty array",
			volumeInlineConstituents: []*models.VolumeInlineConstituentsInlineArrayItem{},
			expectedConstituentCount: nillable.GetInt32Ptr(0),
			description:              "Should return count of 0 when VolumeInlineConstituents is empty array",
		},
		{
			name:                     "VolumeInlineConstituents is nil",
			volumeInlineConstituents: nil,
			expectedConstituentCount: nil,
			description:              "Should return nil when VolumeInlineConstituents is nil",
		},
		{
			name: "VolumeInlineConstituents with single item",
			volumeInlineConstituents: []*models.VolumeInlineConstituentsInlineArrayItem{
				{Name: nillable.ToPointer("constituent1")},
			},
			expectedConstituentCount: nillable.GetInt32Ptr(1),
			description:              "Should return count of 1 when VolumeInlineConstituents has 1 item",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			mockStorage := new(ontaprest.MockStorageClient)
			mockClient := new(ontaprest.MockRESTClient)
			mockClient.On("Storage").Return(mockStorage)
			originalGetOntapClientFunc := getOntapClientFunc
			defer func() {
				getOntapClientFunc = originalGetOntapClientFunc
			}()
			getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
				return mockClient, nil
			}
			rc := &OntapRestProvider{}

			volumeName := "testVolume"
			volumeUUID := "testUUID"
			svmName := "testSVM"

			mockVolume := &ontaprest.Volume{
				Volume: models.Volume{
					UUID: nillable.ToPointer(volumeUUID),
					Name: &volumeName,
					Space: &models.VolumeInlineSpace{
						Available: nillable.GetInt64Ptr(100000),
						Size:      nillable.GetInt64Ptr(200000),
						LogicalSpace: &models.VolumeInlineSpaceInlineLogicalSpace{
							Used: nillable.GetInt64Ptr(50000),
						},
					},
					State: nillable.ToPointer(models.VolumeStateOnline),
					SnapshotPolicy: &models.VolumeInlineSnapshotPolicy{
						Name: nillable.GetStringPtr("none"),
					},
					SnapshotDirectoryAccessEnabled: nillable.GetBoolPtr(false),
					VolumeInlineConstituents:       tt.volumeInlineConstituents,
				},
			}
			mockStorage.On("VolumeGet", mock.Anything).Return(mockVolume, nil)

			params := GetVolumeParams{
				UUID:       volumeUUID,
				VolumeName: volumeName,
				SvmName:    svmName,
			}

			// Act
			resp, err := rc.GetVolume(params)

			// Assert
			assert.NoError(t, err)
			assert.NotNil(t, resp)
			if tt.expectedConstituentCount == nil {
				assert.Nil(t, resp.ConstituentCount, tt.description)
			} else {
				require.NotNil(t, resp.ConstituentCount, tt.description)
				assert.Equal(t, *tt.expectedConstituentCount, *resp.ConstituentCount, tt.description)
			}

			mockStorage.AssertExpectations(t)
			mockClient.AssertExpectations(t)
		})
	}
}

func TestUpdateVolume(t *testing.T) {
	mockStorage := new(ontaprest.MockStorageClient)
	mockClient := new(ontaprest.MockRESTClient)
	mockClient.On("Storage").Return(mockStorage)
	originalGetOntapClientFunc := getOntapClientFunc
	defer func() {
		getOntapClientFunc = originalGetOntapClientFunc
	}()
	getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
		return mockClient, nil
	}
	rc := &OntapRestProvider{}

	params := UpdateVolumeParams{
		UUID:               "testUUID",
		Size:               2048,
		SnapshotPolicyName: "testSnapshot",
		TieringPolicy: &TieringPolicy{
			CoolnessPeriod:            7,
			CoolAccessRetrievalPolicy: "default",
			CoolAccessTieringPolicy:   "auto",
		},
	}

	// Case 1: VolumeModify returns error
	mockStorage.On("VolumeModify", mock.Anything).Return(false, nil, errors.New("modify error")).Once()
	err := rc.UpdateVolume(params)
	assert.Error(t, err)
	assertErrContains(t, err, "modify error")

	// Case 2: VolumeModify returns success == true
	mockStorage.On("VolumeModify", mock.Anything).Return(true, nil, nil).Once()
	err = rc.UpdateVolume(params)
	assert.NoError(t, err)

	// Case 3: VolumeModify returns success == false, Poll returns nil
	mockJob := &ontaprest.JobAccepted{JobUUID: "job-uuid"}
	mockStorage.On("VolumeModify", mock.Anything).Return(false, mockJob, nil).Once()
	mockClient.On("Poll", mockJob.JobUUID).Return(nil).Once()
	err = rc.UpdateVolume(params)
	assert.NoError(t, err)

	// Case 4: VolumeModify returns success == false, Poll returns error
	mockJob2 := &ontaprest.JobAccepted{JobUUID: "job-uuid-2"}
	mockStorage.On("VolumeModify", mock.Anything).Return(false, mockJob2, nil).Once()
	mockClient.On("Poll", mockJob2.JobUUID).Return(errors.New("poll error")).Once()
	err = rc.UpdateVolume(params)
	assert.Error(t, err)
	assertErrContains(t, err, "poll error")

	// Case 5: getOntapClientFunc returns error
	getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
		return nil, errors.New("getOntapClientFunc error")
	}
	err = rc.UpdateVolume(params)
	assert.Error(t, err)
	assert.Equal(t, "getOntapClientFunc error", err.Error())

	mockStorage.AssertExpectations(t)
	mockClient.AssertExpectations(t)
}

func TestUpdateVolume_WithMaxSizeError(t *testing.T) {
	mockStorage := new(ontaprest.MockStorageClient)
	mockClient := new(ontaprest.MockRESTClient)
	mockClient.On("Storage").Return(mockStorage)
	originalGetOntapClientFunc := getOntapClientFunc
	defer func() {
		getOntapClientFunc = originalGetOntapClientFunc
	}()
	getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
		return mockClient, nil
	}
	rc := &OntapRestProvider{}

	params := UpdateVolumeParams{
		UUID:               "testUUID",
		Size:               999999999999999, // Set to a very large size that exceeds the maximum
		SnapshotPolicyName: "testSnapshot",
	}

	// Test with a typical ONTAP error message for exceeding maximum volume size
	ontapErrorMsg := "Request to grow volume \"volug13\" in SVM \"gcnv-3eb33bf58bfdd5c-svm-01\" failed because the resulting volume size is greater than the maximum size. The maximum possible size is 900TB (989560464998400B)"
	mockStorage.On("VolumeModify", mock.Anything).Return(false, nil, errors.New(ontapErrorMsg)).Once()

	err := rc.UpdateVolume(params)

	// Check that it's the right error type and contains the right message
	assert.Error(t, err)

	// Check if it's a CustomError with the expected code
	customErr, ok := err.(*vsaerrors.CustomError)
	assert.True(t, ok, "Error should be of type *vsaerrors.CustomError")
	assert.Equal(t, vsaerrors.ErrVolumeExceedsMaximumSize, customErr.TrackingID)

	// Check that the error message is sanitized (doesn't contain SVM name)
	assert.NotContains(t, err.Error(), "gcnv-3eb33bf58bfdd5c-svm-01")
	assert.Contains(t, err.Error(), "The volume size exceeds the maximum allowed size for this storage pool.")

	mockStorage.AssertExpectations(t)
	mockClient.AssertExpectations(t)
}

func TestUpdateVolume_WithMaxSizeErrorButNoMaxSizeInfo(t *testing.T) {
	mockStorage := new(ontaprest.MockStorageClient)
	mockClient := new(ontaprest.MockRESTClient)
	mockClient.On("Storage").Return(mockStorage)
	originalGetOntapClientFunc := getOntapClientFunc
	defer func() {
		getOntapClientFunc = originalGetOntapClientFunc
	}()
	getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
		return mockClient, nil
	}
	rc := &OntapRestProvider{}

	params := UpdateVolumeParams{
		UUID:               "testUUID",
		Size:               999999999999999, // Set to a very large size that exceeds the maximum
		SnapshotPolicyName: "testSnapshot",
	}

	// Test with an ONTAP error message that mentions maximum size but doesn't include the specific size info
	// This tests the case where maxSizeStart > 0 check fails
	mockStorage.On("VolumeModify", mock.Anything).Return(false, nil, errors.New(ErrMsgVolumeMaxSizeExceeded)).Once()

	err := rc.UpdateVolume(params)

	// Should fall back to the default error handling
	assert.Error(t, err)
	customErr, ok := err.(*vsaerrors.CustomError)
	assert.True(t, ok, "Error should be of type *vsaerrors.CustomError")
	assert.Equal(t, vsaerrors.ErrOntapRestAPIError, customErr.TrackingID)
	// For the general API error, we should get the generic error message
	assert.Contains(t, err.Error(), "An internal error occurred.")

	mockStorage.AssertExpectations(t)
	mockClient.AssertExpectations(t)
}

func TestUpdateVolume_SetsNasUid(t *testing.T) {
	mockStorage := new(ontaprest.MockStorageClient)
	mockClient := new(ontaprest.MockRESTClient)
	mockClient.On("Storage").Return(mockStorage)
	originalGetOntapClientFunc := getOntapClientFunc
	defer func() {
		getOntapClientFunc = originalGetOntapClientFunc
	}()
	getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
		return mockClient, nil
	}
	rc := &OntapRestProvider{}

	uid := int64(2000)
	params := UpdateVolumeParams{
		UUID:   "testUUID",
		NasUid: &uid,
	}

	mockStorage.On("VolumeModify", mock.MatchedBy(func(p *ontaprest.VolumeModifyParams) bool {
		return p.UUID == "testUUID" && p.NasUid != nil && *p.NasUid == uid
	})).Return(true, nil, nil).Once()

	err := rc.UpdateVolume(params)
	assert.NoError(t, err)
	mockStorage.AssertExpectations(t)
	mockClient.AssertExpectations(t)
}

func TestUpdateVolume_NasUidNil_NotSentToONTAP(t *testing.T) {
	mockStorage := new(ontaprest.MockStorageClient)
	mockClient := new(ontaprest.MockRESTClient)
	mockClient.On("Storage").Return(mockStorage)
	originalGetOntapClientFunc := getOntapClientFunc
	defer func() {
		getOntapClientFunc = originalGetOntapClientFunc
	}()
	getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
		return mockClient, nil
	}
	rc := &OntapRestProvider{}

	params := UpdateVolumeParams{
		UUID:   "testUUID",
		NasUid: nil,
	}

	mockStorage.On("VolumeModify", mock.MatchedBy(func(p *ontaprest.VolumeModifyParams) bool {
		return p.UUID == "testUUID" && p.NasUid == nil
	})).Return(true, nil, nil).Once()

	err := rc.UpdateVolume(params)
	assert.NoError(t, err)
	mockStorage.AssertExpectations(t)
	mockClient.AssertExpectations(t)
}

func TestUpdateVolume_SetsUnixPermissions(t *testing.T) {
	mockStorage := new(ontaprest.MockStorageClient)
	mockClient := new(ontaprest.MockRESTClient)
	mockClient.On("Storage").Return(mockStorage)
	originalGetOntapClientFunc := getOntapClientFunc
	defer func() {
		getOntapClientFunc = originalGetOntapClientFunc
	}()
	getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
		return mockClient, nil
	}
	rc := &OntapRestProvider{}

	permissions := "0770"
	params := UpdateVolumeParams{
		UUID:            "testUUID",
		UnixPermissions: &permissions,
	}

	mockStorage.On("VolumeModify", mock.MatchedBy(func(p *ontaprest.VolumeModifyParams) bool {
		return p.UUID == "testUUID" && p.UnixPermissions != nil && *p.UnixPermissions == permissions
	})).Return(true, nil, nil).Once()

	err := rc.UpdateVolume(params)
	assert.NoError(t, err)

	mockStorage.AssertExpectations(t)
	mockClient.AssertExpectations(t)
}

func TestUpdateVolume_WithVolumeSizeTooSmallError(t *testing.T) {
	mockStorage := new(ontaprest.MockStorageClient)
	mockClient := new(ontaprest.MockRESTClient)
	mockClient.On("Storage").Return(mockStorage)
	originalgetOntapClientFunc := getOntapClientFunc
	defer func() {
		getOntapClientFunc = originalgetOntapClientFunc
	}()
	getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
		return mockClient, nil
	}
	rc := &OntapRestProvider{}

	params := UpdateVolumeParams{
		UUID:               "testUUID",
		Size:               100 * 1024 * 1024, // 100MB - too small
		SnapshotPolicyName: "testSnapshot",
	}

	// Test with ONTAP error message that includes minimum size requirement
	ontapErrorMsg := "Selected volume size is too small to hold the current volume data. New volume size must be at least 500GB (536870912000B) to hold the current volume data"
	mockStorage.On("VolumeModify", mock.Anything).Return(false, nil, errors.New(ontapErrorMsg)).Once()

	err := rc.UpdateVolume(params)

	assert.Error(t, err)
	customErr, ok := err.(*vsaerrors.CustomError)
	assert.True(t, ok, "Error should be of type *vsaerrors.CustomError")
	assert.Equal(t, vsaerrors.ErrVolumeSizeTooSmall, customErr.TrackingID)
	assert.Contains(t, err.Error(), "Selected volume size is too small to hold the current volume data")

	mockStorage.AssertExpectations(t)
	mockClient.AssertExpectations(t)
}

func TestUpdateVolume_WithVolumeSizeTooSmallErrorButNoMinSizeInfo(t *testing.T) {
	mockStorage := new(ontaprest.MockStorageClient)
	mockClient := new(ontaprest.MockRESTClient)
	mockClient.On("Storage").Return(mockStorage)
	originalgetOntapClientFunc := getOntapClientFunc
	defer func() {
		getOntapClientFunc = originalgetOntapClientFunc
	}()
	getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
		return mockClient, nil
	}
	rc := &OntapRestProvider{}

	params := UpdateVolumeParams{
		UUID:               "testUUID",
		Size:               100 * 1024 * 1024, // 100MB - too small
		SnapshotPolicyName: "testSnapshot",
	}

	// Test with ONTAP error message that mentions size too small but doesn't include the specific size info
	ontapErrorMsg := "Selected volume size is too small to hold the current volume data"
	mockStorage.On("VolumeModify", mock.Anything).Return(false, nil, errors.New(ontapErrorMsg)).Once()

	err := rc.UpdateVolume(params)

	assert.Error(t, err)
	customErr, ok := err.(*vsaerrors.CustomError)
	assert.True(t, ok, "Error should be of type *vsaerrors.CustomError")
	assert.Equal(t, vsaerrors.ErrVolumeSizeTooSmall, customErr.TrackingID)
	assert.Contains(t, err.Error(), "Selected volume size is too small to hold the current volume data")
	assert.Contains(t, err.Error(), "Please increase the volume size")

	mockStorage.AssertExpectations(t)
	mockClient.AssertExpectations(t)
}

func TestUpdateVolume_WithVolumeSizeTooSmallErrorWithPartialMinSizeInfo(t *testing.T) {
	mockStorage := new(ontaprest.MockStorageClient)
	mockClient := new(ontaprest.MockRESTClient)
	mockClient.On("Storage").Return(mockStorage)
	originalgetOntapClientFunc := getOntapClientFunc
	defer func() {
		getOntapClientFunc = originalgetOntapClientFunc
	}()
	getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
		return mockClient, nil
	}
	rc := &OntapRestProvider{}

	params := UpdateVolumeParams{
		UUID:               "testUUID",
		Size:               100 * 1024 * 1024, // 100MB - too small
		SnapshotPolicyName: "testSnapshot",
	}

	// Test with ONTAP error message that has "New volume size must be at least" but no " to hold" delimiter
	ontapErrorMsg := "Selected volume size is too small to hold the current volume data. New volume size must be at least 500GB"
	mockStorage.On("VolumeModify", mock.Anything).Return(false, nil, errors.New(ontapErrorMsg)).Once()

	err := rc.UpdateVolume(params)

	assert.Error(t, err)
	customErr, ok := err.(*vsaerrors.CustomError)
	assert.True(t, ok, "Error should be of type *vsaerrors.CustomError")
	assert.Equal(t, vsaerrors.ErrVolumeSizeTooSmall, customErr.TrackingID)
	assert.Contains(t, err.Error(), "Selected volume size is too small to hold the current volume data")
	assert.Contains(t, err.Error(), "Please increase the volume size")

	mockStorage.AssertExpectations(t)
	mockClient.AssertExpectations(t)
}

func TestUpdateVolume_WithQosPolicy(t *testing.T) {
	mockStorage := new(ontaprest.MockStorageClient)
	mockClient := new(ontaprest.MockRESTClient)
	mockClient.On("Storage").Return(mockStorage)
	originalGetOntapClientFunc := getOntapClientFunc
	defer func() {
		getOntapClientFunc = originalGetOntapClientFunc
	}()
	getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
		return mockClient, nil
	}
	rc := &OntapRestProvider{}

	t.Run("AssignQosPolicy", func(tt *testing.T) {
		policyName := "test-qos-policy"
		params := UpdateVolumeParams{
			UUID:          "testUUID",
			QosPolicyName: &policyName,
		}

		mockStorage.On("VolumeModify", mock.MatchedBy(func(p *ontaprest.VolumeModifyParams) bool {
			return p.UUID == "testUUID" && p.QosPolicy != nil && *p.QosPolicy == policyName
		})).Return(true, nil, nil).Once()

		err := rc.UpdateVolume(params)
		assert.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("UnassignQosPolicy_None", func(tt *testing.T) {
		nonePolicy := "none"
		params := UpdateVolumeParams{
			UUID:          "testUUID",
			QosPolicyName: &nonePolicy,
		}

		mockStorage.On("VolumeModify", mock.MatchedBy(func(p *ontaprest.VolumeModifyParams) bool {
			return p.UUID == "testUUID" && p.QosPolicy != nil && *p.QosPolicy == "none"
		})).Return(true, nil, nil).Once()

		err := rc.UpdateVolume(params)
		assert.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("UnassignQosPolicy_EmptyString_PassesThrough", func(tt *testing.T) {
		emptyPolicy := ""
		params := UpdateVolumeParams{
			UUID:          "testUUID",
			QosPolicyName: &emptyPolicy,
		}

		ontapError := errors.New("QoS policy group \"\" does not exist")
		mockStorage.On("VolumeModify", mock.MatchedBy(func(p *ontaprest.VolumeModifyParams) bool {
			// Verify empty string is passed through to ONTAP (not converted)
			return p.UUID == "testUUID" && p.QosPolicy != nil && *p.QosPolicy == ""
		})).Return(false, nil, ontapError).Once()

		err := rc.UpdateVolume(params)
		// Error should be returned (wrapped by vsaerrors.NewVCPError)
		// The empty string passes through and ONTAP rejects it with an error
		assert.Error(tt, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("NoQosPolicyChange_Nil", func(tt *testing.T) {
		params := UpdateVolumeParams{
			UUID:          "testUUID",
			QosPolicyName: nil,
		}

		mockStorage.On("VolumeModify", mock.MatchedBy(func(p *ontaprest.VolumeModifyParams) bool {
			return p.UUID == "testUUID" && p.QosPolicy == nil
		})).Return(true, nil, nil).Once()

		err := rc.UpdateVolume(params)
		assert.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("AssignQosPolicyWithOtherParams", func(tt *testing.T) {
		policyName := "test-qos-policy"
		snapReserve := int64(10)
		params := UpdateVolumeParams{
			UUID:          "testUUID",
			SnapReserve:   &snapReserve,
			QosPolicyName: &policyName,
		}

		mockStorage.On("VolumeModify", mock.MatchedBy(func(p *ontaprest.VolumeModifyParams) bool {
			return p.UUID == "testUUID" &&
				p.QosPolicy != nil &&
				*p.QosPolicy == policyName &&
				p.SnapReserve != nil &&
				*p.SnapReserve == snapReserve
		})).Return(true, nil, nil).Once()

		err := rc.UpdateVolume(params)
		assert.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
	})
}

func TestUnassignQoSPolicyFromVolume(t *testing.T) {
	mockStorage := new(ontaprest.MockStorageClient)
	mockClient := new(ontaprest.MockRESTClient)
	mockClient.On("Storage").Return(mockStorage)
	originalGetOntapClientFunc := getOntapClientFunc
	defer func() {
		getOntapClientFunc = originalGetOntapClientFunc
	}()
	getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
		return mockClient, nil
	}
	rc := &OntapRestProvider{}

	t.Run("Success", func(tt *testing.T) {
		mockStorage.On("VolumeModify", mock.MatchedBy(func(p *ontaprest.VolumeModifyParams) bool {
			return p.UUID == "testUUID" && p.QosPolicy != nil && *p.QosPolicy == "none"
		})).Return(true, nil, nil).Once()

		err := rc.UnassignQoSPolicyFromVolume("testUUID")
		assert.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenVolumeModifyReturnsError", func(tt *testing.T) {
		ontapError := errors.New("volume modify error")
		mockStorage.On("VolumeModify", mock.MatchedBy(func(p *ontaprest.VolumeModifyParams) bool {
			return p.UUID == "testUUID" && p.QosPolicy != nil && *p.QosPolicy == "none"
		})).Return(false, nil, ontapError).Once()

		err := rc.UnassignQoSPolicyFromVolume("testUUID")
		assert.Error(tt, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenVolumeModifyReturnsJob", func(tt *testing.T) {
		mockJob := &ontaprest.JobAccepted{JobUUID: "job-uuid"}
		mockStorage.On("VolumeModify", mock.MatchedBy(func(p *ontaprest.VolumeModifyParams) bool {
			return p.UUID == "testUUID" && p.QosPolicy != nil && *p.QosPolicy == "none"
		})).Return(false, mockJob, nil).Once()
		mockClient.On("Poll", mockJob.JobUUID).Return(nil).Once()

		err := rc.UnassignQoSPolicyFromVolume("testUUID")
		assert.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
		mockClient.AssertExpectations(tt)
	})
}

func TestGetVolumes(t *testing.T) {
	t.Run("GetVolumesSuccess", func(t *testing.T) {
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("Storage").Return(mockStorage)
		originalGetOntapClientFunc := getOntapClientFunc
		defer func() {
			getOntapClientFunc = originalGetOntapClientFunc
		}()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		mockStorage.On("VolumeCollectionGet", &ontaprest.VolumeCollectionGetParams{
			BaseParams: ontaprest.BaseParams{
				Fields: []string{"uuid", "name", "space.*", "svm", "is_svm_root", "style", "type", "clone.split_complete_percent", "clone.is_flexclone"},
			},
		}, mock.Anything).Return(nil)

		_, err := rc.GetVolumes()
		assert.NoError(t, err)
		mockStorage.AssertExpectations(t)
		mockClient.AssertExpectations(t)
	})
	t.Run("GetVolumesFailure", func(t *testing.T) {
		originalGetOntapClientFunc := getOntapClientFunc
		defer func() {
			getOntapClientFunc = originalGetOntapClientFunc
		}()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return nil, errors.New("getOntapClientFunc error")
		}
		rc := &OntapRestProvider{}
		_, err := rc.GetVolumes()
		assert.Error(t, err)
		assert.Equal(t, "getOntapClientFunc error", err.Error())
	})
	t.Run("GetVolumesWithCloneInfoRefresh", func(t *testing.T) {
		// Enable clone info refresh for this test
		oldValue := enableCloneInfoRefresh
		enableCloneInfoRefresh = true
		defer func() { enableCloneInfoRefresh = oldValue }()

		mockStorage := new(ontaprest.MockStorageClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("Storage").Return(mockStorage)
		originalGetOntapClientFunc := getOntapClientFunc
		defer func() {
			getOntapClientFunc = originalGetOntapClientFunc
		}()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		// Verify that clone fields are included when enableCloneInfoRefresh is true
		mockStorage.On("VolumeCollectionGet", &ontaprest.VolumeCollectionGetParams{
			BaseParams: ontaprest.BaseParams{
				Fields: []string{"uuid", "name", "space.*", "svm", "is_svm_root", "style", "type", "clone.split_complete_percent", "clone.is_flexclone", "clone.parent_snapshot.name", "clone.parent_volume.name"},
			},
		}, mock.Anything).Return(nil)

		_, err := rc.GetVolumes()
		assert.NoError(t, err)
		mockStorage.AssertExpectations(t)
		mockClient.AssertExpectations(t)
	})
}

func TestGetVolumesForInventory_UsesProjectSVMFilterAndInventoryFields(t *testing.T) {
	mockStorage := new(ontaprest.MockStorageClient)
	mockClient := new(ontaprest.MockRESTClient)
	mockClient.On("Storage").Return(mockStorage)

	originalGetOntapClientFunc := getOntapClientFunc
	t.Cleanup(func() {
		getOntapClientFunc = originalGetOntapClientFunc
	})
	getOntapClientFunc = func(ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
		return mockClient, nil
	}

	const svmPattern = "svm_accountuuid_*"
	mockStorage.On("VolumeCollectionGet", mock.MatchedBy(func(params *ontaprest.VolumeCollectionGetParams) bool {
		return params != nil &&
			params.SvmName != nil &&
			*params.SvmName == svmPattern &&
			params.MaxRecords != nil &&
			*params.MaxRecords == 10000 &&
			assert.ElementsMatch(t,
				[]string{"name", "uuid", "svm.name", "svm.uuid", "space.size", "aggregates.name"},
				params.Fields)
	}), mock.Anything).Run(func(args mock.Arguments) {
		callback := args.Get(1).(ontaprest.UserCallbackFunc[[]*ontaprest.Volume])
		uuid := "volume-uuid"
		name := "volume-name"
		require.NoError(t, callback([]*ontaprest.Volume{
			nil,
			{Volume: models.Volume{UUID: &uuid, Name: &name}},
		}))
	}).Return(nil)

	volumes, err := (&OntapRestProvider{}).GetVolumesForInventory(svmPattern)

	require.NoError(t, err)
	require.Len(t, volumes, 1)
	assert.Equal(t, "volume-uuid", volumes[0].ExternalUUID)
	mockStorage.AssertExpectations(t)
	mockClient.AssertExpectations(t)
}

func TestUpdateVolume_ForSplit(t *testing.T) {
	mockStorage := new(ontaprest.MockStorageClient)
	mockClient := new(ontaprest.MockRESTClient)
	mockClient.On("Storage").Return(mockStorage)
	originalGetOntapClientFunc := getOntapClientFunc
	defer func() {
		getOntapClientFunc = originalGetOntapClientFunc
	}()
	getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
		return mockClient, nil
	}
	rc := &OntapRestProvider{}

	params := UpdateVolumeParams{
		UUID:          "testUUID",
		InitiateSplit: true,
	}

	// Case 1: VolumeModify returns error
	mockStorage.On("VolumeModify", mock.Anything).Return(false, nil, errors.New("modify error")).Once()
	err := rc.UpdateVolume(params)
	assert.Error(t, err)
	assertErrContains(t, err, "modify error")

	// Case 2: VolumeModify returns success == true
	mockStorage.On("VolumeModify", mock.Anything).Return(true, nil, nil).Once()
	err = rc.UpdateVolume(params)
	assert.NoError(t, err)

	// Case 3: VolumeModify returns success == false, Poll returns nil
	mockJob := &ontaprest.JobAccepted{JobUUID: "job-uuid"}
	mockStorage.On("VolumeModify", mock.Anything).Return(false, mockJob, nil).Once()
	err = rc.UpdateVolume(params)
	assert.NoError(t, err)

	// Case 5: getOntapClientFunc returns error
	getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
		return nil, errors.New("getOntapClientFunc error")
	}
	err = rc.UpdateVolume(params)
	assert.Error(t, err)
	assert.Equal(t, "getOntapClientFunc error", err.Error())

	mockStorage.AssertExpectations(t)
	mockClient.AssertExpectations(t)
}

func TestVolumeEncryptionStatus(t *testing.T) {
	volumeName := "testVolume"
	volumeUUID := "testUUID"
	svmName := "testSVM"
	params := GetVolumeParams{
		UUID:       volumeUUID,
		VolumeName: volumeName,
		SvmName:    svmName,
		IsRestore:  false,
	}
	t.Run("WhenGetVolumeEncryptionStatusReturnsError", func(tt *testing.T) {
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("Storage").Return(mockStorage)

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		mockStorage.On("VolumeGet", mock.Anything).Return(nil, errors.New("Volume not found"))
		result, err := rc.GetVolumeEncryptionStatus(params)
		assert.Nil(tt, result)
		assert.Error(tt, err)
		assert.EqualError(tt, err, "Volume not found")
	})
	t.Run("WhenGetVolumeEncryptionStatusCallReturnsWithoutVolume", func(tt *testing.T) {
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("Storage").Return(mockStorage)

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		mockStorage.On("VolumeGet", mock.Anything).Return(nil, nil)
		result, err := rc.GetVolumeEncryptionStatus(params)
		assert.Nil(tt, result)
		assert.Error(tt, err)
		assert.EqualError(tt, err, "volume not found")
	})
	t.Run("WhenGetVolumeEncryptionStatusDoesNotHaveEncryptionField", func(tt *testing.T) {
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("Storage").Return(mockStorage)

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		mockVolume := &ontaprest.Volume{
			Volume: models.Volume{
				UUID: nillable.ToPointer(volumeUUID),
				Name: &volumeName,
				Space: &models.VolumeInlineSpace{
					Available: nillable.GetInt64Ptr(100000), // Example available space
					Size:      nillable.GetInt64Ptr(200000), // Example total size
				},
				State: nillable.ToPointer(models.VolumeStateOnline),
			},
		}
		mockStorage.On("VolumeGet", mock.Anything).Return(mockVolume, nil)
		result, err := rc.GetVolumeEncryptionStatus(params)
		assert.Nil(tt, result)
		assert.Error(tt, err)
		assert.EqualError(tt, err, "Encryption field is not populated in Get Volume from VSA")
	})
	t.Run("WhenGetVolumeEncryptionStatusSucceeds", func(tt *testing.T) {
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("Storage").Return(mockStorage)

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}
		enabled := true
		state := "encrypted"
		typeEncryption := "volume"
		mockVolume := &ontaprest.Volume{
			Volume: models.Volume{
				UUID: nillable.ToPointer(volumeUUID),
				Name: &volumeName,
				Space: &models.VolumeInlineSpace{
					Available: nillable.GetInt64Ptr(100000), // Example available space
					Size:      nillable.GetInt64Ptr(200000), // Example total size
				},
				State: nillable.ToPointer(models.VolumeStateOnline),
				Encryption: &models.VolumeInlineEncryption{
					Enabled: &enabled,
					State:   &state,
					Type:    &typeEncryption,
				},
			},
		}
		mockStorage.On("VolumeGet", mock.Anything).Return(mockVolume, nil)

		result, err := rc.GetVolumeEncryptionStatus(params)
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.NotNil(tt, result.Encryption)
		assert.True(tt, *result.Enabled)
		assert.Equal(tt, "encrypted", *result.Encryption.State)
	})
}

func TestUpdateVolumeEnableEncryption(t *testing.T) {
	params := UpdateVolumeParams{
		UUID:             "testUUID",
		EncryptionEnable: true,
	}
	t.Run("WhenUpdateVolumeEnableEncryptionReturnsError", func(tt *testing.T) {
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("Storage").Return(mockStorage)

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		mockStorage.On("VolumeModify", mock.Anything).Return(false, nil, errors.New("volume modify error"))

		err := rc.UpdateVolumeEnableEncryption(params)
		assert.Error(tt, err)
		assert.EqualError(tt, err, "volume modify error")
	})
	t.Run("WhenUpdateVolumeEnableEncryptionReturnsSuccess", func(tt *testing.T) {
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("Storage").Return(mockStorage)

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		mockJob := &ontaprest.JobAccepted{JobUUID: "job-uuid"}
		mockStorage.On("VolumeModify", mock.Anything).Return(true, mockJob, nil)

		err := rc.UpdateVolumeEnableEncryption(params)
		assert.NoError(tt, err)
		assert.Nil(tt, err)
	})
}

func TestUpdateVolume_WithExportPolicyAndJunctionPath(t *testing.T) {
	t.Run("WhenExportPolicyIsSet_ShouldSetExportPolicyInVolumeModifyParams", func(tt *testing.T) {
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("Storage").Return(mockStorage)

		originalGetOntapClientFunc := getOntapClientFunc
		defer func() {
			getOntapClientFunc = originalGetOntapClientFunc
		}()

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}

		rc := &OntapRestProvider{}

		exportPolicy := "test-export-policy"
		params := UpdateVolumeParams{
			UUID:         "testUUID",
			ExportPolicy: &exportPolicy,
		}

		// Mock VolumeModify to capture the parameters and verify ExportPolicy is set
		mockStorage.On("VolumeModify", mock.MatchedBy(func(volumeModifyParams *ontaprest.VolumeModifyParams) bool {
			return volumeModifyParams.ExportPolicy != nil && *volumeModifyParams.ExportPolicy == exportPolicy
		})).Return(true, nil, nil).Once()

		err := rc.UpdateVolume(params)
		assert.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenJunctionPathIsSet_ShouldSetPathInVolumeModifyParams", func(tt *testing.T) {
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("Storage").Return(mockStorage)

		originalGetOntapClientFunc := getOntapClientFunc
		defer func() {
			getOntapClientFunc = originalGetOntapClientFunc
		}()

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}

		rc := &OntapRestProvider{}

		junctionPath := "/test/junction/path"
		params := UpdateVolumeParams{
			UUID:         "testUUID",
			JunctionPath: &junctionPath,
		}

		// Mock VolumeModify to capture the parameters and verify Path is set
		mockStorage.On("VolumeModify", mock.MatchedBy(func(volumeModifyParams *ontaprest.VolumeModifyParams) bool {
			return volumeModifyParams.Path != nil && *volumeModifyParams.Path == junctionPath
		})).Return(true, nil, nil).Once()

		err := rc.UpdateVolume(params)
		assert.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenBothExportPolicyAndJunctionPathAreSet_ShouldSetBothInVolumeModifyParams", func(tt *testing.T) {
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("Storage").Return(mockStorage)

		originalGetOntapClientFunc := getOntapClientFunc
		defer func() {
			getOntapClientFunc = originalGetOntapClientFunc
		}()

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}

		rc := &OntapRestProvider{}

		exportPolicy := "test-export-policy"
		junctionPath := "/test/junction/path"
		params := UpdateVolumeParams{
			UUID:         "testUUID",
			ExportPolicy: &exportPolicy,
			JunctionPath: &junctionPath,
		}

		// Mock VolumeModify to capture the parameters and verify both are set
		mockStorage.On("VolumeModify", mock.MatchedBy(func(volumeModifyParams *ontaprest.VolumeModifyParams) bool {
			return volumeModifyParams.ExportPolicy != nil && *volumeModifyParams.ExportPolicy == exportPolicy &&
				volumeModifyParams.Path != nil && *volumeModifyParams.Path == junctionPath
		})).Return(true, nil, nil).Once()

		err := rc.UpdateVolume(params)
		assert.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenExportPolicyAndJunctionPathAreNil_ShouldNotSetInVolumeModifyParams", func(tt *testing.T) {
		// This test ensures the conditional logic works correctly when fields are nil
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("Storage").Return(mockStorage)

		originalGetOntapClientFunc := getOntapClientFunc
		defer func() {
			getOntapClientFunc = originalGetOntapClientFunc
		}()

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}

		rc := &OntapRestProvider{}

		params := UpdateVolumeParams{
			UUID:         "testUUID",
			ExportPolicy: nil, // nil - should not trigger line 254
			JunctionPath: nil, // nil - should not trigger line 257
		}

		// Mock VolumeModify to capture the parameters and verify neither is set
		mockStorage.On("VolumeModify", mock.MatchedBy(func(volumeModifyParams *ontaprest.VolumeModifyParams) bool {
			return volumeModifyParams.ExportPolicy == nil && volumeModifyParams.Path == nil
		})).Return(true, nil, nil).Once()

		err := rc.UpdateVolume(params)
		assert.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
	})
}

func TestUpdateVolume_WithCloudWriteModeDisable(t *testing.T) {
	t.Run("WhenDisablingCloudWriteMode_ShouldCallVolumeModifyCloudWriteMode", func(tt *testing.T) {
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("Storage").Return(mockStorage)
		originalGetOntapClientFunc := getOntapClientFunc
		defer func() {
			getOntapClientFunc = originalGetOntapClientFunc
		}()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}

		rc := &OntapRestProvider{}

		exportPolicy := "test-export-policy"
		falseVal := false
		params := UpdateVolumeParams{
			UUID:         "testUUID",
			ExportPolicy: &exportPolicy,
			TieringPolicy: &TieringPolicy{
				CoolAccessTieringPolicy: "auto",
				CloudWriteModeEnabled:   &falseVal,
			},
		}

		// Expect VolumeModifyCloudWriteMode to be called before VolumeModify
		mockStorage.On("VolumeModifyCloudWriteMode", mock.MatchedBy(func(volumeModifyParams *ontaprest.VolumeModifyParams) bool {
			return volumeModifyParams.UUID == "testUUID"
		})).Return(true, nil, nil).Once()

		// VolumeModify must not re-send cloud_write_enabled after the dedicated disable call
		// (ONTAP rejects that field on DP read-only volumes — error 917628).
		mockStorage.On("VolumeModify", mock.MatchedBy(func(volumeModifyParams *ontaprest.VolumeModifyParams) bool {
			return volumeModifyParams.UUID == "testUUID" &&
				volumeModifyParams.TieringPolicy != nil &&
				volumeModifyParams.TieringPolicy.CloudWriteModeEnabled == nil
		})).Return(true, nil, nil).Once()

		err := rc.UpdateVolume(params)
		assert.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenCloudWriteModeEnabledIsTrue_ShouldNotCallVolumeModifyCloudWriteMode", func(tt *testing.T) {
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("Storage").Return(mockStorage)
		originalGetOntapClientFunc := getOntapClientFunc
		defer func() {
			getOntapClientFunc = originalGetOntapClientFunc
		}()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}

		rc := &OntapRestProvider{}

		exportPolicy := "test-export-policy"
		trueVal := true
		params := UpdateVolumeParams{
			UUID:         "testUUID",
			ExportPolicy: &exportPolicy,
			TieringPolicy: &TieringPolicy{
				CoolAccessTieringPolicy: "all",
				CloudWriteModeEnabled:   &trueVal,
			},
		}

		// Should NOT call VolumeModifyCloudWriteMode, only VolumeModify
		mockStorage.On("VolumeModify", mock.MatchedBy(func(volumeModifyParams *ontaprest.VolumeModifyParams) bool {
			return volumeModifyParams.UUID == "testUUID"
		})).Return(true, nil, nil).Once()

		err := rc.UpdateVolume(params)
		assert.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenBlockVolume_ShouldNotCallVolumeModifyCloudWriteMode", func(tt *testing.T) {
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("Storage").Return(mockStorage)
		originalGetOntapClientFunc := getOntapClientFunc
		defer func() {
			getOntapClientFunc = originalGetOntapClientFunc
		}()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}

		rc := &OntapRestProvider{}

		// No ExportPolicy means block volume
		// For block volumes, CloudWriteModeEnabled should be nil (not supported)
		params := UpdateVolumeParams{
			UUID: "testUUID",
			TieringPolicy: &TieringPolicy{
				CoolAccessTieringPolicy: "auto",
				CloudWriteModeEnabled:   nil,
			},
		}

		// Should NOT call VolumeModifyCloudWriteMode for block volumes
		mockStorage.On("VolumeModify", mock.MatchedBy(func(volumeModifyParams *ontaprest.VolumeModifyParams) bool {
			return volumeModifyParams.UUID == "testUUID"
		})).Return(true, nil, nil).Once()

		err := rc.UpdateVolume(params)
		assert.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenVolumeModifyCloudWriteModeFails_ShouldReturnError", func(tt *testing.T) {
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("Storage").Return(mockStorage)
		originalGetOntapClientFunc := getOntapClientFunc
		defer func() {
			getOntapClientFunc = originalGetOntapClientFunc
		}()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}

		rc := &OntapRestProvider{}

		exportPolicy := "test-export-policy"
		falseVal := false
		params := UpdateVolumeParams{
			UUID:         "testUUID",
			ExportPolicy: &exportPolicy,
			TieringPolicy: &TieringPolicy{
				CoolAccessTieringPolicy: "auto",
				CloudWriteModeEnabled:   &falseVal,
			},
		}

		// Expect VolumeModifyCloudWriteMode to fail
		mockStorage.On("VolumeModifyCloudWriteMode", mock.Anything).Return(false, nil, errors.New("disable cloud write error")).Once()

		err := rc.UpdateVolume(params)
		assert.Error(tt, err)
		// The error is wrapped twice by vsaerrors.NewVCPError, so we just check that an error occurred
		mockStorage.AssertExpectations(tt)
	})
}

func TestRevertVolume(t *testing.T) {
	t.Run("TestRevertVolume_Success", func(t *testing.T) {
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("Storage").Return(mockStorage)
		originalGetOntapClientFunc := getOntapClientFunc
		defer func() {
			getOntapClientFunc = originalGetOntapClientFunc
		}()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		params := RevertVolumeParams{
			VolumeID:   "testVolumeUUID",
			SnapshotID: "testSnapshotUUID",
			PreRevertVolume: &datamodel.Volume{
				SizeInBytes: 1000000,
			},
		}

		// Mock VolumeModify returns done = true
		mockStorage.On("VolumeModify", mock.Anything).Return(true, nil, nil).Once()

		err := rc.RevertVolume(params)
		assert.NoError(t, err)

		mockStorage.AssertExpectations(t)
		mockClient.AssertExpectations(t)
	})

	t.Run("TestRevertVolume_GetOntapClientError", func(t *testing.T) {
		originalGetOntapClientFunc := getOntapClientFunc
		defer func() {
			getOntapClientFunc = originalGetOntapClientFunc
		}()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return nil, errors.New("getOntapClientFunc error")
		}
		rc := &OntapRestProvider{}

		params := RevertVolumeParams{
			VolumeID:   "testVolumeUUID",
			SnapshotID: "testSnapshotUUID",
		}

		err := rc.RevertVolume(params)

		assert.Error(t, err)
		assertErrContains(t, err, "getOntapClientFunc error")
	})

	t.Run("TestRevertVolume_VolumeModifyError", func(t *testing.T) {
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("Storage").Return(mockStorage)
		originalGetOntapClientFunc := getOntapClientFunc
		defer func() {
			getOntapClientFunc = originalGetOntapClientFunc
		}()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		params := RevertVolumeParams{
			VolumeID:   "testVolumeUUID",
			SnapshotID: "testSnapshotUUID",
		}

		mockStorage.On("VolumeModify", mock.Anything).Return(false, nil, errors.New("volume modify error"))

		err := rc.RevertVolume(params)

		assert.Error(t, err)
		assertErrContains(t, err, "volume modify error")

		mockStorage.AssertExpectations(t)
		mockClient.AssertExpectations(t)
	})

	t.Run("TestRevertVolume_VolumeModifyErrorWithSnapshotInUse", func(t *testing.T) {
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("Storage").Return(mockStorage)
		originalGetOntapClientFunc := getOntapClientFunc
		defer func() {
			getOntapClientFunc = originalGetOntapClientFunc
		}()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		params := RevertVolumeParams{
			VolumeID:   "testVolumeUUID",
			SnapshotID: "testSnapshotUUID",
		}

		// Test for "Failed to restore snapshot"
		mockStorage.On("VolumeModify", mock.Anything).Return(false, nil, errors.New("Failed to restore snapshot due to clone")).Once()
		err := rc.RevertVolume(params)
		assert.Error(t, err)
		customErr, ok := err.(*vsaerrors.CustomError)
		assert.True(t, ok)
		assert.Equal(t, vsaerrors.ErrRevertVolumeWhenSnapshotInUse, customErr.TrackingID)
		assert.Contains(t, err.Error(), "Cannot revert the volume as snapshot is in use by the cloned volume")

		// Test for "Volume snap restore error"
		mockStorage.On("VolumeModify", mock.Anything).Return(false, nil, errors.New("Volume snap restore error: snapshot in use")).Once()
		err = rc.RevertVolume(params)
		assert.Error(t, err)
		customErr, ok = err.(*vsaerrors.CustomError)
		assert.True(t, ok)
		assert.Equal(t, vsaerrors.ErrRevertVolumeWhenSnapshotInUse, customErr.TrackingID)
		assert.Contains(t, err.Error(), "Cannot revert the volume as snapshot is in use by the cloned volume")

		mockStorage.AssertExpectations(t)
		mockClient.AssertExpectations(t)
	})

	t.Run("TestRevertVolume_VolumeModifyErrorWithReplicationDestination", func(t *testing.T) {
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("Storage").Return(mockStorage)
		originalGetOntapClientFunc := getOntapClientFunc
		defer func() {
			getOntapClientFunc = originalGetOntapClientFunc
		}()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		params := RevertVolumeParams{
			VolumeID:   "testVolumeUUID",
			SnapshotID: "testSnapshotUUID",
		}

		mockStorage.On("VolumeModify", mock.Anything).Return(false, nil, errors.New("Only a Snapshot copy of a read/write volume can be promoted"))

		err := rc.RevertVolume(params)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "Cannot revert a Volume Replication Destination Volume")

		mockStorage.AssertExpectations(t)
		mockClient.AssertExpectations(t)
	})

	t.Run("TestRevertVolume_PollError", func(t *testing.T) {
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("Storage").Return(mockStorage)
		originalGetOntapClientFunc := getOntapClientFunc
		defer func() {
			getOntapClientFunc = originalGetOntapClientFunc
		}()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		params := RevertVolumeParams{
			VolumeID:   "testVolumeUUID",
			SnapshotID: "testSnapshotUUID",
		}

		// Mock VolumeModify returns done = false with job
		mockJob := &ontaprest.JobAccepted{JobUUID: "test-job-uuid"}
		mockStorage.On("VolumeModify", mock.Anything).Return(false, mockJob, nil).Once()

		// Mock Poll returns error
		mockClient.On("Poll", mockJob.JobUUID).Return(errors.New("poll error")).Once()

		err := rc.RevertVolume(params)

		assert.Error(t, err)
		assertErrContains(t, err, "poll error")

		mockStorage.AssertExpectations(t)
		mockClient.AssertExpectations(t)
	})

	t.Run("TestRevertVolume_PollErrorWithSnapshotNotFound", func(t *testing.T) {
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("Storage").Return(mockStorage)
		originalGetOntapClientFunc := getOntapClientFunc
		defer func() {
			getOntapClientFunc = originalGetOntapClientFunc
		}()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		params := RevertVolumeParams{
			VolumeID:   "testVolumeUUID",
			SnapshotID: "testSnapshotUUID",
		}

		// Mock VolumeModify returns done = false with job
		mockJob := &ontaprest.JobAccepted{JobUUID: "test-job-uuid"}
		mockStorage.On("VolumeModify", mock.Anything).Return(false, mockJob, nil).Once()

		// Mock Poll returns not found error
		mockClient.On("Poll", mockJob.JobUUID).Return(errors.New("snapshot not found")).Once()

		err := rc.RevertVolume(params)

		assert.Error(t, err)
		assertErrContains(t, err, "not found")

		mockStorage.AssertExpectations(t)
		mockClient.AssertExpectations(t)
	})

	t.Run("TestRevertVolume_PollErrorWithEntryNotFound", func(t *testing.T) {
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("Storage").Return(mockStorage)
		originalGetOntapClientFunc := getOntapClientFunc
		defer func() {
			getOntapClientFunc = originalGetOntapClientFunc
		}()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		params := RevertVolumeParams{
			VolumeID:   "testVolumeUUID",
			SnapshotID: "testSnapshotUUID",
		}

		// Mock VolumeModify returns done = false with job
		mockJob := &ontaprest.JobAccepted{JobUUID: "test-job-uuid"}
		mockStorage.On("VolumeModify", mock.Anything).Return(false, mockJob, nil).Once()

		// Mock Poll returns error with "entry doesn't exist"
		mockClient.On("Poll", mockJob.JobUUID).Return(errors.New("entry doesn't exist")).Once()

		err := rc.RevertVolume(params)

		assert.Error(t, err)
		assertErrContains(t, err, "entry doesn't exist")

		mockStorage.AssertExpectations(t)
		mockClient.AssertExpectations(t)
	})

	t.Run("TestRevertVolume_PollErrorWithReplicationDestination", func(t *testing.T) {
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("Storage").Return(mockStorage)
		originalGetOntapClientFunc := getOntapClientFunc
		defer func() {
			getOntapClientFunc = originalGetOntapClientFunc
		}()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		params := RevertVolumeParams{
			VolumeID:   "testVolumeUUID",
			SnapshotID: "testSnapshotUUID",
		}

		// Mock VolumeModify returns done = false with job
		mockJob := &ontaprest.JobAccepted{JobUUID: "test-job-uuid"}
		mockStorage.On("VolumeModify", mock.Anything).Return(false, mockJob, nil).Once()

		// Mock Poll returns error with replication destination message
		mockClient.On("Poll", mockJob.JobUUID).Return(errors.New("Only a Snapshot copy of a read/write volume can be promoted")).Once()

		err := rc.RevertVolume(params)

		assert.Error(t, err)
		assertErrContains(t, err, "Only a Snapshot copy of a read/write volume can be promoted")

		mockStorage.AssertExpectations(t)
		mockClient.AssertExpectations(t)
	})
}

func TestDeleteVolume_WhenVolumeNotFound_ThenReturnNil(t *testing.T) {
	// Test to cover line 133: return nil when error contains "entry not found"
	mockStorage := new(ontaprest.MockStorageClient)
	mockClient := new(ontaprest.MockRESTClient)
	mockClient.On("Storage").Return(mockStorage)
	originalGetOntapClientFunc := getOntapClientFunc
	defer func() {
		getOntapClientFunc = originalGetOntapClientFunc
	}()
	getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
		return mockClient, nil
	}
	rc := &OntapRestProvider{}

	volumeUUID := "testUUID"
	volumeName := "testVolume"

	// Mock VolumeGet to return error indicating volume not found
	mockStorage.On("VolumeGet", mock.Anything).Return(nil, errors.New("entry not found"))

	err := rc.DeleteVolume(volumeUUID, volumeName)

	// Should return nil when volume not found (line 133)
	assert.NoError(t, err)
	assert.Nil(t, err)

	mockStorage.AssertExpectations(t)
	mockClient.AssertExpectations(t)
}

func TestDeleteVolume_WhenUUIDAndNameParametersEmpty_ThenReturnNil(t *testing.T) {
	// Test to cover line 133: return nil when error contains "UUID and Name parameters cannot be empty when querying for a volume"
	mockStorage := new(ontaprest.MockStorageClient)
	mockClient := new(ontaprest.MockRESTClient)
	mockClient.On("Storage").Return(mockStorage)
	originalGetOntapClientFunc := getOntapClientFunc
	defer func() {
		getOntapClientFunc = originalGetOntapClientFunc
	}()
	getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
		return mockClient, nil
	}
	rc := &OntapRestProvider{}

	volumeUUID := "testUUID"
	volumeName := "testVolume"

	// Mock VolumeGet to return error indicating UUID and Name parameters cannot be empty
	mockStorage.On("VolumeGet", mock.Anything).Return(nil, errors.New("UUID and Name parameters cannot be empty when querying for a volume"))

	err := rc.DeleteVolume(volumeUUID, volumeName)

	// Should return nil when UUID and Name parameters cannot be empty (line 133)
	assert.NoError(t, err)
	assert.Nil(t, err)

	mockStorage.AssertExpectations(t)
	mockClient.AssertExpectations(t)
}

func TestDeleteVolume_WhenVolumeDoesNotExist_ThenReturnNil(t *testing.T) {
	// Test to cover line 96: return nil when volume doesn't exist
	mockStorage := new(ontaprest.MockStorageClient)
	mockClient := new(ontaprest.MockRESTClient)
	mockClient.On("Storage").Return(mockStorage)
	originalGetOntapClientFunc := getOntapClientFunc
	defer func() {
		getOntapClientFunc = originalGetOntapClientFunc
	}()
	getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
		return mockClient, nil
	}
	rc := &OntapRestProvider{}

	volumeUUID := "testUUID"
	volumeName := "testVolume"

	// Mock VolumeGet to return error indicating volume doesn't exist
	mockStorage.On("VolumeGet", mock.Anything).Return(nil, errors.New("entry doesn't exist"))

	err := rc.DeleteVolume(volumeUUID, volumeName)

	// Should return nil when volume doesn't exist (line 96)
	assert.NoError(t, err)
	assert.Nil(t, err)

	mockStorage.AssertExpectations(t)
	mockClient.AssertExpectations(t)
}

func TestUnmountVolume(t *testing.T) {
	t.Run("Success", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient := new(ontaprest.MockRESTClient)
		rc := &OntapRestProvider{}

		accepted := &ontaprest.JobAccepted{JobUUID: ""}

		volumeUUID := "testUUID"

		mm.EXPECT().getOntapClientFunc(mock.Anything).Return(mockClient, nil)
		mockClient.EXPECT().Storage().Return(mockStorage)
		mockStorage.EXPECT().VolumeUnmount(mock.Anything).Return(accepted, nil)

		resp, err := rc.UnmountVolume(volumeUUID)

		assert.NoError(tt, err)
		assert.NotNil(tt, resp)

		mockStorage.AssertExpectations(tt)
		mockClient.AssertExpectations(tt)
	})

	t.Run("WhenGetOntapClientFuncError", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		rc := &OntapRestProvider{}
		errMsg := "client error"
		mm.EXPECT().getOntapClientFunc(mock.Anything).Return(nil, errors.New(errMsg))

		resp, err := rc.UnmountVolume("testUUID")
		assert.Error(tt, err)
		assert.Nil(tt, resp)
		assert.Contains(tt, err.Error(), errMsg)
	})

	t.Run("WhenVolumeUnmountError", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient := new(ontaprest.MockRESTClient)
		rc := &OntapRestProvider{}
		errMsg := "unmount error"
		mm.EXPECT().getOntapClientFunc(mock.Anything).Return(mockClient, nil)
		mockClient.EXPECT().Storage().Return(mockStorage)
		mockStorage.EXPECT().VolumeUnmount(mock.Anything).Return(nil, errors.New(errMsg))

		resp, err := rc.UnmountVolume("testUUID")
		assert.Error(tt, err)
		assert.Nil(tt, resp)
		assert.Contains(tt, err.Error(), errMsg)
	})

	t.Run("WhenNilJobReturned", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient := new(ontaprest.MockRESTClient)
		rc := &OntapRestProvider{}

		mm.EXPECT().getOntapClientFunc(mock.Anything).Return(mockClient, nil)
		mockClient.EXPECT().Storage().Return(mockStorage)
		mockStorage.EXPECT().VolumeUnmount(mock.Anything).Return(nil, nil)

		resp, err := rc.UnmountVolume("testUUID")
		assert.Nil(tt, err)
		assert.Nil(tt, resp)

		mockStorage.AssertExpectations(tt)
		mockClient.AssertExpectations(tt)
	})
}

func TestMountVolume(t *testing.T) {
	t.Run("Success", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient := new(ontaprest.MockRESTClient)
		rc := &OntapRestProvider{}

		accepted := &ontaprest.JobAccepted{JobUUID: ""}
		params := MountVolumeParams{
			UUID:         "test-uuid",
			JunctionPath: "/test/path",
		}

		mm.EXPECT().getOntapClientFunc(mock.Anything).Return(mockClient, nil)
		mockClient.EXPECT().Storage().Return(mockStorage)
		mockStorage.EXPECT().VolumeMount(mock.MatchedBy(func(p *ontaprest.VolumeMountParams) bool {
			return p.UUID == params.UUID && p.JunctionPath == params.JunctionPath
		})).Return(accepted, nil)
		mockClient.EXPECT().Poll("").Return(nil)

		resp, err := rc.MountVolume(params)

		assert.NoError(tt, err)
		assert.NotNil(tt, resp)
		assert.Equal(tt, "", resp.JobUUID)

		mockStorage.AssertExpectations(tt)
		mockClient.AssertExpectations(tt)
	})

	t.Run("SuccessWithJob", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient := new(ontaprest.MockRESTClient)
		rc := &OntapRestProvider{}

		jobUUID := "job-uuid-123"
		accepted := &ontaprest.JobAccepted{JobUUID: jobUUID}
		params := MountVolumeParams{
			UUID:         "test-uuid",
			JunctionPath: "/test/path",
		}

		mm.EXPECT().getOntapClientFunc(mock.Anything).Return(mockClient, nil)
		mockClient.EXPECT().Storage().Return(mockStorage)
		mockStorage.EXPECT().VolumeMount(mock.MatchedBy(func(p *ontaprest.VolumeMountParams) bool {
			return p.UUID == params.UUID && p.JunctionPath == params.JunctionPath
		})).Return(accepted, nil)
		mockClient.EXPECT().Poll(jobUUID).Return(nil)

		resp, err := rc.MountVolume(params)

		assert.NoError(tt, err)
		assert.NotNil(tt, resp)
		assert.Equal(tt, jobUUID, resp.JobUUID)

		mockStorage.AssertExpectations(tt)
		mockClient.AssertExpectations(tt)
	})

	t.Run("WhenGetOntapClientFuncError", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		rc := &OntapRestProvider{}
		errMsg := "client error"
		params := MountVolumeParams{
			UUID:         "test-uuid",
			JunctionPath: "/test/path",
		}

		mm.EXPECT().getOntapClientFunc(mock.Anything).Return(nil, errors.New(errMsg))

		resp, err := rc.MountVolume(params)
		assert.Error(tt, err)
		assert.Nil(tt, resp)
		assert.Contains(tt, err.Error(), errMsg)
	})

	t.Run("WhenVolumeMountError", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient := new(ontaprest.MockRESTClient)
		rc := &OntapRestProvider{}
		errMsg := "mount error"
		params := MountVolumeParams{
			UUID:         "test-uuid",
			JunctionPath: "/test/path",
		}

		mm.EXPECT().getOntapClientFunc(mock.Anything).Return(mockClient, nil)
		mockClient.EXPECT().Storage().Return(mockStorage)
		mockStorage.EXPECT().VolumeMount(mock.Anything).Return(nil, errors.New(errMsg))

		resp, err := rc.MountVolume(params)
		assert.Error(tt, err)
		assert.Nil(tt, resp)
		assert.Contains(tt, err.Error(), errMsg)

		mockStorage.AssertExpectations(tt)
		mockClient.AssertExpectations(tt)
	})

	t.Run("WhenPollingError", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient := new(ontaprest.MockRESTClient)
		rc := &OntapRestProvider{}

		jobUUID := "job-uuid-123"
		accepted := &ontaprest.JobAccepted{JobUUID: jobUUID}
		pollErr := errors.New("polling failed")
		params := MountVolumeParams{
			UUID:         "test-uuid",
			JunctionPath: "/test/path",
		}

		mm.EXPECT().getOntapClientFunc(mock.Anything).Return(mockClient, nil)
		mockClient.EXPECT().Storage().Return(mockStorage)
		mockStorage.EXPECT().VolumeMount(mock.Anything).Return(accepted, nil)
		mockClient.EXPECT().Poll(jobUUID).Return(pollErr)

		resp, err := rc.MountVolume(params)
		assert.Error(tt, err)
		assert.Nil(tt, resp)
		assert.Equal(tt, pollErr, err)

		mockStorage.AssertExpectations(tt)
		mockClient.AssertExpectations(tt)
	})

	t.Run("EmptyJunctionPath", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient := new(ontaprest.MockRESTClient)
		rc := &OntapRestProvider{}

		accepted := &ontaprest.JobAccepted{JobUUID: ""}
		params := MountVolumeParams{
			UUID:         "test-uuid",
			JunctionPath: "", // Empty junction path
		}

		mm.EXPECT().getOntapClientFunc(mock.Anything).Return(mockClient, nil)
		mockClient.EXPECT().Storage().Return(mockStorage)
		mockStorage.EXPECT().VolumeMount(mock.MatchedBy(func(p *ontaprest.VolumeMountParams) bool {
			return p.UUID == params.UUID && p.JunctionPath == ""
		})).Return(accepted, nil)
		mockClient.EXPECT().Poll("").Return(nil)

		resp, err := rc.MountVolume(params)

		assert.NoError(tt, err)
		assert.NotNil(tt, resp)

		mockStorage.AssertExpectations(tt)
		mockClient.AssertExpectations(tt)
	})
}

// TestGetVolumeConstituentCountFallback tests the constituent count extraction logic
func TestGetVolumeConstituentCountFallback(t *testing.T) {
	tests := []struct {
		name               string
		ontapVolume        *ontaprest.Volume
		expectedCount      *int32
		expectedCountValue int32
	}{
		{
			name: "ConstituentCount field available",
			ontapVolume: &ontaprest.Volume{
				Volume: models.Volume{
					ConstituentCount: nillable.ToPointer(int64(8)),
				},
			},
			expectedCount:      nillable.GetInt32Ptr(8),
			expectedCountValue: 8,
		},
		{
			name: "ConstituentCount nil but VolumeInlineConstituents available",
			ontapVolume: &ontaprest.Volume{
				Volume: models.Volume{
					ConstituentCount: nil,
					VolumeInlineConstituents: []*models.VolumeInlineConstituentsInlineArrayItem{
						{Name: nillable.ToPointer("vol1")},
						{Name: nillable.ToPointer("vol2")},
						{Name: nillable.ToPointer("vol3")},
					},
				},
			},
			expectedCount:      nillable.GetInt32Ptr(3),
			expectedCountValue: 3,
		},
		{
			name: "Both ConstituentCount and VolumeInlineConstituents nil",
			ontapVolume: &ontaprest.Volume{
				Volume: models.Volume{
					ConstituentCount:         nil,
					VolumeInlineConstituents: nil,
				},
			},
			expectedCount:      nil,
			expectedCountValue: 0,
		},
		{
			name: "ConstituentCount available and VolumeInlineConstituents also available - prefer ConstituentCount",
			ontapVolume: &ontaprest.Volume{
				Volume: models.Volume{
					ConstituentCount: nillable.ToPointer(int64(5)),
					VolumeInlineConstituents: []*models.VolumeInlineConstituentsInlineArrayItem{
						{Name: nillable.ToPointer("vol1")},
						{Name: nillable.ToPointer("vol2")},
					},
				},
			},
			expectedCount:      nillable.GetInt32Ptr(5),
			expectedCountValue: 5,
		},
		{
			name: "Empty VolumeInlineConstituents array",
			ontapVolume: &ontaprest.Volume{
				Volume: models.Volume{
					ConstituentCount:         nil,
					VolumeInlineConstituents: []*models.VolumeInlineConstituentsInlineArrayItem{},
				},
			},
			expectedCount:      nillable.GetInt32Ptr(0),
			expectedCountValue: 0,
		},
		{
			name: "CreateVolume with ConstituentCount field",
			ontapVolume: &ontaprest.Volume{
				Volume: models.Volume{
					ConstituentCount: nillable.ToPointer(int64(5)),
				},
			},
			expectedCount:      nillable.GetInt32Ptr(5),
			expectedCountValue: 5,
		},
		{
			name: "CreateVolume with VolumeInlineConstituents array",
			ontapVolume: &ontaprest.Volume{
				Volume: models.Volume{
					ConstituentCount: nil,
					VolumeInlineConstituents: []*models.VolumeInlineConstituentsInlineArrayItem{
						{Name: nillable.ToPointer("vol1")},
						{Name: nillable.ToPointer("vol2")},
					},
				},
			},
			expectedCount:      nillable.GetInt32Ptr(2),
			expectedCountValue: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Extract constituent count using the same logic as in GetVolume
			var constituentCount *int32
			if tt.ontapVolume.ConstituentCount != nil {
				count := int32(*tt.ontapVolume.ConstituentCount)
				constituentCount = &count
			} else if tt.ontapVolume.VolumeInlineConstituents != nil {
				// If constituent_count is not available but constituents array is available (even if empty), use the length
				count := int32(len(tt.ontapVolume.VolumeInlineConstituents))
				constituentCount = &count
			}

			// Verify the result
			if tt.expectedCount == nil {
				assert.Nil(t, constituentCount, "Expected constituent count to be nil")
			} else {
				require.NotNil(t, constituentCount, "Expected constituent count to not be nil")
				assert.Equal(t, tt.expectedCountValue, *constituentCount, "Constituent count should match expected value")
			}
		})
	}
}

// TestCreateVolumeConstituentCount tests the constituent count extraction in CreateVolume method
func TestCreateVolumeConstituentCount(t *testing.T) {
	tests := []struct {
		name                     string
		mockVolume               *ontaprest.Volume
		expectedConstituentCount *int32
		description              string
	}{
		{
			name: "CreateVolume with ConstituentCount field",
			mockVolume: &ontaprest.Volume{
				Volume: models.Volume{
					UUID:             nillable.ToPointer("test-uuid"),
					Name:             nillable.ToPointer("test-volume"),
					State:            nillable.ToPointer(models.VolumeStateOnline),
					ConstituentCount: nillable.ToPointer(int64(5)),
					Space: &models.VolumeInlineSpace{
						Available: nillable.ToPointer(int64(1024)),
					},
				},
			},
			expectedConstituentCount: nillable.GetInt32Ptr(5),
			description:              "Should extract constituent count from ConstituentCount field",
		},
		{
			name: "CreateVolume with VolumeInlineConstituents array",
			mockVolume: &ontaprest.Volume{
				Volume: models.Volume{
					UUID:             nillable.ToPointer("test-uuid-2"),
					Name:             nillable.ToPointer("test-volume-2"),
					State:            nillable.ToPointer(models.VolumeStateOnline),
					ConstituentCount: nil,
					VolumeInlineConstituents: []*models.VolumeInlineConstituentsInlineArrayItem{
						{Name: nillable.ToPointer("vol1")},
						{Name: nillable.ToPointer("vol2")},
					},
					Space: &models.VolumeInlineSpace{
						Available: nillable.ToPointer(int64(1024)),
					},
				},
			},
			expectedConstituentCount: nillable.GetInt32Ptr(2),
			description:              "Should count constituents from array when ConstituentCount is nil",
		},
		{
			name: "CreateVolume with empty VolumeInlineConstituents array",
			mockVolume: &ontaprest.Volume{
				Volume: models.Volume{
					UUID:                     nillable.ToPointer("test-uuid-3"),
					Name:                     nillable.ToPointer("test-volume-3"),
					State:                    nillable.ToPointer(models.VolumeStateOnline),
					ConstituentCount:         nil,
					VolumeInlineConstituents: []*models.VolumeInlineConstituentsInlineArrayItem{}, // Empty array
					Space: &models.VolumeInlineSpace{
						Available: nillable.ToPointer(int64(1024)),
					},
				},
			},
			expectedConstituentCount: nillable.GetInt32Ptr(0),
			description:              "Should return 0 for empty constituents array",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			mockStorage := new(ontaprest.MockStorageClient)
			mockClient := new(ontaprest.MockRESTClient)
			mockClient.On("Storage").Return(mockStorage)
			originalGetOntapClientFunc := getOntapClientFunc
			defer func() {
				getOntapClientFunc = originalGetOntapClientFunc
			}()
			getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
				return mockClient, nil
			}
			rc := &OntapRestProvider{}

			volumeName := "testVolume"
			params := CreateVolumeParams{
				VolumeName: volumeName,
				SvmName:    "testSVM",
				Aggregates: []string{"testAggregate"},
				Size:       int64(1024),
				VolumeType: "rw",
			}

			mockJob := &ontaprest.JobAccepted{
				JobUUID:      "testJobUUID",
				ResourceUUID: "testResourceUUID",
			}

			mockStorage.On("VolumeCreate", mock.Anything).Return(tt.mockVolume, mockJob, nil)
			mockClient.On("Poll", mockJob.JobUUID).Return(nil)

			// Act
			resp, err := rc.CreateVolume(params)

			// Assert
			assert.NoError(t, err)
			assert.NotNil(t, resp)
			assert.Equal(t, tt.expectedConstituentCount, resp.ConstituentCount, tt.description)

			mockStorage.AssertExpectations(t)
			mockClient.AssertExpectations(t)
		})
	}
}

// TestCreateVolume_AllSquashUid verifies that when AllSquashUid is set, CreateVolume issues a
// post-create VolumeModify with nas.uid set to the specified UID.
func TestCreateVolume_AllSquashUid(t *testing.T) {
	volumeName := "testVolume"
	volState := models.VolumeStateOnline
	volUUID := "vol-uuid-allsquash"
	volSpace := int64(1024)

	mockVolume := &ontaprest.Volume{
		Volume: models.Volume{
			UUID:  &volUUID,
			Name:  &volumeName,
			State: &volState,
			Space: &models.VolumeInlineSpace{Available: &volSpace},
		},
	}
	mockCreateJob := &ontaprest.JobAccepted{JobUUID: "create-job-uuid"}

	t.Run("WhenAllSquashUidSet_ThenVolumeModifyCalledWithNasUid", func(t *testing.T) {
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("Storage").Return(mockStorage)
		originalGetOntapClientFunc := getOntapClientFunc
		defer func() { getOntapClientFunc = originalGetOntapClientFunc }()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		uid := int64(2000)
		params := CreateVolumeParams{
			VolumeName:   volumeName,
			SvmName:      "testSVM",
			Aggregates:   []string{"agg"},
			Size:         volSpace,
			VolumeType:   "rw",
			AllSquashUid: &uid,
		}

		mockStorage.On("VolumeCreate", mock.Anything).Return(mockVolume, mockCreateJob, nil)
		mockClient.On("Poll", mockCreateJob.JobUUID).Return(nil)
		mockStorage.On("VolumeModify", mock.MatchedBy(func(p *ontaprest.VolumeModifyParams) bool {
			return p.UUID == volUUID && p.NasUid != nil && *p.NasUid == uid
		})).Return(true, nil, nil).Once()

		resp, err := rc.CreateVolume(params)
		assert.NoError(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, volUUID, resp.ExternalUUID)
		mockStorage.AssertExpectations(t)
		mockClient.AssertExpectations(t)
	})

	t.Run("WhenAllSquashUidZero_ThenVolumeModifyCalledWithNasUidZero", func(t *testing.T) {
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("Storage").Return(mockStorage)
		originalGetOntapClientFunc := getOntapClientFunc
		defer func() { getOntapClientFunc = originalGetOntapClientFunc }()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		uid := int64(0)
		params := CreateVolumeParams{
			VolumeName:   volumeName,
			SvmName:      "testSVM",
			Aggregates:   []string{"agg"},
			Size:         volSpace,
			VolumeType:   "rw",
			AllSquashUid: &uid,
		}

		mockStorage.On("VolumeCreate", mock.Anything).Return(mockVolume, mockCreateJob, nil)
		mockClient.On("Poll", mockCreateJob.JobUUID).Return(nil)
		mockStorage.On("VolumeModify", mock.MatchedBy(func(p *ontaprest.VolumeModifyParams) bool {
			return p.UUID == volUUID && p.NasUid != nil && *p.NasUid == int64(0)
		})).Return(true, nil, nil).Once()

		resp, err := rc.CreateVolume(params)
		assert.NoError(t, err)
		assert.NotNil(t, resp)
		mockStorage.AssertExpectations(t)
		mockClient.AssertExpectations(t)
	})

	t.Run("WhenAllSquashUidNil_ThenVolumeModifyNotCalledForNasUid", func(t *testing.T) {
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("Storage").Return(mockStorage)
		originalGetOntapClientFunc := getOntapClientFunc
		defer func() { getOntapClientFunc = originalGetOntapClientFunc }()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		params := CreateVolumeParams{
			VolumeName:   volumeName,
			SvmName:      "testSVM",
			Aggregates:   []string{"agg"},
			Size:         volSpace,
			VolumeType:   "rw",
			AllSquashUid: nil,
		}

		mockStorage.On("VolumeCreate", mock.Anything).Return(mockVolume, mockCreateJob, nil)
		mockClient.On("Poll", mockCreateJob.JobUUID).Return(nil)

		resp, err := rc.CreateVolume(params)
		assert.NoError(t, err)
		assert.NotNil(t, resp)
		mockStorage.AssertNotCalled(t, "VolumeModify")
		mockStorage.AssertExpectations(t)
		mockClient.AssertExpectations(t)
	})

	t.Run("WhenAllSquashUidSet_AndVolumeModifyFails_ThenErrorReturned", func(t *testing.T) {
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("Storage").Return(mockStorage)
		originalGetOntapClientFunc := getOntapClientFunc
		defer func() { getOntapClientFunc = originalGetOntapClientFunc }()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		uid := int64(1000)
		params := CreateVolumeParams{
			VolumeName:   volumeName,
			SvmName:      "testSVM",
			Aggregates:   []string{"agg"},
			Size:         volSpace,
			VolumeType:   "rw",
			AllSquashUid: &uid,
		}

		mockStorage.On("VolumeCreate", mock.Anything).Return(mockVolume, mockCreateJob, nil)
		mockClient.On("Poll", mockCreateJob.JobUUID).Return(nil)
		mockStorage.On("VolumeModify", mock.Anything).Return(false, nil, errors.New("nas uid modify failed")).Once()

		resp, err := rc.CreateVolume(params)
		assert.Error(t, err)
		assert.Nil(t, resp)
		assert.Contains(t, err.Error(), "nas uid modify failed")
		mockStorage.AssertExpectations(t)
		mockClient.AssertExpectations(t)
	})

	t.Run("WhenAllSquashUidSet_AndVolumeModifyJobFails_ThenErrorReturned", func(t *testing.T) {
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("Storage").Return(mockStorage)
		originalGetOntapClientFunc := getOntapClientFunc
		defer func() { getOntapClientFunc = originalGetOntapClientFunc }()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		uid := int64(500)
		params := CreateVolumeParams{
			VolumeName:   volumeName,
			SvmName:      "testSVM",
			Aggregates:   []string{"agg"},
			Size:         volSpace,
			VolumeType:   "rw",
			AllSquashUid: &uid,
		}
		modifyJob := &ontaprest.JobAccepted{JobUUID: "modify-job-uuid"}

		mockStorage.On("VolumeCreate", mock.Anything).Return(mockVolume, mockCreateJob, nil)
		mockClient.On("Poll", mockCreateJob.JobUUID).Return(nil)
		mockStorage.On("VolumeModify", mock.Anything).Return(false, modifyJob, nil).Once()
		mockClient.On("Poll", modifyJob.JobUUID).Return(errors.New("job poll error")).Once()

		resp, err := rc.CreateVolume(params)
		assert.Error(t, err)
		assert.Nil(t, resp)
		assert.Contains(t, err.Error(), "job poll error")
		mockStorage.AssertExpectations(t)
		mockClient.AssertExpectations(t)
	})
}

func TestGetVolumeForExpertMode(t *testing.T) {
	t.Run("WhenVolumeIsFound_ThenReturnVolumeResponse", func(tt *testing.T) {
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("Storage").Return(mockStorage)
		originalGetOntapClientFunc := getOntapClientFunc
		defer func() {
			getOntapClientFunc = originalGetOntapClientFunc
		}()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		volumeName := "testVolume"
		volumeUUID := "testUUID"
		svmName := "testSVM"
		volumeSize := int64(200000)
		volumeState := models.VolumeStateOnline
		volumeStyle := "flexvol"

		mockVolume := &ontaprest.Volume{
			Volume: models.Volume{
				UUID:  nillable.ToPointer(volumeUUID),
				Name:  &volumeName,
				Size:  &volumeSize,
				State: nillable.ToPointer(volumeState),
				Style: &volumeStyle,
			},
		}
		mockStorage.On("VolumeGet", mock.Anything).Return(mockVolume, nil)

		params := GetVolumeParams{
			UUID:       volumeUUID,
			VolumeName: volumeName,
			SvmName:    svmName,
		}
		resp, err := rc.GetVolumeForExpertMode(params)

		assert.NoError(tt, err)
		assert.NotNil(tt, resp)
		assert.Equal(tt, volumeName, resp.Name)
		assert.Equal(tt, volumeUUID, resp.ExternalUUID)
		assert.Equal(tt, volumeSize, resp.Size)
		assert.Equal(tt, string(volumeState), resp.State)
		assert.Equal(tt, volumeStyle, resp.Style)

		mockStorage.AssertExpectations(tt)
		mockClient.AssertExpectations(tt)
	})

	t.Run("WhenVolumeIsFoundWithFlexgroupStyle_ThenReturnVolumeResponse", func(tt *testing.T) {
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("Storage").Return(mockStorage)
		originalGetOntapClientFunc := getOntapClientFunc
		defer func() {
			getOntapClientFunc = originalGetOntapClientFunc
		}()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		volumeName := "testVolume"
		volumeUUID := "testUUID"
		svmName := "testSVM"
		volumeSize := int64(500000)
		volumeState := models.VolumeStateOnline
		volumeStyle := "flexgroup"

		mockVolume := &ontaprest.Volume{
			Volume: models.Volume{
				UUID:  nillable.ToPointer(volumeUUID),
				Name:  &volumeName,
				Size:  &volumeSize,
				State: nillable.ToPointer(volumeState),
				Style: &volumeStyle,
			},
		}
		mockStorage.On("VolumeGet", mock.Anything).Return(mockVolume, nil)

		params := GetVolumeParams{
			UUID:       volumeUUID,
			VolumeName: volumeName,
			SvmName:    svmName,
		}
		resp, err := rc.GetVolumeForExpertMode(params)

		assert.NoError(tt, err)
		assert.NotNil(tt, resp)
		assert.Equal(tt, volumeStyle, resp.Style)
		assert.Equal(tt, volumeSize, resp.Size)

		mockStorage.AssertExpectations(tt)
		mockClient.AssertExpectations(tt)
	})

	t.Run("WhenVolumeIsFoundWithNilStyle_ThenReturnEmptyStyle", func(tt *testing.T) {
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("Storage").Return(mockStorage)
		originalGetOntapClientFunc := getOntapClientFunc
		defer func() {
			getOntapClientFunc = originalGetOntapClientFunc
		}()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		volumeName := "testVolume"
		volumeUUID := "testUUID"
		svmName := "testSVM"
		volumeSize := int64(200000)
		volumeState := models.VolumeStateOnline

		mockVolume := &ontaprest.Volume{
			Volume: models.Volume{
				UUID:  nillable.ToPointer(volumeUUID),
				Name:  &volumeName,
				Size:  &volumeSize,
				State: nillable.ToPointer(volumeState),
				Style: nil, // Style is nil
			},
		}
		mockStorage.On("VolumeGet", mock.Anything).Return(mockVolume, nil)

		params := GetVolumeParams{
			UUID:       volumeUUID,
			VolumeName: volumeName,
			SvmName:    svmName,
		}
		resp, err := rc.GetVolumeForExpertMode(params)

		assert.NoError(tt, err)
		assert.NotNil(tt, resp)
		assert.Equal(tt, "", resp.Style) // Should return empty string when Style is nil

		mockStorage.AssertExpectations(tt)
		mockClient.AssertExpectations(tt)
	})

	t.Run("WhenGetVolumeForExpertMode_ThenCloneFieldsAreNotMapped", func(tt *testing.T) {
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("Storage").Return(mockStorage)
		originalGetOntapClientFunc := getOntapClientFunc
		defer func() {
			getOntapClientFunc = originalGetOntapClientFunc
		}()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		volumeName := "testVolume"
		volumeUUID := "testUUID"
		svmName := "testSVM"
		volumeSize := int64(200000)
		volumeState := models.VolumeStateOnline
		parentVolName := "parent-vol"
		parentVolUUID := "parent-vol-uuid"
		parentSnapName := "parent-snap"
		parentSnapUUID := "parent-snap-uuid"

		mockVolume := &ontaprest.Volume{
			Volume: models.Volume{
				UUID:  nillable.ToPointer(volumeUUID),
				Name:  &volumeName,
				Size:  &volumeSize,
				State: nillable.ToPointer(volumeState),
				Clone: &models.VolumeInlineClone{
					ParentVolume: &models.VolumeInlineCloneInlineParentVolume{
						Name: &parentVolName,
						UUID: &parentVolUUID,
					},
					ParentSnapshot: &models.SnapshotReference{
						Name: &parentSnapName,
						UUID: &parentSnapUUID,
					},
				},
			},
		}
		mockStorage.On("VolumeGet", mock.Anything).Return(mockVolume, nil)

		resp, err := rc.GetVolumeForExpertMode(GetVolumeParams{
			UUID:       volumeUUID,
			VolumeName: volumeName,
			SvmName:    svmName,
		})
		assert.NoError(tt, err)
		assert.Nil(tt, resp.Clone)
	})

	t.Run("WhenGetOntapClientFuncFails_ThenReturnError", func(tt *testing.T) {
		originalGetOntapClientFunc := getOntapClientFunc
		defer func() {
			getOntapClientFunc = originalGetOntapClientFunc
		}()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return nil, errors.New("getOntapClientFunc error")
		}
		rc := &OntapRestProvider{}

		params := GetVolumeParams{
			UUID:       "testUUID",
			VolumeName: "testVolume",
			SvmName:    "testSVM",
		}
		resp, err := rc.GetVolumeForExpertMode(params)

		assert.Error(tt, err)
		assert.Nil(tt, resp)
		assertErrContains(tt, err, "getOntapClientFunc error")
	})

	t.Run("WhenVolumeGetReturnsError_ThenReturnError", func(tt *testing.T) {
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("Storage").Return(mockStorage)
		originalGetOntapClientFunc := getOntapClientFunc
		defer func() {
			getOntapClientFunc = originalGetOntapClientFunc
		}()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		mockStorage.On("VolumeGet", mock.Anything).Return(nil, errors.New("volume get error"))

		params := GetVolumeParams{
			UUID:       "testUUID",
			VolumeName: "testVolume",
			SvmName:    "testSVM",
		}
		resp, err := rc.GetVolumeForExpertMode(params)

		assert.Error(tt, err)
		assert.Nil(tt, resp)
		assert.Equal(tt, "volume get error", err.Error())

		mockStorage.AssertExpectations(tt)
		mockClient.AssertExpectations(tt)
	})
}

// TestGetVolumeForExpertMode_WhenIncompleteOntapResponse_ThenReturnError ensures missing
// required ONTAP fields are not misreported as not-found (e.g. during in-progress delete).
func TestGetVolumeForExpertMode_WhenIncompleteOntapResponse_ThenReturnError(t *testing.T) {
	volumeName := "testVolume"
	volumeUUID := "testUUID"
	svmName := "testSVM"
	volumeSize := int64(200000)
	volumeState := models.VolumeStateOnline

	tests := []struct {
		name       string
		mockVolume *ontaprest.Volume
	}{
		{
			name:       "nil volume",
			mockVolume: nil,
		},
		{
			name: "nil name",
			mockVolume: &ontaprest.Volume{
				Volume: models.Volume{
					UUID:  nillable.ToPointer(volumeUUID),
					Name:  nil,
					Size:  &volumeSize,
					State: nillable.ToPointer(volumeState),
				},
			},
		},
		{
			name: "nil uuid",
			mockVolume: &ontaprest.Volume{
				Volume: models.Volume{
					UUID:  nil,
					Name:  &volumeName,
					Size:  &volumeSize,
					State: nillable.ToPointer(volumeState),
				},
			},
		},
		{
			name: "nil size",
			mockVolume: &ontaprest.Volume{
				Volume: models.Volume{
					UUID:  nillable.ToPointer(volumeUUID),
					Name:  &volumeName,
					Size:  nil,
					State: nillable.ToPointer(volumeState),
				},
			},
		},
		{
			name: "nil state",
			mockVolume: &ontaprest.Volume{
				Volume: models.Volume{
					UUID:  nillable.ToPointer(volumeUUID),
					Name:  &volumeName,
					Size:  &volumeSize,
					State: nil,
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(tt *testing.T) {
			mockStorage := new(ontaprest.MockStorageClient)
			mockClient := new(ontaprest.MockRESTClient)
			mockClient.On("Storage").Return(mockStorage)
			originalGetOntapClientFunc := getOntapClientFunc
			defer func() {
				getOntapClientFunc = originalGetOntapClientFunc
			}()
			getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
				return mockClient, nil
			}
			rc := &OntapRestProvider{}

			mockStorage.On("VolumeGet", mock.Anything).Return(tc.mockVolume, nil).Once()

			params := GetVolumeParams{
				UUID:       volumeUUID,
				VolumeName: volumeName,
				SvmName:    svmName,
			}
			resp, err := rc.GetVolumeForExpertMode(params)

			assert.Nil(tt, resp)
			assert.Error(tt, err)
			assert.False(tt, errors.IsNotFoundErr(err), "incomplete response must not be not-found, got: %v", err)
			assert.Contains(tt, err.Error(), "incomplete ONTAP volume response")

			mockStorage.AssertExpectations(tt)
			mockClient.AssertExpectations(tt)
		})
	}
}

func TestGetCloneVolumeForExpertMode(t *testing.T) {
	t.Run("WhenCloneParentMetadataPresent_ThenReturnCloneParentFields", func(tt *testing.T) {
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("Storage").Return(mockStorage)
		originalGetOntapClientFunc := getOntapClientFunc
		defer func() {
			getOntapClientFunc = originalGetOntapClientFunc
		}()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		volumeName := "clone-vol"
		volumeUUID := "clone-vol-uuid"
		svmName := "testSVM"
		volumeSize := int64(200000)
		volumeState := models.VolumeStateOnline
		parentVolName := "parent-vol"
		parentVolUUID := "parent-vol-uuid"
		parentSnapName := "snap-1"
		parentSnapUUID := "snap-uuid-1"

		mockVolume := &ontaprest.Volume{
			Volume: models.Volume{
				UUID:  nillable.ToPointer(volumeUUID),
				Name:  &volumeName,
				Size:  &volumeSize,
				State: nillable.ToPointer(volumeState),
				Clone: &models.VolumeInlineClone{
					ParentVolume: &models.VolumeInlineCloneInlineParentVolume{
						Name: &parentVolName,
						UUID: &parentVolUUID,
					},
					ParentSnapshot: &models.SnapshotReference{
						Name: &parentSnapName,
						UUID: &parentSnapUUID,
					},
				},
			},
		}
		mockStorage.On("VolumeGet", mock.Anything).Return(mockVolume, nil)

		resp, err := rc.GetCloneVolumeForExpertMode(GetVolumeParams{
			UUID:       volumeUUID,
			VolumeName: volumeName,
			SvmName:    svmName,
		})
		assert.NoError(tt, err)
		require.NotNil(tt, resp.Clone)
		assert.Equal(tt, parentVolName, resp.Clone.ParentVolumeName)
		assert.Equal(tt, parentVolUUID, resp.Clone.ParentVolumeUUID)
		assert.Equal(tt, parentSnapName, resp.Clone.ParentSnapshotName)
		assert.Equal(tt, parentSnapUUID, resp.Clone.ParentSnapshotUUID)

		mockStorage.AssertExpectations(tt)
		mockClient.AssertExpectations(tt)
	})

	t.Run("WhenVolumeGetReturnsIncompleteVolume_ThenReturnError", func(tt *testing.T) {
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("Storage").Return(mockStorage)
		originalGetOntapClientFunc := getOntapClientFunc
		defer func() {
			getOntapClientFunc = originalGetOntapClientFunc
		}()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		volumeName := "clone-vol"
		volumeUUID := "clone-vol-uuid"
		volumeSize := int64(200000)

		mockVolume := &ontaprest.Volume{
			Volume: models.Volume{
				UUID:  nillable.ToPointer(volumeUUID),
				Name:  &volumeName,
				Size:  &volumeSize,
				State: nil,
			},
		}
		mockStorage.On("VolumeGet", mock.Anything).Return(mockVolume, nil)

		resp, err := rc.GetCloneVolumeForExpertMode(GetVolumeParams{
			UUID:       volumeUUID,
			VolumeName: volumeName,
			SvmName:    "testSVM",
		})
		assert.Nil(tt, resp)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "GetCloneVolumeForExpertMode")
		assert.Contains(tt, err.Error(), "incomplete ONTAP volume response")

		mockStorage.AssertExpectations(tt)
		mockClient.AssertExpectations(tt)
	})

	t.Run("WhenGetOntapClientFails_ThenReturnError", func(tt *testing.T) {
		originalGetOntapClientFunc := getOntapClientFunc
		defer func() { getOntapClientFunc = originalGetOntapClientFunc }()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return nil, errors.New("client unavailable")
		}
		rc := &OntapRestProvider{}
		resp, err := rc.GetCloneVolumeForExpertMode(GetVolumeParams{SvmName: "svm"})
		assert.Nil(tt, resp)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "client unavailable")
	})

	t.Run("WhenVolumeGetFails_ThenReturnError", func(tt *testing.T) {
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("Storage").Return(mockStorage)
		originalGetOntapClientFunc := getOntapClientFunc
		defer func() { getOntapClientFunc = originalGetOntapClientFunc }()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		mockStorage.On("VolumeGet", mock.Anything).Return(nil, errors.New("ontap timeout"))
		rc := &OntapRestProvider{}
		resp, err := rc.GetCloneVolumeForExpertMode(GetVolumeParams{VolumeName: "v1", SvmName: "svm"})
		assert.Nil(tt, resp)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "ontap timeout")
		mockStorage.AssertExpectations(tt)
		mockClient.AssertExpectations(tt)
	})
}

func TestGetVolumeNASDetails(t *testing.T) {
	t.Run("WhenGetOntapClientFails_ThenReturnError", func(t *testing.T) {
		origFunc := getOntapClientFunc
		defer func() { getOntapClientFunc = origFunc }()
		getOntapClientFunc = func(ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return nil, errors.New("connection refused")
		}

		rc := &OntapRestProvider{}
		result, err := rc.GetVolumeNASDetails("vol-uuid")

		assert.Nil(t, result)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "connection refused")
	})

	t.Run("WhenVolumeGetFails_ThenReturnError", func(t *testing.T) {
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("Storage").Return(mockStorage)

		origFunc := getOntapClientFunc
		defer func() { getOntapClientFunc = origFunc }()
		getOntapClientFunc = func(ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}

		mockStorage.On("VolumeGet", mock.MatchedBy(func(params *ontaprest.VolumeGetParams) bool {
			return params.UUID == "vol-uuid"
		})).Return(nil, errors.New("volume not found"))

		rc := &OntapRestProvider{}
		result, err := rc.GetVolumeNASDetails("vol-uuid")

		assert.Nil(t, result)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "volume not found")
		mockStorage.AssertExpectations(t)
	})

	t.Run("WhenPassesCorrectParams_ThenFieldsContainNasFields", func(t *testing.T) {
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("Storage").Return(mockStorage)

		origFunc := getOntapClientFunc
		defer func() { getOntapClientFunc = origFunc }()
		getOntapClientFunc = func(ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}

		mockStorage.On("VolumeGet", mock.MatchedBy(func(params *ontaprest.VolumeGetParams) bool {
			return params.UUID == "vol-uuid" &&
				len(params.Fields) == 3 &&
				params.Fields[0] == "nas.path" &&
				params.Fields[1] == "nas.security_style" &&
				params.Fields[2] == "nas.export_policy"
		})).Return(&ontaprest.Volume{Volume: models.Volume{}}, nil)

		rc := &OntapRestProvider{}
		_, err := rc.GetVolumeNASDetails("vol-uuid")

		assert.NoError(t, err)
		mockStorage.AssertExpectations(t)
	})

	t.Run("WhenVolumeHasAllNasFields_ThenReturnPopulatedDetails", func(t *testing.T) {
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("Storage").Return(mockStorage)

		origFunc := getOntapClientFunc
		defer func() { getOntapClientFunc = origFunc }()
		getOntapClientFunc = func(ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}

		mockStorage.On("VolumeGet", mock.Anything).Return(&ontaprest.Volume{
			Volume: models.Volume{
				Nas: &models.VolumeInlineNas{
					Path:          nillable.ToPointer("/vol/test"),
					SecurityStyle: nillable.ToPointer("mixed"),
					ExportPolicy: &models.VolumeInlineNasInlineExportPolicy{
						Name: nillable.ToPointer("my-policy"),
					},
				},
			},
		}, nil)

		rc := &OntapRestProvider{}
		result, err := rc.GetVolumeNASDetails("vol-uuid")

		assert.NoError(t, err)
		assert.Equal(t, "/vol/test", result.NASPath)
		assert.Equal(t, "mixed", result.SecurityStyle)
		assert.Equal(t, "my-policy", result.ExportPolicyName)
	})

	t.Run("WhenVolumeHasNoNas_ThenReturnEmptyDetails", func(t *testing.T) {
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("Storage").Return(mockStorage)

		origFunc := getOntapClientFunc
		defer func() { getOntapClientFunc = origFunc }()
		getOntapClientFunc = func(ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}

		mockStorage.On("VolumeGet", mock.Anything).Return(&ontaprest.Volume{
			Volume: models.Volume{},
		}, nil)

		rc := &OntapRestProvider{}
		result, err := rc.GetVolumeNASDetails("vol-uuid")

		assert.NoError(t, err)
		assert.Equal(t, "", result.NASPath)
		assert.Equal(t, "", result.SecurityStyle)
		assert.Equal(t, "", result.ExportPolicyName)
	})

	t.Run("WhenVolumeIsNil_ThenReturnEmptyDetails", func(t *testing.T) {
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("Storage").Return(mockStorage)

		origFunc := getOntapClientFunc
		defer func() { getOntapClientFunc = origFunc }()
		getOntapClientFunc = func(ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}

		mockStorage.On("VolumeGet", mock.Anything).Return((*ontaprest.Volume)(nil), nil)

		rc := &OntapRestProvider{}
		result, err := rc.GetVolumeNASDetails("vol-uuid")

		assert.NoError(t, err)
		assert.Equal(t, "", result.NASPath)
		assert.Equal(t, "", result.SecurityStyle)
		assert.Equal(t, "", result.ExportPolicyName)
	})

	t.Run("WhenNasPathOnly_ThenOnlyPathPopulated", func(t *testing.T) {
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("Storage").Return(mockStorage)

		origFunc := getOntapClientFunc
		defer func() { getOntapClientFunc = origFunc }()
		getOntapClientFunc = func(ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}

		mockStorage.On("VolumeGet", mock.Anything).Return(&ontaprest.Volume{
			Volume: models.Volume{
				Nas: &models.VolumeInlineNas{
					Path: nillable.ToPointer("/vol/data"),
				},
			},
		}, nil)

		rc := &OntapRestProvider{}
		result, err := rc.GetVolumeNASDetails("vol-uuid")

		assert.NoError(t, err)
		assert.Equal(t, "/vol/data", result.NASPath)
		assert.Equal(t, "", result.SecurityStyle)
		assert.Equal(t, "", result.ExportPolicyName)
	})

	t.Run("WhenSecurityStyleOnly_ThenOnlyStylePopulated", func(t *testing.T) {
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("Storage").Return(mockStorage)

		origFunc := getOntapClientFunc
		defer func() { getOntapClientFunc = origFunc }()
		getOntapClientFunc = func(ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}

		mockStorage.On("VolumeGet", mock.Anything).Return(&ontaprest.Volume{
			Volume: models.Volume{
				Nas: &models.VolumeInlineNas{
					SecurityStyle: nillable.ToPointer("ntfs"),
				},
			},
		}, nil)

		rc := &OntapRestProvider{}
		result, err := rc.GetVolumeNASDetails("vol-uuid")

		assert.NoError(t, err)
		assert.Equal(t, "", result.NASPath)
		assert.Equal(t, "ntfs", result.SecurityStyle)
		assert.Equal(t, "", result.ExportPolicyName)
	})

	t.Run("WhenExportPolicyHasNilName_ThenExportPolicyNameEmpty", func(t *testing.T) {
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("Storage").Return(mockStorage)

		origFunc := getOntapClientFunc
		defer func() { getOntapClientFunc = origFunc }()
		getOntapClientFunc = func(ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}

		mockStorage.On("VolumeGet", mock.Anything).Return(&ontaprest.Volume{
			Volume: models.Volume{
				Nas: &models.VolumeInlineNas{
					Path:         nillable.ToPointer("/vol/test"),
					ExportPolicy: &models.VolumeInlineNasInlineExportPolicy{},
				},
			},
		}, nil)

		rc := &OntapRestProvider{}
		result, err := rc.GetVolumeNASDetails("vol-uuid")

		assert.NoError(t, err)
		assert.Equal(t, "/vol/test", result.NASPath)
		assert.Equal(t, "", result.ExportPolicyName)
	})
}

func TestInitiateSplitVolume(t *testing.T) {
	t.Run("WhenGetOntapClientFails_ReturnsError", func(t *testing.T) {
		origFunc := getOntapClientFunc
		defer func() { getOntapClientFunc = origFunc }()
		getOntapClientFunc = func(ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return nil, errors.New("client error")
		}

		rc := &OntapRestProvider{}
		jobUUID, err := rc.InitiateSplitVolume("vol-uuid")
		assert.Error(t, err)
		assert.Equal(t, "", jobUUID)
		assert.Contains(t, err.Error(), "client error")
	})

	t.Run("WhenVolumeModifyFails_ReturnsError", func(t *testing.T) {
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("Storage").Return(mockStorage)

		origFunc := getOntapClientFunc
		defer func() { getOntapClientFunc = origFunc }()
		getOntapClientFunc = func(ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}

		modifyErr := errors.New("modify failed")
		mockStorage.On("VolumeModify", mock.Anything).Return(false, nil, modifyErr)

		rc := &OntapRestProvider{}
		jobUUID, err := rc.InitiateSplitVolume("vol-uuid")
		assert.Error(t, err)
		assert.Equal(t, "", jobUUID)
		assert.Same(t, modifyErr, err)
		mockStorage.AssertExpectations(t)
	})

	t.Run("WhenVolumeModifySucceedsSynchronously_ReturnsEmptyJobUUID", func(t *testing.T) {
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("Storage").Return(mockStorage)

		origFunc := getOntapClientFunc
		defer func() { getOntapClientFunc = origFunc }()
		getOntapClientFunc = func(ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}

		// success=true means ONTAP completed synchronously — no job UUID.
		mockStorage.On("VolumeModify", mock.Anything).Return(true, nil, nil)

		rc := &OntapRestProvider{}
		jobUUID, err := rc.InitiateSplitVolume("vol-uuid")
		assert.NoError(t, err)
		assert.Equal(t, "", jobUUID)
		mockStorage.AssertExpectations(t)
	})

	t.Run("WhenVolumeModifyReturnsNilJob_ReturnsEmptyJobUUID", func(t *testing.T) {
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("Storage").Return(mockStorage)

		origFunc := getOntapClientFunc
		defer func() { getOntapClientFunc = origFunc }()
		getOntapClientFunc = func(ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}

		// success=false, job=nil — treated as synchronous completion with no job UUID.
		mockStorage.On("VolumeModify", mock.Anything).Return(false, (*ontaprest.JobAccepted)(nil), nil)

		rc := &OntapRestProvider{}
		jobUUID, err := rc.InitiateSplitVolume("vol-uuid")
		assert.NoError(t, err)
		assert.Equal(t, "", jobUUID)
		mockStorage.AssertExpectations(t)
	})

	t.Run("WhenVolumeModifyReturnsJob_ReturnsJobUUID", func(t *testing.T) {
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("Storage").Return(mockStorage)

		origFunc := getOntapClientFunc
		defer func() { getOntapClientFunc = origFunc }()
		getOntapClientFunc = func(ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}

		expectedJobUUID := "ontap-split-job-uuid"
		mockStorage.On("VolumeModify", mock.Anything).Return(false, &ontaprest.JobAccepted{JobUUID: expectedJobUUID}, nil)

		rc := &OntapRestProvider{}
		jobUUID, err := rc.InitiateSplitVolume("vol-uuid")
		assert.NoError(t, err)
		assert.Equal(t, expectedJobUUID, jobUUID)
		mockStorage.AssertExpectations(t)
	})
}

func TestStopSplitVolume(t *testing.T) {
	t.Run("WhenGetOntapClientFails_ReturnsError", func(t *testing.T) {
		origFunc := getOntapClientFunc
		defer func() { getOntapClientFunc = origFunc }()
		getOntapClientFunc = func(ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return nil, errors.New("client error")
		}

		rc := &OntapRestProvider{}
		err := rc.StopSplitVolume("vol-uuid")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "client error")
	})

	t.Run("WhenVolumeModifyFails_ReturnsError", func(t *testing.T) {
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("Storage").Return(mockStorage)

		origFunc := getOntapClientFunc
		defer func() { getOntapClientFunc = origFunc }()
		getOntapClientFunc = func(ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}

		modifyErr := errors.New("modify failed")
		mockStorage.On("VolumeModify", mock.Anything).Return(false, nil, modifyErr)

		rc := &OntapRestProvider{}
		err := rc.StopSplitVolume("vol-uuid")
		assert.Error(t, err)
		assert.Same(t, modifyErr, err)
		mockStorage.AssertExpectations(t)
	})

	t.Run("WhenVolumeModifySucceeds_SendsSplitInitiatedFalse", func(t *testing.T) {
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("Storage").Return(mockStorage)

		origFunc := getOntapClientFunc
		defer func() { getOntapClientFunc = origFunc }()
		getOntapClientFunc = func(ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}

		// The decisive contract: ONTAP must receive split_initiated=false and the
		// correct volume UUID. Anything else passing through MatchedBy fails the test.
		mockStorage.On("VolumeModify", mock.MatchedBy(func(p *ontaprest.VolumeModifyParams) bool {
			if p == nil || p.SplitInitiated == nil {
				return false
			}
			return p.UUID == "vol-uuid" && *p.SplitInitiated == false && p.MatchParentStorageTier == false
		})).Return(true, nil, nil)

		rc := &OntapRestProvider{}
		err := rc.StopSplitVolume("vol-uuid")
		assert.NoError(t, err)
		mockStorage.AssertExpectations(t)
	})

	t.Run("WhenVolumeModifyReturnsJob_StillSucceeds", func(t *testing.T) {
		// ONTAP may return a job UUID even on stop. StopSplitVolume is fire-and-forget
		// (the caller doesn't need to poll), so a returned job MUST NOT surface as an
		// error or alter the success contract.
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("Storage").Return(mockStorage)

		origFunc := getOntapClientFunc
		defer func() { getOntapClientFunc = origFunc }()
		getOntapClientFunc = func(ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}

		mockStorage.On("VolumeModify", mock.Anything).Return(false, &ontaprest.JobAccepted{JobUUID: "stop-job"}, nil)

		rc := &OntapRestProvider{}
		err := rc.StopSplitVolume("vol-uuid")
		assert.NoError(t, err)
		mockStorage.AssertExpectations(t)
	})
}

func TestGetVolumeCloneInfo(t *testing.T) {
	t.Run("WhenGetOntapClientFails_ReturnsError", func(t *testing.T) {
		origFunc := getOntapClientFunc
		defer func() { getOntapClientFunc = origFunc }()
		getOntapClientFunc = func(ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return nil, errors.New("client error")
		}

		rc := &OntapRestProvider{}
		info, err := rc.GetVolumeCloneInfo("vol-uuid")
		assert.Error(t, err)
		assert.Nil(t, info)
		assert.Contains(t, err.Error(), "client error")
	})

	t.Run("WhenVolumeGetFails_ReturnsError", func(t *testing.T) {
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("Storage").Return(mockStorage)

		origFunc := getOntapClientFunc
		defer func() { getOntapClientFunc = origFunc }()
		getOntapClientFunc = func(ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}

		getErr := errors.New("ontap get failed")
		mockStorage.On("VolumeGet", mock.Anything).Return((*ontaprest.Volume)(nil), getErr)

		rc := &OntapRestProvider{}
		info, err := rc.GetVolumeCloneInfo("vol-uuid")
		assert.Error(t, err)
		assert.Same(t, getErr, err)
		assert.Nil(t, info)
		mockStorage.AssertExpectations(t)
	})

	t.Run("WhenVolumeHasNoClone_ReturnsNilInfoNoError", func(t *testing.T) {
		// A volume that is not a clone returns nil clone info with no error so
		// the caller can decide whether the absence is acceptable for its flow.
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("Storage").Return(mockStorage)

		origFunc := getOntapClientFunc
		defer func() { getOntapClientFunc = origFunc }()
		getOntapClientFunc = func(ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}

		mockStorage.On("VolumeGet", mock.Anything).Return(&ontaprest.Volume{
			Volume: models.Volume{Clone: nil},
		}, nil)

		rc := &OntapRestProvider{}
		info, err := rc.GetVolumeCloneInfo("vol-uuid")
		assert.NoError(t, err)
		assert.Nil(t, info)
		mockStorage.AssertExpectations(t)
	})

	t.Run("WhenVolumeIsClone_ReturnsCloneFields", func(t *testing.T) {
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("Storage").Return(mockStorage)

		origFunc := getOntapClientFunc
		defer func() { getOntapClientFunc = origFunc }()
		getOntapClientFunc = func(ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}

		parentName := "parent-vol"
		parentUUID := "parent-uuid"
		snapName := "snap-1"
		snapUUID := "snap-uuid"
		isFlex := true
		splitInitiated := true
		var splitPct int64 = 47

		mockStorage.On("VolumeGet", mock.Anything).Return(&ontaprest.Volume{
			Volume: models.Volume{
				Clone: &models.VolumeInlineClone{
					ParentVolume: &models.VolumeInlineCloneInlineParentVolume{
						Name: &parentName,
						UUID: &parentUUID,
					},
					ParentSnapshot: &models.SnapshotReference{
						Name: &snapName,
						UUID: &snapUUID,
					},
					IsFlexclone:          &isFlex,
					SplitInitiated:       &splitInitiated,
					SplitCompletePercent: &splitPct,
				},
			},
		}, nil)

		rc := &OntapRestProvider{}
		info, err := rc.GetVolumeCloneInfo("vol-uuid")
		assert.NoError(t, err)
		require.NotNil(t, info)
		assert.Equal(t, parentName, info.ParentVolumeName)
		assert.Equal(t, parentUUID, info.ParentVolumeUUID)
		assert.Equal(t, snapName, info.ParentSnapshotName)
		assert.Equal(t, snapUUID, info.ParentSnapshotUUID)
		require.NotNil(t, info.IsFlexclone)
		assert.True(t, *info.IsFlexclone)
		require.NotNil(t, info.SplitInitiated)
		assert.True(t, *info.SplitInitiated)
		require.NotNil(t, info.SplitCompletePercent)
		assert.Equal(t, int64(47), *info.SplitCompletePercent)
		mockStorage.AssertExpectations(t)
	})
}

func TestGetVolumeSANDetails(t *testing.T) {
	makeSANMocks := func() (*ontaprest.MockSANClient, *ontaprest.MockNVMeClient, *ontaprest.MockRESTClient) {
		mockSAN := new(ontaprest.MockSANClient)
		mockNVMe := new(ontaprest.MockNVMeClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("SAN").Return(mockSAN)
		mockClient.On("NVMe").Return(mockNVMe)
		return mockSAN, mockNVMe, mockClient
	}

	setupClientFunc := func(t *testing.T, mockClient *ontaprest.MockRESTClient) {
		orig := getOntapClientFunc
		t.Cleanup(func() { getOntapClientFunc = orig })
		getOntapClientFunc = func(ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
	}

	t.Run("WhenGetOntapClientFails_ThenReturnError", func(t *testing.T) {
		orig := getOntapClientFunc
		t.Cleanup(func() { getOntapClientFunc = orig })
		getOntapClientFunc = func(ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return nil, errors.New("connection refused")
		}

		rc := &OntapRestProvider{}
		result, err := rc.GetVolumeSANDetails("test-svm", "test-vol")

		assert.Nil(t, result)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "connection refused")
	})

	t.Run("WhenLunGetFails_ThenReturnError", func(t *testing.T) {
		mockSAN, _, mockClient := makeSANMocks()
		setupClientFunc(t, mockClient)
		rc := &OntapRestProvider{}

		mockSAN.On("LunGet", mock.Anything).Return(nil, errors.New("ontap api failure"))

		result, err := rc.GetVolumeSANDetails("test-svm", "test-vol")

		assert.Nil(t, result)
		assert.Error(t, err)
		var vcpErr *vsaerrors.CustomError
		require.True(t, vsaerrors.As(err, &vcpErr))
		assert.Contains(t, vcpErr.OriginalErr.Error(), "ontap api failure")
		mockSAN.AssertExpectations(t)
	})

	t.Run("WhenLunGetReturnsNotFound_ThenHasLunsFalse", func(t *testing.T) {
		mockSAN, mockNVMe, mockClient := makeSANMocks()
		setupClientFunc(t, mockClient)
		rc := &OntapRestProvider{}

		mockSAN.On("LunGet", mock.Anything).Return(nil, errors.NewNotFoundErr("lun", nil))
		mockNVMe.On("NamespaceGet", mock.Anything).Return(nil, errors.NewNotFoundErr("nvme namespace", nil))

		result, err := rc.GetVolumeSANDetails("test-svm", "test-vol")

		assert.NoError(t, err)
		assert.False(t, result.HasLUNs)
		assert.False(t, result.HasNamespaces)
		mockSAN.AssertExpectations(t)
		mockNVMe.AssertExpectations(t)
	})

	t.Run("WhenNamespaceGetReturnsNotFound_ThenHasNamespacesFalse", func(t *testing.T) {
		mockSAN, mockNVMe, mockClient := makeSANMocks()
		setupClientFunc(t, mockClient)
		rc := &OntapRestProvider{}

		mockSAN.On("LunGet", mock.Anything).Return(nil, errors.NewNotFoundErr("lun", nil))
		mockNVMe.On("NamespaceGet", mock.Anything).Return(nil, errors.NewNotFoundErr("nvme namespace", nil))

		result, err := rc.GetVolumeSANDetails("test-svm", "test-vol")

		assert.NoError(t, err)
		assert.False(t, result.HasLUNs)
		assert.False(t, result.HasNamespaces)
		mockSAN.AssertExpectations(t)
		mockNVMe.AssertExpectations(t)
	})

	t.Run("WhenLunGetReturnsLuns_ThenHasLunsTrue", func(t *testing.T) {
		mockSAN, mockNVMe, mockClient := makeSANMocks()
		setupClientFunc(t, mockClient)
		rc := &OntapRestProvider{}

		svmName := "test-svm"
		volName := "test-vol"
		mockSAN.On("LunGet", mock.MatchedBy(func(p *ontaprest.LunGetParams) bool {
			return *p.SvmName == svmName && *p.VolumeName == volName
		})).Return([]*ontaprest.Lun{
			{Lun: models.Lun{Name: nillable.ToPointer("/vol/test-vol/lun0"), UUID: nillable.ToPointer("lun-uuid-1")}},
		}, nil)
		mockNVMe.On("NamespaceGet", mock.Anything).Return([]*ontaprest.NvmeNamespace{}, nil)

		result, err := rc.GetVolumeSANDetails(svmName, volName)

		assert.NoError(t, err)
		assert.True(t, result.HasLUNs)
		assert.False(t, result.HasNamespaces)
		mockSAN.AssertExpectations(t)
		mockNVMe.AssertExpectations(t)
	})

	t.Run("WhenNamespaceGetFails_ThenReturnError", func(t *testing.T) {
		mockSAN, mockNVMe, mockClient := makeSANMocks()
		setupClientFunc(t, mockClient)
		rc := &OntapRestProvider{}

		mockSAN.On("LunGet", mock.Anything).Return(nil, errors.NewNotFoundErr("lun", nil))
		mockNVMe.On("NamespaceGet", mock.Anything).Return(nil, errors.New("nvme api failure"))

		result, err := rc.GetVolumeSANDetails("test-svm", "test-vol")

		assert.Nil(t, result)
		assert.Error(t, err)
		var vcpErr *vsaerrors.CustomError
		require.True(t, vsaerrors.As(err, &vcpErr))
		assert.Contains(t, vcpErr.OriginalErr.Error(), "nvme api failure")
		mockSAN.AssertExpectations(t)
		mockNVMe.AssertExpectations(t)
	})

	t.Run("WhenNamespaceGetReturnsNamespaces_ThenHasNamespacesTrue", func(t *testing.T) {
		mockSAN, mockNVMe, mockClient := makeSANMocks()
		setupClientFunc(t, mockClient)
		rc := &OntapRestProvider{}

		svmName := "test-svm"
		volName := "test-vol"
		mockSAN.On("LunGet", mock.Anything).Return(nil, errors.NewNotFoundErr("lun", nil))
		mockNVMe.On("NamespaceGet", mock.MatchedBy(func(p *ontaprest.NvmeNamespaceGetParams) bool {
			return *p.SvmName == svmName && *p.VolumeName == volName
		})).Return([]*ontaprest.NvmeNamespace{
			{NvmeNamespace: models.NvmeNamespace{Name: nillable.ToPointer("/vol/test-vol/ns0"), UUID: nillable.ToPointer("ns-uuid-1")}},
		}, nil)

		result, err := rc.GetVolumeSANDetails(svmName, volName)

		assert.NoError(t, err)
		assert.False(t, result.HasLUNs)
		assert.True(t, result.HasNamespaces)
		mockSAN.AssertExpectations(t)
		mockNVMe.AssertExpectations(t)
	})

	t.Run("WhenBothLunsAndNamespacesFound_ThenBothFlagsTrue", func(t *testing.T) {
		mockSAN, mockNVMe, mockClient := makeSANMocks()
		setupClientFunc(t, mockClient)
		rc := &OntapRestProvider{}

		mockSAN.On("LunGet", mock.Anything).Return([]*ontaprest.Lun{
			{Lun: models.Lun{Name: nillable.ToPointer("/vol/test-vol/lun0"), UUID: nillable.ToPointer("lun-uuid-1")}},
		}, nil)
		mockNVMe.On("NamespaceGet", mock.Anything).Return([]*ontaprest.NvmeNamespace{
			{NvmeNamespace: models.NvmeNamespace{Name: nillable.ToPointer("/vol/test-vol/ns0"), UUID: nillable.ToPointer("ns-uuid-1")}},
		}, nil)

		result, err := rc.GetVolumeSANDetails("test-svm", "test-vol")

		assert.NoError(t, err)
		assert.True(t, result.HasLUNs)
		assert.True(t, result.HasNamespaces)
		mockSAN.AssertExpectations(t)
		mockNVMe.AssertExpectations(t)
	})

	t.Run("WhenPassesCorrectParams_ThenSvmAndVolumeNameForwardedToOntap", func(t *testing.T) {
		mockSAN, mockNVMe, mockClient := makeSANMocks()
		setupClientFunc(t, mockClient)
		rc := &OntapRestProvider{}

		svmName := "my-svm"
		volName := "my-vol"
		mockSAN.On("LunGet", mock.MatchedBy(func(p *ontaprest.LunGetParams) bool {
			return p.SvmName != nil && *p.SvmName == svmName &&
				p.VolumeName != nil && *p.VolumeName == volName
		})).Return(nil, errors.NewNotFoundErr("lun", nil))
		mockNVMe.On("NamespaceGet", mock.MatchedBy(func(p *ontaprest.NvmeNamespaceGetParams) bool {
			return p.SvmName != nil && *p.SvmName == svmName &&
				p.VolumeName != nil && *p.VolumeName == volName
		})).Return(nil, errors.NewNotFoundErr("nvme namespace", nil))

		_, err := rc.GetVolumeSANDetails(svmName, volName)

		assert.NoError(t, err)
		mockSAN.AssertExpectations(t)
		mockNVMe.AssertExpectations(t)
	})
}

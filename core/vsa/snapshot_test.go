package vsa

import (
	"context"
	"testing"
	"time"

	"github.com/go-openapi/strfmt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/models"
	ontaprest "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/ontap-rest"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
)

func assertErrContains(t *testing.T, err error, substring string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error containing %q, got nil", substring)
	}
	var customErr *vsaerrors.CustomError
	if vsaerrors.As(err, &customErr) && customErr.Unwrap() != nil {
		assert.ErrorContains(t, customErr.Unwrap(), substring)
		return
	}
	assert.ErrorContains(t, err, substring)
}

func TestCreateSnapshot(t *testing.T) {
	originalgetOntapClientFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = originalgetOntapClientFunc }()

	t.Run("CreateSnapshotSuccess", func(t *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient.On("Storage").Return(mockStorage)

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		params := CreateSnapshotParams{
			VolumeUUID: "testVolumeUUID",
			Name:       "testSnapshot",
			Comment:    "testComment",
		}

		mockSnapshot := &ontaprest.Snapshot{
			Snapshot: models.Snapshot{
				Comment:     nillable.ToPointer("testComment"),
				Name:        nillable.ToPointer("testSnapshot"),
				Size:        nillable.ToPointer(int64(1024)),
				UUID:        nillable.ToPointer("testUUID"),
				LogicalSize: nillable.ToPointer(int64(1024)),
			},
		}
		mockJob := &ontaprest.JobAccepted{
			JobUUID: "testJobUUID",
		}

		mockStorage.On("SnapshotGet", mock.Anything).Return(mockSnapshot, nil)
		mockStorage.On("SnapshotCreate", mock.Anything).Return(mockSnapshot, mockJob, nil)
		mockClient.On("Poll", mockJob.JobUUID).Return(nil)

		resp, err := rc.CreateSnapshot(params)

		assert.NoError(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, "testSnapshot", resp.Name)
		assert.Equal(t, "testUUID", resp.ExternalUUID)
		assert.Equal(t, int64(0), resp.SizeInBytes)
		mockClient.AssertCalled(t, "Poll", mockJob.JobUUID)
	})

	t.Run("CreateSnapshotErrorOnCreate", func(t *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient.On("Storage").Return(mockStorage)

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		params := CreateSnapshotParams{
			VolumeUUID: "testVolumeUUID",
			Name:       "testSnapshot",
			Comment:    "testComment",
		}

		mockStorage.On("SnapshotCreate", mock.Anything).Return(nil, nil, errors.New("creation error"))

		resp, err := rc.CreateSnapshot(params)

		assert.Error(t, err)
		assert.Nil(t, resp)

		mockStorage.AssertExpectations(t)
		mockClient.AssertExpectations(t)
	})

	t.Run("CreateSnapshotErrorOnPoll", func(t *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient.On("Storage").Return(mockStorage)

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		params := CreateSnapshotParams{
			VolumeUUID: "testVolumeUUID",
			Name:       "testSnapshot",
			Comment:    "testComment",
		}

		mockSnapshot := &ontaprest.Snapshot{
			Snapshot: models.Snapshot{
				Comment: nillable.ToPointer("testComment"),
				Name:    nillable.ToPointer("testSnapshot"),
				Size:    nillable.ToPointer(int64(1024)),
			},
		}
		mockJob := &ontaprest.JobAccepted{
			JobUUID: "testJobUUID",
		}

		mockStorage.On("SnapshotCreate", mock.Anything).Return(mockSnapshot, mockJob, nil)
		mockClient.On("Poll", mockJob.JobUUID).Return(errors.New("polling error"))

		resp, err := rc.CreateSnapshot(params)

		assert.Error(t, err)
		assert.Nil(t, resp)

		mockStorage.AssertExpectations(t)
		mockClient.AssertExpectations(t)
	})

	t.Run("CreateSnapshotConflictError", func(t *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient.On("Storage").Return(mockStorage)

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		params := CreateSnapshotParams{
			VolumeUUID: "testVolumeUUID",
			Name:       "testSnapshot",
			Comment:    "testComment",
		}

		// Return a conflict error
		conflictErr := errors.NewConflictErr("snapshot already exists")
		mockStorage.On("SnapshotCreate", mock.Anything).Return(nil, nil, conflictErr)

		resp, err := rc.CreateSnapshot(params)

		assert.Error(t, err)
		assert.Nil(t, resp)

		// Verify it's a VCPError with ErrCreateSnapshotConflict
		var customErr *vsaerrors.CustomError
		assert.ErrorAs(t, err, &customErr)
		assert.Equal(t, vsaerrors.ErrCreateSnapshotConflict, customErr.TrackingID)

		mockStorage.AssertExpectations(t)
		mockClient.AssertExpectations(t)
	})

	t.Run("CreateSnapshotRWVolumeError", func(t *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient.On("Storage").Return(mockStorage)

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		params := CreateSnapshotParams{
			VolumeUUID: "testVolumeUUID",
			Name:       "testSnapshot",
			Comment:    "testComment",
		}

		// Return ONTAP RW volume error
		rwVolumeErr := errors.NewBadRequestErr("Snapshots can only be created on read/write (RW) volumes")
		mockStorage.On("SnapshotCreate", mock.Anything).Return(nil, nil, rwVolumeErr)

		resp, err := rc.CreateSnapshot(params)

		assert.Error(t, err)
		assert.Nil(t, resp)

		// Verify it's a VCPError with ErrSnapshotNotAllowedForVolume tracking ID
		var customErr *vsaerrors.CustomError
		assert.ErrorAs(t, err, &customErr)
		assert.Equal(t, vsaerrors.ErrSnapshotNotAllowedForVolume, customErr.TrackingID)
		assert.Contains(t, customErr.OriginalErr.Error(), "snapshot creation operation not allowed for this volume")

		mockStorage.AssertExpectations(t)
		mockClient.AssertExpectations(t)
	})

	t.Run("CreateSnapshotRWVolumeErrorOnPoll", func(t *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient.On("Storage").Return(mockStorage)

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		params := CreateSnapshotParams{
			VolumeUUID: "testVolumeUUID",
			Name:       "testSnapshot",
			Comment:    "testComment",
		}

		mockSnapshot := &ontaprest.Snapshot{}
		mockJob := &ontaprest.JobAccepted{
			JobUUID:      "testJobUUID",
			ResourceUUID: "testResourceUUID",
		}

		mockStorage.On("SnapshotCreate", mock.Anything).Return(mockSnapshot, mockJob, nil)
		// Return ONTAP RW volume error during polling
		mockClient.On("Poll", mockJob.JobUUID).Return(errors.New("Snapshots can only be created on read/write (RW) volumes"))

		resp, err := rc.CreateSnapshot(params)

		assert.Error(t, err)
		assert.Nil(t, resp)

		// Verify it's a VCPError with ErrSnapshotNotAllowedForVolume tracking ID
		var customErr *vsaerrors.CustomError
		assert.ErrorAs(t, err, &customErr)
		assert.Equal(t, vsaerrors.ErrSnapshotNotAllowedForVolume, customErr.TrackingID)
		assert.Contains(t, customErr.OriginalErr.Error(), "snapshot creation operation not allowed for this volume")

		mockStorage.AssertExpectations(t)
		mockClient.AssertExpectations(t)
	})

	t.Run("CreateSnapshotInsufficientSpaceError", func(t *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient.On("Storage").Return(mockStorage)

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		params := CreateSnapshotParams{
			VolumeUUID: "testVolumeUUID",
			Name:       "testSnapshot",
			Comment:    "testComment",
		}

		// Return ONTAP insufficient space error
		insufficientSpaceErr := errors.New("Snapshot operation failed: No space left on device. Additional space required: 268KB.")
		mockStorage.On("SnapshotCreate", mock.Anything).Return(nil, nil, insufficientSpaceErr)

		resp, err := rc.CreateSnapshot(params)

		assert.Error(t, err)
		assert.Nil(t, resp)

		// Verify it's a VCPError with ErrSnapshotInsufficientSpace
		var customErr *vsaerrors.CustomError
		assert.ErrorAs(t, err, &customErr)
		assert.Equal(t, vsaerrors.ErrSnapshotInsufficientSpace, customErr.TrackingID)

		mockStorage.AssertExpectations(t)
		mockClient.AssertExpectations(t)
	})

	t.Run("CreateSnapshotInsufficientSpaceErrorOnPoll", func(t *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient.On("Storage").Return(mockStorage)

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		params := CreateSnapshotParams{
			VolumeUUID: "testVolumeUUID",
			Name:       "testSnapshot",
			Comment:    "testComment",
		}

		mockSnapshot := &ontaprest.Snapshot{}
		mockJob := &ontaprest.JobAccepted{
			JobUUID:      "testJobUUID",
			ResourceUUID: "testResourceUUID",
		}

		mockStorage.On("SnapshotCreate", mock.Anything).Return(mockSnapshot, mockJob, nil)
		// Return ONTAP insufficient space error during polling
		mockClient.On("Poll", mockJob.JobUUID).Return(errors.New("Snapshot operation failed: No space left on device. Additional space required: 268KB."))

		resp, err := rc.CreateSnapshot(params)

		assert.Error(t, err)
		assert.Nil(t, resp)

		// Verify it's a VCPError with ErrSnapshotInsufficientSpace
		var customErr *vsaerrors.CustomError
		assert.ErrorAs(t, err, &customErr)
		assert.Equal(t, vsaerrors.ErrSnapshotInsufficientSpace, customErr.TrackingID)

		mockStorage.AssertExpectations(t)
		mockClient.AssertExpectations(t)
	})

	t.Run("CreateSnapshotMaximumLimitExceededError", func(t *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient.On("Storage").Return(mockStorage)

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		params := CreateSnapshotParams{
			VolumeUUID: "testVolumeUUID",
			Name:       "testSnapshot",
			Comment:    "testComment",
		}

		// Return ONTAP maximum limit exceeded error
		maxLimitErr := errors.New("Cannot exceed maximum number of snapshots.")
		mockStorage.On("SnapshotCreate", mock.Anything).Return(nil, nil, maxLimitErr)

		resp, err := rc.CreateSnapshot(params)

		assert.Error(t, err)
		assert.Nil(t, resp)

		// Verify it's a VCPError with ErrSnapshotMaximumLimitExceeded
		var customErr *vsaerrors.CustomError
		assert.ErrorAs(t, err, &customErr)
		assert.Equal(t, vsaerrors.ErrSnapshotMaximumLimitExceeded, customErr.TrackingID)

		mockStorage.AssertExpectations(t)
		mockClient.AssertExpectations(t)
	})

	t.Run("CreateSnapshotMaximumLimitExceededErrorOnPoll", func(t *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient.On("Storage").Return(mockStorage)

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		params := CreateSnapshotParams{
			VolumeUUID: "testVolumeUUID",
			Name:       "testSnapshot",
			Comment:    "testComment",
		}

		mockSnapshot := &ontaprest.Snapshot{}
		mockJob := &ontaprest.JobAccepted{
			JobUUID:      "testJobUUID",
			ResourceUUID: "testResourceUUID",
		}

		mockStorage.On("SnapshotCreate", mock.Anything).Return(mockSnapshot, mockJob, nil)
		// Return ONTAP maximum limit exceeded error during polling
		mockClient.On("Poll", mockJob.JobUUID).Return(errors.New("Cannot exceed maximum number of snapshots."))

		resp, err := rc.CreateSnapshot(params)

		assert.Error(t, err)
		assert.Nil(t, resp)

		// Verify it's a VCPError with ErrSnapshotMaximumLimitExceeded
		var customErr *vsaerrors.CustomError
		assert.ErrorAs(t, err, &customErr)
		assert.Equal(t, vsaerrors.ErrSnapshotMaximumLimitExceeded, customErr.TrackingID)

		mockStorage.AssertExpectations(t)
		mockClient.AssertExpectations(t)
	})

	t.Run("CreateSnapshotInvalidResponse", func(t *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient.On("Storage").Return(mockStorage)

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		params := CreateSnapshotParams{
			VolumeUUID: "testVolumeUUID",
			Name:       "testSnapshot",
			Comment:    "testComment",
		}

		mockSnapshot := &ontaprest.Snapshot{}

		mockStorage.On("SnapshotGet", mock.Anything).Return(nil, nil)
		mockStorage.On("SnapshotCreate", mock.Anything).Return(mockSnapshot, nil, nil)

		resp, err := rc.CreateSnapshot(params)

		assert.Error(t, err)
		assert.Nil(t, resp)
		assertErrContains(t, err, "invalid Snapshot create response from API")
	})

	t.Run("CreateSnapshotVolumeNotFound", func(t *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient.On("Storage").Return(mockStorage)

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		params := CreateSnapshotParams{
			VolumeUUID: "nonExistentVolumeUUID",
			Name:       "testSnapshot",
			Comment:    "testComment",
		}

		mockStorage.On("SnapshotCreate", mock.Anything).Return(nil, nil, errors.NewNotFoundErr("Volume", nil))

		resp, err := rc.CreateSnapshot(params)

		assert.Error(t, err)
		assert.Nil(t, resp)
		assertErrContains(t, err, "Volume not found")
		mockStorage.AssertExpectations(t)
		mockClient.AssertExpectations(t)
	})

	t.Run("CreateSnapshot_GetError", func(t *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient.On("Storage").Return(mockStorage)

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		params := CreateSnapshotParams{
			VolumeUUID: "testVolumeUUID",
			Name:       "testSnapshot",
			Comment:    "testComment",
		}

		mockSnapshot := &ontaprest.Snapshot{
			Snapshot: models.Snapshot{
				Comment:     nillable.ToPointer("testComment"),
				Name:        nillable.ToPointer("testSnapshot"),
				Size:        nillable.ToPointer(int64(1024)),
				UUID:        nillable.ToPointer("testUUID"),
				LogicalSize: nillable.ToPointer(int64(1024)),
			},
		}
		mockJob := &ontaprest.JobAccepted{
			JobUUID: "testJobUUID",
		}

		mockStorage.On("SnapshotGet", mock.Anything).Return(mockSnapshot, errors.New("get error"))
		mockStorage.On("SnapshotCreate", mock.Anything).Return(mockSnapshot, mockJob, nil)
		mockClient.On("Poll", mockJob.JobUUID).Return(nil)

		resp, err := rc.CreateSnapshot(params)

		assert.Error(t, err)
		assert.Nil(t, resp)
	})

	t.Run("CreateSnapshot_NilResponseError", func(t *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient.On("Storage").Return(mockStorage)

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		params := CreateSnapshotParams{
			VolumeUUID: "testVolumeUUID",
			Name:       "testSnapshot",
			Comment:    "testComment",
		}

		mockSnapshot := &ontaprest.Snapshot{
			Snapshot: models.Snapshot{
				UUID: nillable.ToPointer("testUUID"),
			},
		}
		mockJob := &ontaprest.JobAccepted{
			JobUUID: "testJobUUID",
		}

		mockStorage.On("SnapshotGet", mock.Anything).Return(mockSnapshot, nil)
		mockStorage.On("SnapshotCreate", mock.Anything).Return(mockSnapshot, mockJob, nil)
		mockClient.On("Poll", mockJob.JobUUID).Return(nil)

		resp, err := rc.CreateSnapshot(params)

		assert.Error(t, err)
		assert.Nil(t, resp)
	})
}

func TestCreateSnapshotPolicy(t *testing.T) {
	originalgetOntapClientFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = originalgetOntapClientFunc }()

	t.Run("Success", func(t *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockStorage := new(ontaprest.MockStorageClient)
		mockCluster := new(ontaprest.MockClusterClient)
		mockClient.On("Storage").Return(mockStorage)
		mockClient.On("Cluster").Return(mockCluster)

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		schedule := &Schedule{
			Name:        "sched1",
			Months:      []int{1},
			DaysOfMonth: []int{1},
			DaysOfWeek:  []int{1},
			Hours:       []int{1},
			Minutes:     []int{0},
		}
		policy := &SnapshotPolicy{
			Name:      "policy1",
			Comment:   "comment",
			IsEnabled: true,
			Schedules: []*SnapshotPolicySchedule{
				{
					SnapmirrorLabel: "label1",
					Count:           1,
					Schedule:        schedule,
				},
			},
		}

		mockStorage.On("SnapshotPolicyCreate", mock.Anything).Return(nil)

		err := rc.CreateSnapshotPolicy(policy)
		assert.NoError(t, err)
		mockStorage.AssertExpectations(t)
	})

	t.Run("ErrorTooManySchedules", func(t *testing.T) {
		rc := &OntapRestProvider{}
		policy := &SnapshotPolicy{
			Name:      "policy1",
			Comment:   "comment",
			IsEnabled: true,
			Schedules: []*SnapshotPolicySchedule{
				{}, {}, {}, {}, {}, // 5 schedules
			},
		}
		err := rc.CreateSnapshotPolicy(policy)
		assert.Error(t, err)
		assertErrContains(t, err, "too many snapshot policy schedules")
	})

	t.Run("ErrorNoSchedules", func(t *testing.T) {
		rc := &OntapRestProvider{}
		policy := &SnapshotPolicy{
			Name:      "policy1",
			Comment:   "comment",
			IsEnabled: true,
			Schedules: []*SnapshotPolicySchedule{},
		}
		err := rc.CreateSnapshotPolicy(policy)
		assert.Error(t, err)
		assertErrContains(t, err, "must have at least one snapshot policy schedule")
	})

	t.Run("ErrorOnCreate", func(t *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockStorage := new(ontaprest.MockStorageClient)
		mockCluster := new(ontaprest.MockClusterClient)
		mockClient.On("Storage").Return(mockStorage)
		mockClient.On("Cluster").Return(mockCluster)

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		schedule := &Schedule{
			Name:        "sched1",
			Months:      []int{1},
			DaysOfMonth: []int{1},
			DaysOfWeek:  []int{1},
			Hours:       []int{1},
			Minutes:     []int{0},
		}
		policy := &SnapshotPolicy{
			Name:      "policy1",
			Comment:   "comment",
			IsEnabled: true,
			Schedules: []*SnapshotPolicySchedule{
				{
					SnapmirrorLabel: "label1",
					Count:           1,
					Schedule:        schedule,
				},
			},
		}

		mockStorage.On("SnapshotPolicyCreate", mock.Anything).Return(errors.New("some error"))

		err := rc.CreateSnapshotPolicy(policy)
		assert.Error(t, err)
		assertErrContains(t, err, "some error")
	})

	t.Run("ErrorOnScheduleCreate", func(t *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockStorage := new(ontaprest.MockStorageClient)
		mockCluster := new(ontaprest.MockClusterClient)
		mockClient.On("Storage").Return(mockStorage)
		mockClient.On("Cluster").Return(mockCluster)

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		schedule := &Schedule{
			Name:        "sched1",
			Months:      []int{1},
			DaysOfMonth: []int{1},
			DaysOfWeek:  []int{1},
			Hours:       []int{1},
			Minutes:     []int{0},
		}
		policy := &SnapshotPolicy{
			Name:      "policy1",
			Comment:   "comment",
			IsEnabled: true,
			Schedules: []*SnapshotPolicySchedule{
				{
					SnapmirrorLabel: "label1",
					Count:           1,
					Schedule:        schedule,
				},
			},
		}

		// First call returns "not found" error with "Schedule" prefix, triggering schedule creation
		mockStorage.On("SnapshotPolicyCreate", mock.Anything).Return(errors.New("Schedule not found"))
		// ScheduleCreate returns an error that is not "exists" or "duplicate entry"
		mockCluster.On("ScheduleCreate", mock.Anything).Return(errors.New("unexpected error"))

		err := rc.CreateSnapshotPolicy(policy)
		assert.Error(t, err)
		assertErrContains(t, err, "unexpected error")
	})

	t.Run("ScheduleAlreadyExists", func(t *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockStorage := new(ontaprest.MockStorageClient)
		mockCluster := new(ontaprest.MockClusterClient)
		mockClient.On("Storage").Return(mockStorage)
		mockClient.On("Cluster").Return(mockCluster)

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		schedule := &Schedule{
			Name:        "sched1",
			Months:      []int{1},
			DaysOfMonth: []int{1},
			DaysOfWeek:  []int{1},
			Hours:       []int{1},
			Minutes:     []int{0},
		}
		policy := &SnapshotPolicy{
			Name:      "policy1",
			Comment:   "comment",
			IsEnabled: true,
			Schedules: []*SnapshotPolicySchedule{
				{
					SnapmirrorLabel: "label1",
					Count:           1,
					Schedule:        schedule,
				},
			},
		}

		mockStorage.On("SnapshotPolicyCreate", mock.Anything).Return(errors.New("Schedule not found")).Once()
		mockCluster.On("ScheduleCreate", mock.Anything).Return(errors.New("already exists")).Once()
		mockStorage.On("SnapshotPolicyCreate", mock.Anything).Return(nil).Once()

		err := rc.CreateSnapshotPolicy(policy)
		assert.NoError(t, err)
		mockStorage.AssertExpectations(t)
		mockCluster.AssertExpectations(t)
	})
}

func TestDeleteSnapshot(t *testing.T) {
	originalgetOntapClientFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = originalgetOntapClientFunc }()

	t.Run("DeleteSnapshotSuccess", func(t *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient.On("Storage").Return(mockStorage)

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		snapshotName := "testSnapshot"
		snapshotUUID := "testSnapshotUUID"
		volumeUUID := "testVolumeUUID"

		mockSnapshot := &ontaprest.Snapshot{
			Snapshot: models.Snapshot{
				Name: nillable.ToPointer(snapshotName),
				UUID: nillable.ToPointer(snapshotUUID),
			},
		}
		mockJob := &ontaprest.JobAccepted{
			JobUUID: "testJobUUID",
		}

		mockStorage.On("SnapshotGet", mock.Anything).Return(mockSnapshot, nil)
		mockStorage.On("SnapshotDelete", mock.Anything).Return(false, mockJob, nil)
		mockClient.On("Poll", mockJob.JobUUID).Return(nil)

		err := rc.DeleteSnapshot(snapshotUUID, volumeUUID)

		assert.NoError(t, err)
		mockStorage.AssertExpectations(t)
		mockClient.AssertExpectations(t)
	})

	t.Run("DeleteSnapshotNotFound", func(t *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient.On("Storage").Return(mockStorage)

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}

		rc := &OntapRestProvider{
			Logger: log.NewLogger().(*log.Slogger), // Ensure logger implements *log.Slogger
		}
		snapshotUUID := "testSnapshotUUID"
		volumeUUID := "testVolumeUUID"

		mockStorage.On("SnapshotGet", mock.Anything).Return(nil, errors.NewNotFoundErr("Snapshot", nil))
		mockVolume := &ontaprest.Volume{
			Volume: models.Volume{
				State: nillable.ToPointer("online"),
			},
		}
		mockStorage.On("VolumeGet", mock.Anything).Return(mockVolume, nil)

		err := rc.DeleteSnapshot(snapshotUUID, volumeUUID)

		assert.NoError(t, err)
		mockStorage.AssertExpectations(t)
		mockClient.AssertExpectations(t)
	})

	t.Run("DeleteSnapshotVolumeNotFound", func(t *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient.On("Storage").Return(mockStorage)

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		snapshotUUID := "testSnapshotUUID"
		volumeUUID := "testVolumeUUID"

		mockStorage.On("SnapshotGet", mock.Anything).Return(nil, errors.NewNotFoundErr("Snapshot", nil))
		mockStorage.On("VolumeGet", mock.Anything).Return(nil, errors.NewNotFoundErr("Volume", nil))

		err := rc.DeleteSnapshot(snapshotUUID, volumeUUID)

		assert.NoError(t, err)
		mockStorage.AssertExpectations(t)
		mockClient.AssertExpectations(t)
	})

	t.Run("DeleteSnapshotVolumeNotOnline", func(t *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient.On("Storage").Return(mockStorage)

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		snapshotUUID := "testSnapshotUUID"
		volumeUUID := "testVolumeUUID"

		mockStorage.On("SnapshotGet", mock.Anything).Return(nil, errors.NewNotFoundErr("Snapshot", nil))
		mockVolume := &ontaprest.Volume{
			Volume: models.Volume{
				State: nillable.ToPointer("offline"),
			},
		}
		mockStorage.On("VolumeGet", mock.Anything).Return(mockVolume, nil)

		err := rc.DeleteSnapshot(snapshotUUID, volumeUUID)

		assert.Error(t, err)
		assertErrContains(t, err, "Cannot delete snapshot because volume is not online")
		mockStorage.AssertExpectations(t)
		mockClient.AssertExpectations(t)
	})

	t.Run("DeleteSnapshotErrorOnGet", func(t *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient.On("Storage").Return(mockStorage)

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		snapshotUUID := "testSnapshotUUID"
		volumeUUID := "testVolumeUUID"

		mockStorage.On("SnapshotGet", mock.Anything).Return(nil, errors.New("get error"))

		err := rc.DeleteSnapshot(snapshotUUID, volumeUUID)

		assert.Error(t, err)
		mockStorage.AssertExpectations(t)
		mockClient.AssertExpectations(t)
	})

	t.Run("DeleteSnapshotErrorOnDelete", func(t *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient.On("Storage").Return(mockStorage)

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{
			Logger: log.NewLogger().(*log.Slogger),
		}

		snapshotName := "testSnapshot"
		snapshotUUID := "testSnapshotUUID"
		volumeUUID := "testVolumeUUID"

		mockSnapshot := &ontaprest.Snapshot{
			Snapshot: models.Snapshot{
				Name: nillable.ToPointer(snapshotName),
				UUID: nillable.ToPointer(snapshotUUID),
			},
		}
		mockJob := &ontaprest.JobAccepted{
			JobUUID: "testJobUUID",
		}

		mockStorage.On("SnapshotGet", mock.Anything).Return(mockSnapshot, nil)
		mockStorage.On("SnapshotDelete", mock.Anything).Return(false, mockJob, errors.New("delete error"))

		err := rc.DeleteSnapshot(snapshotUUID, volumeUUID)

		assert.Error(t, err)
		mockStorage.AssertExpectations(t)
		mockClient.AssertExpectations(t)
	})

	t.Run("DeleteSnapshotSnapshotHasOwners", func(t *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient.On("Storage").Return(mockStorage)

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		snapshotUUID := "testSnapshotUUID"
		volumeUUID := "testVolumeUUID"

		mockSnapshot := &ontaprest.Snapshot{
			Snapshot: models.Snapshot{
				UUID:   nillable.ToPointer(snapshotUUID),
				Owners: []*string{nillable.ToPointer("owner1"), nillable.ToPointer("owner2")}},
		}

		mockStorage.On("SnapshotGet", mock.Anything).Return(mockSnapshot, nil)

		err := rc.DeleteSnapshot(snapshotUUID, volumeUUID)

		assert.Error(t, err)
		assertErrContains(t, err, "Cannot delete a snapshot that is being actively used")
		mockStorage.AssertExpectations(t)
		mockClient.AssertExpectations(t)
	})

	t.Run("DeleteSnapshotErrorOnPoll", func(t *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient.On("Storage").Return(mockStorage)

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		snapshotUUID := "testSnapshotUUID"
		volumeUUID := "testVolumeUUID"

		mockSnapshot := &ontaprest.Snapshot{
			Snapshot: models.Snapshot{
				UUID: nillable.ToPointer(snapshotUUID),
			},
		}
		mockJob := &ontaprest.JobAccepted{
			JobUUID: "testJobUUID",
		}

		mockStorage.On("SnapshotGet", mock.Anything).Return(mockSnapshot, nil)
		mockStorage.On("SnapshotDelete", mock.Anything).Return(true, mockJob, nil)
		mockClient.On("Poll", mockJob.JobUUID).Return(errors.New("polling error"))

		err := rc.DeleteSnapshot(snapshotUUID, volumeUUID)

		assert.Error(t, err)
		assertErrContains(t, err, "polling error")
		mockStorage.AssertExpectations(t)
		mockClient.AssertExpectations(t)
	})
}

func TestGetSnapshots(t *testing.T) {
	originalgetOntapClientFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = originalgetOntapClientFunc }()

	t.Run("GetSnapshotsSuccess", func(t *testing.T) {
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("Storage").Return(mockStorage)

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		mockStorage.On("SnapshotCollectionGet", mock.Anything, mock.Anything).Return(nil)

		_, err := rc.GetSnapshots("test-volume-uuid")
		assert.NoError(t, err)
		mockStorage.AssertExpectations(t)
		mockClient.AssertExpectations(t)
	})
}

func TestGenerateNameForSchedule(t *testing.T) {
	t.Run("MonthlySchedule", func(t *testing.T) {
		schedule := &Schedule{
			DaysOfMonth: []int{1, 15},
			Minutes:     []int{0},
			Hours:       []int{2},
		}
		name := generateNameForSchedule(schedule)
		assert.Contains(t, name, "monthly-on-day-1+15")
		assert.Contains(t, name, "0-min-past")
		assert.Contains(t, name, "2am")
	})

	t.Run("WeeklySchedule", func(t *testing.T) {
		schedule := &Schedule{
			DaysOfWeek: []int{1, 2},
			Minutes:    []int{30},
			Hours:      []int{5},
		}
		name := generateNameForSchedule(schedule)
		assert.Contains(t, name, "weekly-on-monday+tuesday")
		assert.Contains(t, name, "30-min-past")
		assert.Contains(t, name, "5am")
	})

	t.Run("DailySchedule", func(t *testing.T) {
		schedule := &Schedule{
			Hours:   []int{7},
			Minutes: []int{45},
		}
		name := generateNameForSchedule(schedule)
		assert.Contains(t, name, "daily-45-min-past")
		assert.Contains(t, name, "7am")
	})

	t.Run("HourlySchedule", func(t *testing.T) {
		schedule := &Schedule{
			Minutes: []int{15},
		}
		name := generateNameForSchedule(schedule)
		assert.Contains(t, name, "hourly-15-min-past-hour")
	})

	t.Run("EmptySchedule", func(t *testing.T) {
		schedule := &Schedule{}
		name := generateNameForSchedule(schedule)
		assert.Contains(t, name, "hourly-0-min-past-hour")
	})
}

func TestDeleteSnapshotPolicy(t *testing.T) {
	originalgetOntapClientFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = originalgetOntapClientFunc }()

	t.Run("DeleteSnapshotPolicySuccess", func(t *testing.T) {
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("Storage").Return(mockStorage)
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}

		snapshotPolicyName := "testSnapshotPolicy"

		mockStorage.On("SnapshotPolicyDelete", mock.Anything).Return(nil)
		rc := &OntapRestProvider{}
		err := rc.DeleteSnapshotPolicy(snapshotPolicyName)

		assert.NoError(t, err)

		mockStorage.AssertExpectations(t)
		mockClient.AssertExpectations(t)
	})

	t.Run("DeleteSnapshotPolicyError", func(t *testing.T) {
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("Storage").Return(mockStorage)
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}

		rc := &OntapRestProvider{}
		snapshotPolicyName := "testSnapshotPolicy"

		mockStorage.On("SnapshotPolicyDelete", mock.Anything).Return(errors.New("deletion error"))
		err := rc.DeleteSnapshotPolicy(snapshotPolicyName)

		assert.Error(t, err)
		mockStorage.AssertExpectations(t)
		mockClient.AssertExpectations(t)
	})
}

func TestEqualSnapshotPolicySchedule(t *testing.T) {
	s1 := SnapshotPolicySchedule{
		SnapmirrorLabel: "label",
		Count:           1,
		Schedule: &Schedule{
			Minutes:     []int{0, 30},
			Hours:       []int{1, 2},
			DaysOfWeek:  []int{1, 2},
			DaysOfMonth: []int{5, 10},
			Months:      []int{1, 6},
		},
	}
	s2 := SnapshotPolicySchedule{
		SnapmirrorLabel: "label",
		Count:           2,
		Schedule: &Schedule{
			Minutes:     []int{30, 0},
			Hours:       []int{2, 1},
			DaysOfWeek:  []int{2, 1},
			DaysOfMonth: []int{10, 5},
			Months:      []int{6, 1},
		},
	}
	s3 := SnapshotPolicySchedule{
		SnapmirrorLabel: "label",
		Count:           1,
		Schedule: &Schedule{
			Minutes:     []int{0},
			Hours:       []int{1},
			DaysOfWeek:  []int{1},
			DaysOfMonth: []int{5},
			Months:      []int{1},
		},
	}
	assert.True(t, equalSnapshotPolicySchedule(s1, s2))
	assert.False(t, equalSnapshotPolicySchedule(s1, s3))
}

func TestEqualIntArrays(t *testing.T) {
	assert.True(t, equalIntArrays([]int{1, 2, 3}, []int{3, 2, 1}))
	assert.False(t, equalIntArrays([]int{1, 2}, []int{1, 2, 3}))
	assert.True(t, equalIntArrays([]int{}, []int{}))
	assert.False(t, equalIntArrays([]int{1}, []int{2}))
}

func TestUpdateSnapshotPolicy(t *testing.T) {
	originalgetOntapClientFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = originalgetOntapClientFunc }()

	t.Run("UpdateSnapshotPolicySuccess", func(t *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient.On("Storage").Return(mockStorage)
		ctx := context.Background()

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		params := &UpdateSnapshotPolicyParams{
			UpdatingSnapshotPolicy: &SnapshotPolicy{
				Name:      "policy1",
				IsEnabled: true,
				Schedules: []*SnapshotPolicySchedule{
					{
						SnapmirrorLabel: "label1",
						Count:           1,
						Schedule:        &Schedule{Name: "sched1"},
					},
				},
			},
			CurrentSnapshotPolicy: &SnapshotPolicy{
				Name:      "policy1",
				IsEnabled: false,
				Schedules: []*SnapshotPolicySchedule{
					{
						SnapmirrorLabel: "label1",
						Count:           1,
						Schedule:        &Schedule{Name: "sched1"},
					},
				},
			},
		}

		mockStorage.On("SnapshotPolicyFind", mock.Anything).Return(&models.SnapshotPolicy{UUID: nillable.GetStringPtr("test"), SnapshotPolicyInlineCopies: make([]*models.SnapshotPolicyInlineCopiesInlineArrayItem, 0)}, nil)
		mockStorage.On("SnapshotPolicyModify", mock.Anything).Return(nil)

		err := rc.UpdateSnapshotPolicy(ctx, params)
		assert.NoError(t, err)
		mockStorage.AssertExpectations(t)
		mockClient.AssertExpectations(t)
	})

	t.Run("UpdateSnapshotPolicyError", func(t *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient.On("Storage").Return(mockStorage)
		ctx := context.Background()

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		params := &UpdateSnapshotPolicyParams{
			UpdatingSnapshotPolicy: &SnapshotPolicy{
				Name:      "policy1",
				IsEnabled: true,
				Schedules: []*SnapshotPolicySchedule{
					{
						SnapmirrorLabel: "label1",
						Count:           1,
						Schedule:        &Schedule{Name: "sched1"},
					},
				},
			},
			CurrentSnapshotPolicy: &SnapshotPolicy{
				Name:      "policy1",
				IsEnabled: false,
				Schedules: []*SnapshotPolicySchedule{
					{
						SnapmirrorLabel: "label1",
						Count:           1,
						Schedule:        &Schedule{Name: "sched1"},
					},
				},
			},
		}

		mockStorage.On("SnapshotPolicyFind", mock.Anything).Return(nil, errors.New("find error"))

		err := rc.UpdateSnapshotPolicy(ctx, params)
		assert.Error(t, err)
		mockStorage.AssertExpectations(t)
		mockClient.AssertExpectations(t)
	})
}

func Test_generateSnapshotPolicyScheduleUpdateStrategy(t *testing.T) {
	// Helper to create a schedule
	makeSchedule := func(mins, hrs, dows, doms, months []int) *SnapshotPolicySchedule {
		return &SnapshotPolicySchedule{
			SnapmirrorLabel: "label",
			Count:           1,
			Schedule: &Schedule{
				Minutes:     mins,
				Hours:       hrs,
				DaysOfWeek:  dows,
				DaysOfMonth: doms,
				Months:      months,
			},
		}
	}

	t.Run("No changes", func(t *testing.T) {
		current := []*SnapshotPolicySchedule{makeSchedule([]int{0}, []int{1}, nil, nil, nil)}
		updating := []*SnapshotPolicySchedule{makeSchedule([]int{0}, []int{1}, nil, nil, nil)}
		actions := _generateSnapshotPolicyScheduleUpdateStrategy(updating, current)
		assert.Len(t, actions, 0)
	})

	t.Run("Add new schedule", func(t *testing.T) {
		current := []*SnapshotPolicySchedule{}
		updating := []*SnapshotPolicySchedule{makeSchedule([]int{0}, []int{1}, nil, nil, nil)}
		actions := _generateSnapshotPolicyScheduleUpdateStrategy(updating, current)
		assert.Len(t, actions, 1)
		assert.Equal(t, add, actions[0].Action)
	})

	t.Run("Remove schedule", func(t *testing.T) {
		current := []*SnapshotPolicySchedule{makeSchedule([]int{0}, []int{1}, nil, nil, nil)}
		updating := []*SnapshotPolicySchedule{}
		actions := _generateSnapshotPolicyScheduleUpdateStrategy(updating, current)
		assert.Len(t, actions, 1)
		assert.Equal(t, rem, actions[0].Action)
	})

	t.Run("Modify count", func(t *testing.T) {
		cur := makeSchedule([]int{0}, []int{1}, nil, nil, nil)
		upd := makeSchedule([]int{0}, []int{1}, nil, nil, nil)
		upd.Count = 2
		actions := _generateSnapshotPolicyScheduleUpdateStrategy([]*SnapshotPolicySchedule{upd}, []*SnapshotPolicySchedule{cur})
		assert.Len(t, actions, 1)
		assert.Equal(t, mod, actions[0].Action)
	})

	t.Run("Replace schedule with same label but different time", func(t *testing.T) {
		cur := makeSchedule([]int{0}, []int{1}, nil, nil, nil)
		upd := makeSchedule([]int{30}, []int{2}, nil, nil, nil)
		actions := _generateSnapshotPolicyScheduleUpdateStrategy([]*SnapshotPolicySchedule{upd}, []*SnapshotPolicySchedule{cur})
		assert.True(t, len(actions) >= 2)
		assert.Equal(t, add, actions[0].Action)
		assert.Equal(t, rem, actions[1].Action)
	})

	t.Run("Replace single schedule with same prefix triggers tmp schedule", func(t *testing.T) {
		cur := makeSchedule([]int{0}, []int{1}, nil, nil, nil)
		upd := makeSchedule([]int{30}, []int{2}, nil, nil, nil)
		actions := _generateSnapshotPolicyScheduleUpdateStrategy([]*SnapshotPolicySchedule{upd}, []*SnapshotPolicySchedule{cur})
		// Should prepend tmp schedule, then rem, add, then remove tmp
		if len(actions) == 4 {
			assert.Equal(t, add, actions[0].Action)
			assert.Equal(t, "vsa-tmp", actions[0].SnapshotPolicySchedule.Schedule.Name)
			assert.Equal(t, rem, actions[1].Action)
			assert.Equal(t, add, actions[2].Action)
			assert.Equal(t, rem, actions[3].Action)
			assert.Equal(t, "vsa-tmp", actions[3].SnapshotPolicySchedule.Schedule.Name)
		}
	})
}

func Test_updateSnapshotPolicy(t *testing.T) {
	originalgetOntapClientFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = originalgetOntapClientFunc }()

	t.Run("Success with enabled change", func(t *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockStorage := new(ontaprest.MockStorageClient)
		mockCluster := new(ontaprest.MockClusterClient)
		mockClient.On("Storage").Return(mockStorage)
		mockClient.On("Cluster").Return(mockCluster)
		ctx := context.Background()

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}

		params := &UpdateSnapshotPolicyParams{
			UpdatingSnapshotPolicy: &SnapshotPolicy{
				Name:      "policy1",
				IsEnabled: true,
				Schedules: []*SnapshotPolicySchedule{
					{
						SnapmirrorLabel: "label1",
						Count:           1,
						Schedule:        &Schedule{Name: "sched1"},
					},
				},
			},
			CurrentSnapshotPolicy: &SnapshotPolicy{
				Name:      "policy1",
				IsEnabled: false,
				Schedules: []*SnapshotPolicySchedule{
					{
						SnapmirrorLabel: "label1",
						Count:           1,
						Schedule:        &Schedule{Name: "sched1"},
					},
				},
			},
		}

		mockStorage.On("SnapshotPolicyFind", mock.Anything).Return(&models.SnapshotPolicy{
			UUID: nillable.GetStringPtr("test"),
			SnapshotPolicyInlineCopies: []*models.SnapshotPolicyInlineCopiesInlineArrayItem{
				{
					SnapmirrorLabel: nillable.GetStringPtr("label1"),
					Schedule: &models.SnapshotPolicyInlineCopiesInlineArrayItemInlineSchedule{
						Name: nillable.GetStringPtr("sched1"),
						UUID: nillable.GetStringPtr("sched-uuid"),
					},
				},
			},
		}, nil)
		mockStorage.On("SnapshotPolicyModify", mock.Anything).Return(nil)

		err := _updateSnapshotPolicy(ctx, mockClient, params)
		assert.NoError(t, err)
		mockStorage.AssertExpectations(t)
	})

	t.Run("Error on find", func(t *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient.On("Storage").Return(mockStorage)
		ctx := context.Background()

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}

		params := &UpdateSnapshotPolicyParams{
			UpdatingSnapshotPolicy: &SnapshotPolicy{Name: "policy1"},
			CurrentSnapshotPolicy:  &SnapshotPolicy{Name: "policy1"},
		}

		mockStorage.On("SnapshotPolicyFind", mock.Anything).Return(nil, errors.New("find error"))

		err := _updateSnapshotPolicy(ctx, mockClient, params)
		assert.Error(t, err)
	})

	t.Run("Error on modify", func(t *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient.On("Storage").Return(mockStorage)
		ctx := context.Background()

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}

		params := &UpdateSnapshotPolicyParams{
			UpdatingSnapshotPolicy: &SnapshotPolicy{
				Name:      "policy1",
				IsEnabled: true,
				Schedules: []*SnapshotPolicySchedule{},
			},
			CurrentSnapshotPolicy: &SnapshotPolicy{
				Name:      "policy1",
				IsEnabled: false,
				Schedules: []*SnapshotPolicySchedule{},
			},
		}

		mockStorage.On("SnapshotPolicyFind", mock.Anything).Return(&models.SnapshotPolicy{
			UUID:                       nillable.GetStringPtr("test"),
			SnapshotPolicyInlineCopies: []*models.SnapshotPolicyInlineCopiesInlineArrayItem{},
		}, nil)
		mockStorage.On("SnapshotPolicyModify", mock.Anything).Return(errors.New("modify error"))

		err := _updateSnapshotPolicy(ctx, mockClient, params)
		assert.Error(t, err)
	})

	t.Run("Error on add schedule", func(t *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient.On("Storage").Return(mockStorage)
		ctx := context.Background()

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}

		params := &UpdateSnapshotPolicyParams{
			UpdatingSnapshotPolicy: &SnapshotPolicy{
				Name:      "policy1",
				IsEnabled: true,
				Schedules: []*SnapshotPolicySchedule{
					{
						SnapmirrorLabel: "label1",
						Count:           1,
						Schedule:        &Schedule{Name: "sched1"},
					},
				},
			},
			CurrentSnapshotPolicy: &SnapshotPolicy{
				Name:      "policy1",
				IsEnabled: true,
				Schedules: []*SnapshotPolicySchedule{},
			},
		}

		mockStorage.On("SnapshotPolicyFind", mock.Anything).Return(&models.SnapshotPolicy{
			UUID:                       nillable.GetStringPtr("test"),
			SnapshotPolicyInlineCopies: []*models.SnapshotPolicyInlineCopiesInlineArrayItem{},
		}, nil)

		// Patch addSnapshotPolicySchedule to return error
		origAdd := addSnapshotPolicySchedule
		addSnapshotPolicySchedule = func(ctx context.Context, api ontaprest.RESTClient, policyUUID string, schedule *SnapshotPolicySchedule) (string, error) {
			return "", errors.New("add error")
		}
		defer func() { addSnapshotPolicySchedule = origAdd }()

		err := _updateSnapshotPolicy(ctx, mockClient, params)
		assert.Error(t, err)
	})

	t.Run("Error on remove schedule", func(t *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient.On("Storage").Return(mockStorage)
		ctx := context.Background()

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}

		params := &UpdateSnapshotPolicyParams{
			UpdatingSnapshotPolicy: &SnapshotPolicy{
				Name:      "policy1",
				IsEnabled: true,
				Schedules: []*SnapshotPolicySchedule{},
			},
			CurrentSnapshotPolicy: &SnapshotPolicy{
				Name:      "policy1",
				IsEnabled: true,
				Schedules: []*SnapshotPolicySchedule{
					{
						SnapmirrorLabel: "label1",
						Count:           1,
						Schedule:        &Schedule{Name: "sched1"},
					},
				},
			},
		}

		mockStorage.On("SnapshotPolicyFind", mock.Anything).Return(&models.SnapshotPolicy{
			UUID: nillable.GetStringPtr("test"),
			SnapshotPolicyInlineCopies: []*models.SnapshotPolicyInlineCopiesInlineArrayItem{
				{
					Schedule: &models.SnapshotPolicyInlineCopiesInlineArrayItemInlineSchedule{
						Name: nillable.GetStringPtr("sched1"),
						UUID: nillable.GetStringPtr("sched-uuid"),
					},
					SnapmirrorLabel: nillable.GetStringPtr("label1"),
				},
			},
		}, nil)

		// Patch removeSnapshotPolicySchedule to return error
		origRem := removeSnapshotPolicySchedule
		removeSnapshotPolicySchedule = func(ctx context.Context, api ontaprest.RESTClient, policyUUID string, scheduleUUID string) error {
			return errors.New("remove error")
		}
		defer func() { removeSnapshotPolicySchedule = origRem }()

		err := _updateSnapshotPolicy(ctx, mockClient, params)
		assert.Error(t, err)
	})

	t.Run("Error on modify schedule", func(t *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient.On("Storage").Return(mockStorage)
		ctx := context.Background()

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}

		params := &UpdateSnapshotPolicyParams{
			UpdatingSnapshotPolicy: &SnapshotPolicy{
				Name:      "policy1",
				IsEnabled: true,
				Schedules: []*SnapshotPolicySchedule{
					{
						SnapmirrorLabel: "label1",
						Count:           2, // count changed
						Schedule:        &Schedule{Name: "sched1"},
					},
				},
			},
			CurrentSnapshotPolicy: &SnapshotPolicy{
				Name:      "policy1",
				IsEnabled: true,
				Schedules: []*SnapshotPolicySchedule{
					{
						SnapmirrorLabel: "label1",
						Count:           1,
						Schedule:        &Schedule{Name: "sched1"},
					},
				},
			},
		}

		mockStorage.On("SnapshotPolicyFind", mock.Anything).Return(&models.SnapshotPolicy{
			UUID: nillable.GetStringPtr("test"),
			SnapshotPolicyInlineCopies: []*models.SnapshotPolicyInlineCopiesInlineArrayItem{
				{
					Schedule: &models.SnapshotPolicyInlineCopiesInlineArrayItemInlineSchedule{
						Name: nillable.GetStringPtr("sched1"),
						UUID: nillable.GetStringPtr("sched-uuid"),
					},
					SnapmirrorLabel: nillable.GetStringPtr("label1"),
				},
			},
		}, nil)

		// Patch modifySnapshotPolicySchedule to return error
		origMod := modifySnapshotPolicySchedule
		modifySnapshotPolicySchedule = func(ctx context.Context, api ontaprest.RESTClient, policyUUID string, scheduleUUID string, schedule *SnapshotPolicySchedule) error {
			return errors.New("modify error")
		}
		defer func() { modifySnapshotPolicySchedule = origMod }()

		err := _updateSnapshotPolicy(ctx, mockClient, params)
		assert.Error(t, err)
	})

	t.Run("success tmp schedule", func(t *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient.On("Storage").Return(mockStorage)
		ctx := context.Background()

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}

		params := &UpdateSnapshotPolicyParams{
			UpdatingSnapshotPolicy: &SnapshotPolicy{
				Name:      "policy1",
				IsEnabled: true,
				Schedules: []*SnapshotPolicySchedule{
					{
						SnapmirrorLabel: "label1",
						Count:           1,
						Schedule:        &Schedule{Minutes: []int{30}, Hours: []int{2}},
					},
				},
			},
			CurrentSnapshotPolicy: &SnapshotPolicy{
				Name:      "policy1",
				IsEnabled: true,
				Schedules: []*SnapshotPolicySchedule{
					{
						SnapmirrorLabel: "label1",
						Count:           1,
						Schedule:        &Schedule{Minutes: []int{0}, Hours: []int{1}},
					},
				},
			},
		}

		mockStorage.On("SnapshotPolicyFind", mock.Anything).Return(&models.SnapshotPolicy{
			UUID: nillable.GetStringPtr("test"),
			SnapshotPolicyInlineCopies: []*models.SnapshotPolicyInlineCopiesInlineArrayItem{
				{
					Schedule: &models.SnapshotPolicyInlineCopiesInlineArrayItemInlineSchedule{
						Name: nillable.GetStringPtr("vsa-..."), // name doesn't matter for error path
						UUID: nillable.GetStringPtr("sched-uuid"),
					},
					SnapmirrorLabel: nillable.GetStringPtr("label1"),
				},
			},
		}, nil)

		// Patch addSnapshotPolicySchedule to return error for tmp schedule
		origAdd := addSnapshotPolicySchedule
		addSnapshotPolicySchedule = func(ctx context.Context, api ontaprest.RESTClient, policyUUID string, schedule *SnapshotPolicySchedule) (string, error) {
			return "sched-uuid", nil
		}
		defer func() { addSnapshotPolicySchedule = origAdd }()

		// Patch removeSnapshotPolicySchedule to return error for tmp schedule
		origRem := removeSnapshotPolicySchedule
		removeSnapshotPolicySchedule = func(ctx context.Context, api ontaprest.RESTClient, policyUUID string, scheduleUUID string) error {
			return nil
		}
		defer func() { removeSnapshotPolicySchedule = origRem }()

		// Patch modifySnapshotPolicySchedule to return error for tmp schedule
		origMod := modifySnapshotPolicySchedule
		modifySnapshotPolicySchedule = func(ctx context.Context, api ontaprest.RESTClient, policyUUID string, scheduleUUID string, schedule *SnapshotPolicySchedule) error {
			return nil
		}
		defer func() { modifySnapshotPolicySchedule = origMod }()

		err := _updateSnapshotPolicy(ctx, mockClient, params)
		assert.NoError(t, err)
	})
}

func Test_addSnapshotPolicySchedule(t *testing.T) {
	mockClient := new(ontaprest.MockRESTClient)
	mockStorage := new(ontaprest.MockStorageClient)
	mockCluster := new(ontaprest.MockClusterClient)
	mockClient.On("Storage").Return(mockStorage)
	mockClient.On("Cluster").Return(mockCluster)
	ctx := context.Background()

	schedule := &SnapshotPolicySchedule{
		SnapmirrorLabel: "label",
		Count:           1,
		Schedule: &Schedule{
			Name:    "sched1",
			Minutes: []int{0},
		},
	}

	t.Run("Success", func(t *testing.T) {
		mockStorage.On("SnapshotPolicyScheduleCreate", mock.Anything).Return("uuid", nil).Once()
		uuid, err := _addSnapshotPolicySchedule(ctx, mockClient, "policy-uuid", schedule)
		assert.NoError(t, err)
		assert.Equal(t, "uuid", uuid)
		mockStorage.AssertExpectations(t)
	})

	t.Run("Schedule not found, ScheduleCreate success", func(t *testing.T) {
		mockStorage.On("SnapshotPolicyScheduleCreate", mock.Anything).Return("", errors.New("not found")).Once()
		mockCluster.On("ScheduleCreate", mock.Anything).Return(nil).Once()
		mockStorage.On("SnapshotPolicyScheduleCreate", mock.Anything).Return("uuid2", nil).Once()
		uuid, err := _addSnapshotPolicySchedule(ctx, mockClient, "policy-uuid", schedule)
		assert.NoError(t, err)
		assert.Equal(t, "uuid2", uuid)
		mockStorage.AssertExpectations(t)
		mockCluster.AssertExpectations(t)
	})

	t.Run("Schedule not found, ScheduleCreate already exists", func(t *testing.T) {
		mockStorage.On("SnapshotPolicyScheduleCreate", mock.Anything).Return("", errors.New("not found")).Once()
		mockCluster.On("ScheduleCreate", mock.Anything).Return(errors.New("already exists")).Once()
		mockStorage.On("SnapshotPolicyScheduleCreate", mock.Anything).Return("uuid3", nil).Once()
		uuid, err := _addSnapshotPolicySchedule(ctx, mockClient, "policy-uuid", schedule)
		assert.NoError(t, err)
		assert.Equal(t, "uuid3", uuid)
		mockStorage.AssertExpectations(t)
		mockCluster.AssertExpectations(t)
	})

	t.Run("Schedule not found, ScheduleCreate error", func(t *testing.T) {
		mockStorage.On("SnapshotPolicyScheduleCreate", mock.Anything).Return("", errors.New("not found")).Once()
		mockCluster.On("ScheduleCreate", mock.Anything).Return(errors.New("unexpected error")).Once()
		uuid, err := _addSnapshotPolicySchedule(ctx, mockClient, "policy-uuid", schedule)
		assert.Error(t, err)
		assert.Empty(t, uuid)
		mockStorage.AssertExpectations(t)
		mockCluster.AssertExpectations(t)
	})

	t.Run("Other error", func(t *testing.T) {
		mockStorage.On("SnapshotPolicyScheduleCreate", mock.Anything).Return("", errors.New("other error")).Once()
		uuid, err := _addSnapshotPolicySchedule(ctx, mockClient, "policy-uuid", schedule)
		assert.Error(t, err)
		assert.Empty(t, uuid)
		mockStorage.AssertExpectations(t)
	})
}

func Test_modifySnapshotPolicySchedule(t *testing.T) {
	mockClient := new(ontaprest.MockRESTClient)
	mockStorage := new(ontaprest.MockStorageClient)
	mockClient.On("Storage").Return(mockStorage)
	ctx := context.Background()

	schedule := &SnapshotPolicySchedule{
		SnapmirrorLabel: "label",
		Count:           2,
		Schedule: &Schedule{
			Name:    "sched1",
			Minutes: []int{0},
		},
	}

	t.Run("Success", func(t *testing.T) {
		mockStorage.On("SnapshotPolicyScheduleModify", mock.Anything).Return(nil).Once()
		err := _modifySnapshotPolicySchedule(ctx, mockClient, "policy-uuid", "sched-uuid", schedule)
		assert.NoError(t, err)
		mockStorage.AssertExpectations(t)
	})

	t.Run("Not found error", func(t *testing.T) {
		mockStorage.On("SnapshotPolicyScheduleModify", mock.Anything).Return(errors.New("not found")).Once()
		err := _modifySnapshotPolicySchedule(ctx, mockClient, "policy-uuid", "sched-uuid", schedule)
		assert.NoError(t, err)
		mockStorage.AssertExpectations(t)
	})

	t.Run("Does not exist error", func(t *testing.T) {
		mockStorage.On("SnapshotPolicyScheduleModify", mock.Anything).Return(errors.New("does not exist")).Once()
		err := _modifySnapshotPolicySchedule(ctx, mockClient, "policy-uuid", "sched-uuid", schedule)
		assert.NoError(t, err)
		mockStorage.AssertExpectations(t)
	})

	t.Run("Other error", func(t *testing.T) {
		mockStorage.On("SnapshotPolicyScheduleModify", mock.Anything).Return(errors.New("other error")).Once()
		err := _modifySnapshotPolicySchedule(ctx, mockClient, "policy-uuid", "sched-uuid", schedule)
		assert.Error(t, err)
		mockStorage.AssertExpectations(t)
	})
}

func Test_removeSnapshotPolicySchedule(t *testing.T) {
	mockClient := new(ontaprest.MockRESTClient)
	mockStorage := new(ontaprest.MockStorageClient)
	mockClient.On("Storage").Return(mockStorage)
	ctx := context.Background()

	t.Run("Success", func(t *testing.T) {
		mockStorage.On("SnapshotPolicyScheduleDelete", mock.Anything).Return(nil).Once()
		err := _removeSnapshotPolicySchedule(ctx, mockClient, "policy-uuid", "sched-uuid")
		assert.NoError(t, err)
		mockStorage.AssertExpectations(t)
	})

	t.Run("Not found error", func(t *testing.T) {
		mockStorage.On("SnapshotPolicyScheduleDelete", mock.Anything).Return(errors.New("not found")).Once()
		err := _removeSnapshotPolicySchedule(ctx, mockClient, "policy-uuid", "sched-uuid")
		assert.NoError(t, err)
		mockStorage.AssertExpectations(t)
	})

	t.Run("Does not exist error", func(t *testing.T) {
		mockStorage.On("SnapshotPolicyScheduleDelete", mock.Anything).Return(errors.New("does not exist")).Once()
		err := _removeSnapshotPolicySchedule(ctx, mockClient, "policy-uuid", "sched-uuid")
		assert.NoError(t, err)
		mockStorage.AssertExpectations(t)
	})

	t.Run("Other error", func(t *testing.T) {
		mockStorage.On("SnapshotPolicyScheduleDelete", mock.Anything).Return(errors.New("other error")).Once()
		err := _removeSnapshotPolicySchedule(ctx, mockClient, "policy-uuid", "sched-uuid")
		assert.Error(t, err)
		mockStorage.AssertExpectations(t)
	})
}

func TestActionString(t *testing.T) {
	assert.Equal(t, "add", add.String())
	assert.Equal(t, "rem", rem.String())
	assert.Equal(t, "mod", mod.String())
}

func TestDeleteSnapshot_SnapshotNotFound(t *testing.T) {
	mockClient := new(ontaprest.MockRESTClient)
	mockStorage := new(ontaprest.MockStorageClient)
	mockClient.On("Storage").Return(mockStorage)

	getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
		return mockClient, nil
	}
	rc := &OntapRestProvider{
		Logger: log.NewLogger().(*log.Slogger),
	}

	snapshotUUID := "nonexistent-snapshot-uuid"
	volumeUUID := "test-volume-uuid"

	// Simulate SnapshotGet returns nil snapshot and a not found error
	mockStorage.On("SnapshotGet", mock.Anything).Return(nil, errors.NewNotFoundErr("Snapshot", nil))
	// Simulate VolumeGet returns a valid online volume
	mockVolume := &ontaprest.Volume{
		Volume: models.Volume{
			State: nillable.ToPointer("online"),
		},
	}
	mockStorage.On("VolumeGet", mock.Anything).Return(mockVolume, nil)

	err := rc.DeleteSnapshot(snapshotUUID, volumeUUID)
	assert.NoError(t, err)
	mockStorage.AssertExpectations(t)
	mockClient.AssertExpectations(t)
}

func TestListSnapmirrorSnapshots(t *testing.T) {
	t.Run("ListSnapshotSuccess", func(t *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient.On("Storage").Return(mockStorage)

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}
		snap := &ontaprest.Snapshot{
			Snapshot: models.Snapshot{
				Name:             nillable.ToPointer("monthly.test"),
				UUID:             nillable.ToPointer("snap uuid"),
				CreateTime:       nillable.ToPointer(strfmt.DateTime(time.Now())),
				ReclaimableSpace: nillable.ToPointer(int64(1)),
				Size:             nillable.ToPointer(int64(2)),
				LogicalSize:      nillable.ToPointer(int64(3)),
			},
		}
		volumeUUID := "testVolumeUUID"
		mockVolume := &ontaprest.Volume{
			Volume: models.Volume{
				State: nillable.ToPointer("online"),
			},
		}
		mockStorage.On("VolumeGet", mock.Anything).Return(mockVolume, nil)

		mockStorage.On("SnapshotCollectionGet", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
			callback := args.Get(1).(ontaprest.UserCallbackFunc[[]*ontaprest.Snapshot])
			_ = callback([]*ontaprest.Snapshot{snap})
		}).Return(nil)
		res, err := rc.ListSnapmirrorSnapshots(volumeUUID)
		assert.NoError(t, err)
		assert.Len(t, res, 1)
		mockStorage.AssertExpectations(t)
		mockClient.AssertExpectations(t)
	})
	t.Run("ListSnapshotVolumeNotFound", func(t *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient.On("Storage").Return(mockStorage)

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		volumeUUID := "testVolumeUUID"

		mockStorage.On("VolumeGet", mock.Anything).Return(nil, errors.NewNotFoundErr("Volume", nil))

		_, err := rc.ListSnapmirrorSnapshots(volumeUUID)

		assert.Error(t, err)
		assertErrContains(t, err, "Volume not found")
		mockStorage.AssertExpectations(t)
		mockClient.AssertExpectations(t)
	})

	t.Run("ListSnapshotVolumeNotOnline", func(t *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient.On("Storage").Return(mockStorage)

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		volumeUUID := "testVolumeUUID"

		mockVolume := &ontaprest.Volume{
			Volume: models.Volume{
				State: nillable.ToPointer("offline"),
			},
		}
		mockStorage.On("VolumeGet", mock.Anything).Return(mockVolume, nil)

		_, err := rc.ListSnapmirrorSnapshots(volumeUUID)

		assert.Error(t, err)
		assertErrContains(t, err, "Cannot delete snapshot because volume is not online")
		mockStorage.AssertExpectations(t)
		mockClient.AssertExpectations(t)
	})

	t.Run("ListSnapshotGetError", func(t *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient.On("Storage").Return(mockStorage)

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		volumeUUID := "testVolumeUUID"
		mockVolume := &ontaprest.Volume{
			Volume: models.Volume{
				State: nillable.ToPointer("online"),
			},
		}
		mockStorage.On("VolumeGet", mock.Anything).Return(mockVolume, nil)
		mockStorage.On("SnapshotCollectionGet", mock.Anything, mock.Anything).Return(errors.New("get error"))

		_, err := rc.ListSnapmirrorSnapshots(volumeUUID)
		assert.Error(t, err)
		assert.EqualError(t, err, "get error")
		mockStorage.AssertExpectations(t)
		mockClient.AssertExpectations(t)
	})
}

func TestGetSnapshot(t *testing.T) {
	originalGetOntapClientFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = originalGetOntapClientFunc }()

	t.Run("Success", func(t *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient.On("Storage").Return(mockStorage)

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		snapshotUUID := "test-snapshot-uuid"
		volumeUUID := "test-volume-uuid"

		mockSnapshot := &ontaprest.Snapshot{
			Snapshot: models.Snapshot{
				Name:        nillable.ToPointer("test-snapshot"),
				UUID:        nillable.ToPointer(snapshotUUID),
				Size:        nillable.ToPointer(int64(1024)),
				LogicalSize: nillable.ToPointer(int64(2048)),
			},
		}

		mockStorage.On("SnapshotGet", mock.Anything).Return(mockSnapshot, nil)

		resp, err := rc.GetSnapshot(snapshotUUID, volumeUUID)

		assert.NoError(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, "test-snapshot", resp.Name)
		assert.Equal(t, snapshotUUID, resp.ExternalUUID)
		assert.Equal(t, int64(1024), resp.SizeInBytes)
		assert.Equal(t, int64(2048), resp.LogicalSizeInBytes)
		mockStorage.AssertExpectations(t)
	})

	t.Run("ClientError", func(t *testing.T) {
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return nil, errors.New("client error")
		}
		rc := &OntapRestProvider{}

		resp, err := rc.GetSnapshot("snapshot-uuid", "volume-uuid")

		assert.Error(t, err)
		assert.Nil(t, resp)
		assertErrContains(t, err, "client error")
	})

	t.Run("SnapshotGetAPIError", func(t *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient.On("Storage").Return(mockStorage)

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		mockStorage.On("SnapshotGet", mock.Anything).Return(nil, errors.New("API error"))

		resp, err := rc.GetSnapshot("snapshot-uuid", "volume-uuid")

		assert.Error(t, err)
		assert.Nil(t, resp)
		mockStorage.AssertExpectations(t)
	})

	t.Run("SnapshotNotFoundVolumeExists", func(t *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient.On("Storage").Return(mockStorage)

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{
			Logger: log.NewLogger().(*log.Slogger),
		}

		mockVolume := &ontaprest.Volume{
			Volume: models.Volume{
				State: nillable.ToPointer("online"),
			},
		}

		mockStorage.On("SnapshotGet", mock.Anything).Return(nil, errors.NewNotFoundErr("Snapshot", nil))
		mockStorage.On("VolumeGet", mock.Anything).Return(mockVolume, nil)

		resp, err := rc.GetSnapshot("snapshot-uuid", "volume-uuid")

		assert.Error(t, err)
		assert.Nil(t, resp)
		mockStorage.AssertExpectations(t)
	})

	t.Run("SnapshotNotFoundVolumeNotFound", func(t *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient.On("Storage").Return(mockStorage)

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		mockStorage.On("SnapshotGet", mock.Anything).Return(nil, errors.NewNotFoundErr("Snapshot", nil))
		mockStorage.On("VolumeGet", mock.Anything).Return(nil, errors.NewNotFoundErr("Volume", nil))

		resp, err := rc.GetSnapshot("snapshot-uuid", "volume-uuid")

		assert.Error(t, err)
		assert.Nil(t, resp)
		assertErrContains(t, err, "Volume")
		mockStorage.AssertExpectations(t)
	})

	t.Run("SnapshotNotFoundVolumeGetError", func(t *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient.On("Storage").Return(mockStorage)

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		mockStorage.On("SnapshotGet", mock.Anything).Return(nil, errors.NewNotFoundErr("Snapshot", nil))
		mockStorage.On("VolumeGet", mock.Anything).Return(nil, errors.New("volume API error"))

		resp, err := rc.GetSnapshot("snapshot-uuid", "volume-uuid")

		assert.Error(t, err)
		assert.Nil(t, resp)
		mockStorage.AssertExpectations(t)
	})

	t.Run("SnapshotNotFoundVolumeOffline", func(t *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient.On("Storage").Return(mockStorage)

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		mockVolume := &ontaprest.Volume{
			Volume: models.Volume{
				State: nillable.ToPointer("offline"),
			},
		}

		mockStorage.On("SnapshotGet", mock.Anything).Return(nil, errors.NewNotFoundErr("Snapshot", nil))
		mockStorage.On("VolumeGet", mock.Anything).Return(mockVolume, nil)

		resp, err := rc.GetSnapshot("snapshot-uuid", "volume-uuid")

		assert.Error(t, err)
		assert.Nil(t, resp)
		assertErrContains(t, err, "Snapshot on offline volume")
		mockStorage.AssertExpectations(t)
	})

	t.Run("NilSnapshotResponse", func(t *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient.On("Storage").Return(mockStorage)

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		mockStorage.On("SnapshotGet", mock.Anything).Return(nil, nil)

		resp, err := rc.GetSnapshot("snapshot-uuid", "volume-uuid")

		assert.Error(t, err)
		assert.Nil(t, resp)
		assertErrContains(t, err, "snapshot is nil")
		mockStorage.AssertExpectations(t)
	})

	t.Run("SnapshotMissingName", func(t *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient.On("Storage").Return(mockStorage)

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		mockSnapshot := &ontaprest.Snapshot{
			Snapshot: models.Snapshot{
				UUID:        nillable.ToPointer("uuid"),
				Size:        nillable.ToPointer(int64(1024)),
				LogicalSize: nillable.ToPointer(int64(2048)),
				// Name is nil
			},
		}

		mockStorage.On("SnapshotGet", mock.Anything).Return(mockSnapshot, nil)

		resp, err := rc.GetSnapshot("snapshot-uuid", "volume-uuid")

		assert.Error(t, err)
		assert.Nil(t, resp)
		assertErrContains(t, err, "missing required fields")
		mockStorage.AssertExpectations(t)
	})

	t.Run("SnapshotMissingUUID", func(t *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockStorage := new(ontaprest.MockStorageClient)
		mockClient.On("Storage").Return(mockStorage)

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		mockSnapshot := &ontaprest.Snapshot{
			Snapshot: models.Snapshot{
				Name:        nillable.ToPointer("test-snapshot"),
				Size:        nillable.ToPointer(int64(1024)),
				LogicalSize: nillable.ToPointer(int64(2048)),
				// UUID is nil
			},
		}

		mockStorage.On("SnapshotGet", mock.Anything).Return(mockSnapshot, nil)

		resp, err := rc.GetSnapshot("snapshot-uuid", "volume-uuid")

		assert.Error(t, err)
		assert.Nil(t, resp)
		assert.Contains(t, err.Error(), "ONTAP resource is in an inconsistent state")
		assert.Contains(t, err.(*vsaerrors.CustomError).OriginalErr.Error(), "missing required fields")
		mockStorage.AssertExpectations(t)
	})
}

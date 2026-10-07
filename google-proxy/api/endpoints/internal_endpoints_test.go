package api

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/cvpapi"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/cvpapi/backup_vault"
	cvpmodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/factory"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	gcpgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
	"gorm.io/gorm"
)

func TestInternalDescribePool(t *testing.T) {
	t.Run("WhenErrorGetPoolByName", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		mockOrchestrator.EXPECT().GetPoolByName(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("some error"))
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		params := gcpgenserver.V1betaInternalDescribePoolParams{
			PoolName:      "test-pool",
			ProjectNumber: "test-project",
			LocationId:    "test-location",
		}
		_, err := handler.V1betaInternalDescribePool(context.Background(), params)
		assert.Error(tt, err)
		assert.Equal(tt, "some error", err.Error())
	})
	t.Run("WhenPoolNotFound", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		mockOrchestrator.EXPECT().GetPoolByName(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.NewNotFoundErr("pool", nil))
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		params := gcpgenserver.V1betaInternalDescribePoolParams{
			PoolName:      "test-pool",
			ProjectNumber: "test-project",
			LocationId:    "test-location",
		}
		expectedResponse := &gcpgenserver.V1betaInternalDescribePoolNotFound{
			Code:    404,
			Message: "Pool not found",
		}
		resp, err := handler.V1betaInternalDescribePool(context.Background(), params)
		assert.NoError(tt, err)
		assert.Equal(tt, expectedResponse, resp)
	})
	t.Run("WhenSuccess", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		pool := &models.Pool{
			Name: "test-pool",
			BaseModel: models.BaseModel{
				UUID: "test-uuid",
			},
			VendorSubNetID: "test-vendor-subnet-id",
			ServiceLevel:   "test-service-level",
			QosType:        "test-qos-type",
			SizeInBytes:    1000,
			PoolAttributes: &models.PoolAttributes{
				SecondaryZone: "test-secondary-zone",
			},
			ClusterDetails: &models.ClusterDetails{
				ExternalName:     "test-external-name",
				InterClusterLifs: []string{"10.0.0.1", "10.0.0.2"},
			},
			CustomPerformanceParams: &models.CustomPerformanceParams{
				Enabled: true,
			},
		}

		mockOrchestrator.EXPECT().GetPoolByName(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(pool, nil)
		mockOrchestrator.EXPECT().HasActiveClusterUpgrade(mock.Anything, pool.UUID).Return(false, nil)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		params := gcpgenserver.V1betaInternalDescribePoolParams{
			PoolName:      "test-pool",
			ProjectNumber: "test-project",
			LocationId:    "test-location",
		}
		expectedResponse := &gcpgenserver.PoolInternalV1beta{
			Network:                  pool.VendorSubNetID,
			PoolId:                   gcpgenserver.NewOptString(pool.UUID),
			ResourceId:               pool.Name,
			ServiceLevel:             gcpgenserver.PoolInternalV1betaServiceLevel(pool.ServiceLevel),
			QosType:                  gcpgenserver.NewOptNilString(pool.QosType),
			SizeInBytes:              float64(pool.SizeInBytes),
			AllocatedBytes:           gcpgenserver.NewOptNilFloat64(pool.PoolAttributes.AllocatedBytes),
			TotalThroughputMibps:     gcpgenserver.NewOptNilFloat64(pool.TotalThroughputMibps),
			AvailableThroughputMibps: gcpgenserver.NewOptNilFloat64(pool.TotalThroughputMibps - pool.UtilizedThroughputMibps),
			NumberOfVolumes:          gcpgenserver.NewOptNilInt32(int32(pool.PoolAttributes.NumberOfVolumes)),
			StoragePoolState:         gcpgenserver.NewOptPoolInternalV1betaStoragePoolState(gcpgenserver.PoolInternalV1betaStoragePoolState(pool.State)),
			StoragePoolStateDetails:  gcpgenserver.NewOptString(pool.StateDetails),
			CreatedAt:                gcpgenserver.NewOptDateTime(pool.CreatedAt),
			UpdatedAt:                gcpgenserver.NewOptDateTime(pool.UpdatedAt),
			StateDetails:             gcpgenserver.NewOptString(pool.StateDetails),
			Description:              gcpgenserver.NewOptNilString(pool.Description),
			Zone:                     gcpgenserver.NewOptString(pool.Zone),
			AllowAutoTiering:         gcpgenserver.NewOptNilBool(pool.AllowAutoTiering),
			SecondaryZone:            gcpgenserver.NewOptString(pool.PoolAttributes.SecondaryZone),
			CustomPerformanceEnabled: gcpgenserver.NewOptBool(pool.CustomPerformanceParams.Enabled),
			InterclusterLifs:         pool.ClusterDetails.InterClusterLifs,
			ClusterName:              gcpgenserver.NewOptString(pool.ClusterDetails.ExternalName),
			TotalIops:                gcpgenserver.NewOptNilFloat64(float64(pool.CustomPerformanceParams.Iops)),
			SatisfiesPzs:             gcpgenserver.NewOptNilBool(false),
			SatisfiesPzi:             gcpgenserver.NewOptNilBool(false),
			LargeCapacity:            gcpgenserver.NewOptBool(pool.LargeCapacity),
			HasActiveClusterUpgrade:  gcpgenserver.NewOptBool(false),
		}
		resp, err := handler.V1betaInternalDescribePool(context.Background(), params)
		assert.NoError(tt, err)
		assert.Equal(tt, expectedResponse, resp)
	})
}

func TestInternalCreateVolumeReplication(t *testing.T) {
	t.Run("WhenEndpointNotDst", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		reqParams := &gcpgenserver.VolumeReplicationCreateInternalV1beta{
			EndpointType: "src",
		}
		params := gcpgenserver.V1betaInternalCreateVolumeReplicationParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
		}
		expectedResponse := &gcpgenserver.V1betaInternalCreateVolumeReplicationBadRequest{
			Code:    400,
			Message: "Incorrect endpoint type",
		}
		resp, err := handler.V1betaInternalCreateVolumeReplication(context.Background(), reqParams, params)
		assert.NoError(tt, err)
		assert.Equal(tt, expectedResponse, resp)
	})
	t.Run("WhenCreateVolumeReplicationError", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		reqParams := &gcpgenserver.VolumeReplicationCreateInternalV1beta{
			EndpointType: "dst",
		}
		params := gcpgenserver.V1betaInternalCreateVolumeReplicationParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
		}
		mockOrchestrator.EXPECT().CreateVolumeReplicationInternal(mock.Anything, mock.Anything).Return(nil, nil, errors.New("some error"))
		_, err := handler.V1betaInternalCreateVolumeReplication(context.Background(), reqParams, params)
		assert.Error(tt, err)
		assert.Equal(tt, "some error", err.Error())
	})
	t.Run("WhenSuccess", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		reqParams := &gcpgenserver.VolumeReplicationCreateInternalV1beta{
			EndpointType: "dst",
		}
		params := gcpgenserver.V1betaInternalCreateVolumeReplicationParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
		}
		volumeReplication := &models.VolumeReplication{
			BaseModel: models.BaseModel{
				UUID: "uuid-1",
			},
			Name:        "test-replication",
			Description: "Test replication",
			Uri:         "test-uri",
			RemoteUri:   "test-remote-uri",
			ReplicationAttributes: &models.ReplicationDetails{
				EndpointType:               "dst",
				ReplicationType:            "test-replication-type",
				ReplicationSchedule:        "test-schedule",
				SourceVolumeUUID:           "test-source-volume-uuid",
				SourceRegion:               "test-source-region",
				SourceHostName:             "test-source-host",
				SourceReplicationUUID:      "test-source-replication-uuid",
				SourceSvmName:              "test-source-svm",
				SourceVolumeName:           "test-source-volume",
				DestinationVolumeUUID:      "test-destination-volume-uuid",
				DestinationRegion:          "test-destination-region",
				DestinationHostName:        "test-destination-host",
				DestinationReplicationUUID: "test-destination-replication-uuid",
				DestinationSvmName:         "test-destination-svm",
				DestinationVolumeName:      "test-destination-volume",
			},
		}
		job := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				UUID:      "test-job-uuid",
				CreatedAt: time.Now(),
			},
			WorkflowID: "test-workflow-id",
			Type:       "job-type-create-volume-replication",
			State:      "job-state-processing",
		}
		expectedResponse := &gcpgenserver.VolumeReplicationInternalV1beta{
			VolumeReplicationUuid: gcpgenserver.NewOptString(volumeReplication.UUID),
			EndpointType:          gcpgenserver.VolumeReplicationInternalV1betaEndpointType(volumeReplication.ReplicationAttributes.EndpointType),
			RemoteRegion:          volumeReplication.ReplicationAttributes.SourceRegion,
			SourceHostName:        volumeReplication.ReplicationAttributes.SourceHostName,
			SourceServerName:      volumeReplication.ReplicationAttributes.SourceSvmName,
			SourceVolumeName:      volumeReplication.ReplicationAttributes.SourceVolumeName,
			SourceVolumeUuid:      gcpgenserver.NewOptString(volumeReplication.ReplicationAttributes.SourceVolumeUUID),
			SourcePoolUuid:        gcpgenserver.NewOptString(volumeReplication.ReplicationAttributes.SourcePoolUUID),
			DestinationHostName:   volumeReplication.ReplicationAttributes.DestinationHostName,
			DestinationServerName: volumeReplication.ReplicationAttributes.DestinationSvmName,
			DestinationVolumeName: volumeReplication.ReplicationAttributes.DestinationVolumeName,
			DestinationVolumeUuid: gcpgenserver.NewOptString(volumeReplication.ReplicationAttributes.DestinationVolumeUUID),
			DestinationPoolUuid:   gcpgenserver.NewOptString(volumeReplication.ReplicationAttributes.DestinationPoolUUID),
			ReplicationType: gcpgenserver.OptVolumeReplicationInternalV1betaReplicationType{
				Value: gcpgenserver.VolumeReplicationInternalV1betaReplicationType(volumeReplication.ReplicationAttributes.ReplicationType),
				Set:   true,
			},
			Jobs: []gcpgenserver.JobV1beta{
				{
					JobId:    gcpgenserver.NewOptString(job.UUID),
					Created:  gcpgenserver.NewOptDateTime(job.CreatedAt),
					WorkerId: gcpgenserver.NewOptString(job.WorkflowID),
				},
			},
		}
		mockOrchestrator.EXPECT().CreateVolumeReplicationInternal(mock.Anything, mock.Anything).Return(volumeReplication, job, nil)
		actualResp, err := handler.V1betaInternalCreateVolumeReplication(context.Background(), reqParams, params)
		assert.NoError(tt, err)
		assert.Equal(tt, expectedResponse, actualResp)
	})
}

func TestV1betaInternalGetReplicationJobs(t *testing.T) {
	t.Run("ReturnsInternalServerErrorWhenGetReplicationJobsFails", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		mockOrchestrator.EXPECT().GetReplicationJobs(mock.Anything, "test-project", "test-pool").Return(nil, errors.New("some error"))
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		params := gcpgenserver.V1betaInternalGetReplicationJobsParams{
			ProjectNumber: "test-project",
			PoolUUID:      gcpgenserver.NewOptString("test-pool"),
			LocationId:    "test-location",
		}
		resp, err := handler.V1betaInternalGetReplicationJobs(context.Background(), params)
		assert.Error(tt, err)
		assert.Equal(tt, "some error", err.Error())
		assert.IsType(tt, &gcpgenserver.V1betaInternalGetReplicationJobsInternalServerError{}, resp)
	})
	t.Run("ReturnsEmptyListWhenNoReplicationJobsExist", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		mockOrchestrator.EXPECT().GetReplicationJobs(mock.Anything, "test-project", "test-pool").Return([]*models.Job{}, nil)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		params := gcpgenserver.V1betaInternalGetReplicationJobsParams{
			ProjectNumber: "test-project",
			PoolUUID:      gcpgenserver.NewOptString("test-pool"),
			LocationId:    "test-location",
		}
		resp, err := handler.V1betaInternalGetReplicationJobs(context.Background(), params)
		assert.NoError(tt, err)
		assert.IsType(tt, &gcpgenserver.V1betaInternalGetReplicationJobsOK{}, resp)
		assert.Empty(tt, resp.(*gcpgenserver.V1betaInternalGetReplicationJobsOK).Jobs)
	})
	t.Run("ReturnsReplicationJobsWhenTheyExist", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		jobs := []*models.Job{
			{
				BaseModel: models.BaseModel{
					UUID:      "job-uuid-1",
					CreatedAt: time.Now(),
					UpdatedAt: time.Now(),
				},
				CorrelationID: "correlation-id-1",
				State:         "COMPLETED",
				Type:          "REPLICATION",
				ScheduledAt:   time.Now(),
			},
			{
				BaseModel: models.BaseModel{
					UUID:      "job-uuid-2",
					CreatedAt: time.Now(),
					UpdatedAt: time.Now(),
				},
				CorrelationID: "correlation-id-2",
				State:         "FAILED",
				Type:          "REPLICATION",

				ScheduledAt: time.Now(),
			},
		}

		mockOrchestrator.EXPECT().GetReplicationJobs(mock.Anything, "test-project", "test-pool").Return(jobs, nil)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		params := gcpgenserver.V1betaInternalGetReplicationJobsParams{
			ProjectNumber: "test-project",
			PoolUUID:      gcpgenserver.NewOptString("test-pool"),
			LocationId:    "test-location",
		}
		resp, err := handler.V1betaInternalGetReplicationJobs(context.Background(), params)
		assert.NoError(tt, err)
		assert.IsType(tt, &gcpgenserver.V1betaInternalGetReplicationJobsOK{}, resp)
		assert.Len(tt, resp.(*gcpgenserver.V1betaInternalGetReplicationJobsOK).Jobs, 2)
		assert.Equal(tt, "job-uuid-1", resp.(*gcpgenserver.V1betaInternalGetReplicationJobsOK).Jobs[0].JobUuid.Value)
		assert.Equal(tt, "job-uuid-2", resp.(*gcpgenserver.V1betaInternalGetReplicationJobsOK).Jobs[1].JobUuid.Value)
	})
}

func TestV1betaGetMultipleReplicationsInternal(t *testing.T) {
	t.Run("WhenGetReplicationsError", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		params := gcpgenserver.V1betaGetMultipleReplicationsInternalParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
		}
		req := &gcpgenserver.ReplicationIDListV1beta{
			ReplicationUUIDs: []string{"replication-1", "replication-2"},
		}
		mockOrchestrator.EXPECT().GetMultipleReplicationsInternal(context.Background(), mock.Anything, mock.Anything).Return(nil, errors.New("some error"))
		resp, err := handler.V1betaGetMultipleReplicationsInternal(context.Background(), req, params)
		if intErr, ok := resp.(*gcpgenserver.V1betaGetMultipleReplicationsInternalInternalServerError); ok {
			assert.Equal(tt, 500, int(intErr.Code))
		}

		assert.Error(tt, err)
		assert.Equal(tt, "some error", err.Error())
	})
	t.Run("WhenGetReplicationReturnsNotFound", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		params := gcpgenserver.V1betaGetMultipleReplicationsInternalParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
		}
		req := &gcpgenserver.ReplicationIDListV1beta{
			ReplicationUUIDs: []string{"replication-1", "replication-2"},
		}
		mockOrchestrator.EXPECT().GetMultipleReplicationsInternal(context.Background(), mock.Anything, mock.Anything).Return(nil, errors.NewNotFoundErr("replication", nil))
		resp, err := handler.V1betaGetMultipleReplicationsInternal(context.Background(), req, params)
		if notFoundResp, ok := resp.(*gcpgenserver.V1betaGetMultipleReplicationsInternalNotFound); ok {
			assert.Equal(tt, 404, int(notFoundResp.Code))
		}
		assert.NoError(tt, err)
		assert.NotNil(tt, resp)
	})
	t.Run("WhenGetMultipleReplicationsSuccess", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		defer func() {
			convertToVolumeReplicationsInternalV1Beta = _convertToVolumeReplicationsInternalV1Beta
		}()

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		params := gcpgenserver.V1betaGetMultipleReplicationsInternalParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
		}
		req := &gcpgenserver.ReplicationIDListV1beta{
			ReplicationUUIDs: []string{"replication-1", "replication-2"},
		}
		replications := []*datamodel.VolumeReplication{
			{
				Name: "replication-1",
			},
			{
				Name: "replication-2",
			},
		}
		expectedResponse := &gcpgenserver.V1betaGetMultipleReplicationsInternalOK{
			Replications: []gcpgenserver.VolumeReplicationInternalV1beta{
				gcpgenserver.VolumeReplicationInternalV1beta{
					Name:         gcpgenserver.NewOptString("replication-1"),
					EndpointType: gcpgenserver.VolumeReplicationInternalV1betaEndpointTypeDst,
				},
				gcpgenserver.VolumeReplicationInternalV1beta{
					Name:         gcpgenserver.NewOptString("replication-2"),
					EndpointType: gcpgenserver.VolumeReplicationInternalV1betaEndpointTypeDst,
				},
			},
		}

		convertToVolumeReplicationsInternalV1Beta = func(reps []*datamodel.VolumeReplication) []gcpgenserver.VolumeReplicationInternalV1beta {
			var internalReps []gcpgenserver.VolumeReplicationInternalV1beta
			for _, rep := range reps {
				internalReps = append(internalReps, gcpgenserver.VolumeReplicationInternalV1beta{
					Name:         gcpgenserver.NewOptString(rep.Name),
					EndpointType: gcpgenserver.VolumeReplicationInternalV1betaEndpointTypeDst,
				})
			}
			return internalReps
		}

		mockOrchestrator.EXPECT().GetMultipleReplicationsInternal(mock.Anything, mock.Anything, mock.Anything).Return(replications, nil)

		resp, err := handler.V1betaGetMultipleReplicationsInternal(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.Equal(tt, expectedResponse, resp)
	})
}

func TestBetaInternalmountVolumeReplication(t *testing.T) {
	t.Run("ReturnsInternalServerErrorWhenPerformMountCheckFails", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.On("PerformMountCheck", mock.Anything, "volume-replication-id", "project-number").
			Return(nil, errors.New("mount check failed"))

		handler := Handler{Orchestrator: mockOrchestrator}
		params := gcpgenserver.V1betaInternalMountVolumeReplicationParams{
			VolumeReplicationId: "volume-replication-id",
			ProjectNumber:       "project-number",
		}
		result, _ := handler.V1betaInternalMountVolumeReplication(context.Background(), params)
		assert.IsType(tt, &gcpgenserver.V1betaInternalMountVolumeReplicationInternalServerError{Code: 500, Message: "mount check failed"}, result)
		mockOrchestrator.AssertExpectations(tt)
	})
	t.Run("ReturnsVolumeReplicationInternalWhenMountCheckSucceeds", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockJob := &models.Job{
			BaseModel: models.BaseModel{
				UUID:      "job-uuid",
				CreatedAt: time.Now(),
			},
			WorkflowID: "worker-id",
			State:      "completed",
		}
		mockOrchestrator.On("PerformMountCheck", mock.Anything, "volume-replication-id", "project-number").
			Return(mockJob, nil)

		handler := Handler{Orchestrator: mockOrchestrator}
		params := gcpgenserver.V1betaInternalMountVolumeReplicationParams{
			VolumeReplicationId: "volume-replication-id",
			ProjectNumber:       "project-number",
		}
		result, err := handler.V1betaInternalMountVolumeReplication(context.Background(), params)

		assert.NoError(tt, err)
		assert.IsType(tt, &gcpgenserver.InternalJobV1beta{}, result)
		volumeReplication := result.(*gcpgenserver.InternalJobV1beta)
		assert.Equal(tt, "job-uuid", volumeReplication.JobUuid.Value)
		mockOrchestrator.AssertExpectations(tt)
	})
}

func TestV1betaInternalResumeVolumeReplication(t *testing.T) {
	t.Run("WhenResumeVolumeReplicationInternalServerError", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		params := gcpgenserver.V1betaInternalResumeVolumeReplicationParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
			ForceResume:   gcpgenserver.OptBool{Set: true, Value: false},
		}
		expectedResponse := &gcpgenserver.V1betaInternalResumeVolumeReplicationInternalServerError{
			Code:    500,
			Message: "Internal server error while resuming replication",
		}
		mockOrchestrator.EXPECT().ResumeReplicationInternal(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil, errors.New("some error"))
		resp, err := handler.V1betaInternalResumeVolumeReplication(context.Background(), params)
		assert.NoError(tt, err)
		assert.Equal(tt, expectedResponse, resp)
	})
	t.Run("WhenResumeVolumeReplicationBadRequest", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		params := gcpgenserver.V1betaInternalResumeVolumeReplicationParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
			ForceResume:   gcpgenserver.OptBool{Set: true, Value: false},
		}
		expectedResponse := &gcpgenserver.V1betaInternalResumeVolumeReplicationBadRequest{
			Code:    400,
			Message: "Invalid request parameters",
		}
		mockOrchestrator.EXPECT().ResumeReplicationInternal(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil, errors.NewUserInputValidationErr("Invalid request parameters"))
		resp, err := handler.V1betaInternalResumeVolumeReplication(context.Background(), params)
		assert.NoError(tt, err)
		assert.Equal(tt, expectedResponse, resp)
	})
	t.Run("WhenResumeVolumeReplicationNotFound", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		params := gcpgenserver.V1betaInternalResumeVolumeReplicationParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
			ForceResume:   gcpgenserver.OptBool{Set: true, Value: false},
		}
		expectedResponse := &gcpgenserver.V1betaInternalResumeVolumeReplicationNotFound{
			Code:    404,
			Message: "Volume replication not found",
		}
		mockOrchestrator.EXPECT().ResumeReplicationInternal(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil, errors.NewNotFoundErr("Volume replication not found", nil))
		resp, err := handler.V1betaInternalResumeVolumeReplication(context.Background(), params)
		assert.NoError(tt, err)
		assert.Equal(tt, expectedResponse, resp)
	})
	t.Run("WhenSuccess", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		params := gcpgenserver.V1betaInternalResumeVolumeReplicationParams{
			ProjectNumber:       "test-project",
			LocationId:          "test-location",
			ForceResume:         gcpgenserver.OptBool{Set: true, Value: false},
			VolumeReplicationId: "test-replication-id",
		}
		replication := &models.VolumeReplication{
			Name: "test-replication",
			BaseModel: models.BaseModel{
				UUID: "replication-uuid",
			},
			ReplicationAttributes: &models.ReplicationDetails{
				DestinationReplicationUUID: "destination-replication-uuid",
			},
		}
		job := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				UUID:      "job-uuid",
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			},
			WorkflowID: "job-workflow-id",
		}
		expectedResponse := convertToInternalV1betaVolumeReplication(replication, job)
		mockOrchestrator.EXPECT().ResumeReplicationInternal(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(replication, job, nil)
		resp, err := handler.V1betaInternalResumeVolumeReplication(context.Background(), params)
		assert.NoError(tt, err)
		assert.Equal(tt, expectedResponse, resp)
	})
}

func TestV1betaInternalDeleteVolumeReplicationRow(t *testing.T) {
	t.Run("ReturnsInternalServerErrorWhenDeleteFails", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		ctx := context.Background()
		params := gcpgenserver.V1betaInternalReleaseVolumeReplicationParams{
			ProjectNumber:       "test-project",
			LocationId:          "test-location",
			VolumeReplicationId: "test-replication-id",
		}
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}
		defer func() {
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()
		mockOrchestrator.EXPECT().ReleaseVolumeReplication(ctx, mock.Anything, mock.Anything).Return(nil, nil, errors.New("delete error"))
		resp, err := handler.V1betaInternalReleaseVolumeReplication(ctx, params)
		assert.NoError(tt, err)
		assert.IsType(tt, &gcpgenserver.V1betaInternalReleaseVolumeReplicationInternalServerError{}, resp)
		assert.Equal(tt, float64(500), resp.(*gcpgenserver.V1betaInternalReleaseVolumeReplicationInternalServerError).Code)
	})
	t.Run("ReturnsNotFoundErrorWhenDeleteFails", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		ctx := context.Background()
		params := gcpgenserver.V1betaInternalReleaseVolumeReplicationParams{
			ProjectNumber:       "test-project",
			LocationId:          "test-location",
			VolumeReplicationId: "test-replication-id",
		}
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}
		defer func() {
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()
		mockOrchestrator.EXPECT().ReleaseVolumeReplication(ctx, mock.Anything, mock.Anything).Return(nil, nil, errors.NewNotFoundErr("Volume replication not found", nil))
		resp, err := handler.V1betaInternalReleaseVolumeReplication(ctx, params)
		assert.NoError(tt, err)
		assert.IsType(tt, &gcpgenserver.V1betaInternalReleaseVolumeReplicationBadRequest{}, resp)
		assert.Equal(tt, float64(404), resp.(*gcpgenserver.V1betaInternalReleaseVolumeReplicationBadRequest).Code)
	})
	t.Run("ReturnsOKWhenSuccess", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		ctx := context.Background()
		params := gcpgenserver.V1betaInternalReleaseVolumeReplicationParams{
			ProjectNumber:       "test-project",
			LocationId:          "test-location",
			VolumeReplicationId: "test-replication-id",
		}
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}
		defer func() {
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()
		volumeReplication := &models.VolumeReplication{
			BaseModel: models.BaseModel{
				UUID: "uuid-1",
			},
			Name:        "test-replication",
			Description: "Test replication",
			Uri:         "test-uri",
			RemoteUri:   "test-remote-uri",
			ReplicationAttributes: &models.ReplicationDetails{
				EndpointType:               "dst",
				ReplicationType:            "test-replication-type",
				ReplicationSchedule:        "test-schedule",
				SourceVolumeUUID:           "test-source-volume-uuid",
				SourceRegion:               "test-source-region",
				SourceHostName:             "test-source-host",
				SourceReplicationUUID:      "test-source-replication-uuid",
				SourceSvmName:              "test-source-svm",
				SourceVolumeName:           "test-source-volume",
				DestinationVolumeUUID:      "test-destination-volume-uuid",
				DestinationRegion:          "test-destination-region",
				DestinationHostName:        "test-destination-host",
				DestinationReplicationUUID: "test-destination-replication-uuid",
				DestinationSvmName:         "test-destination-svm",
				DestinationVolumeName:      "test-destination-volume",
			},
		}
		job := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				UUID:      "test-job-uuid",
				CreatedAt: time.Now(),
			},
			WorkflowID: "test-workflow-id",
			Type:       "job-type-create-volume-replication",
			State:      "job-state-processing",
		}
		expectedOperationName := fmt.Sprintf("/v1beta/projects/%s/locations/%s/operations/%s", params.ProjectNumber, params.LocationId, job.UUID)
		expectedResponse := &gcpgenserver.OperationV1beta{
			Name: gcpgenserver.NewOptString(expectedOperationName),
			Done: gcpgenserver.NewOptBool(true),
		}
		mockOrchestrator.EXPECT().ReleaseVolumeReplication(ctx, mock.Anything, mock.Anything).Return(volumeReplication, job, nil)

		resp, err := handler.V1betaInternalReleaseVolumeReplication(ctx, params)
		operationResp := resp.(*gcpgenserver.OperationV1beta)

		assert.NoError(tt, err)
		assert.Equal(tt, expectedResponse.Name.Value, operationResp.Name.Value)
		assert.Equal(tt, expectedResponse.Done.Value, operationResp.Done.Value)
	})
	t.Run("PassesIsCleanupTrueToOrchestrator", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		ctx := context.Background()
		params := gcpgenserver.V1betaInternalReleaseVolumeReplicationParams{
			ProjectNumber:       "test-project",
			LocationId:          "test-location",
			VolumeReplicationId: "test-replication-id",
			IsCleanup:           gcpgenserver.NewOptBool(true),
		}
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}
		defer func() {
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()
		job := &datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "job-uuid"},
		}
		mockOrchestrator.EXPECT().ReleaseVolumeReplication(ctx, params.VolumeReplicationId, true).Return(&models.VolumeReplication{}, job, nil)

		resp, err := handler.V1betaInternalReleaseVolumeReplication(ctx, params)
		assert.NoError(tt, err)
		assert.IsType(tt, &gcpgenserver.OperationV1beta{}, resp)
	})
}

func TestV1betaInternalDeleteVolumeReplication(t *testing.T) {
	t.Run("ReturnsInternalServerErrorWhenDeleteFails", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		ctx := context.Background()
		params := gcpgenserver.V1betaInternalDeleteVolumeReplicationParams{
			ProjectNumber:       "test-project",
			LocationId:          "test-location",
			VolumeReplicationId: "test-replication-id",
		}
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}
		defer func() {
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()
		mockOrchestrator.EXPECT().DeleteReplicationInternal(ctx, params.VolumeReplicationId, false, false).Return(nil, nil, errors.New("delete error"))
		resp, err := handler.V1betaInternalDeleteVolumeReplication(ctx, params)
		assert.NoError(tt, err)
		assert.IsType(tt, &gcpgenserver.V1betaInternalDeleteVolumeReplicationInternalServerError{}, resp)
		assert.Equal(tt, float64(500), resp.(*gcpgenserver.V1betaInternalDeleteVolumeReplicationInternalServerError).Code)
	})
	t.Run("WhenVolumeReplicationNotFound", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		ctx := context.Background()
		params := gcpgenserver.V1betaInternalDeleteVolumeReplicationParams{
			ProjectNumber:       "test-project",
			LocationId:          "test-location",
			VolumeReplicationId: "test-replication-id",
			CleanupAfterReverse: gcpgenserver.NewOptBool(true),
		}
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}
		defer func() {
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()
		mockOrchestrator.EXPECT().DeleteReplicationInternal(ctx, params.VolumeReplicationId, true, false).Return(nil, nil, errors.NewNotFoundErr("Volume replication not found", nil))
		resp, err := handler.V1betaInternalDeleteVolumeReplication(ctx, params)
		assert.NoError(tt, err)
		assert.IsType(tt, &gcpgenserver.V1betaInternalDeleteVolumeReplicationBadRequest{}, resp)
		assert.Equal(tt, float64(404), resp.(*gcpgenserver.V1betaInternalDeleteVolumeReplicationBadRequest).Code)
	})
	t.Run("ReturnsOKWhenSuccess", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		ctx := context.Background()
		params := gcpgenserver.V1betaInternalDeleteVolumeReplicationParams{
			ProjectNumber:       "test-project",
			LocationId:          "test-location",
			VolumeReplicationId: "test-replication-id",
			CleanupAfterReverse: gcpgenserver.NewOptBool(false),
		}
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}
		defer func() {
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()
		volumeReplication := &models.VolumeReplication{
			BaseModel: models.BaseModel{
				UUID: "uuid-1",
			},
			Name:        "test-replication",
			Description: "Test replication",
			Uri:         "test-uri",
			RemoteUri:   "test-remote-uri",
			ReplicationAttributes: &models.ReplicationDetails{
				EndpointType:               "dst",
				ReplicationType:            "test-replication-type",
				ReplicationSchedule:        "test-schedule",
				SourceVolumeUUID:           "test-source-volume-uuid",
				SourceRegion:               "test-source-region",
				SourceHostName:             "test-source-host",
				SourceReplicationUUID:      "test-source-replication-uuid",
				SourceSvmName:              "test-source-svm",
				SourceVolumeName:           "test-source-volume",
				DestinationVolumeUUID:      "test-destination-volume-uuid",
				DestinationRegion:          "test-destination-region",
				DestinationHostName:        "test-destination-host",
				DestinationReplicationUUID: "test-destination-replication-uuid",
				DestinationSvmName:         "test-destination-svm",
				DestinationVolumeName:      "test-destination-volume",
			},
		}
		job := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				UUID:      "test-job-uuid",
				CreatedAt: time.Now(),
			},
			WorkflowID: "test-workflow-id",
			Type:       "job-type-create-volume-replication",
			State:      "job-state-processing",
		}
		expectedResponse := &gcpgenserver.VolumeReplicationInternalV1beta{
			VolumeReplicationUuid: gcpgenserver.NewOptString(volumeReplication.UUID),
			EndpointType:          gcpgenserver.VolumeReplicationInternalV1betaEndpointType(volumeReplication.ReplicationAttributes.EndpointType),
			RemoteRegion:          volumeReplication.ReplicationAttributes.SourceRegion,
			SourceHostName:        volumeReplication.ReplicationAttributes.SourceHostName,
			SourceServerName:      volumeReplication.ReplicationAttributes.SourceSvmName,
			SourceVolumeName:      volumeReplication.ReplicationAttributes.SourceVolumeName,
			SourceVolumeUuid:      gcpgenserver.NewOptString(volumeReplication.ReplicationAttributes.SourceVolumeUUID),
			SourcePoolUuid:        gcpgenserver.NewOptString(volumeReplication.ReplicationAttributes.SourcePoolUUID),
			DestinationHostName:   volumeReplication.ReplicationAttributes.DestinationHostName,
			DestinationServerName: volumeReplication.ReplicationAttributes.DestinationSvmName,
			DestinationVolumeName: volumeReplication.ReplicationAttributes.DestinationVolumeName,
			DestinationVolumeUuid: gcpgenserver.NewOptString(volumeReplication.ReplicationAttributes.DestinationVolumeUUID),
			DestinationPoolUuid:   gcpgenserver.NewOptString(volumeReplication.ReplicationAttributes.DestinationPoolUUID),
			ReplicationType: gcpgenserver.OptVolumeReplicationInternalV1betaReplicationType{
				Value: gcpgenserver.VolumeReplicationInternalV1betaReplicationType(volumeReplication.ReplicationAttributes.ReplicationType),
				Set:   true,
			},
			Jobs: []gcpgenserver.JobV1beta{
				{
					JobId:    gcpgenserver.NewOptString(job.UUID),
					Created:  gcpgenserver.NewOptDateTime(job.CreatedAt),
					WorkerId: gcpgenserver.NewOptString(job.WorkflowID),
				},
			},
		}
		mockOrchestrator.EXPECT().DeleteReplicationInternal(ctx, mock.Anything, false, false).Return(volumeReplication, job, nil)
		resp, err := handler.V1betaInternalDeleteVolumeReplication(ctx, params)
		assert.NoError(tt, err)
		assert.Equal(tt, expectedResponse, resp)
	})
	t.Run("ReturnsOKWhenSuccessWithIsCleanupTrue", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		ctx := context.Background()
		params := gcpgenserver.V1betaInternalDeleteVolumeReplicationParams{
			ProjectNumber:       "test-project",
			LocationId:          "test-location",
			VolumeReplicationId: "test-replication-id",
			CleanupAfterReverse: gcpgenserver.NewOptBool(false),
			IsCleanup:           gcpgenserver.NewOptBool(true),
		}
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}
		defer func() {
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()
		volumeReplication := &models.VolumeReplication{
			BaseModel: models.BaseModel{
				UUID: "uuid-1",
			},
			Name:        "test-replication",
			Description: "Test replication",
			Uri:         "test-uri",
			RemoteUri:   "test-remote-uri",
			ReplicationAttributes: &models.ReplicationDetails{
				EndpointType:               "dst",
				ReplicationType:            "test-replication-type",
				ReplicationSchedule:        "test-schedule",
				SourceVolumeUUID:           "test-source-volume-uuid",
				SourceRegion:               "test-source-region",
				SourceHostName:             "test-source-host",
				SourceReplicationUUID:      "test-source-replication-uuid",
				SourceSvmName:              "test-source-svm",
				SourceVolumeName:           "test-source-volume",
				DestinationVolumeUUID:      "test-destination-volume-uuid",
				DestinationRegion:          "test-destination-region",
				DestinationHostName:        "test-destination-host",
				DestinationReplicationUUID: "test-destination-replication-uuid",
				DestinationSvmName:         "test-destination-svm",
				DestinationVolumeName:      "test-destination-volume",
			},
		}
		job := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				UUID:      "test-job-uuid",
				CreatedAt: time.Now(),
			},
			WorkflowID: "test-workflow-id",
			Type:       "job-type-create-volume-replication",
			State:      "job-state-processing",
		}
		expectedResponse := &gcpgenserver.VolumeReplicationInternalV1beta{
			VolumeReplicationUuid: gcpgenserver.NewOptString(volumeReplication.UUID),
			EndpointType:          gcpgenserver.VolumeReplicationInternalV1betaEndpointType(volumeReplication.ReplicationAttributes.EndpointType),
			RemoteRegion:          volumeReplication.ReplicationAttributes.SourceRegion,
			SourceHostName:        volumeReplication.ReplicationAttributes.SourceHostName,
			SourceServerName:      volumeReplication.ReplicationAttributes.SourceSvmName,
			SourceVolumeName:      volumeReplication.ReplicationAttributes.SourceVolumeName,
			SourceVolumeUuid:      gcpgenserver.NewOptString(volumeReplication.ReplicationAttributes.SourceVolumeUUID),
			SourcePoolUuid:        gcpgenserver.NewOptString(volumeReplication.ReplicationAttributes.SourcePoolUUID),
			DestinationHostName:   volumeReplication.ReplicationAttributes.DestinationHostName,
			DestinationServerName: volumeReplication.ReplicationAttributes.DestinationSvmName,
			DestinationVolumeName: volumeReplication.ReplicationAttributes.DestinationVolumeName,
			DestinationVolumeUuid: gcpgenserver.NewOptString(volumeReplication.ReplicationAttributes.DestinationVolumeUUID),
			DestinationPoolUuid:   gcpgenserver.NewOptString(volumeReplication.ReplicationAttributes.DestinationPoolUUID),
			ReplicationType: gcpgenserver.OptVolumeReplicationInternalV1betaReplicationType{
				Value: gcpgenserver.VolumeReplicationInternalV1betaReplicationType(volumeReplication.ReplicationAttributes.ReplicationType),
				Set:   true,
			},
			Jobs: []gcpgenserver.JobV1beta{
				{
					JobId:    gcpgenserver.NewOptString(job.UUID),
					Created:  gcpgenserver.NewOptDateTime(job.CreatedAt),
					WorkerId: gcpgenserver.NewOptString(job.WorkflowID),
				},
			},
		}
		mockOrchestrator.EXPECT().DeleteReplicationInternal(ctx, mock.Anything, false, true).Return(volumeReplication, job, nil)
		resp, err := handler.V1betaInternalDeleteVolumeReplication(ctx, params)
		assert.NoError(tt, err)
		assert.Equal(tt, expectedResponse, resp)
	})
}

func TestV1betaInternalDeleteVolumeSnapshot(t *testing.T) {
	params := gcpgenserver.V1betaInternalDeleteVolumeSnapmirrorSnapshotParams{
		ProjectNumber: "test-project",
		LocationId:    "test-location",
		VolumeId:      "test-volume",
	}
	t.Run("ReturnsBadRequestOnInvalidLocation", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "", "", &gcpgenserver.Error{
				Code:    400,
				Message: "Invalid location ID",
			}
		}
		defer func() {
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()
		resp, err := handler.V1betaInternalDeleteVolumeSnapmirrorSnapshot(context.Background(), params)
		assert.NoError(tt, err)
		assert.IsType(tt, &gcpgenserver.V1betaInternalDeleteVolumeSnapmirrorSnapshotBadRequest{}, resp)

		assert.NotNil(tt, resp)
		assert.Equal(tt, float64(400), resp.(*gcpgenserver.V1betaInternalDeleteVolumeSnapmirrorSnapshotBadRequest).Code)
		assert.Equal(tt, "Invalid location ID", resp.(*gcpgenserver.V1betaInternalDeleteVolumeSnapmirrorSnapshotBadRequest).Message)
	})
	t.Run("ReturnsNotFoundWhenSnapshotNotFound", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}
		defer func() {
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()
		mockOrchestrator.EXPECT().DeleteSnapmirrorSnapshots(mock.Anything, mock.Anything).Return("", errors.NewNotFoundErr("snapshot", nil))
		resp, _ := handler.V1betaInternalDeleteVolumeSnapmirrorSnapshot(context.Background(), params)
		assert.NotNil(tt, resp)
		assert.Equal(tt, float64(404), resp.(*gcpgenserver.V1betaInternalDeleteVolumeSnapmirrorSnapshotBadRequest).Code)
	})
	t.Run("ReturnsBadRequestOnUserInputValidationErr", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}
		defer func() {
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()
		mockOrchestrator.EXPECT().DeleteSnapmirrorSnapshots(mock.Anything, mock.Anything).Return("", errors.NewUserInputValidationErr("bad input"))
		resp, _ := handler.V1betaInternalDeleteVolumeSnapmirrorSnapshot(context.Background(), params)
		assert.NotNil(tt, resp)
		assert.Equal(tt, float64(400), resp.(*gcpgenserver.V1betaInternalDeleteVolumeSnapmirrorSnapshotBadRequest).Code)
	})
	t.Run("ReturnsConflictOnConflictErr", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}
		defer func() {
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()
		mockOrchestrator.EXPECT().DeleteSnapmirrorSnapshots(mock.Anything, mock.Anything).Return("", errors.NewConflictErr("conflict"))
		resp, _ := handler.V1betaInternalDeleteVolumeSnapmirrorSnapshot(context.Background(), params)
		assert.NotNil(tt, resp)
		assert.Equal(tt, float64(409), resp.(*gcpgenserver.V1betaInternalDeleteVolumeSnapmirrorSnapshotConflict).Code)
	})
	t.Run("ReturnsInternalServerErrorOnOtherError", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}
		defer func() {
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()
		mockOrchestrator.EXPECT().DeleteSnapmirrorSnapshots(mock.Anything, mock.Anything).Return("", errors.New("internal error"))
		resp, _ := handler.V1betaInternalDeleteVolumeSnapmirrorSnapshot(context.Background(), params)
		assert.NotNil(tt, resp)
		assert.Equal(tt, float64(500), resp.(*gcpgenserver.V1betaInternalDeleteVolumeSnapmirrorSnapshotInternalServerError).Code)
	})
	t.Run("ReturnsInternalServerErrorOnOtherError", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}
		operationID := "/v1beta/projects/" + params.ProjectNumber + "/locations/" + params.LocationId + "/operations/" + "op-id"
		defer func() {
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()
		mockOrchestrator.EXPECT().DeleteSnapmirrorSnapshots(mock.Anything, mock.Anything).Return("op-id", nil)
		resp, err := handler.V1betaInternalDeleteVolumeSnapmirrorSnapshot(context.Background(), params)
		assert.NoError(tt, err)
		assert.NotNil(tt, resp)
		assert.Equal(tt, operationID, resp.(*gcpgenserver.OperationV1beta).Name.Value)
	})
}

func TestV1betaInternalStopVolumeReplication(t *testing.T) {
	t.Run("WhenVolumeReplicationNotFound", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaInternalStopVolumeReplicationParams{
			VolumeReplicationId: "test-replication-id",
			ProjectNumber:       "test-project",
			LocationId:          "test-location",
		}
		req := &gcpgenserver.V1betaInternalStopVolumeReplicationReq{
			Force: gcpgenserver.OptBool{Set: true, Value: false},
		}

		mockOrchestrator.EXPECT().StopReplicationInternal(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil, errors.NewNotFoundErr("Volume replication not found", nil))

		resp, err := handler.V1betaInternalStopVolumeReplication(context.Background(), req, params)
		assert.NoError(tt, err)
		assert.Equal(tt, float64(404), resp.(*gcpgenserver.V1betaInternalStopVolumeReplicationNotFound).Code)
		assert.Equal(tt, "Volume replication not found", resp.(*gcpgenserver.V1betaInternalStopVolumeReplicationNotFound).Message)
	})

	t.Run("WhenInvalidRequestParameters", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaInternalStopVolumeReplicationParams{
			VolumeReplicationId: "test-replication-id",
			ProjectNumber:       "test-project",
			LocationId:          "test-location",
		}
		req := &gcpgenserver.V1betaInternalStopVolumeReplicationReq{
			Force: gcpgenserver.OptBool{Set: true, Value: false},
		}

		mockOrchestrator.EXPECT().StopReplicationInternal(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil, errors.NewUserInputValidationErr("Invalid request parameters"))

		resp, err := handler.V1betaInternalStopVolumeReplication(context.Background(), req, params)
		assert.NoError(tt, err)
		assert.Equal(tt, float64(400), resp.(*gcpgenserver.V1betaInternalStopVolumeReplicationBadRequest).Code)
		assert.Equal(tt, "Invalid request parameters", resp.(*gcpgenserver.V1betaInternalStopVolumeReplicationBadRequest).Message)
	})

	t.Run("WhenInternalServerError", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaInternalStopVolumeReplicationParams{
			VolumeReplicationId: "test-replication-id",
			ProjectNumber:       "test-project",
			LocationId:          "test-location",
		}
		req := &gcpgenserver.V1betaInternalStopVolumeReplicationReq{
			Force: gcpgenserver.OptBool{Set: true, Value: false},
		}

		mockOrchestrator.EXPECT().StopReplicationInternal(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil, errors.New("Internal server error"))

		resp, err := handler.V1betaInternalStopVolumeReplication(context.Background(), req, params)
		assert.NoError(tt, err)
		assert.Equal(tt, float64(500), resp.(*gcpgenserver.V1betaInternalStopVolumeReplicationInternalServerError).Code)
		assert.Equal(tt, "Internal server error while stopping replication", resp.(*gcpgenserver.V1betaInternalStopVolumeReplicationInternalServerError).Message)
	})
	t.Run("WhenStopReplicationSucceeds", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaInternalStopVolumeReplicationParams{
			VolumeReplicationId: "test-replication-id",
			ProjectNumber:       "test-project",
			LocationId:          "test-location",
		}
		req := &gcpgenserver.V1betaInternalStopVolumeReplicationReq{
			Force: gcpgenserver.OptBool{Set: true, Value: true},
		}

		replication := &models.VolumeReplication{
			Name: "test-replication",
			BaseModel: models.BaseModel{
				UUID: "replication-uuid",
			},
			ReplicationAttributes: &models.ReplicationDetails{
				DestinationReplicationUUID: "destination-replication-uuid",
			},
		}
		job := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				UUID:      "job-uuid",
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			},
			WorkflowID: "job-workflow-id",
		}

		mockOrchestrator.EXPECT().StopReplicationInternal(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(replication, job, nil)

		resp, err := handler.V1betaInternalStopVolumeReplication(context.Background(), req, params)
		assert.NoError(tt, err)
		assert.Equal(tt, convertToInternalV1betaVolumeReplication(replication, job), resp)
	})
}

func TestV1betaUpdateVolumeReplicationInternal(t *testing.T) {
	t.Run("WhenUpdateSuccess", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		req := &gcpgenserver.VolumeReplicationUpdateInternalV1beta{
			Description: gcpgenserver.NewOptNilString("desc"),
		}
		params := gcpgenserver.V1betaInternalUpdateVolumeReplicationParams{
			VolumeReplicationId: "rep-uuid",
			ProjectNumber:       "proj-1",
			XCorrelationID:      gcpgenserver.OptString{Value: "corr-id", Set: true},
			LocationId:          "loc-1",
		}
		volumeReplication := &models.VolumeReplication{
			BaseModel:             models.BaseModel{UUID: "rep-uuid"},
			ReplicationAttributes: &models.ReplicationDetails{EndpointType: "dst"},
		}
		job := &datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "job-uuid"}}
		mockOrchestrator.EXPECT().UpdateVolumeReplicationInternal(mock.Anything, mock.Anything).Return(volumeReplication, job, nil)

		resp, err := handler.V1betaInternalUpdateVolumeReplication(context.Background(), req, params)
		assert.NoError(tt, err)
		assert.NotNil(tt, resp)
	})

	t.Run("WhenNotFoundError", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		req := &gcpgenserver.VolumeReplicationUpdateInternalV1beta{}
		params := gcpgenserver.V1betaInternalUpdateVolumeReplicationParams{
			VolumeReplicationId: "rep-uuid",
			ProjectNumber:       "proj-1",
			XCorrelationID:      gcpgenserver.OptString{Value: "corr-id", Set: true},
			LocationId:          "loc-1",
		}
		mockOrchestrator.EXPECT().UpdateVolumeReplicationInternal(mock.Anything, mock.Anything).Return(nil, nil, errors.NewNotFoundErr("replication", nil))

		resp, err := handler.V1betaInternalUpdateVolumeReplication(context.Background(), req, params)
		assert.NoError(tt, err)
		assert.IsType(tt, &gcpgenserver.V1betaInternalUpdateVolumeReplicationBadRequest{}, resp)
		assert.Equal(tt, 404, int(resp.(*gcpgenserver.V1betaInternalUpdateVolumeReplicationBadRequest).Code))
	})

	t.Run("WhenInternalError", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		req := &gcpgenserver.VolumeReplicationUpdateInternalV1beta{}
		params := gcpgenserver.V1betaInternalUpdateVolumeReplicationParams{
			VolumeReplicationId: "rep-uuid",
			ProjectNumber:       "proj-1",
			XCorrelationID:      gcpgenserver.OptString{Value: "corr-id", Set: true},
			LocationId:          "loc-1",
		}
		mockOrchestrator.EXPECT().UpdateVolumeReplicationInternal(mock.Anything, mock.Anything).Return(nil, nil, errors.New("some error"))

		resp, err := handler.V1betaInternalUpdateVolumeReplication(context.Background(), req, params)
		assert.NoError(tt, err)
		assert.IsType(tt, &gcpgenserver.V1betaInternalUpdateVolumeReplicationInternalServerError{}, resp)
		assert.Equal(tt, "some error", resp.(*gcpgenserver.V1betaInternalUpdateVolumeReplicationInternalServerError).Message)
	})

	t.Run("WhenUpdateSuccessWithClusterLocation", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		req := &gcpgenserver.VolumeReplicationUpdateInternalV1beta{
			Description:     gcpgenserver.NewOptNilString("desc"),
			ClusterLocation: gcpgenserver.NewOptString("us-west1"),
		}
		params := gcpgenserver.V1betaInternalUpdateVolumeReplicationParams{
			VolumeReplicationId: "rep-uuid",
			ProjectNumber:       "proj-1",
			XCorrelationID:      gcpgenserver.OptString{Value: "corr-id", Set: true},
			LocationId:          "loc-1",
		}
		volumeReplication := &models.VolumeReplication{
			BaseModel:             models.BaseModel{UUID: "rep-uuid"},
			ReplicationAttributes: &models.ReplicationDetails{EndpointType: "dst"},
		}
		job := &datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "job-uuid"}}
		mockOrchestrator.EXPECT().UpdateVolumeReplicationInternal(mock.Anything, mock.Anything).Return(volumeReplication, job, nil)

		resp, err := handler.V1betaInternalUpdateVolumeReplication(context.Background(), req, params)
		assert.NoError(tt, err)
		assert.NotNil(tt, resp)
	})
}

func TestV1betaInternalDescribeVolume(t *testing.T) {
	t.Run("WhenVolumeNotFound", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		mockOrchestrator.EXPECT().GetVolume(mock.Anything, "vol-123", true).Return(nil, errors.NewNotFoundErr("volume", nil))

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		params := gcpgenserver.V1betaInternalDescribeVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
			VolumeId:      "vol-123",
		}

		expectedResponse := &gcpgenserver.V1betaInternalDescribeVolumeNotFound{
			Code:    404,
			Message: "Volume not found",
		}

		resp, err := handler.V1betaInternalDescribeVolume(context.Background(), params)
		assert.NoError(tt, err)
		assert.Equal(tt, expectedResponse, resp)
	})

	t.Run("WhenGetVolumeReturnsError", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		mockOrchestrator.EXPECT().GetVolume(mock.Anything, "vol-123", true).Return(nil, errors.New("database error"))

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		params := gcpgenserver.V1betaInternalDescribeVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
			VolumeId:      "vol-123",
		}

		resp, err := handler.V1betaInternalDescribeVolume(context.Background(), params)
		assert.Error(tt, err)
		assert.Equal(tt, "database error", err.Error())

		// Should return Internal Server Error
		serverError, ok := resp.(*gcpgenserver.V1betaInternalDescribeVolumeInternalServerError)
		assert.True(tt, ok, "Response should be V1betaInternalDescribeVolumeInternalServerError")
		assert.Equal(tt, float64(500), serverError.Code)
		assert.Equal(tt, "Internal server error", serverError.Message)
	})

	t.Run("WhenJsonMarshalFails", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		// Mock volume data
		volume := &models.Volume{
			BaseModel: models.BaseModel{
				UUID: "vol-123",
			},
			DisplayName:    "test-volume",
			AccountName:    "test-account",
			PoolID:         "pool-123",
			PoolName:       "test-pool",
			CreationToken:  "creation-token",
			LifeCycleState: "READY",
			QuotaInBytes:   1073741824,
			SvmName:        "test-svm",
			ProtocolTypes:  []string{"NFSV3"},
			EncryptionType: "SOFTWARE_ENCRYPTION",
			Region:         "us-central1",
		}

		mockOrchestrator.EXPECT().GetVolume(mock.Anything, "vol-123", true).Return(volume, nil)

		// Restore original functions after tests
		defer func() {
			jsonMarshal = json.Marshal
		}()

		// Mock jsonMarshal to return error
		jsonMarshal = func(v interface{}) ([]byte, error) {
			return nil, errors.New("marshal error")
		}

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		params := gcpgenserver.V1betaInternalDescribeVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
			VolumeId:      "vol-123",
		}

		resp, err := handler.V1betaInternalDescribeVolume(context.Background(), params)
		assert.NoError(tt, err)

		// Should return Internal Server Error with marshal error message
		serverError, ok := resp.(*gcpgenserver.V1betaInternalDescribeVolumeInternalServerError)
		assert.True(tt, ok, "Response should be V1betaInternalDescribeVolumeInternalServerError")
		assert.Equal(tt, float64(500), serverError.Code)
		assert.Equal(tt, "marshal error", serverError.Message)
	})

	t.Run("WhenJsonUnmarshalFails", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		// Mock volume data
		volume := &models.Volume{
			BaseModel: models.BaseModel{
				UUID: "vol-123",
			},
			DisplayName:    "test-volume",
			AccountName:    "test-account",
			PoolID:         "pool-123",
			PoolName:       "test-pool",
			CreationToken:  "creation-token",
			LifeCycleState: "READY",
			QuotaInBytes:   1073741824,
			SvmName:        "test-svm",
			ProtocolTypes:  []string{"NFSV3"},
			EncryptionType: "SOFTWARE_ENCRYPTION",
			Region:         "us-central1",
		}

		// Restore original functions after tests
		defer func() {
			jsonMarshal = json.Marshal
			jsonUnmarshal = json.Unmarshal
		}()

		mockOrchestrator.EXPECT().GetVolume(mock.Anything, "vol-123", true).Return(volume, nil)
		jsonUnmarshal = func(data []byte, v interface{}) error {
			return errors.New("unmarshal error")
		}

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		params := gcpgenserver.V1betaInternalDescribeVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
			VolumeId:      "vol-123",
		}

		resp, err := handler.V1betaInternalDescribeVolume(context.Background(), params)
		assert.NoError(tt, err)

		// Should return Internal Server Error with unmarshal error message
		serverError, ok := resp.(*gcpgenserver.V1betaInternalDescribeVolumeInternalServerError)
		assert.True(tt, ok, "Response should be V1betaInternalDescribeVolumeInternalServerError")
		assert.Equal(tt, float64(500), serverError.Code)
		assert.Equal(tt, "unmarshal error", serverError.Message)
	})

	t.Run("WhenSuccessfulVolumeDescribe", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		// Mock volume data with SvmName
		volume := &models.Volume{
			BaseModel: models.BaseModel{
				UUID: "vol-123",
			},
			DisplayName:    "test-volume",
			AccountName:    "test-account",
			PoolID:         "pool-123",
			PoolName:       "test-pool",
			CreationToken:  "creation-token",
			LifeCycleState: "READY",
			QuotaInBytes:   1073741824,
			SvmName:        "test-svm",
			ProtocolTypes:  []string{"NFSV3"},
			EncryptionType: "SOFTWARE_ENCRYPTION",
			Region:         "us-central1",
		}

		mockOrchestrator.EXPECT().GetVolume(mock.Anything, "vol-123", true).Return(volume, nil)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		params := gcpgenserver.V1betaInternalDescribeVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
			VolumeId:      "vol-123",
		}

		resp, err := handler.V1betaInternalDescribeVolume(context.Background(), params)
		assert.NoError(tt, err)

		// Verify response type
		successResp, ok := resp.(*gcpgenserver.InternalVolumeV1beta)
		assert.True(tt, ok, "Response should be *InternalVolumeV1beta")
		assert.NotNil(tt, successResp)

		// Verify SvmName is set
		assert.True(tt, successResp.SvmName.Set)
		assert.Equal(tt, "test-svm", successResp.SvmName.Value)
	})

	t.Run("WhenSuccessfulVolumeDescribeWithoutSvmName", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		// Mock volume data without SvmName
		volume := &models.Volume{
			BaseModel: models.BaseModel{
				UUID: "vol-123",
			},
			DisplayName:    "test-volume",
			AccountName:    "test-account",
			PoolID:         "pool-123",
			PoolName:       "test-pool",
			CreationToken:  "creation-token",
			LifeCycleState: "READY",
			QuotaInBytes:   1073741824,
			SvmName:        "", // Empty SvmName
			ProtocolTypes:  []string{"NFSV3"},
			EncryptionType: "SOFTWARE_ENCRYPTION",
			Region:         "us-central1",
		}

		mockOrchestrator.EXPECT().GetVolume(mock.Anything, "vol-123", true).Return(volume, nil)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		params := gcpgenserver.V1betaInternalDescribeVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
			VolumeId:      "vol-123",
		}

		resp, err := handler.V1betaInternalDescribeVolume(context.Background(), params)
		assert.NoError(tt, err)

		// Verify response type
		successResp, ok := resp.(*gcpgenserver.InternalVolumeV1beta)
		assert.True(tt, ok, "Response should be *InternalVolumeV1beta")
		assert.NotNil(tt, successResp)

		// Verify SvmName is empty when volume SvmName is empty
		assert.True(tt, successResp.SvmName.Set)
		assert.Equal(tt, "", successResp.SvmName.Value)
	})
}

func TestV1betaInternalDescribeVolumeByName(t *testing.T) {
	t.Run("WhenVolumeNotFound", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		mockOrchestrator.EXPECT().GetVolumeByName(mock.Anything, "test-volume", "test-project").Return(nil, errors.NewNotFoundErr("volume", nil))

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		params := gcpgenserver.V1betaInternalDescribeVolumeByNameParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
			Name:          "test-volume",
		}

		expectedResponse := &gcpgenserver.V1betaInternalDescribeVolumeByNameNotFound{
			Code:    404,
			Message: "Volume not found",
		}

		resp, err := handler.V1betaInternalDescribeVolumeByName(context.Background(), params)
		assert.NoError(tt, err)
		assert.Equal(tt, expectedResponse, resp)
	})

	t.Run("WhenGetVolumeByNameReturnsError", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		mockOrchestrator.EXPECT().GetVolumeByName(mock.Anything, "test-volume", "test-project").Return(nil, errors.New("database error"))

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		params := gcpgenserver.V1betaInternalDescribeVolumeByNameParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
			Name:          "test-volume",
		}

		resp, err := handler.V1betaInternalDescribeVolumeByName(context.Background(), params)
		assert.Error(tt, err)
		assert.Equal(tt, "database error", err.Error())

		serverError, ok := resp.(*gcpgenserver.V1betaInternalDescribeVolumeByNameInternalServerError)
		assert.True(tt, ok, "Response should be V1betaInternalDescribeVolumeByNameInternalServerError")
		assert.Equal(tt, float64(500), serverError.Code)
		assert.Equal(tt, "Internal server error", serverError.Message)
	})

	t.Run("WhenJsonMarshalFails", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		volume := &models.Volume{
			BaseModel: models.BaseModel{
				UUID: "vol-123",
			},
			DisplayName:    "test-volume",
			AccountName:    "test-account",
			PoolID:         "pool-123",
			PoolName:       "test-pool",
			CreationToken:  "creation-token",
			LifeCycleState: "READY",
			QuotaInBytes:   1073741824,
			SvmName:        "test-svm",
			ProtocolTypes:  []string{"NFSV3"},
			EncryptionType: "SOFTWARE_ENCRYPTION",
			Region:         "us-central1",
		}

		mockOrchestrator.EXPECT().GetVolumeByName(mock.Anything, "test-volume", "test-project").Return(volume, nil)

		defer func() {
			jsonMarshal = json.Marshal
		}()

		jsonMarshal = func(v interface{}) ([]byte, error) {
			return nil, errors.New("marshal error")
		}

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		params := gcpgenserver.V1betaInternalDescribeVolumeByNameParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
			Name:          "test-volume",
		}

		resp, err := handler.V1betaInternalDescribeVolumeByName(context.Background(), params)
		assert.NoError(tt, err)

		serverError, ok := resp.(*gcpgenserver.V1betaInternalDescribeVolumeByNameInternalServerError)
		assert.True(tt, ok, "Response should be V1betaInternalDescribeVolumeByNameInternalServerError")
		assert.Equal(tt, float64(500), serverError.Code)
		assert.Equal(tt, "marshal error", serverError.Message)
	})

	t.Run("WhenJsonUnmarshalFails", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		volume := &models.Volume{
			BaseModel: models.BaseModel{
				UUID: "vol-123",
			},
			DisplayName:    "test-volume",
			AccountName:    "test-account",
			PoolID:         "pool-123",
			PoolName:       "test-pool",
			CreationToken:  "creation-token",
			LifeCycleState: "READY",
			QuotaInBytes:   1073741824,
			SvmName:        "test-svm",
			ProtocolTypes:  []string{"NFSV3"},
			EncryptionType: "SOFTWARE_ENCRYPTION",
			Region:         "us-central1",
		}

		defer func() {
			jsonMarshal = json.Marshal
			jsonUnmarshal = json.Unmarshal
		}()

		mockOrchestrator.EXPECT().GetVolumeByName(mock.Anything, "test-volume", "test-project").Return(volume, nil)
		jsonUnmarshal = func(data []byte, v interface{}) error {
			return errors.New("unmarshal error")
		}

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		params := gcpgenserver.V1betaInternalDescribeVolumeByNameParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
			Name:          "test-volume",
		}

		resp, err := handler.V1betaInternalDescribeVolumeByName(context.Background(), params)
		assert.NoError(tt, err)

		serverError, ok := resp.(*gcpgenserver.V1betaInternalDescribeVolumeByNameInternalServerError)
		assert.True(tt, ok, "Response should be V1betaInternalDescribeVolumeByNameInternalServerError")
		assert.Equal(tt, float64(500), serverError.Code)
		assert.Equal(tt, "unmarshal error", serverError.Message)
	})

	t.Run("WhenSuccessfulVolumeDescribe", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		volume := &models.Volume{
			BaseModel: models.BaseModel{
				UUID: "vol-123",
			},
			DisplayName:    "test-volume",
			AccountName:    "test-account",
			PoolID:         "pool-123",
			PoolName:       "test-pool",
			CreationToken:  "creation-token",
			LifeCycleState: "READY",
			QuotaInBytes:   1073741824,
			SvmName:        "test-svm",
			ProtocolTypes:  []string{"NFSV3"},
			EncryptionType: "SOFTWARE_ENCRYPTION",
			Region:         "us-central1",
		}

		mockOrchestrator.EXPECT().GetVolumeByName(mock.Anything, "test-volume", "test-project").Return(volume, nil)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		params := gcpgenserver.V1betaInternalDescribeVolumeByNameParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
			Name:          "test-volume",
		}

		resp, err := handler.V1betaInternalDescribeVolumeByName(context.Background(), params)
		assert.NoError(tt, err)

		successResp, ok := resp.(*gcpgenserver.InternalVolumeV1beta)
		assert.True(tt, ok, "Response should be *InternalVolumeV1beta")
		assert.NotNil(tt, successResp)
		assert.True(tt, successResp.SvmName.Set)
		assert.Equal(tt, "test-svm", successResp.SvmName.Value)
	})

	t.Run("WhenSuccessfulVolumeDescribeWithoutSvmName", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		volume := &models.Volume{
			BaseModel: models.BaseModel{
				UUID: "vol-123",
			},
			DisplayName:    "test-volume",
			AccountName:    "test-account",
			PoolID:         "pool-123",
			PoolName:       "test-pool",
			CreationToken:  "creation-token",
			LifeCycleState: "READY",
			QuotaInBytes:   1073741824,
			SvmName:        "",
			ProtocolTypes:  []string{"NFSV3"},
			EncryptionType: "SOFTWARE_ENCRYPTION",
			Region:         "us-central1",
		}

		mockOrchestrator.EXPECT().GetVolumeByName(mock.Anything, "test-volume", "test-project").Return(volume, nil)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		params := gcpgenserver.V1betaInternalDescribeVolumeByNameParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
			Name:          "test-volume",
		}

		resp, err := handler.V1betaInternalDescribeVolumeByName(context.Background(), params)
		assert.NoError(tt, err)

		successResp, ok := resp.(*gcpgenserver.InternalVolumeV1beta)
		assert.True(tt, ok, "Response should be *InternalVolumeV1beta")
		assert.NotNil(tt, successResp)
		assert.True(tt, successResp.SvmName.Set)
		assert.Equal(tt, "", successResp.SvmName.Value)
	})
}

// TestInternalVolumeV1beta_ResourceId_ValidationChange tests the validation change
// for InternalVolume_v1beta resourceId from the old pattern ^[a-zA-Z][a-zA-Z0-9_]{0,62}$
// to the new pattern ^[a-z]([a-z0-9-_]{0,61}[a-z0-9])?$
func TestInternalVolumeV1beta_ResourceId_ValidationChange(t *testing.T) {
	t.Run("ValidResourceIds_ShouldPass", func(t *testing.T) {
		validResourceIds := []string{
			"a",             // Single lowercase letter
			"ab",            // Two characters
			"a-b",           // Hyphen allowed
			"a_b",           // Underscore allowed
			"volume1",       // Common format
			"my-volume-123", // Mixed with hyphen
			"test_vol_1",    // Mixed with underscore
		}

		for _, resourceId := range validResourceIds {
			volume := gcpgenserver.InternalVolumeV1beta{
				ResourceId: gcpgenserver.NewOptString(resourceId),
			}
			err := volume.Validate()
			assert.NoError(t, err, "ResourceId '%s' should be valid", resourceId)
		}
	})

	t.Run("InvalidResourceIds_ShouldFail", func(t *testing.T) {
		invalidResourceIds := []string{
			"",        // Empty string
			"A",       // Uppercase not allowed
			"Volume1", // Starts with uppercase
			"1volume", // Starts with number
			"-volume", // Starts with hyphen
			"_volume", // Starts with underscore
			"volume-", // Ends with hyphen
			"volume_", // Ends with underscore
			"vol#ume", // Special characters not allowed
			"vol ume", // Space not allowed
		}

		for _, resourceId := range invalidResourceIds {
			volume := gcpgenserver.InternalVolumeV1beta{
				ResourceId: gcpgenserver.NewOptString(resourceId),
			}
			err := volume.Validate()
			assert.Error(t, err, "ResourceId '%s' should be invalid", resourceId)
		}
	})
}

func TestV1betaInternalUpdateVolumeReplicationAttributes(t *testing.T) {
	t.Run("WhenNotFoundError", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		notFoundErr := errors.NewNotFoundErr("volume replication", nil)
		mockOrchestrator.EXPECT().UpdateVolumeReplicationAttributes(mock.Anything, mock.AnythingOfType("models.UpdateVolumeReplicationAttributesParams")).Return(nil, notFoundErr)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		req := &gcpgenserver.VolumeReplicationInternalV1beta{
			VolumeReplicationUuid: gcpgenserver.NewOptString("replication-123"),
			EndpointType:          gcpgenserver.VolumeReplicationInternalV1betaEndpointTypeDst,
		}

		params := gcpgenserver.V1betaInternalUpdateVolumeReplicationAttributesParams{
			ProjectNumber:       "project-123",
			LocationId:          "us-central1",
			VolumeReplicationId: "replication-123",
		}

		response, err := handler.V1betaInternalUpdateVolumeReplicationAttributes(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.IsType(tt, &gcpgenserver.V1betaInternalUpdateVolumeReplicationAttributesBadRequest{}, response)
		badRequestResp := response.(*gcpgenserver.V1betaInternalUpdateVolumeReplicationAttributesBadRequest)
		assert.Equal(tt, float64(400), badRequestResp.Code)
		assert.Contains(tt, badRequestResp.Message, "volume replication")
	})

	t.Run("WhenInternalServerError", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		internalErr := errors.New("database connection failed")
		mockOrchestrator.EXPECT().UpdateVolumeReplicationAttributes(mock.Anything, mock.AnythingOfType("models.UpdateVolumeReplicationAttributesParams")).Return(nil, internalErr)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		req := &gcpgenserver.VolumeReplicationInternalV1beta{
			VolumeReplicationUuid: gcpgenserver.NewOptString("replication-456"),
			EndpointType:          gcpgenserver.VolumeReplicationInternalV1betaEndpointTypeSrc,
		}

		params := gcpgenserver.V1betaInternalUpdateVolumeReplicationAttributesParams{
			ProjectNumber:       "project-456",
			LocationId:          "europe-west1",
			VolumeReplicationId: "replication-456",
		}

		response, err := handler.V1betaInternalUpdateVolumeReplicationAttributes(context.Background(), req, params)

		assert.Error(tt, err)
		assert.Equal(tt, "database connection failed", err.Error())
		assert.IsType(tt, &gcpgenserver.V1betaInternalUpdateVolumeReplicationAttributesInternalServerError{}, response)
		internalServerResp := response.(*gcpgenserver.V1betaInternalUpdateVolumeReplicationAttributesInternalServerError)
		assert.Equal(tt, float64(500), internalServerResp.Code)
		assert.Equal(tt, "Internal server error", internalServerResp.Message)
	})

	t.Run("WhenSuccessful", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		job := &models.Job{
			BaseModel: models.BaseModel{
				UUID: "job-789",
			},
			State: datamodel.JobsStateDONE,
		}
		mockOrchestrator.EXPECT().UpdateVolumeReplicationAttributes(mock.Anything, mock.AnythingOfType("models.UpdateVolumeReplicationAttributesParams")).Return(job, nil)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		req := &gcpgenserver.VolumeReplicationInternalV1beta{
			VolumeReplicationUuid: gcpgenserver.NewOptString("replication-789"),
			EndpointType:          gcpgenserver.VolumeReplicationInternalV1betaEndpointTypeDst,
			SourceHostName:        "source-host",
			SourceServerName:      "source-svm",
			SourceVolumeName:      "source-volume",
		}

		params := gcpgenserver.V1betaInternalUpdateVolumeReplicationAttributesParams{
			ProjectNumber:       "project-789",
			LocationId:          "asia-southeast1",
			VolumeReplicationId: "replication-789",
		}

		response, err := handler.V1betaInternalUpdateVolumeReplicationAttributes(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.IsType(tt, &gcpgenserver.OperationV1beta{}, response)
		operationResp := response.(*gcpgenserver.OperationV1beta)
		expectedOpName := fmt.Sprintf("/v1beta/projects/%s/locations/%s/operations/%s", params.ProjectNumber, params.LocationId, job.UUID)
		assert.Equal(tt, expectedOpName, operationResp.Name.Value)
		assert.True(tt, operationResp.Done.Value)
	})
}

func TestV1betaInternalReverseVolumeReplication(t *testing.T) {
	t.Run("WhenNotFoundError", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		notFoundErr := errors.NewNotFoundErr("volume replication", nil)
		mockOrchestrator.EXPECT().ReverseReplicationInternal(mock.Anything, mock.Anything, mock.Anything).Return(nil, nil, notFoundErr)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		params := gcpgenserver.V1betaInternalReverseVolumeReplicationParams{
			ProjectNumber:       "project-123",
			LocationId:          "us-central1",
			VolumeReplicationId: "replication-123",
			XCorrelationID:      gcpgenserver.NewOptString("corr-123"),
		}

		response, err := handler.V1betaInternalReverseVolumeReplication(context.Background(), params)

		assert.NoError(tt, err)
		assert.IsType(tt, &gcpgenserver.V1betaInternalReverseVolumeReplicationNotFound{}, response)
		notFoundResp := response.(*gcpgenserver.V1betaInternalReverseVolumeReplicationNotFound)
		assert.Equal(tt, float64(404), notFoundResp.Code)
		assert.Equal(tt, "Volume replication not found", notFoundResp.Message)
	})

	t.Run("WhenUserInputValidationError", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		validationErr := errors.NewUserInputValidationErr("Invalid replication state for reverse operation")
		mockOrchestrator.EXPECT().ReverseReplicationInternal(mock.Anything, mock.Anything, mock.Anything).Return(nil, nil, validationErr)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		params := gcpgenserver.V1betaInternalReverseVolumeReplicationParams{
			ProjectNumber:       "project-456",
			LocationId:          "europe-west1",
			VolumeReplicationId: "replication-456",
			XCorrelationID:      gcpgenserver.NewOptString("corr-456"),
		}

		response, err := handler.V1betaInternalReverseVolumeReplication(context.Background(), params)

		assert.NoError(tt, err)
		assert.IsType(tt, &gcpgenserver.V1betaInternalReverseVolumeReplicationBadRequest{}, response)
		badRequestResp := response.(*gcpgenserver.V1betaInternalReverseVolumeReplicationBadRequest)
		assert.Equal(tt, float64(400), badRequestResp.Code)
		assert.Equal(tt, "Invalid request parameters", badRequestResp.Message)
	})

	t.Run("WhenInternalServerError", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		internalErr := errors.New("database connection failed")
		mockOrchestrator.EXPECT().ReverseReplicationInternal(mock.Anything, mock.Anything, mock.Anything).Return(nil, nil, internalErr)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		params := gcpgenserver.V1betaInternalReverseVolumeReplicationParams{
			ProjectNumber:       "project-789",
			LocationId:          "asia-southeast1",
			VolumeReplicationId: "replication-789",
			XCorrelationID:      gcpgenserver.NewOptString("corr-789"),
		}

		response, err := handler.V1betaInternalReverseVolumeReplication(context.Background(), params)

		assert.NoError(tt, err)
		assert.IsType(tt, &gcpgenserver.V1betaInternalReverseVolumeReplicationInternalServerError{}, response)
		internalServerResp := response.(*gcpgenserver.V1betaInternalReverseVolumeReplicationInternalServerError)
		assert.Equal(tt, float64(500), internalServerResp.Code)
		assert.Equal(tt, "Internal server error while reversing replication", internalServerResp.Message)
	})

	t.Run("WhenSuccessful", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		volumeReplication := &models.VolumeReplication{
			BaseModel: models.BaseModel{
				UUID: "replication-success",
			},
			Name:  "test-replication",
			State: "reversed",
			ReplicationAttributes: &models.ReplicationDetails{
				EndpointType:          "dst",
				SourceRegion:          "us-central1",
				SourceHostName:        "source-host",
				SourceSvmName:         "source-svm",
				SourceVolumeName:      "source-volume",
				SourceVolumeUUID:      "src-vol-uuid",
				SourcePoolUUID:        "src-pool-uuid",
				DestinationHostName:   "dest-host",
				DestinationSvmName:    "dest-svm",
				DestinationVolumeName: "dest-volume",
				DestinationVolumeUUID: "dest-vol-uuid",
				DestinationPoolUUID:   "dest-pool-uuid",
				ReplicationType:       "async",
			},
		}

		job := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				UUID: "job-success",
			},
			State: "done",
		}

		mockOrchestrator.EXPECT().ReverseReplicationInternal(mock.Anything, mock.Anything, mock.Anything).Return(volumeReplication, job, nil)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		params := gcpgenserver.V1betaInternalReverseVolumeReplicationParams{
			ProjectNumber:       "project-success",
			LocationId:          "us-west1",
			VolumeReplicationId: "replication-success",
			XCorrelationID:      gcpgenserver.NewOptString("corr-success"),
		}

		response, err := handler.V1betaInternalReverseVolumeReplication(context.Background(), params)

		assert.NoError(tt, err)
		assert.IsType(tt, &gcpgenserver.VolumeReplicationInternalV1beta{}, response)
		volumeReplResp := response.(*gcpgenserver.VolumeReplicationInternalV1beta)
		assert.Equal(tt, "replication-success", volumeReplResp.VolumeReplicationUuid.Value)
		assert.Equal(tt, gcpgenserver.VolumeReplicationInternalV1betaEndpointTypeDst, volumeReplResp.EndpointType)
		assert.Equal(tt, "source-host", volumeReplResp.SourceHostName)
		mockOrchestrator.AssertExpectations(tt)
	})

	t.Run("WhenEmptyCorrelationId", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		volumeReplication := &models.VolumeReplication{
			BaseModel: models.BaseModel{
				UUID: "replication-no-corr",
			},
			Name:  "test-replication-no-corr",
			State: "reversed",
			ReplicationAttributes: &models.ReplicationDetails{
				EndpointType:          "dst",
				SourceRegion:          "us-central1",
				SourceHostName:        "source-host-no-corr",
				SourceSvmName:         "source-svm-no-corr",
				SourceVolumeName:      "source-volume-no-corr",
				SourceVolumeUUID:      "src-vol-uuid-no-corr",
				SourcePoolUUID:        "src-pool-uuid-no-corr",
				DestinationHostName:   "dest-host-no-corr",
				DestinationSvmName:    "dest-svm-no-corr",
				DestinationVolumeName: "dest-volume-no-corr",
				DestinationVolumeUUID: "dest-vol-uuid-no-corr",
				DestinationPoolUUID:   "dest-pool-uuid-no-corr",
				ReplicationType:       "async",
			},
		}

		job := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				UUID: "job-no-corr",
			},
			State: "done",
		}

		mockOrchestrator.EXPECT().ReverseReplicationInternal(mock.Anything, "replication-no-corr", "project-no-corr").Return(volumeReplication, job, nil)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		params := gcpgenserver.V1betaInternalReverseVolumeReplicationParams{
			ProjectNumber:       "project-no-corr",
			LocationId:          "us-east1",
			VolumeReplicationId: "replication-no-corr",
			// No XCorrelationID provided
		}

		response, err := handler.V1betaInternalReverseVolumeReplication(context.Background(), params)

		assert.NoError(tt, err)
		assert.IsType(tt, &gcpgenserver.VolumeReplicationInternalV1beta{}, response)
		volumeReplResp := response.(*gcpgenserver.VolumeReplicationInternalV1beta)
		assert.NotNil(tt, volumeReplResp)
	})
}

func TestV1betaInternalUpdateVolume(t *testing.T) {
	t.Run("WhenLocationParsingFails", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		req := &gcpgenserver.VolumeUpdateV1beta{}
		params := gcpgenserver.V1betaInternalUpdateVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "invalid-location",
			VolumeId:      "vol-123",
		}

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "", "", &gcpgenserver.Error{
				Code:    400,
				Message: "Invalid location format",
			}
		}
		defer func() {
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()

		expectedResponse := &gcpgenserver.V1betaInternalUpdateVolumeBadRequest{
			Code:    400,
			Message: "Invalid location format",
		}

		resp, err := handler.V1betaInternalUpdateVolume(context.Background(), req, params)
		assert.NoError(tt, err)
		assert.Equal(tt, expectedResponse, resp)
	})

	t.Run("WhenPrepareUpdateVolumeParamsFailsWithValidationError", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		req := &gcpgenserver.VolumeUpdateV1beta{}
		params := gcpgenserver.V1betaInternalUpdateVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "us-central1",
			VolumeId:      "vol-123",
		}

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-central1", "us-central1-a", nil
		}
		defer func() {
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()

		volume := &models.Volume{
			BaseModel: models.BaseModel{UUID: "vol-123"},
		}
		mockOrchestrator.EXPECT().GetVolume(mock.Anything, "vol-123", false).Return(volume, nil)

		prepareUpdateVolumeParams = func(req *gcpgenserver.VolumeUpdateV1beta, params gcpgenserver.V1betaUpdateVolumeParams, region string, dbVolume *models.Volume) (*commonparams.UpdateVolumeParams, error) {
			return nil, errors.NewUserInputValidationErr("Invalid volume parameters")
		}
		defer func() {
			prepareUpdateVolumeParams = _prepareUpdateVolumeParams
		}()

		expectedResponse := &gcpgenserver.V1betaInternalUpdateVolumeBadRequest{
			Code:    400,
			Message: "Invalid volume parameters",
		}

		resp, err := handler.V1betaInternalUpdateVolume(context.Background(), req, params)
		assert.NoError(tt, err)
		assert.Equal(tt, expectedResponse, resp)
	})

	t.Run("WhenPrepareUpdateVolumeParamsFailsWithNotFoundError", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		req := &gcpgenserver.VolumeUpdateV1beta{}
		params := gcpgenserver.V1betaInternalUpdateVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "us-central1",
			VolumeId:      "vol-123",
		}

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-central1", "us-central1-a", nil
		}
		defer func() {
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()

		volume := &models.Volume{
			BaseModel: models.BaseModel{UUID: "vol-123"},
		}
		mockOrchestrator.EXPECT().GetVolume(mock.Anything, "vol-123", false).Return(volume, nil)

		prepareUpdateVolumeParams = func(req *gcpgenserver.VolumeUpdateV1beta, params gcpgenserver.V1betaUpdateVolumeParams, region string, dbVolume *models.Volume) (*commonparams.UpdateVolumeParams, error) {
			return nil, errors.NewNotFoundErr("Volume", nil)
		}
		defer func() {
			prepareUpdateVolumeParams = _prepareUpdateVolumeParams
		}()

		expectedResponse := &gcpgenserver.V1betaInternalUpdateVolumeBadRequest{
			Code:    400,
			Message: "Volume not found",
		}

		resp, err := handler.V1betaInternalUpdateVolume(context.Background(), req, params)
		assert.NoError(tt, err)
		assert.Equal(tt, expectedResponse, resp)
	})

	t.Run("WhenPrepareUpdateVolumeParamsFailsWithOtherError", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		req := &gcpgenserver.VolumeUpdateV1beta{}
		params := gcpgenserver.V1betaInternalUpdateVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "us-central1",
			VolumeId:      "vol-123",
		}

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-central1", "us-central1-a", nil
		}
		defer func() {
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()

		volume := &models.Volume{
			BaseModel: models.BaseModel{UUID: "vol-123"},
		}
		mockOrchestrator.EXPECT().GetVolume(mock.Anything, "vol-123", false).Return(volume, nil)

		prepareUpdateVolumeParams = func(req *gcpgenserver.VolumeUpdateV1beta, params gcpgenserver.V1betaUpdateVolumeParams, region string, dbVolume *models.Volume) (*commonparams.UpdateVolumeParams, error) {
			return nil, errors.New("Database connection failed")
		}
		defer func() {
			prepareUpdateVolumeParams = _prepareUpdateVolumeParams
		}()

		expectedResponse := &gcpgenserver.V1betaInternalUpdateVolumeInternalServerError{
			Code:    500,
			Message: "Database connection failed",
		}

		resp, err := handler.V1betaInternalUpdateVolume(context.Background(), req, params)
		assert.Error(tt, err)
		assert.Equal(tt, "Database connection failed", err.Error())
		assert.Equal(tt, expectedResponse, resp)
	})

	t.Run("WhenOrchestratorUpdateVolumeFailsWithValidationError", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		req := &gcpgenserver.VolumeUpdateV1beta{}
		params := gcpgenserver.V1betaInternalUpdateVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "us-central1",
			VolumeId:      "vol-123",
		}

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-central1", "us-central1-a", nil
		}
		defer func() {
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()

		volume := &models.Volume{
			BaseModel: models.BaseModel{UUID: "vol-123"},
		}
		mockOrchestrator.EXPECT().GetVolume(mock.Anything, "vol-123", false).Return(volume, nil)

		prepareUpdateVolumeParams = func(req *gcpgenserver.VolumeUpdateV1beta, params gcpgenserver.V1betaUpdateVolumeParams, region string, dbVolume *models.Volume) (*commonparams.UpdateVolumeParams, error) {
			return &commonparams.UpdateVolumeParams{}, nil
		}
		defer func() {
			prepareUpdateVolumeParams = _prepareUpdateVolumeParams
		}()

		// Mock orchestrator to return validation error
		mockOrchestrator.EXPECT().UpdateVolume(mock.Anything, mock.Anything).Return(nil, "", errors.NewUserInputValidationErr("Invalid volume state"))

		expectedResponse := &gcpgenserver.V1betaInternalUpdateVolumeBadRequest{
			Code:    400,
			Message: "Invalid volume state",
		}

		resp, err := handler.V1betaInternalUpdateVolume(context.Background(), req, params)
		assert.NoError(tt, err)
		assert.Equal(tt, expectedResponse, resp)
	})

	t.Run("WhenOrchestratorUpdateVolumeFailsWithNotFoundError", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		req := &gcpgenserver.VolumeUpdateV1beta{}
		params := gcpgenserver.V1betaInternalUpdateVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "us-central1",
			VolumeId:      "vol-123",
		}

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-central1", "us-central1-a", nil
		}
		defer func() {
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()

		volume := &models.Volume{
			BaseModel: models.BaseModel{UUID: "vol-123"},
		}
		mockOrchestrator.EXPECT().GetVolume(mock.Anything, "vol-123", false).Return(volume, nil)

		prepareUpdateVolumeParams = func(req *gcpgenserver.VolumeUpdateV1beta, params gcpgenserver.V1betaUpdateVolumeParams, region string, dbVolume *models.Volume) (*commonparams.UpdateVolumeParams, error) {
			return &commonparams.UpdateVolumeParams{}, nil
		}
		defer func() {
			prepareUpdateVolumeParams = _prepareUpdateVolumeParams
		}()

		// Mock orchestrator to return not found error
		mockOrchestrator.EXPECT().UpdateVolume(mock.Anything, mock.Anything).Return(nil, "", errors.NewNotFoundErr("Volume", nil))

		expectedResponse := &gcpgenserver.V1betaInternalUpdateVolumeBadRequest{
			Code:    400,
			Message: "Volume not found",
		}

		resp, err := handler.V1betaInternalUpdateVolume(context.Background(), req, params)
		assert.NoError(tt, err)
		assert.Equal(tt, expectedResponse, resp)
	})

	t.Run("WhenOrchestratorUpdateVolumeFailsWithOtherError", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		req := &gcpgenserver.VolumeUpdateV1beta{}
		params := gcpgenserver.V1betaInternalUpdateVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "us-central1",
			VolumeId:      "vol-123",
		}

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-central1", "us-central1-a", nil
		}
		defer func() {
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()

		volume := &models.Volume{
			BaseModel: models.BaseModel{UUID: "vol-123"},
		}
		mockOrchestrator.EXPECT().GetVolume(mock.Anything, "vol-123", false).Return(volume, nil)

		prepareUpdateVolumeParams = func(req *gcpgenserver.VolumeUpdateV1beta, params gcpgenserver.V1betaUpdateVolumeParams, region string, dbVolume *models.Volume) (*commonparams.UpdateVolumeParams, error) {
			return &commonparams.UpdateVolumeParams{}, nil
		}
		defer func() {
			prepareUpdateVolumeParams = _prepareUpdateVolumeParams
		}()

		// Mock orchestrator to return other error
		mockOrchestrator.EXPECT().UpdateVolume(mock.Anything, mock.Anything).Return(nil, "", errors.New("Internal database error"))

		expectedResponse := &gcpgenserver.V1betaInternalUpdateVolumeInternalServerError{
			Code:    500,
			Message: "Internal database error",
		}

		resp, err := handler.V1betaInternalUpdateVolume(context.Background(), req, params)
		assert.Error(tt, err)
		assert.Equal(tt, "Internal database error", err.Error())
		assert.Equal(tt, expectedResponse, resp)
	})

	t.Run("WhenLifeCycleStateUpdating_ThenReturnDoneAsFalse", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		req := &gcpgenserver.VolumeUpdateV1beta{}
		params := gcpgenserver.V1betaInternalUpdateVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "us-central1",
			VolumeId:      "vol-123",
		}

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-central1", "us-central1-a", nil
		}
		defer func() {
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()

		volume := &models.Volume{
			BaseModel: models.BaseModel{UUID: "vol-123"},
		}
		mockOrchestrator.EXPECT().GetVolume(mock.Anything, "vol-123", false).Return(volume, nil)

		prepareUpdateVolumeParams = func(req *gcpgenserver.VolumeUpdateV1beta, params gcpgenserver.V1betaUpdateVolumeParams, region string, dbVolume *models.Volume) (*commonparams.UpdateVolumeParams, error) {
			return &commonparams.UpdateVolumeParams{}, nil
		}
		defer func() {
			prepareUpdateVolumeParams = _prepareUpdateVolumeParams
		}()

		jobUUID := "job-uuid"
		updatedVolume := &models.Volume{
			BaseModel:      models.BaseModel{UUID: "vol-1"},
			LifeCycleState: "UPDATING",
		}
		mockOrchestrator.EXPECT().UpdateVolume(mock.Anything, mock.Anything).Return(updatedVolume, jobUUID, nil)

		result, err := handler.V1betaInternalUpdateVolume(context.Background(), req, params)
		assert.NoError(tt, err)
		op, ok := result.(*gcpgenserver.OperationV1beta)
		assert.True(tt, ok)
		assert.Equal(tt, "/v1beta/projects/test-project/locations/us-central1/operations/job-uuid", op.Name.Value)
		assert.False(tt, op.Done.Value)
	})

	t.Run("WhenLifeCycleStateNotUpdating_ThenReturnDoneAsTrue", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		req := &gcpgenserver.VolumeUpdateV1beta{}
		params := gcpgenserver.V1betaInternalUpdateVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "us-central1",
			VolumeId:      "vol-123",
		}

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-central1", "us-central1-a", nil
		}
		defer func() {
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()

		volume := &models.Volume{
			BaseModel: models.BaseModel{UUID: "vol-123"},
		}
		mockOrchestrator.EXPECT().GetVolume(mock.Anything, "vol-123", false).Return(volume, nil)

		prepareUpdateVolumeParams = func(req *gcpgenserver.VolumeUpdateV1beta, params gcpgenserver.V1betaUpdateVolumeParams, region string, dbVolume *models.Volume) (*commonparams.UpdateVolumeParams, error) {
			return &commonparams.UpdateVolumeParams{}, nil
		}
		defer func() {
			prepareUpdateVolumeParams = _prepareUpdateVolumeParams
		}()

		jobUUID := "job-uuid"
		updatedVolume := &models.Volume{
			BaseModel:      models.BaseModel{UUID: "vol-1"},
			LifeCycleState: "STATE",
		}
		mockOrchestrator.EXPECT().UpdateVolume(mock.Anything, mock.Anything).Return(updatedVolume, jobUUID, nil)

		result, err := handler.V1betaInternalUpdateVolume(context.Background(), req, params)
		assert.NoError(tt, err)
		op, ok := result.(*gcpgenserver.OperationV1beta)
		assert.True(tt, ok)
		assert.Equal(tt, "/v1beta/projects/test-project/locations/us-central1/operations/job-uuid", op.Name.Value)
		assert.True(tt, op.Done.Value)
	})

	t.Run("WhenGetVolumeReturnsNotFoundError", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		req := &gcpgenserver.VolumeUpdateV1beta{}
		params := gcpgenserver.V1betaInternalUpdateVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "us-central1",
			VolumeId:      "vol-123",
		}

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-central1", "us-central1-a", nil
		}
		defer func() {
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()

		// Mock GetVolume to return NotFoundErr
		mockOrchestrator.EXPECT().GetVolume(mock.Anything, "vol-123", false).Return(nil, errors.NewNotFoundErr("Volume", nil))

		expectedResponse := &gcpgenserver.V1betaInternalUpdateVolumeNotFound{
			Code:    404,
			Message: "Volume not found",
		}

		resp, err := handler.V1betaInternalUpdateVolume(context.Background(), req, params)
		assert.NoError(tt, err)
		assert.Equal(tt, expectedResponse, resp)
	})

	t.Run("WhenGetVolumeReturnsInternalError", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		req := &gcpgenserver.VolumeUpdateV1beta{}
		params := gcpgenserver.V1betaInternalUpdateVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "us-central1",
			VolumeId:      "vol-123",
		}

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-central1", "us-central1-a", nil
		}
		defer func() {
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()

		// Mock GetVolume to return a generic error
		mockOrchestrator.EXPECT().GetVolume(mock.Anything, "vol-123", false).Return(nil, errors.New("database connection error"))

		expectedResponse := &gcpgenserver.V1betaInternalUpdateVolumeInternalServerError{
			Code:    500,
			Message: "Internal server error",
		}

		resp, err := handler.V1betaInternalUpdateVolume(context.Background(), req, params)
		assert.NoError(tt, err)
		assert.Equal(tt, expectedResponse, resp)
	})
}

func TestV1betaInternalDescribeBackupVault_Success(t *testing.T) {
	t.Run("WhenBackupVaultNotFound", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		params := gcpgenserver.V1betaInternalDescribeBackupVaultParams{
			ProjectNumber: "test-project",
			LocationId:    "us-central1",
			BackupVaultId: "non-existent-uuid",
		}

		mockOrchestrator.EXPECT().GetBackupVaultByExternalUUIDAndOwnerID(mock.Anything, "non-existent-uuid", "test-project").Return(nil, errors.NewNotFoundErr("backup vault", nil))

		expectedResponse := &gcpgenserver.V1betaInternalDescribeBackupVaultNotFound{
			Code:    404,
			Message: "BackupVault not found",
		}

		resp, err := handler.V1betaInternalDescribeBackupVault(context.Background(), params)
		assert.NoError(tt, err)
		assert.Equal(tt, expectedResponse, resp)
	})

	t.Run("WhenGetBackupVaultReturnsError", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		params := gcpgenserver.V1betaInternalDescribeBackupVaultParams{
			ProjectNumber: "test-project",
			LocationId:    "us-central1",
			BackupVaultId: "test-backup-vault-uuid",
		}

		mockOrchestrator.EXPECT().GetBackupVaultByExternalUUIDAndOwnerID(mock.Anything, "test-backup-vault-uuid", "test-project").Return(nil, errors.New("database connection error"))

		expectedResponse := &gcpgenserver.V1betaInternalDescribeBackupVaultInternalServerError{
			Code:    500,
			Message: "Failed to get BackupVault from VCP database",
		}

		resp, err := handler.V1betaInternalDescribeBackupVault(context.Background(), params)
		assert.Error(tt, err)
		assert.Equal(tt, "database connection error", err.Error())
		assert.Equal(tt, expectedResponse, resp)
	})

	t.Run("WhenSuccessfulDescribe", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		params := gcpgenserver.V1betaInternalDescribeBackupVaultParams{
			ProjectNumber: "test-project",
			LocationId:    "us-central1",
			BackupVaultId: "test-backup-vault-uuid",
		}

		now := time.Now()
		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{
				UUID:      "test-backup-vault-uuid",
				CreatedAt: now,
				UpdatedAt: now,
			},
			Name:                  "test-backup-vault",
			AccountVendorID:       "test-account-vendor-id",
			LifeCycleState:        "READY",
			BackupVaultType:       "CROSS_REGION",
			Description:           func() *string { s := "Test backup vault"; return &s }(),
			BackupRegionName:      func() *string { s := "us-west1"; return &s }(),
			SourceRegionName:      func() *string { s := "us-central1"; return &s }(),
			LifeCycleStateDetails: "Ready for backup operations",
		}

		mockOrchestrator.EXPECT().GetBackupVaultByExternalUUIDAndOwnerID(mock.Anything, "test-backup-vault-uuid", "test-project").Return(backupVault, nil)

		resp, err := handler.V1betaInternalDescribeBackupVault(context.Background(), params)
		assert.NoError(tt, err)

		result, ok := resp.(*gcpgenserver.BackupVaultInternalV1beta)
		assert.True(tt, ok)
		assert.Equal(tt, "test-backup-vault-uuid", result.BackupVaultId)
		assert.Equal(tt, "test-backup-vault", result.ResourceId)
		assert.Equal(tt, "test-account-vendor-id", result.AccountVendorId)
		assert.Equal(tt, gcpgenserver.BackupVaultInternalV1betaLifeCycleStateREADY, result.LifeCycleState)
		assert.Equal(tt, gcpgenserver.BackupVaultInternalV1betaBackupVaultTypeCROSSREGION, result.BackupVaultType)
		assert.True(tt, result.Description.IsSet())
		assert.Equal(tt, "Test backup vault", result.Description.Value)
		assert.True(tt, result.BackupRegion.IsSet())
		assert.Equal(tt, "us-west1", result.BackupRegion.Value)
		assert.True(tt, result.SourceRegion.IsSet())
		assert.Equal(tt, "us-central1", result.SourceRegion.Value)
		assert.True(tt, result.CreatedAt.IsSet())
		assert.True(tt, result.UpdatedAt.IsSet())
	})

	t.Run("WhenSuccessfulDescribeWithMinimalFields", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		params := gcpgenserver.V1betaInternalDescribeBackupVaultParams{
			ProjectNumber: "test-project",
			LocationId:    "us-central1",
			BackupVaultId: "test-backup-vault-uuid",
		}

		now := time.Now()
		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{
				UUID:      "test-backup-vault-uuid",
				CreatedAt: now,
				UpdatedAt: now,
			},
			Name:            "test-backup-vault",
			AccountVendorID: "test-account-vendor-id",
			LifeCycleState:  "READY",
			BackupVaultType: "CROSS_REGION",
		}

		mockOrchestrator.EXPECT().GetBackupVaultByExternalUUIDAndOwnerID(mock.Anything, "test-backup-vault-uuid", "test-project").Return(backupVault, nil)

		resp, err := handler.V1betaInternalDescribeBackupVault(context.Background(), params)
		assert.NoError(tt, err)

		result, ok := resp.(*gcpgenserver.BackupVaultInternalV1beta)
		assert.True(tt, ok)
		assert.Equal(tt, "test-backup-vault-uuid", result.BackupVaultId)
		assert.Equal(tt, "test-backup-vault", result.ResourceId)
		assert.Equal(tt, "test-account-vendor-id", result.AccountVendorId)
		assert.False(tt, result.Description.IsSet())
		assert.False(tt, result.BackupRegion.IsSet())
		assert.False(tt, result.SourceRegion.IsSet())
	})

	t.Run("WhenSuccessfulDescribeWithImmutableAttributes", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		params := gcpgenserver.V1betaInternalDescribeBackupVaultParams{
			ProjectNumber: "test-project",
			LocationId:    "us-central1",
			BackupVaultId: "test-backup-vault-uuid",
		}

		now := time.Now()
		retentionDuration := int64(86400) // 1 day in seconds
		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{
				UUID:      "test-backup-vault-uuid",
				CreatedAt: now,
				UpdatedAt: now,
			},
			Name:            "test-backup-vault",
			AccountVendorID: "test-account-vendor-id",
			LifeCycleState:  "READY",
			BackupVaultType: "CROSS_REGION",
			ImmutableAttributes: &datamodel.ImmutableAttributes{
				BackupMinimumEnforcedRetentionDuration: &retentionDuration,
				IsDailyBackupImmutable:                 true,
				IsWeeklyBackupImmutable:                true,
				IsMonthlyBackupImmutable:               false,
				IsAdhocBackupImmutable:                 false,
			},
		}

		mockOrchestrator.EXPECT().GetBackupVaultByExternalUUIDAndOwnerID(mock.Anything, "test-backup-vault-uuid", "test-project").Return(backupVault, nil)

		resp, err := handler.V1betaInternalDescribeBackupVault(context.Background(), params)
		assert.NoError(tt, err)

		result, ok := resp.(*gcpgenserver.BackupVaultInternalV1beta)
		assert.True(tt, ok)
		assert.Equal(tt, "test-backup-vault-uuid", result.BackupVaultId)
		assert.True(tt, result.ImmutableAttributes.IsSet())

		immutableAttrs := result.ImmutableAttributes.Value
		assert.True(tt, immutableAttrs.BackupMinimumEnforcedRetentionDuration.IsSet())
		assert.Equal(tt, 86400, immutableAttrs.BackupMinimumEnforcedRetentionDuration.Value)
		assert.True(tt, immutableAttrs.IsDailyBackupImmutable.Value)
		assert.True(tt, immutableAttrs.IsWeeklyBackupImmutable.Value)
		assert.False(tt, immutableAttrs.IsMonthlyBackupImmutable.Value)
		assert.False(tt, immutableAttrs.IsAdhocBackupImmutable.Value)
	})

	t.Run("WhenSuccessfulDescribeWithPartialImmutableAttributes", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		params := gcpgenserver.V1betaInternalDescribeBackupVaultParams{
			ProjectNumber: "test-project",
			LocationId:    "us-central1",
			BackupVaultId: "test-backup-vault-uuid",
		}

		now := time.Now()
		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{
				UUID:      "test-backup-vault-uuid",
				CreatedAt: now,
				UpdatedAt: now,
			},
			Name:            "test-backup-vault",
			AccountVendorID: "test-account-vendor-id",
			LifeCycleState:  "READY",
			BackupVaultType: "CROSS_REGION",
			ImmutableAttributes: &datamodel.ImmutableAttributes{
				IsDailyBackupImmutable:  true,
				IsWeeklyBackupImmutable: false,
			},
		}

		mockOrchestrator.EXPECT().GetBackupVaultByExternalUUIDAndOwnerID(mock.Anything, "test-backup-vault-uuid", "test-project").Return(backupVault, nil)

		resp, err := handler.V1betaInternalDescribeBackupVault(context.Background(), params)
		assert.NoError(tt, err)

		result, ok := resp.(*gcpgenserver.BackupVaultInternalV1beta)
		assert.True(tt, ok)
		assert.True(tt, result.ImmutableAttributes.IsSet())

		immutableAttrs := result.ImmutableAttributes.Value
		assert.False(tt, immutableAttrs.BackupMinimumEnforcedRetentionDuration.IsSet())
		assert.True(tt, immutableAttrs.IsDailyBackupImmutable.Value)
		assert.False(tt, immutableAttrs.IsWeeklyBackupImmutable.Value)
	})

	t.Run("WhenSuccessfulDescribeWithCMEKAttributes", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		params := gcpgenserver.V1betaInternalDescribeBackupVaultParams{
			ProjectNumber: "test-project",
			LocationId:    "us-central1",
			BackupVaultId: "test-backup-vault-uuid",
		}

		now := time.Now()
		kmsConfigPath := "projects/test-project/locations/us-central1/kmsConfigs/myconfig"
		encryptionState := "ENCRYPTION_STATE_COMPLETED"
		backupsPrimaryKeyVersion := "projects/test-project/locations/us-central1/keyRings/test-ring/cryptoKeys/test-key/cryptoKeyVersions/1"
		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{
				UUID:      "test-backup-vault-uuid",
				CreatedAt: now,
				UpdatedAt: now,
			},
			Name:            "test-backup-vault",
			AccountVendorID: "test-account-vendor-id",
			LifeCycleState:  "READY",
			BackupVaultType: "CROSS_REGION",
			CmekAttributes: &datamodel.CmekAttributes{
				KmsConfigResourcePath:    &kmsConfigPath,
				EncryptionState:          &encryptionState,
				BackupsPrimaryKeyVersion: &backupsPrimaryKeyVersion,
			},
		}

		mockOrchestrator.EXPECT().GetBackupVaultByExternalUUIDAndOwnerID(mock.Anything, "test-backup-vault-uuid", "test-project").Return(backupVault, nil)

		resp, err := handler.V1betaInternalDescribeBackupVault(context.Background(), params)
		assert.NoError(tt, err)

		result, ok := resp.(*gcpgenserver.BackupVaultInternalV1beta)
		assert.True(tt, ok)
		assert.Equal(tt, "test-backup-vault-uuid", result.BackupVaultId)
		assert.True(tt, result.KmsConfigResourcePath.IsSet())
		assert.Equal(tt, kmsConfigPath, result.KmsConfigResourcePath.Value)
		assert.True(tt, result.EncryptionState.IsSet())
		assert.Equal(tt, gcpgenserver.BackupVaultInternalV1betaEncryptionStateENCRYPTIONSTATECOMPLETED, result.EncryptionState.Value)
		assert.True(tt, result.BackupsPrimaryKeyVersion.IsSet())
		assert.Equal(tt, backupsPrimaryKeyVersion, result.BackupsPrimaryKeyVersion.Value)
	})

	t.Run("WhenSuccessfulDescribeWithPartialCMEKAttributes", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		params := gcpgenserver.V1betaInternalDescribeBackupVaultParams{
			ProjectNumber: "test-project",
			LocationId:    "us-central1",
			BackupVaultId: "test-backup-vault-uuid",
		}

		now := time.Now()
		kmsConfigPath := "projects/test-project/locations/us-central1/kmsConfigs/myconfig"
		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{
				UUID:      "test-backup-vault-uuid",
				CreatedAt: now,
				UpdatedAt: now,
			},
			Name:            "test-backup-vault",
			AccountVendorID: "test-account-vendor-id",
			LifeCycleState:  "READY",
			BackupVaultType: "CROSS_REGION",
			CmekAttributes: &datamodel.CmekAttributes{
				KmsConfigResourcePath: &kmsConfigPath,
				// EncryptionState and BackupsPrimaryKeyVersion are nil
			},
		}

		mockOrchestrator.EXPECT().GetBackupVaultByExternalUUIDAndOwnerID(mock.Anything, "test-backup-vault-uuid", "test-project").Return(backupVault, nil)

		resp, err := handler.V1betaInternalDescribeBackupVault(context.Background(), params)
		assert.NoError(tt, err)

		result, ok := resp.(*gcpgenserver.BackupVaultInternalV1beta)
		assert.True(tt, ok)
		assert.Equal(tt, "test-backup-vault-uuid", result.BackupVaultId)
		assert.True(tt, result.KmsConfigResourcePath.IsSet())
		assert.Equal(tt, kmsConfigPath, result.KmsConfigResourcePath.Value)
		assert.False(tt, result.EncryptionState.IsSet())
		assert.False(tt, result.BackupsPrimaryKeyVersion.IsSet())
	})

	t.Run("WhenSuccessfulDescribeWithBucketDetails", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		params := gcpgenserver.V1betaInternalDescribeBackupVaultParams{
			ProjectNumber: "test-project",
			LocationId:    "us-central1",
			BackupVaultId: "test-backup-vault-uuid",
		}

		now := time.Now()
		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{
				UUID:      "test-backup-vault-uuid",
				CreatedAt: now,
				UpdatedAt: now,
			},
			Name:            "test-backup-vault",
			AccountVendorID: "test-account-vendor-id",
			LifeCycleState:  "READY",
			BackupVaultType: "CROSS_REGION",
			BucketDetails: []*datamodel.BucketDetails{
				{
					BucketName:          "test-bucket-1",
					ServiceAccountName:  "test-service-account-1",
					VendorSubnetID:      "test-subnet-1",
					TenantProjectNumber: "test-tenant-project-1",
					SatisfiesPzi:        true,
					SatisfiesPzs:        false,
				},
				{
					BucketName:          "test-bucket-2",
					ServiceAccountName:  "test-service-account-2",
					VendorSubnetID:      "test-subnet-2",
					TenantProjectNumber: "test-tenant-project-2",
					SatisfiesPzi:        false,
					SatisfiesPzs:        true,
				},
			},
		}

		mockOrchestrator.EXPECT().GetBackupVaultByExternalUUIDAndOwnerID(mock.Anything, "test-backup-vault-uuid", "test-project").Return(backupVault, nil)

		resp, err := handler.V1betaInternalDescribeBackupVault(context.Background(), params)
		assert.NoError(tt, err)

		result, ok := resp.(*gcpgenserver.BackupVaultInternalV1beta)
		assert.True(tt, ok)
		assert.Equal(tt, "test-backup-vault-uuid", result.BackupVaultId)
		assert.Len(tt, result.BucketDetails, 2)

		// Verify first bucket details
		bucket1 := result.BucketDetails[0]
		assert.Equal(tt, "test-bucket-1", bucket1.BucketName.Value)
		assert.Equal(tt, "test-service-account-1", bucket1.ServiceAccountName.Value)
		assert.Equal(tt, "test-subnet-1", bucket1.VendorSubnetId.Value)
		assert.Equal(tt, "test-tenant-project-1", bucket1.TenantProjectNumber.Value)

		// Verify second bucket details
		bucket2 := result.BucketDetails[1]
		assert.Equal(tt, "test-bucket-2", bucket2.BucketName.Value)
		assert.Equal(tt, "test-service-account-2", bucket2.ServiceAccountName.Value)
		assert.Equal(tt, "test-subnet-2", bucket2.VendorSubnetId.Value)
		assert.Equal(tt, "test-tenant-project-2", bucket2.TenantProjectNumber.Value)
	})

	t.Run("WhenSuccessfulDescribeWithSingleBucketDetail", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		params := gcpgenserver.V1betaInternalDescribeBackupVaultParams{
			ProjectNumber: "test-project",
			LocationId:    "us-central1",
			BackupVaultId: "test-backup-vault-uuid",
		}

		now := time.Now()
		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{
				UUID:      "test-backup-vault-uuid",
				CreatedAt: now,
				UpdatedAt: now,
			},
			Name:            "test-backup-vault",
			AccountVendorID: "test-account-vendor-id",
			LifeCycleState:  "READY",
			BackupVaultType: "CROSS_REGION",
			BucketDetails: []*datamodel.BucketDetails{
				{
					BucketName:          "single-bucket",
					ServiceAccountName:  "single-service-account",
					VendorSubnetID:      "single-subnet",
					TenantProjectNumber: "single-tenant-project",
				},
			},
		}

		mockOrchestrator.EXPECT().GetBackupVaultByExternalUUIDAndOwnerID(mock.Anything, "test-backup-vault-uuid", "test-project").Return(backupVault, nil)

		resp, err := handler.V1betaInternalDescribeBackupVault(context.Background(), params)
		assert.NoError(tt, err)

		result, ok := resp.(*gcpgenserver.BackupVaultInternalV1beta)
		assert.True(tt, ok)
		assert.Len(tt, result.BucketDetails, 1)
		assert.Equal(tt, "single-bucket", result.BucketDetails[0].BucketName.Value)
	})

	t.Run("WhenSuccessfulDescribeWithAllOptionalFields", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		params := gcpgenserver.V1betaInternalDescribeBackupVaultParams{
			ProjectNumber: "test-project",
			LocationId:    "us-central1",
			BackupVaultId: "test-backup-vault-uuid",
		}

		now := time.Now()
		deletedAt := now.Add(time.Hour)
		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{
				UUID:      "test-backup-vault-uuid",
				CreatedAt: now,
				UpdatedAt: now,
				DeletedAt: &gorm.DeletedAt{Time: deletedAt},
			},
			Name:                       "test-backup-vault",
			AccountVendorID:            "test-account-vendor-id",
			LifeCycleState:             "READY",
			BackupVaultType:            "CROSS_REGION",
			Description:                func() *string { s := "Complete test backup vault"; return &s }(),
			BackupRegionName:           func() *string { s := "us-west1"; return &s }(),
			SourceRegionName:           func() *string { s := "us-central1"; return &s }(),
			LifeCycleStateDetails:      "Fully operational with all features",
			CrossRegionBackupVaultName: func() *string { s := "cross-region-backup-vault"; return &s }(),
			ExternalUUID:               func() *string { s := "external-uuid-123"; return &s }(),
		}

		mockOrchestrator.EXPECT().GetBackupVaultByExternalUUIDAndOwnerID(mock.Anything, "test-backup-vault-uuid", "test-project").Return(backupVault, nil)

		resp, err := handler.V1betaInternalDescribeBackupVault(context.Background(), params)
		assert.NoError(tt, err)

		result, ok := resp.(*gcpgenserver.BackupVaultInternalV1beta)
		assert.True(tt, ok)
		assert.Equal(tt, "test-backup-vault-uuid", result.BackupVaultId)
		assert.Equal(tt, "test-backup-vault", result.ResourceId)
		assert.True(tt, result.Description.IsSet())
		assert.Equal(tt, "Complete test backup vault", result.Description.Value)
		assert.True(tt, result.CrossRegionBackupVaultName.IsSet())
		assert.Equal(tt, "cross-region-backup-vault", result.CrossRegionBackupVaultName.Value)
		assert.True(tt, result.ExternalUuid.IsSet())
		assert.Equal(tt, "external-uuid-123", result.ExternalUuid.Value)
		assert.True(tt, result.LifeCycleStateDetails.IsSet())
		assert.Equal(tt, "Fully operational with all features", result.LifeCycleStateDetails.Value)
		assert.True(tt, result.DeletedAt.IsSet())
		assert.Equal(tt, deletedAt, result.DeletedAt.Value)
	})

	t.Run("WhenSuccessfulDescribeWithEmptyLifeCycleStateDetails", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		params := gcpgenserver.V1betaInternalDescribeBackupVaultParams{
			ProjectNumber: "test-project",
			LocationId:    "us-central1",
			BackupVaultId: "test-backup-vault-uuid",
		}

		now := time.Now()
		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{
				UUID:      "test-backup-vault-uuid",
				CreatedAt: now,
				UpdatedAt: now,
			},
			Name:                  "test-backup-vault",
			AccountVendorID:       "test-account-vendor-id",
			LifeCycleState:        "READY",
			BackupVaultType:       "CROSS_REGION",
			LifeCycleStateDetails: "", // Empty string
		}

		mockOrchestrator.EXPECT().GetBackupVaultByExternalUUIDAndOwnerID(mock.Anything, "test-backup-vault-uuid", "test-project").Return(backupVault, nil)

		resp, err := handler.V1betaInternalDescribeBackupVault(context.Background(), params)
		assert.NoError(tt, err)

		result, ok := resp.(*gcpgenserver.BackupVaultInternalV1beta)
		assert.True(tt, ok)
		assert.False(tt, result.LifeCycleStateDetails.IsSet())
	})

	t.Run("WhenSuccessfulDescribeWithCompleteData", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		params := gcpgenserver.V1betaInternalDescribeBackupVaultParams{
			ProjectNumber: "test-project",
			LocationId:    "us-central1",
			BackupVaultId: "test-backup-vault-uuid",
		}

		now := time.Now()
		retentionDuration := int64(172800) // 2 days in seconds
		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{
				UUID:      "test-backup-vault-uuid",
				CreatedAt: now,
				UpdatedAt: now,
			},
			Name:                       "complete-backup-vault",
			AccountVendorID:            "account-123",
			LifeCycleState:             "READY",
			BackupVaultType:            "CROSS_REGION",
			Description:                func() *string { s := "Complete backup vault with all features"; return &s }(),
			BackupRegionName:           func() *string { s := "us-east1"; return &s }(),
			SourceRegionName:           func() *string { s := "us-west2"; return &s }(),
			LifeCycleStateDetails:      "All systems operational",
			CrossRegionBackupVaultName: func() *string { s := "cr-backup-vault"; return &s }(),
			ExternalUUID:               func() *string { s := "ext-uuid-456"; return &s }(),
			ImmutableAttributes: &datamodel.ImmutableAttributes{
				BackupMinimumEnforcedRetentionDuration: &retentionDuration,
				IsDailyBackupImmutable:                 true,
				IsWeeklyBackupImmutable:                true,
				IsMonthlyBackupImmutable:               true,
				IsAdhocBackupImmutable:                 false,
			},
			BucketDetails: []*datamodel.BucketDetails{
				{
					BucketName:          "primary-bucket",
					ServiceAccountName:  "primary-sa",
					VendorSubnetID:      "subnet-primary",
					TenantProjectNumber: "tenant-123",
				},
			},
		}

		mockOrchestrator.EXPECT().GetBackupVaultByExternalUUIDAndOwnerID(mock.Anything, "test-backup-vault-uuid", "test-project").Return(backupVault, nil)

		resp, err := handler.V1betaInternalDescribeBackupVault(context.Background(), params)
		assert.NoError(tt, err)

		result, ok := resp.(*gcpgenserver.BackupVaultInternalV1beta)
		assert.True(tt, ok)
		assert.Equal(tt, "test-backup-vault-uuid", result.BackupVaultId)
		assert.Equal(tt, "complete-backup-vault", result.ResourceId)
		assert.Equal(tt, "account-123", result.AccountVendorId)
		assert.True(tt, result.ImmutableAttributes.IsSet())
		assert.Len(tt, result.BucketDetails, 1)
		assert.True(tt, result.CrossRegionBackupVaultName.IsSet())
		assert.True(tt, result.ExternalUuid.IsSet())

		// Verify immutable attributes
		immutableAttrs := result.ImmutableAttributes.Value
		assert.Equal(tt, 172800, immutableAttrs.BackupMinimumEnforcedRetentionDuration.Value)
		assert.True(tt, immutableAttrs.IsMonthlyBackupImmutable.Value)
	})
}

func TestV1betaInternalDeleteBackupVault(t *testing.T) {
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()

	t.Run("WhenParsingErrorInvalidLocation", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		params := gcpgenserver.V1betaInternalDeleteBackupVaultParams{
			ProjectNumber: "test-project",
			LocationId:    "invalid-location-format",
			BackupVaultId: "test-backup-vault-id",
		}

		resp, err := handler.V1betaInternalDeleteBackupVault(context.Background(), params)
		assert.NoError(tt, err)

		result, ok := resp.(*gcpgenserver.V1betaInternalDeleteBackupVaultBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, float64(400), result.Code)
	})

	t.Run("WhenValidationErrorFromOrchestrator", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		params := gcpgenserver.V1betaInternalDeleteBackupVaultParams{
			ProjectNumber: "test-project",
			LocationId:    "us-central1",
			BackupVaultId: "test-backup-vault-id",
		}

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-central1", "", nil
		}
		defer func() {
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()

		validationErr := errors.NewUserInputValidationErr("BackupVault validation failed")
		mockOrchestrator.EXPECT().DeleteBackupVaultInternal(mock.Anything, mock.Anything).Return("", validationErr)

		resp, err := handler.V1betaInternalDeleteBackupVault(context.Background(), params)
		assert.NoError(tt, err)

		result, ok := resp.(*gcpgenserver.V1betaInternalDeleteBackupVaultBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, float64(400), result.Code)
		assert.Contains(tt, result.Message, "BackupVault validation failed")
	})

	t.Run("WhenInternalServerErrorFromOrchestrator", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		params := gcpgenserver.V1betaInternalDeleteBackupVaultParams{
			ProjectNumber: "test-project",
			LocationId:    "us-central1",
			BackupVaultId: "test-backup-vault-id",
		}

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-central1", "", nil
		}
		defer func() {
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()

		internalErr := errors.New("internal database error")
		mockOrchestrator.EXPECT().DeleteBackupVaultInternal(mock.Anything, mock.Anything).Return("", internalErr)

		resp, err := handler.V1betaInternalDeleteBackupVault(context.Background(), params)
		assert.NoError(tt, err)

		result, ok := resp.(*gcpgenserver.V1betaInternalDeleteBackupVaultInternalServerError)
		assert.True(tt, ok)
		assert.Equal(tt, float64(500), result.Code)
		assert.Equal(tt, "Internal server error while deleting backup vault", result.Message)
	})

	t.Run("WhenSuccessfulDeleteWithOperation", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		params := gcpgenserver.V1betaInternalDeleteBackupVaultParams{
			ProjectNumber: "test-project",
			LocationId:    "us-central1",
			BackupVaultId: "test-backup-vault-id",
		}

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-central1", "", nil
		}
		defer func() {
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()

		operationID := "operation-12345"
		mockOrchestrator.EXPECT().DeleteBackupVaultInternal(mock.Anything, mock.Anything).Return(operationID, nil)

		resp, err := handler.V1betaInternalDeleteBackupVault(context.Background(), params)
		assert.NoError(tt, err)

		result, ok := resp.(*gcpgenserver.OperationV1beta)
		assert.True(tt, ok)
		assert.True(tt, result.Name.IsSet())
		assert.Equal(tt, fmt.Sprintf("/v1beta/projects/%s/locations/%s/operations/%s", params.ProjectNumber, params.LocationId, operationID), result.Name.Value)
		assert.True(tt, result.Done.IsSet())
		assert.False(tt, result.Done.Value)
	})

	t.Run("WhenSuccessfulDeleteWithoutOperation", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		params := gcpgenserver.V1betaInternalDeleteBackupVaultParams{
			ProjectNumber: "test-project",
			LocationId:    "us-central1",
			BackupVaultId: "test-backup-vault-id",
		}

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-central1", "", nil
		}
		defer func() {
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()

		mockOrchestrator.EXPECT().DeleteBackupVaultInternal(mock.Anything, mock.Anything).Return("", nil)

		resp, err := handler.V1betaInternalDeleteBackupVault(context.Background(), params)
		assert.NoError(tt, err)

		result, ok := resp.(*gcpgenserver.OperationV1beta)
		assert.True(tt, ok)
		assert.False(tt, result.Name.IsSet())
		assert.False(tt, result.Done.IsSet())
	})

	t.Run("WhenIdempotentDeleteOnDestinationRegion_ReturnsCompletedOperationNot500", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		params := gcpgenserver.V1betaInternalDeleteBackupVaultParams{
			ProjectNumber: "123456789012",
			LocationId:    "us-west1",
			BackupVaultId: "external-backup-vault-uuid",
		}

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-west1", "", nil
		}
		defer func() {
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()

		mockOrchestrator.EXPECT().DeleteBackupVaultInternal(mock.Anything, mock.MatchedBy(func(p *commonparams.BackupVaultParams) bool {
			return p != nil &&
				p.BackupVaultID == params.BackupVaultId &&
				p.OwnerID == params.ProjectNumber &&
				p.Region == params.LocationId
		})).Return("", nil)

		resp, err := handler.V1betaInternalDeleteBackupVault(context.Background(), params)
		assert.NoError(tt, err)

		_, isInternalServerError := resp.(*gcpgenserver.V1betaInternalDeleteBackupVaultInternalServerError)
		assert.False(tt, isInternalServerError)

		result, ok := resp.(*gcpgenserver.OperationV1beta)
		assert.True(tt, ok)
		assert.False(tt, result.Name.IsSet())
		assert.False(tt, result.Done.IsSet())
	})
}

func TestV1betaInternalUpdateBackupVault(t *testing.T) {
	t.Run("WhenRequestBodyIsNil", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		params := gcpgenserver.V1betaInternalUpdateBackupVaultParams{
			ProjectNumber: "test-project",
			LocationId:    "us-central1",
			BackupVaultId: "test-backup-vault-id",
		}

		resp, err := handler.V1betaInternalUpdateBackupVault(context.Background(), nil, params)
		assert.Error(tt, err)
		assert.Equal(tt, "request body is required", err.Error())

		result, ok := resp.(*gcpgenserver.V1betaInternalUpdateBackupVaultBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, float64(400), result.Code)
		assert.Equal(tt, "Request body is required", result.Message)
	})

	t.Run("WhenBackupVaultIdIsEmpty", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		req := &gcpgenserver.BackupVaultInternalUpdateV1beta{}
		params := gcpgenserver.V1betaInternalUpdateBackupVaultParams{
			ProjectNumber: "test-project",
			LocationId:    "us-central1",
			BackupVaultId: "",
		}

		resp, err := handler.V1betaInternalUpdateBackupVault(context.Background(), req, params)
		assert.Error(tt, err)
		assert.Equal(tt, "backupVaultId is required", err.Error())

		result, ok := resp.(*gcpgenserver.V1betaInternalUpdateBackupVaultBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, float64(400), result.Code)
		assert.Equal(tt, "BackupVaultId is required", result.Message)
	})

	t.Run("WhenProjectNumberIsEmpty", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		req := &gcpgenserver.BackupVaultInternalUpdateV1beta{}
		params := gcpgenserver.V1betaInternalUpdateBackupVaultParams{
			ProjectNumber: "",
			LocationId:    "us-central1",
			BackupVaultId: "test-backup-vault-id",
		}

		resp, err := handler.V1betaInternalUpdateBackupVault(context.Background(), req, params)
		assert.Error(tt, err)
		assert.Equal(tt, "projectNumber is required", err.Error())

		result, ok := resp.(*gcpgenserver.V1betaInternalUpdateBackupVaultBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, float64(400), result.Code)
		assert.Equal(tt, "ProjectNumber is required", result.Message)
	})

	t.Run("WhenValidationErrorFromOrchestrator", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		req := &gcpgenserver.BackupVaultInternalUpdateV1beta{
			Description: gcpgenserver.NewOptString("Updated description"),
		}
		params := gcpgenserver.V1betaInternalUpdateBackupVaultParams{
			ProjectNumber: "test-project",
			LocationId:    "us-central1",
			BackupVaultId: "test-backup-vault-id",
		}

		validationErr := errors.NewUserInputValidationErr("Invalid update parameters")
		mockOrchestrator.EXPECT().UpdateBackupVaultInternal(mock.Anything, mock.MatchedBy(func(param *commonparams.BackupVaultParams) bool {
			return param.BackupVaultID == "test-backup-vault-id" &&
				param.AccountName == "test-project" &&
				param.OwnerID == "test-project" &&
				param.Region == "us-central1" &&
				param.Description != nil &&
				*param.Description == "Updated description"
		}), true).Return(nil, "", validationErr)

		resp, err := handler.V1betaInternalUpdateBackupVault(context.Background(), req, params)
		assert.NoError(tt, err)

		result, ok := resp.(*gcpgenserver.V1betaInternalUpdateBackupVaultBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, float64(400), result.Code)
		assert.Contains(tt, result.Message, "Invalid update parameters")
	})

	t.Run("WhenBackupVaultNotFound", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		req := &gcpgenserver.BackupVaultInternalUpdateV1beta{
			Description: gcpgenserver.NewOptString("Updated description"),
		}
		params := gcpgenserver.V1betaInternalUpdateBackupVaultParams{
			ProjectNumber: "test-project",
			LocationId:    "us-central1",
			BackupVaultId: "non-existent-id",
		}

		notFoundErr := errors.NewNotFoundErr("BackupVault", nil)
		mockOrchestrator.EXPECT().UpdateBackupVaultInternal(mock.Anything, mock.Anything, true).Return(nil, "", notFoundErr)

		resp, err := handler.V1betaInternalUpdateBackupVault(context.Background(), req, params)
		assert.NoError(tt, err)

		result, ok := resp.(*gcpgenserver.V1betaInternalUpdateBackupVaultNotFound)
		assert.True(tt, ok)
		assert.Equal(tt, float64(404), result.Code)
		assert.Equal(tt, "BackupVault not found", result.Message)
	})

	t.Run("WhenConflictErrorFromOrchestrator", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		req := &gcpgenserver.BackupVaultInternalUpdateV1beta{
			Description: gcpgenserver.NewOptString("Updated description"),
		}
		params := gcpgenserver.V1betaInternalUpdateBackupVaultParams{
			ProjectNumber: "test-project",
			LocationId:    "us-central1",
			BackupVaultId: "test-backup-vault-id",
		}

		conflictErr := errors.NewConflictErr("BackupVault update conflict")
		mockOrchestrator.EXPECT().UpdateBackupVaultInternal(mock.Anything, mock.Anything, true).Return(nil, "", conflictErr)

		resp, err := handler.V1betaInternalUpdateBackupVault(context.Background(), req, params)
		assert.NoError(tt, err)

		result, ok := resp.(*gcpgenserver.V1betaInternalUpdateBackupVaultConflict)
		assert.True(tt, ok)
		assert.Equal(tt, float64(409), result.Code)
		assert.Contains(tt, result.Message, "BackupVault update conflict")
	})

	t.Run("WhenInternalServerErrorFromOrchestrator", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		req := &gcpgenserver.BackupVaultInternalUpdateV1beta{
			Description: gcpgenserver.NewOptString("Updated description"),
		}
		params := gcpgenserver.V1betaInternalUpdateBackupVaultParams{
			ProjectNumber: "test-project",
			LocationId:    "us-central1",
			BackupVaultId: "test-backup-vault-id",
		}

		internalErr := errors.New("database connection error")
		mockOrchestrator.EXPECT().UpdateBackupVaultInternal(mock.Anything, mock.Anything, true).Return(nil, "", internalErr)

		resp, err := handler.V1betaInternalUpdateBackupVault(context.Background(), req, params)
		assert.Error(tt, err)

		result, ok := resp.(*gcpgenserver.V1betaInternalUpdateBackupVaultInternalServerError)
		assert.True(tt, ok)
		assert.Equal(tt, float64(500), result.Code)
		assert.Equal(tt, "Failed to update BackupVault in VCP database", result.Message)
	})

	t.Run("WhenSuccessfulUpdateWithDescription", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		req := &gcpgenserver.BackupVaultInternalUpdateV1beta{
			Description: gcpgenserver.NewOptString("Updated description"),
		}
		params := gcpgenserver.V1betaInternalUpdateBackupVaultParams{
			ProjectNumber: "test-project",
			LocationId:    "us-central1",
			BackupVaultId: "test-backup-vault-id",
		}

		operationID := "operation-12345"
		description := "Updated description"
		updatedBackupVault := &models.BackupVaultV1beta{
			BackupVaultID: "test-backup-vault-id",
			Name:          "test-backup-vault",
			Description:   &description,
		}

		mockOrchestrator.EXPECT().UpdateBackupVaultInternal(mock.Anything, mock.Anything, true).Return(updatedBackupVault, operationID, nil)

		resp, err := handler.V1betaInternalUpdateBackupVault(context.Background(), req, params)
		assert.NoError(tt, err)

		result, ok := resp.(*gcpgenserver.OperationV1beta)
		assert.True(tt, ok)
		assert.True(tt, result.Name.IsSet())
		assert.Equal(tt, fmt.Sprintf("/v1beta/projects/%s/locations/%s/operations/%s", params.ProjectNumber, params.LocationId, operationID), result.Name.Value)
		assert.True(tt, result.Done.IsSet())
		assert.False(tt, result.Done.Value)
		assert.NotNil(tt, result.Response)
	})

	t.Run("WhenSuccessfulUpdateWithBackupRetentionPolicy", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		req := &gcpgenserver.BackupVaultInternalUpdateV1beta{
			BackupRetentionPolicy: gcpgenserver.NewOptBackupRetentionPolicyUpdateV1beta(
				gcpgenserver.BackupRetentionPolicyUpdateV1beta{
					BackupMinimumEnforcedRetentionDays: gcpgenserver.NewOptInt(30),
					DailyBackupImmutable:               gcpgenserver.NewOptBool(true),
					WeeklyBackupImmutable:              gcpgenserver.NewOptBool(true),
					MonthlyBackupImmutable:             gcpgenserver.NewOptBool(false),
					ManualBackupImmutable:              gcpgenserver.NewOptBool(false),
				},
			),
		}
		params := gcpgenserver.V1betaInternalUpdateBackupVaultParams{
			ProjectNumber: "test-project",
			LocationId:    "us-central1",
			BackupVaultId: "test-backup-vault-id",
		}

		operationID := "operation-67890"
		updatedBackupVault := &models.BackupVaultV1beta{
			BackupVaultID: "test-backup-vault-id",
			Name:          "test-backup-vault",
		}

		mockOrchestrator.EXPECT().UpdateBackupVaultInternal(mock.Anything, mock.MatchedBy(func(param *commonparams.BackupVaultParams) bool {
			return param.BackupVaultID == "test-backup-vault-id" &&
				param.BackupRetentionPolicy.BackupMinimumEnforcedRetentionDuration != nil &&
				*param.BackupRetentionPolicy.BackupMinimumEnforcedRetentionDuration == int64(30) &&
				param.BackupRetentionPolicy.IsDailyBackupImmutable != nil &&
				*param.BackupRetentionPolicy.IsDailyBackupImmutable == true &&
				param.BackupRetentionPolicy.IsWeeklyBackupImmutable != nil &&
				*param.BackupRetentionPolicy.IsWeeklyBackupImmutable == true &&
				param.BackupRetentionPolicy.IsMonthlyBackupImmutable != nil &&
				*param.BackupRetentionPolicy.IsMonthlyBackupImmutable == false &&
				param.BackupRetentionPolicy.IsAdhocBackupImmutable != nil &&
				*param.BackupRetentionPolicy.IsAdhocBackupImmutable == false
		}), true).Return(updatedBackupVault, operationID, nil)

		resp, err := handler.V1betaInternalUpdateBackupVault(context.Background(), req, params)
		assert.NoError(tt, err)

		result, ok := resp.(*gcpgenserver.OperationV1beta)
		assert.True(tt, ok)
		assert.True(tt, result.Name.IsSet())
		assert.Equal(tt, fmt.Sprintf("/v1beta/projects/%s/locations/%s/operations/%s", params.ProjectNumber, params.LocationId, operationID), result.Name.Value)
	})

	t.Run("WhenSuccessfulUpdateWithPartialBackupRetentionPolicy", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		req := &gcpgenserver.BackupVaultInternalUpdateV1beta{
			BackupRetentionPolicy: gcpgenserver.NewOptBackupRetentionPolicyUpdateV1beta(
				gcpgenserver.BackupRetentionPolicyUpdateV1beta{
					DailyBackupImmutable: gcpgenserver.NewOptBool(true),
				},
			),
		}
		params := gcpgenserver.V1betaInternalUpdateBackupVaultParams{
			ProjectNumber: "test-project",
			LocationId:    "us-central1",
			BackupVaultId: "test-backup-vault-id",
		}

		operationID := ""
		updatedBackupVault := &models.BackupVaultV1beta{
			BackupVaultID: "test-backup-vault-id",
			Name:          "test-backup-vault",
		}

		mockOrchestrator.EXPECT().UpdateBackupVaultInternal(mock.Anything, mock.MatchedBy(func(param *commonparams.BackupVaultParams) bool {
			return param.BackupVaultID == "test-backup-vault-id" &&
				param.Description == nil &&
				param.BackupRetentionPolicy.BackupMinimumEnforcedRetentionDuration == nil &&
				param.BackupRetentionPolicy.IsDailyBackupImmutable != nil &&
				*param.BackupRetentionPolicy.IsDailyBackupImmutable == true
		}), true).Return(updatedBackupVault, operationID, nil)

		resp, err := handler.V1betaInternalUpdateBackupVault(context.Background(), req, params)
		assert.NoError(tt, err)

		result, ok := resp.(*gcpgenserver.OperationV1beta)
		assert.True(tt, ok)
		assert.False(tt, result.Name.IsSet())
		assert.True(tt, result.Done.IsSet())
		assert.True(tt, result.Done.Value)
		assert.NotNil(tt, result.Response)
	})

	t.Run("WhenSuccessfulUpdateWithDescriptionAndRetentionPolicy", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		req := &gcpgenserver.BackupVaultInternalUpdateV1beta{
			Description: gcpgenserver.NewOptString("Complete update"),
			BackupRetentionPolicy: gcpgenserver.NewOptBackupRetentionPolicyUpdateV1beta(
				gcpgenserver.BackupRetentionPolicyUpdateV1beta{
					BackupMinimumEnforcedRetentionDays: gcpgenserver.NewOptInt(45),
					DailyBackupImmutable:               gcpgenserver.NewOptBool(true),
					WeeklyBackupImmutable:              gcpgenserver.NewOptBool(true),
					MonthlyBackupImmutable:             gcpgenserver.NewOptBool(true),
					ManualBackupImmutable:              gcpgenserver.NewOptBool(true),
				},
			),
		}
		params := gcpgenserver.V1betaInternalUpdateBackupVaultParams{
			ProjectNumber: "test-project",
			LocationId:    "us-central1",
			BackupVaultId: "test-backup-vault-id",
		}

		operationID := "operation-complete-123"
		description := "Complete update"
		updatedBackupVault := &models.BackupVaultV1beta{
			BackupVaultID: "test-backup-vault-id",
			Name:          "test-backup-vault",
			Description:   &description,
		}

		mockOrchestrator.EXPECT().UpdateBackupVaultInternal(mock.Anything, mock.MatchedBy(func(param *commonparams.BackupVaultParams) bool {
			return param.BackupVaultID == "test-backup-vault-id" &&
				param.Description != nil &&
				*param.Description == "Complete update" &&
				param.BackupRetentionPolicy.BackupMinimumEnforcedRetentionDuration != nil &&
				*param.BackupRetentionPolicy.BackupMinimumEnforcedRetentionDuration == int64(45) &&
				param.BackupRetentionPolicy.IsDailyBackupImmutable != nil &&
				*param.BackupRetentionPolicy.IsDailyBackupImmutable == true &&
				param.BackupRetentionPolicy.IsMonthlyBackupImmutable != nil &&
				*param.BackupRetentionPolicy.IsMonthlyBackupImmutable == true &&
				param.BackupRetentionPolicy.IsAdhocBackupImmutable != nil &&
				*param.BackupRetentionPolicy.IsAdhocBackupImmutable == true
		}), true).Return(updatedBackupVault, operationID, nil)

		resp, err := handler.V1betaInternalUpdateBackupVault(context.Background(), req, params)
		assert.NoError(tt, err)

		result, ok := resp.(*gcpgenserver.OperationV1beta)
		assert.True(tt, ok)
		assert.True(tt, result.Name.IsSet())
		assert.Equal(tt, fmt.Sprintf("/v1beta/projects/%s/locations/%s/operations/%s", params.ProjectNumber, params.LocationId, operationID), result.Name.Value)
		assert.True(tt, result.Done.IsSet())
		assert.False(tt, result.Done.Value)
		assert.NotNil(tt, result.Response)
	})

	t.Run("WhenSuccessfulUpdateWithoutOperation", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		req := &gcpgenserver.BackupVaultInternalUpdateV1beta{
			Description: gcpgenserver.NewOptString("Sync update"),
		}
		params := gcpgenserver.V1betaInternalUpdateBackupVaultParams{
			ProjectNumber: "test-project",
			LocationId:    "us-central1",
			BackupVaultId: "test-backup-vault-id",
		}

		syncDescription := "Sync update"
		updatedBackupVault := &models.BackupVaultV1beta{
			BackupVaultID: "test-backup-vault-id",
			Name:          "test-backup-vault",
			Description:   &syncDescription,
		}

		mockOrchestrator.EXPECT().UpdateBackupVaultInternal(mock.Anything, mock.Anything, true).Return(updatedBackupVault, "", nil)

		resp, err := handler.V1betaInternalUpdateBackupVault(context.Background(), req, params)
		assert.NoError(tt, err)

		result, ok := resp.(*gcpgenserver.OperationV1beta)
		assert.True(tt, ok)
		assert.False(tt, result.Name.IsSet())
		assert.True(tt, result.Done.IsSet())
		assert.True(tt, result.Done.Value)
		assert.NotNil(tt, result.Response)
	})

	t.Run("WhenBackupRetentionPolicyIsSetButEmpty", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		req := &gcpgenserver.BackupVaultInternalUpdateV1beta{
			BackupRetentionPolicy: gcpgenserver.NewOptBackupRetentionPolicyUpdateV1beta(
				gcpgenserver.BackupRetentionPolicyUpdateV1beta{},
			),
		}
		params := gcpgenserver.V1betaInternalUpdateBackupVaultParams{
			ProjectNumber: "test-project",
			LocationId:    "us-central1",
			BackupVaultId: "test-backup-vault-id",
		}

		operationID := ""
		updatedBackupVault := &models.BackupVaultV1beta{
			BackupVaultID: "test-backup-vault-id",
			Name:          "test-backup-vault",
		}

		mockOrchestrator.EXPECT().UpdateBackupVaultInternal(mock.Anything, mock.MatchedBy(func(param *commonparams.BackupVaultParams) bool {
			return param.BackupVaultID == "test-backup-vault-id" &&
				param.BackupRetentionPolicy.BackupMinimumEnforcedRetentionDuration == nil &&
				param.BackupRetentionPolicy.IsDailyBackupImmutable == nil &&
				param.BackupRetentionPolicy.IsWeeklyBackupImmutable == nil &&
				param.BackupRetentionPolicy.IsMonthlyBackupImmutable == nil &&
				param.BackupRetentionPolicy.IsAdhocBackupImmutable == nil
		}), true).Return(updatedBackupVault, operationID, nil)

		resp, err := handler.V1betaInternalUpdateBackupVault(context.Background(), req, params)
		assert.NoError(tt, err)

		result, ok := resp.(*gcpgenserver.OperationV1beta)
		assert.True(tt, ok)
		assert.False(tt, result.Name.IsSet())
		assert.True(tt, result.Done.IsSet())
		assert.True(tt, result.Done.Value)
		assert.NotNil(tt, result.Response)
	})

	t.Run("WhenOnlyBackupMinimumEnforcedRetentionDaysIsSet", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		req := &gcpgenserver.BackupVaultInternalUpdateV1beta{
			BackupRetentionPolicy: gcpgenserver.NewOptBackupRetentionPolicyUpdateV1beta(
				gcpgenserver.BackupRetentionPolicyUpdateV1beta{
					BackupMinimumEnforcedRetentionDays: gcpgenserver.NewOptInt(60),
				},
			),
		}
		params := gcpgenserver.V1betaInternalUpdateBackupVaultParams{
			ProjectNumber: "test-project",
			LocationId:    "us-central1",
			BackupVaultId: "test-backup-vault-id",
		}

		operationID := ""
		updatedBackupVault := &models.BackupVaultV1beta{
			BackupVaultID: "test-backup-vault-id",
			Name:          "test-backup-vault",
		}

		mockOrchestrator.EXPECT().UpdateBackupVaultInternal(mock.Anything, mock.MatchedBy(func(param *commonparams.BackupVaultParams) bool {
			return param.BackupVaultID == "test-backup-vault-id" &&
				param.BackupRetentionPolicy.BackupMinimumEnforcedRetentionDuration != nil &&
				*param.BackupRetentionPolicy.BackupMinimumEnforcedRetentionDuration == int64(60) &&
				param.BackupRetentionPolicy.IsDailyBackupImmutable == nil &&
				param.BackupRetentionPolicy.IsWeeklyBackupImmutable == nil &&
				param.BackupRetentionPolicy.IsMonthlyBackupImmutable == nil &&
				param.BackupRetentionPolicy.IsAdhocBackupImmutable == nil
		}), true).Return(updatedBackupVault, operationID, nil)

		resp, err := handler.V1betaInternalUpdateBackupVault(context.Background(), req, params)
		assert.NoError(tt, err)

		result, ok := resp.(*gcpgenserver.OperationV1beta)
		assert.True(tt, ok)
		assert.False(tt, result.Name.IsSet())
		assert.True(tt, result.Done.IsSet())
		assert.True(tt, result.Done.Value)
		assert.NotNil(tt, result.Response)
	})

	t.Run("WhenOnlyWeeklyBackupImmutableIsSet", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		req := &gcpgenserver.BackupVaultInternalUpdateV1beta{
			BackupRetentionPolicy: gcpgenserver.NewOptBackupRetentionPolicyUpdateV1beta(
				gcpgenserver.BackupRetentionPolicyUpdateV1beta{
					WeeklyBackupImmutable: gcpgenserver.NewOptBool(true),
				},
			),
		}
		params := gcpgenserver.V1betaInternalUpdateBackupVaultParams{
			ProjectNumber: "test-project",
			LocationId:    "us-central1",
			BackupVaultId: "test-backup-vault-id",
		}

		operationID := ""
		updatedBackupVault := &models.BackupVaultV1beta{
			BackupVaultID: "test-backup-vault-id",
			Name:          "test-backup-vault",
		}

		mockOrchestrator.EXPECT().UpdateBackupVaultInternal(mock.Anything, mock.MatchedBy(func(param *commonparams.BackupVaultParams) bool {
			return param.BackupVaultID == "test-backup-vault-id" &&
				param.BackupRetentionPolicy.BackupMinimumEnforcedRetentionDuration == nil &&
				param.BackupRetentionPolicy.IsDailyBackupImmutable == nil &&
				param.BackupRetentionPolicy.IsWeeklyBackupImmutable != nil &&
				*param.BackupRetentionPolicy.IsWeeklyBackupImmutable == true &&
				param.BackupRetentionPolicy.IsMonthlyBackupImmutable == nil &&
				param.BackupRetentionPolicy.IsAdhocBackupImmutable == nil
		}), true).Return(updatedBackupVault, operationID, nil)

		resp, err := handler.V1betaInternalUpdateBackupVault(context.Background(), req, params)
		assert.NoError(tt, err)

		result, ok := resp.(*gcpgenserver.OperationV1beta)
		assert.True(tt, ok)
		assert.False(tt, result.Name.IsSet())
		assert.True(tt, result.Done.IsSet())
		assert.True(tt, result.Done.Value)
		assert.NotNil(tt, result.Response)
	})

	t.Run("WhenOnlyMonthlyBackupImmutableIsSet", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		req := &gcpgenserver.BackupVaultInternalUpdateV1beta{
			BackupRetentionPolicy: gcpgenserver.NewOptBackupRetentionPolicyUpdateV1beta(
				gcpgenserver.BackupRetentionPolicyUpdateV1beta{
					MonthlyBackupImmutable: gcpgenserver.NewOptBool(false),
				},
			),
		}
		params := gcpgenserver.V1betaInternalUpdateBackupVaultParams{
			ProjectNumber: "test-project",
			LocationId:    "us-central1",
			BackupVaultId: "test-backup-vault-id",
		}

		operationID := ""
		updatedBackupVault := &models.BackupVaultV1beta{
			BackupVaultID: "test-backup-vault-id",
			Name:          "test-backup-vault",
		}

		mockOrchestrator.EXPECT().UpdateBackupVaultInternal(mock.Anything, mock.MatchedBy(func(param *commonparams.BackupVaultParams) bool {
			return param.BackupVaultID == "test-backup-vault-id" &&
				param.BackupRetentionPolicy.BackupMinimumEnforcedRetentionDuration == nil &&
				param.BackupRetentionPolicy.IsDailyBackupImmutable == nil &&
				param.BackupRetentionPolicy.IsWeeklyBackupImmutable == nil &&
				param.BackupRetentionPolicy.IsMonthlyBackupImmutable != nil &&
				*param.BackupRetentionPolicy.IsMonthlyBackupImmutable == false &&
				param.BackupRetentionPolicy.IsAdhocBackupImmutable == nil
		}), true).Return(updatedBackupVault, operationID, nil)

		resp, err := handler.V1betaInternalUpdateBackupVault(context.Background(), req, params)
		assert.NoError(tt, err)

		result, ok := resp.(*gcpgenserver.OperationV1beta)
		assert.True(tt, ok)
		assert.False(tt, result.Name.IsSet())
		assert.True(tt, result.Done.IsSet())
		assert.True(tt, result.Done.Value)
		assert.NotNil(tt, result.Response)
	})

	t.Run("WhenOnlyManualBackupImmutableIsSet", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		req := &gcpgenserver.BackupVaultInternalUpdateV1beta{
			BackupRetentionPolicy: gcpgenserver.NewOptBackupRetentionPolicyUpdateV1beta(
				gcpgenserver.BackupRetentionPolicyUpdateV1beta{
					ManualBackupImmutable: gcpgenserver.NewOptBool(true),
				},
			),
		}
		params := gcpgenserver.V1betaInternalUpdateBackupVaultParams{
			ProjectNumber: "test-project",
			LocationId:    "us-central1",
			BackupVaultId: "test-backup-vault-id",
		}

		operationID := ""
		updatedBackupVault := &models.BackupVaultV1beta{
			BackupVaultID: "test-backup-vault-id",
			Name:          "test-backup-vault",
		}

		mockOrchestrator.EXPECT().UpdateBackupVaultInternal(mock.Anything, mock.MatchedBy(func(param *commonparams.BackupVaultParams) bool {
			return param.BackupVaultID == "test-backup-vault-id" &&
				param.BackupRetentionPolicy.BackupMinimumEnforcedRetentionDuration == nil &&
				param.BackupRetentionPolicy.IsDailyBackupImmutable == nil &&
				param.BackupRetentionPolicy.IsWeeklyBackupImmutable == nil &&
				param.BackupRetentionPolicy.IsMonthlyBackupImmutable == nil &&
				param.BackupRetentionPolicy.IsAdhocBackupImmutable != nil &&
				*param.BackupRetentionPolicy.IsAdhocBackupImmutable == true
		}), true).Return(updatedBackupVault, operationID, nil)

		resp, err := handler.V1betaInternalUpdateBackupVault(context.Background(), req, params)
		assert.NoError(tt, err)

		result, ok := resp.(*gcpgenserver.OperationV1beta)
		assert.True(tt, ok)
		assert.False(tt, result.Name.IsSet())
		assert.True(tt, result.Done.IsSet())
		assert.True(tt, result.Done.Value)
		assert.NotNil(tt, result.Response)
	})

	t.Run("WhenSuccessfulUpdateWithBucketDetailsAllFields", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		req := &gcpgenserver.BackupVaultInternalUpdateV1beta{
			BucketDetails: []gcpgenserver.BackupVaultInternalUpdateV1betaBucketDetailsItem{
				{
					BucketName:          gcpgenserver.NewOptString("test-bucket-1"),
					ServiceAccountName:  gcpgenserver.NewOptString("test-sa-1"),
					VendorSubnetId:      gcpgenserver.NewOptString("subnet-123"),
					TenantProjectNumber: gcpgenserver.NewOptString("project-456"),
					SatisfiesPzs:        gcpgenserver.NewOptBool(true),
					SatisfiesPzi:        gcpgenserver.NewOptBool(true),
				},
			},
		}
		params := gcpgenserver.V1betaInternalUpdateBackupVaultParams{
			ProjectNumber: "test-project",
			LocationId:    "us-central1",
			BackupVaultId: "test-backup-vault-id",
		}

		operationID := "operation-bucket-123"
		updatedBackupVault := &models.BackupVaultV1beta{
			BackupVaultID: "test-backup-vault-id",
			Name:          "test-backup-vault",
		}

		mockOrchestrator.EXPECT().UpdateBackupVaultInternal(mock.Anything, mock.MatchedBy(func(param *commonparams.BackupVaultParams) bool {
			if param.BucketDetails == nil || len(param.BucketDetails) != 1 {
				return false
			}
			bucket := param.BucketDetails[0]
			return bucket.BucketName == "test-bucket-1" &&
				bucket.ServiceAccountName == "test-sa-1" &&
				bucket.VendorSubnetID == "subnet-123" &&
				bucket.TenantProjectNumber == "project-456" &&
				bucket.SatisfiesPzi == true &&
				bucket.SatisfiesPzs == true
		}), true).Return(updatedBackupVault, operationID, nil)

		resp, err := handler.V1betaInternalUpdateBackupVault(context.Background(), req, params)
		assert.NoError(tt, err)

		result, ok := resp.(*gcpgenserver.OperationV1beta)
		assert.True(tt, ok)
		assert.True(tt, result.Name.IsSet())
		assert.Equal(tt, fmt.Sprintf("/v1beta/projects/%s/locations/%s/operations/%s", params.ProjectNumber, params.LocationId, operationID), result.Name.Value)
	})

	t.Run("WhenSuccessfulUpdateWithBucketDetailsPartialFields", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		req := &gcpgenserver.BackupVaultInternalUpdateV1beta{
			BucketDetails: []gcpgenserver.BackupVaultInternalUpdateV1betaBucketDetailsItem{
				{
					BucketName:         gcpgenserver.NewOptString("test-bucket-2"),
					ServiceAccountName: gcpgenserver.NewOptString("test-sa-2"),
					// VendorSubnetId and TenantProjectNumber not set
				},
			},
		}
		params := gcpgenserver.V1betaInternalUpdateBackupVaultParams{
			ProjectNumber: "test-project",
			LocationId:    "us-central1",
			BackupVaultId: "test-backup-vault-id",
		}

		operationID := "operation-bucket-456"
		updatedBackupVault := &models.BackupVaultV1beta{
			BackupVaultID: "test-backup-vault-id",
			Name:          "test-backup-vault",
		}

		mockOrchestrator.EXPECT().UpdateBackupVaultInternal(mock.Anything, mock.MatchedBy(func(param *commonparams.BackupVaultParams) bool {
			if param.BucketDetails == nil || len(param.BucketDetails) != 1 {
				return false
			}
			bucket := param.BucketDetails[0]
			return bucket.BucketName == "test-bucket-2" &&
				bucket.ServiceAccountName == "test-sa-2" &&
				bucket.VendorSubnetID == "" &&
				bucket.TenantProjectNumber == ""
		}), true).Return(updatedBackupVault, operationID, nil)

		resp, err := handler.V1betaInternalUpdateBackupVault(context.Background(), req, params)
		assert.NoError(tt, err)

		result, ok := resp.(*gcpgenserver.OperationV1beta)
		assert.True(tt, ok)
		assert.True(tt, result.Name.IsSet())
		assert.Equal(tt, fmt.Sprintf("/v1beta/projects/%s/locations/%s/operations/%s", params.ProjectNumber, params.LocationId, operationID), result.Name.Value)
	})

	t.Run("WhenSuccessfulUpdateWithMultipleBucketDetails", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		req := &gcpgenserver.BackupVaultInternalUpdateV1beta{
			BucketDetails: []gcpgenserver.BackupVaultInternalUpdateV1betaBucketDetailsItem{
				{
					BucketName:          gcpgenserver.NewOptString("bucket-1"),
					ServiceAccountName:  gcpgenserver.NewOptString("sa-1"),
					VendorSubnetId:      gcpgenserver.NewOptString("subnet-1"),
					TenantProjectNumber: gcpgenserver.NewOptString("project-1"),
					SatisfiesPzi:        gcpgenserver.NewOptBool(true),
					SatisfiesPzs:        gcpgenserver.NewOptBool(true),
				},
				{
					BucketName:         gcpgenserver.NewOptString("bucket-2"),
					ServiceAccountName: gcpgenserver.NewOptString("sa-2"),
					// Only BucketName and ServiceAccountName set
				},
				{
					BucketName:          gcpgenserver.NewOptString("bucket-3"),
					VendorSubnetId:      gcpgenserver.NewOptString("subnet-3"),
					TenantProjectNumber: gcpgenserver.NewOptString("project-3"),
					// ServiceAccountName not set
				},
			},
		}
		params := gcpgenserver.V1betaInternalUpdateBackupVaultParams{
			ProjectNumber: "test-project",
			LocationId:    "us-central1",
			BackupVaultId: "test-backup-vault-id",
		}

		operationID := "operation-multi-bucket"
		updatedBackupVault := &models.BackupVaultV1beta{
			BackupVaultID: "test-backup-vault-id",
			Name:          "test-backup-vault",
		}

		mockOrchestrator.EXPECT().UpdateBackupVaultInternal(mock.Anything, mock.MatchedBy(func(param *commonparams.BackupVaultParams) bool {
			if param.BucketDetails == nil || len(param.BucketDetails) != 3 {
				return false
			}
			bucket1 := param.BucketDetails[0]
			bucket2 := param.BucketDetails[1]
			bucket3 := param.BucketDetails[2]
			return bucket1.BucketName == "bucket-1" &&
				bucket1.ServiceAccountName == "sa-1" &&
				bucket1.VendorSubnetID == "subnet-1" &&
				bucket1.TenantProjectNumber == "project-1" &&
				bucket1.SatisfiesPzi == true &&
				bucket1.SatisfiesPzs == true &&
				bucket2.BucketName == "bucket-2" &&
				bucket2.ServiceAccountName == "sa-2" &&
				bucket2.VendorSubnetID == "" &&
				bucket2.TenantProjectNumber == "" &&
				bucket3.BucketName == "bucket-3" &&
				bucket3.ServiceAccountName == "" &&
				bucket3.VendorSubnetID == "subnet-3" &&
				bucket3.TenantProjectNumber == "project-3"
		}), true).Return(updatedBackupVault, operationID, nil)

		resp, err := handler.V1betaInternalUpdateBackupVault(context.Background(), req, params)
		assert.NoError(tt, err)

		result, ok := resp.(*gcpgenserver.OperationV1beta)
		assert.True(tt, ok)
		assert.True(tt, result.Name.IsSet())
		assert.Equal(tt, fmt.Sprintf("/v1beta/projects/%s/locations/%s/operations/%s", params.ProjectNumber, params.LocationId, operationID), result.Name.Value)
	})

	t.Run("WhenSuccessfulUpdateWithEmptyBucketDetails", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		req := &gcpgenserver.BackupVaultInternalUpdateV1beta{
			BucketDetails: []gcpgenserver.BackupVaultInternalUpdateV1betaBucketDetailsItem{},
		}
		params := gcpgenserver.V1betaInternalUpdateBackupVaultParams{
			ProjectNumber: "test-project",
			LocationId:    "us-central1",
			BackupVaultId: "test-backup-vault-id",
		}

		operationID := "operation-empty-bucket"
		updatedBackupVault := &models.BackupVaultV1beta{
			BackupVaultID: "test-backup-vault-id",
			Name:          "test-backup-vault",
		}

		mockOrchestrator.EXPECT().UpdateBackupVaultInternal(mock.Anything, mock.MatchedBy(func(param *commonparams.BackupVaultParams) bool {
			// When BucketDetails is an empty array, the loop doesn't execute, so bucketDetails remains nil
			return param.BucketDetails == nil
		}), true).Return(updatedBackupVault, operationID, nil)

		resp, err := handler.V1betaInternalUpdateBackupVault(context.Background(), req, params)
		assert.NoError(tt, err)

		result, ok := resp.(*gcpgenserver.OperationV1beta)
		assert.True(tt, ok)
		assert.True(tt, result.Name.IsSet())
		assert.Equal(tt, fmt.Sprintf("/v1beta/projects/%s/locations/%s/operations/%s", params.ProjectNumber, params.LocationId, operationID), result.Name.Value)
	})

	t.Run("WhenSuccessfulUpdateWithBucketDetailsAndOtherFields", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		req := &gcpgenserver.BackupVaultInternalUpdateV1beta{
			Description: gcpgenserver.NewOptString("Updated with buckets"),
			BucketDetails: []gcpgenserver.BackupVaultInternalUpdateV1betaBucketDetailsItem{
				{
					BucketName:          gcpgenserver.NewOptString("combined-bucket"),
					ServiceAccountName:  gcpgenserver.NewOptString("combined-sa"),
					VendorSubnetId:      gcpgenserver.NewOptString("combined-subnet"),
					TenantProjectNumber: gcpgenserver.NewOptString("combined-project"),
				},
			},
		}
		params := gcpgenserver.V1betaInternalUpdateBackupVaultParams{
			ProjectNumber: "test-project",
			LocationId:    "us-central1",
			BackupVaultId: "test-backup-vault-id",
		}

		operationID := "operation-combined"
		description := "Updated with buckets"
		updatedBackupVault := &models.BackupVaultV1beta{
			BackupVaultID: "test-backup-vault-id",
			Name:          "test-backup-vault",
			Description:   &description,
		}

		mockOrchestrator.EXPECT().UpdateBackupVaultInternal(mock.Anything, mock.MatchedBy(func(param *commonparams.BackupVaultParams) bool {
			return param.Description != nil &&
				*param.Description == "Updated with buckets" &&
				param.BucketDetails != nil &&
				len(param.BucketDetails) == 1 &&
				param.BucketDetails[0].BucketName == "combined-bucket" &&
				param.BucketDetails[0].ServiceAccountName == "combined-sa" &&
				param.BucketDetails[0].VendorSubnetID == "combined-subnet" &&
				param.BucketDetails[0].TenantProjectNumber == "combined-project"
		}), true).Return(updatedBackupVault, operationID, nil)

		resp, err := handler.V1betaInternalUpdateBackupVault(context.Background(), req, params)
		assert.NoError(tt, err)

		result, ok := resp.(*gcpgenserver.OperationV1beta)
		assert.True(tt, ok)
		assert.True(tt, result.Name.IsSet())
		assert.Equal(tt, fmt.Sprintf("/v1beta/projects/%s/locations/%s/operations/%s", params.ProjectNumber, params.LocationId, operationID), result.Name.Value)
	})

	t.Run("WhenSuccessfulUpdateWithBucketDetailsOnlyBucketName", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		req := &gcpgenserver.BackupVaultInternalUpdateV1beta{
			BucketDetails: []gcpgenserver.BackupVaultInternalUpdateV1betaBucketDetailsItem{
				{
					BucketName: gcpgenserver.NewOptString("bucket-only"),
					// All other fields not set
				},
			},
		}
		params := gcpgenserver.V1betaInternalUpdateBackupVaultParams{
			ProjectNumber: "test-project",
			LocationId:    "us-central1",
			BackupVaultId: "test-backup-vault-id",
		}

		operationID := "operation-bucket-only"
		updatedBackupVault := &models.BackupVaultV1beta{
			BackupVaultID: "test-backup-vault-id",
			Name:          "test-backup-vault",
		}

		mockOrchestrator.EXPECT().UpdateBackupVaultInternal(mock.Anything, mock.MatchedBy(func(param *commonparams.BackupVaultParams) bool {
			if param.BucketDetails == nil || len(param.BucketDetails) != 1 {
				return false
			}
			bucket := param.BucketDetails[0]
			return bucket.BucketName == "bucket-only" &&
				bucket.ServiceAccountName == "" &&
				bucket.VendorSubnetID == "" &&
				bucket.TenantProjectNumber == ""
		}), true).Return(updatedBackupVault, operationID, nil)

		resp, err := handler.V1betaInternalUpdateBackupVault(context.Background(), req, params)
		assert.NoError(tt, err)

		result, ok := resp.(*gcpgenserver.OperationV1beta)
		assert.True(tt, ok)
		assert.True(tt, result.Name.IsSet())
		assert.Equal(tt, fmt.Sprintf("/v1beta/projects/%s/locations/%s/operations/%s", params.ProjectNumber, params.LocationId, operationID), result.Name.Value)
	})

	t.Run("WhenSuccessfulUpdateWithoutBucketDetails", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		req := &gcpgenserver.BackupVaultInternalUpdateV1beta{
			Description: gcpgenserver.NewOptString("Update without buckets"),
			// BucketDetails is nil (not set)
		}
		params := gcpgenserver.V1betaInternalUpdateBackupVaultParams{
			ProjectNumber: "test-project",
			LocationId:    "us-central1",
			BackupVaultId: "test-backup-vault-id",
		}

		operationID := "operation-no-bucket"
		description := "Update without buckets"
		updatedBackupVault := &models.BackupVaultV1beta{
			BackupVaultID: "test-backup-vault-id",
			Name:          "test-backup-vault",
			Description:   &description,
		}

		mockOrchestrator.EXPECT().UpdateBackupVaultInternal(mock.Anything, mock.MatchedBy(func(param *commonparams.BackupVaultParams) bool {
			// BucketDetails should be nil when not provided in request
			return param.Description != nil &&
				*param.Description == "Update without buckets" &&
				param.BucketDetails == nil
		}), true).Return(updatedBackupVault, operationID, nil)

		resp, err := handler.V1betaInternalUpdateBackupVault(context.Background(), req, params)
		assert.NoError(tt, err)

		result, ok := resp.(*gcpgenserver.OperationV1beta)
		assert.True(tt, ok)
		assert.True(tt, result.Name.IsSet())
		assert.Equal(tt, fmt.Sprintf("/v1beta/projects/%s/locations/%s/operations/%s", params.ProjectNumber, params.LocationId, operationID), result.Name.Value)
	})

	t.Run("WhenPureCMEKUpdateWithEncryptionState", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		req := &gcpgenserver.BackupVaultInternalUpdateV1beta{
			EncryptionState: gcpgenserver.NewOptBackupVaultInternalUpdateV1betaEncryptionState(gcpgenserver.BackupVaultInternalUpdateV1betaEncryptionStateENCRYPTIONSTATECOMPLETED),
		}
		params := gcpgenserver.V1betaInternalUpdateBackupVaultParams{
			ProjectNumber: "test-project",
			LocationId:    "us-central1",
			BackupVaultId: "test-backup-vault-id",
		}

		operationID := "operation-cmek"
		updatedBackupVault := &models.BackupVaultV1beta{
			BackupVaultID: "test-backup-vault-id",
			Name:          "test-backup-vault",
		}

		mockOrchestrator.EXPECT().UpdateBackupVaultInternal(mock.Anything, mock.MatchedBy(func(param *commonparams.BackupVaultParams) bool {
			return param.BackupVaultID == "test-backup-vault-id" &&
				param.CmekEncryptionState != nil &&
				*param.CmekEncryptionState == "ENCRYPTION_STATE_COMPLETED" &&
				param.CmekBackupsPrimaryKeyVersion == nil &&
				param.Description == nil &&
				param.BucketDetails == nil
		}), false).Return(updatedBackupVault, operationID, nil)

		resp, err := handler.V1betaInternalUpdateBackupVault(context.Background(), req, params)
		assert.NoError(tt, err)

		result, ok := resp.(*gcpgenserver.OperationV1beta)
		assert.True(tt, ok)
		assert.True(tt, result.Name.IsSet())
		assert.Equal(tt, fmt.Sprintf("/v1beta/projects/%s/locations/%s/operations/%s", params.ProjectNumber, params.LocationId, operationID), result.Name.Value)
	})

	t.Run("WhenPureCMEKUpdateWithBackupsPrimaryKeyVersion", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		req := &gcpgenserver.BackupVaultInternalUpdateV1beta{
			BackupsPrimaryKeyVersion: gcpgenserver.NewOptString("projects/test/locations/us-central1/keyRings/test/cryptoKeys/key/cryptoKeyVersions/11"),
		}
		params := gcpgenserver.V1betaInternalUpdateBackupVaultParams{
			ProjectNumber: "test-project",
			LocationId:    "us-central1",
			BackupVaultId: "test-backup-vault-id",
		}

		operationID := "operation-cmek-pkv"
		updatedBackupVault := &models.BackupVaultV1beta{
			BackupVaultID: "test-backup-vault-id",
			Name:          "test-backup-vault",
		}

		mockOrchestrator.EXPECT().UpdateBackupVaultInternal(mock.Anything, mock.MatchedBy(func(param *commonparams.BackupVaultParams) bool {
			return param.BackupVaultID == "test-backup-vault-id" &&
				param.CmekEncryptionState == nil &&
				param.CmekBackupsPrimaryKeyVersion != nil &&
				*param.CmekBackupsPrimaryKeyVersion == "projects/test/locations/us-central1/keyRings/test/cryptoKeys/key/cryptoKeyVersions/11" &&
				param.Description == nil &&
				param.BucketDetails == nil
		}), false).Return(updatedBackupVault, operationID, nil)

		resp, err := handler.V1betaInternalUpdateBackupVault(context.Background(), req, params)
		assert.NoError(tt, err)

		result, ok := resp.(*gcpgenserver.OperationV1beta)
		assert.True(tt, ok)
		assert.True(tt, result.Name.IsSet())
		assert.Equal(tt, fmt.Sprintf("/v1beta/projects/%s/locations/%s/operations/%s", params.ProjectNumber, params.LocationId, operationID), result.Name.Value)
	})

	t.Run("WhenPureCMEKUpdateWithBothFields", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		req := &gcpgenserver.BackupVaultInternalUpdateV1beta{
			EncryptionState:          gcpgenserver.NewOptBackupVaultInternalUpdateV1betaEncryptionState(gcpgenserver.BackupVaultInternalUpdateV1betaEncryptionStateENCRYPTIONSTATECOMPLETED),
			BackupsPrimaryKeyVersion: gcpgenserver.NewOptString("projects/test/locations/us-central1/keyRings/test/cryptoKeys/key/cryptoKeyVersions/11"),
		}
		params := gcpgenserver.V1betaInternalUpdateBackupVaultParams{
			ProjectNumber: "test-project",
			LocationId:    "us-central1",
			BackupVaultId: "test-backup-vault-id",
		}

		operationID := "operation-cmek-both"
		updatedBackupVault := &models.BackupVaultV1beta{
			BackupVaultID: "test-backup-vault-id",
			Name:          "test-backup-vault",
		}

		mockOrchestrator.EXPECT().UpdateBackupVaultInternal(mock.Anything, mock.MatchedBy(func(param *commonparams.BackupVaultParams) bool {
			return param.BackupVaultID == "test-backup-vault-id" &&
				param.CmekEncryptionState != nil &&
				*param.CmekEncryptionState == "ENCRYPTION_STATE_COMPLETED" &&
				param.CmekBackupsPrimaryKeyVersion != nil &&
				*param.CmekBackupsPrimaryKeyVersion == "projects/test/locations/us-central1/keyRings/test/cryptoKeys/key/cryptoKeyVersions/11" &&
				param.Description == nil &&
				param.BucketDetails == nil
		}), false).Return(updatedBackupVault, operationID, nil)

		resp, err := handler.V1betaInternalUpdateBackupVault(context.Background(), req, params)
		assert.NoError(tt, err)

		result, ok := resp.(*gcpgenserver.OperationV1beta)
		assert.True(tt, ok)
		assert.True(tt, result.Name.IsSet())
		assert.Equal(tt, fmt.Sprintf("/v1beta/projects/%s/locations/%s/operations/%s", params.ProjectNumber, params.LocationId, operationID), result.Name.Value)
	})

	t.Run("WhenMixedCMEKUpdateWithDescription", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		req := &gcpgenserver.BackupVaultInternalUpdateV1beta{
			EncryptionState: gcpgenserver.NewOptBackupVaultInternalUpdateV1betaEncryptionState(gcpgenserver.BackupVaultInternalUpdateV1betaEncryptionStateENCRYPTIONSTATECOMPLETED),
			Description:     gcpgenserver.NewOptString("Updated description with CMEK"),
		}
		params := gcpgenserver.V1betaInternalUpdateBackupVaultParams{
			ProjectNumber: "test-project",
			LocationId:    "us-central1",
			BackupVaultId: "test-backup-vault-id",
		}

		operationID := "operation-mixed"
		updatedBackupVault := &models.BackupVaultV1beta{
			BackupVaultID: "test-backup-vault-id",
			Name:          "test-backup-vault",
		}

		mockOrchestrator.EXPECT().UpdateBackupVaultInternal(mock.Anything, mock.MatchedBy(func(param *commonparams.BackupVaultParams) bool {
			return param.BackupVaultID == "test-backup-vault-id" &&
				param.CmekEncryptionState != nil &&
				*param.CmekEncryptionState == "ENCRYPTION_STATE_COMPLETED" &&
				param.Description != nil &&
				*param.Description == "Updated description with CMEK"
		}), true).Return(updatedBackupVault, operationID, nil)

		resp, err := handler.V1betaInternalUpdateBackupVault(context.Background(), req, params)
		assert.NoError(tt, err)

		result, ok := resp.(*gcpgenserver.OperationV1beta)
		assert.True(tt, ok)
		assert.True(tt, result.Name.IsSet())
		assert.Equal(tt, fmt.Sprintf("/v1beta/projects/%s/locations/%s/operations/%s", params.ProjectNumber, params.LocationId, operationID), result.Name.Value)
	})

	t.Run("WhenDescriptionIsCleared", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		req := &gcpgenserver.BackupVaultInternalUpdateV1beta{
			Description: gcpgenserver.NewOptString(""),
		}
		params := gcpgenserver.V1betaInternalUpdateBackupVaultParams{
			ProjectNumber: "test-project",
			LocationId:    "us-central1",
			BackupVaultId: "test-backup-vault-id",
		}

		mockOrchestrator.EXPECT().UpdateBackupVaultInternal(mock.Anything, mock.MatchedBy(func(param *commonparams.BackupVaultParams) bool {
			return param.Description != nil && *param.Description == ""
		}), true).Return(&models.BackupVaultV1beta{BackupVaultID: "test-backup-vault-id"}, "operation-clear", nil)

		resp, err := handler.V1betaInternalUpdateBackupVault(context.Background(), req, params)
		assert.NoError(tt, err)
		assert.IsType(tt, &gcpgenserver.OperationV1beta{}, resp)
	})
}

func TestV1betaInternalDescribeBackup(t *testing.T) {
	ctx := context.Background()

	t.Run("WhenBackupIsFoundAndNotInUseForRestoration", func(t *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		defer func() {
			utilParseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()
		utilParseAndValidateRegionAndZone = func(locationId string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "", nil
		}
		sourceRegionName := "us-east4"
		handler := Handler{Orchestrator: mockOrchestrator}
		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{
				UUID: "vault-uuid",
			},
			Name:             "test-backup-vault",
			SourceRegionName: &sourceRegionName,
			BucketDetails:    datamodel.BucketDetailsArray{&datamodel.BucketDetails{BucketName: "test-bucket", SatisfiesPzi: true, SatisfiesPzs: true}},
		}
		backup := &datamodel.Backup{
			BaseModel: datamodel.BaseModel{
				UUID:      "backup-uuid",
				CreatedAt: time.Now(),
			},
			State:         datamodel.LifeCycleStateAvailable,
			Name:          "backup-123",
			VolumeUUID:    "test-vol",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:          "test-bucket",
				UseExistingSnapshot: false,
				RestoreVolumeCount:  0,
			},
			SizeInBytes:             int64(1000),
			LatestLogicalBackupSize: int64(500),
			Description:             "test backup",
			Type:                    "MANUAL",
		}

		params := gcpgenserver.V1betaInternalDescribeBackupParams{
			BackupVaultId:  "vault-123",
			BackupId:       "backup-123",
			ProjectNumber:  "project-123",
			LocationId:     "us-east4",
			XCorrelationID: gcpgenserver.NewOptString("correlation-id"),
		}

		mockOrchestrator.EXPECT().GetBackupByExternalUUID(ctx, params.BackupVaultId, params.BackupId, params.ProjectNumber).Return(backup, nil)

		resp, err := handler.V1betaInternalDescribeBackup(ctx, params)

		assert.NoError(t, err)
		assert.NotNil(t, resp)
		assert.IsType(t, &gcpgenserver.V1betaInternalDescribeBackupOK{}, resp)
		okResp := resp.(*gcpgenserver.V1betaInternalDescribeBackupOK)
		assert.Equal(t, 1, len(okResp.Backups))
		assert.Equal(t, "backup-123", okResp.Backups[0].ResourceId.Value)
		assert.Equal(t, false, okResp.Backups[0].IsRestoring.Value)
	})

	t.Run("WhenBackupIsFoundAndInUseForRestoration", func(t *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		defer func() {
			utilParseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()
		utilParseAndValidateRegionAndZone = func(locationId string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "", nil
		}
		sourceRegionName := "us-east4"
		handler := Handler{Orchestrator: mockOrchestrator}
		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{
				UUID: "vault-uuid",
			},
			Name:             "test-backup-vault",
			SourceRegionName: &sourceRegionName,
			BucketDetails:    datamodel.BucketDetailsArray{&datamodel.BucketDetails{BucketName: "test-bucket", SatisfiesPzi: true, SatisfiesPzs: true}},
		}
		backup := &datamodel.Backup{
			BaseModel: datamodel.BaseModel{
				UUID:      "backup-uuid",
				CreatedAt: time.Now(),
			},
			State:         datamodel.LifeCycleStateAvailable,
			Name:          "backup-123",
			VolumeUUID:    "test-vol",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:          "test-bucket",
				UseExistingSnapshot: false,
				RestoreVolumeCount:  1,
			},
			SizeInBytes:             int64(1000),
			LatestLogicalBackupSize: int64(500),
			Description:             "test backup",
			Type:                    "MANUAL",
		}

		params := gcpgenserver.V1betaInternalDescribeBackupParams{
			BackupVaultId:  "vault-123",
			BackupId:       "backup-123",
			ProjectNumber:  "project-123",
			LocationId:     "us-east4",
			XCorrelationID: gcpgenserver.NewOptString("correlation-id"),
		}

		mockOrchestrator.EXPECT().GetBackupByExternalUUID(ctx, params.BackupVaultId, params.BackupId, params.ProjectNumber).Return(backup, nil)

		resp, err := handler.V1betaInternalDescribeBackup(ctx, params)

		assert.NoError(t, err)
		assert.NotNil(t, resp)
		assert.IsType(t, &gcpgenserver.V1betaInternalDescribeBackupOK{}, resp)
		okResp := resp.(*gcpgenserver.V1betaInternalDescribeBackupOK)
		assert.Equal(t, 1, len(okResp.Backups))
		assert.Equal(t, "backup-123", okResp.Backups[0].ResourceId.Value)
		assert.Equal(t, true, okResp.Backups[0].IsRestoring.Value)
	})

	t.Run("WhenParsingRegionAndZoneFails", func(t *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaInternalDescribeBackupParams{
			LocationId: "invalid-location",
		}

		result, err := handler.V1betaInternalDescribeBackup(ctx, params)
		assert.Nil(t, err)
		assert.IsType(t, &gcpgenserver.V1betaInternalDescribeBackupInternalServerError{}, result)
		assert.Equal(t, float64(400), result.(*gcpgenserver.V1betaInternalDescribeBackupInternalServerError).Code)
	})

	t.Run("WhenGetBackupReturnsError", func(t *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		defer func() {
			utilParseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()
		utilParseAndValidateRegionAndZone = func(locationId string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "", nil
		}
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaInternalDescribeBackupParams{
			BackupVaultId:  "vault-123",
			BackupId:       "backup-123",
			ProjectNumber:  "project-123",
			LocationId:     "us-east4",
			XCorrelationID: gcpgenserver.NewOptString("correlation-id"),
		}

		mockOrchestrator.EXPECT().GetBackupByExternalUUID(ctx, params.BackupVaultId, params.BackupId, params.ProjectNumber).Return(nil, errors.New("database error"))

		resp, err := handler.V1betaInternalDescribeBackup(ctx, params)

		assert.Error(t, err)
		assert.NotNil(t, resp)
		assert.IsType(t, &gcpgenserver.V1betaInternalDescribeBackupInternalServerError{}, resp)
		internalErr := resp.(*gcpgenserver.V1betaInternalDescribeBackupInternalServerError)
		assert.Equal(t, float64(500), internalErr.Code)
		assert.Equal(t, "database error", internalErr.Message)
	})

	t.Run("WhenBackupHasImmutableAttributesAndEnforcedRetention", func(t *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		defer func() {
			utilParseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()
		utilParseAndValidateRegionAndZone = func(locationId string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "", nil
		}
		sourceRegionName := "us-east4"
		retentionDays := int64(30)
		handler := Handler{Orchestrator: mockOrchestrator}
		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{
				UUID: "vault-uuid",
			},
			Name:             "test-backup-vault",
			SourceRegionName: &sourceRegionName,
			BucketDetails:    datamodel.BucketDetailsArray{&datamodel.BucketDetails{BucketName: "test-bucket", SatisfiesPzi: true, SatisfiesPzs: true}},
			ImmutableAttributes: &datamodel.ImmutableAttributes{
				BackupMinimumEnforcedRetentionDuration: &retentionDays,
				IsAdhocBackupImmutable:                 true, // Manual backups are adhoc
			},
		}
		backup := &datamodel.Backup{
			BaseModel: datamodel.BaseModel{
				UUID:      "backup-uuid",
				CreatedAt: time.Now().AddDate(0, 0, -10), // Created 10 days ago
			},
			State:         datamodel.LifeCycleStateAvailable,
			Name:          "backup-123",
			VolumeUUID:    "test-vol",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:          "test-bucket",
				UseExistingSnapshot: false,
			},
			SizeInBytes:             int64(1000),
			LatestLogicalBackupSize: int64(500),
			Description:             "test backup",
			Type:                    "MANUAL",
		}

		params := gcpgenserver.V1betaInternalDescribeBackupParams{
			BackupVaultId:  "vault-123",
			BackupId:       "backup-123",
			ProjectNumber:  "project-123",
			LocationId:     "us-east4",
			XCorrelationID: gcpgenserver.NewOptString("correlation-id"),
		}

		mockOrchestrator.EXPECT().GetBackupByExternalUUID(ctx, params.BackupVaultId, params.BackupId, params.ProjectNumber).Return(backup, nil)

		resp, err := handler.V1betaInternalDescribeBackup(ctx, params)

		assert.NoError(t, err)
		assert.NotNil(t, resp)
		assert.IsType(t, &gcpgenserver.V1betaInternalDescribeBackupOK{}, resp)
		okResp := resp.(*gcpgenserver.V1betaInternalDescribeBackupOK)
		assert.Equal(t, 1, len(okResp.Backups))
		assert.True(t, okResp.Backups[0].EnforcedRetentionEndTime.Set)
	})
}

func TestV1betaInternalDeleteBackupUnderBackupVault(t *testing.T) {
	t.Run("WhenParsingRegionAndZoneFails", func(tt *testing.T) {
		ctx := context.Background()
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaInternalDeleteBackupUnderBackupVaultParams{
			LocationId: "invalid-location",
		}

		result, err := handler.V1betaInternalDeleteBackupUnderBackupVault(ctx, params)
		assert.Nil(tt, err)
		assert.IsType(tt, &gcpgenserver.V1betaInternalDeleteBackupUnderBackupVaultBadRequest{}, result)
		assert.Equal(tt, float64(400), result.(*gcpgenserver.V1betaInternalDeleteBackupUnderBackupVaultBadRequest).Code)
	})

	t.Run("WhenBackupNotFoundInVSA", func(tt *testing.T) {
		ctx := context.Background()
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaInternalDeleteBackupUnderBackupVaultParams{
			BackupVaultId: "vault-id",
			BackupId:      "backup-id",
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
		}

		defer func() {
			utilParseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()
		utilParseAndValidateRegionAndZone = func(locationId string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "", nil
		}

		mockOrchestrator.EXPECT().GetBackupByExternalUUID(ctx, params.BackupVaultId, params.BackupId, params.ProjectNumber).Return(nil, errors.NewNotFoundErr("backup", nil))

		result, err := handler.V1betaInternalDeleteBackupUnderBackupVault(ctx, params)
		assert.Nil(tt, err)
		assert.IsType(tt, &gcpgenserver.V1betaInternalDeleteBackupUnderBackupVaultNotFound{}, result)
		notFound := result.(*gcpgenserver.V1betaInternalDeleteBackupUnderBackupVaultNotFound)
		assert.Equal(tt, float64(404), notFound.Code)
		assert.Contains(tt, notFound.Message, "backup-id")
		assert.Contains(tt, notFound.Message, "vault-id")
	})

	t.Run("WhenGetBackupReturnsNonNotFoundError", func(tt *testing.T) {
		ctx := context.Background()
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaInternalDeleteBackupUnderBackupVaultParams{
			BackupVaultId: "vault-id",
			BackupId:      "backup-id",
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
		}

		defer func() {
			utilParseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()
		utilParseAndValidateRegionAndZone = func(locationId string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "", nil
		}

		mockOrchestrator.EXPECT().GetBackupByExternalUUID(ctx, params.BackupVaultId, params.BackupId, params.ProjectNumber).Return(nil, errors.New("database error"))

		result, err := handler.V1betaInternalDeleteBackupUnderBackupVault(ctx, params)
		assert.NotNil(tt, err)
		assert.IsType(tt, &gcpgenserver.V1betaInternalDeleteBackupUnderBackupVaultInternalServerError{}, result)
		internalErr := result.(*gcpgenserver.V1betaInternalDeleteBackupUnderBackupVaultInternalServerError)
		assert.Equal(tt, float64(500), internalErr.Code)
		assert.Equal(tt, "database error", internalErr.Message)
	})

	t.Run("WhenDeleteBackupInternalReturnsUserInputValidationError", func(tt *testing.T) {
		ctx := context.Background()
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaInternalDeleteBackupUnderBackupVaultParams{
			BackupVaultId: "vault-id",
			BackupId:      "backup-id",
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
		}

		defer func() {
			utilParseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()
		utilParseAndValidateRegionAndZone = func(locationId string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "", nil
		}

		mockOrchestrator.EXPECT().GetBackupByExternalUUID(ctx, params.BackupVaultId, params.BackupId, params.ProjectNumber).Return(&datamodel.Backup{}, nil)
		mockOrchestrator.EXPECT().DeleteBackupInternal(ctx, mock.Anything).Return("", errors.NewUserInputValidationErr("Cannot delete backup as restore is in progress"))

		result, err := handler.V1betaInternalDeleteBackupUnderBackupVault(ctx, params)
		assert.Nil(tt, err)
		assert.IsType(tt, &gcpgenserver.V1betaInternalDeleteBackupUnderBackupVaultBadRequest{}, result)
		badRequest := result.(*gcpgenserver.V1betaInternalDeleteBackupUnderBackupVaultBadRequest)
		assert.Equal(tt, float64(400), badRequest.Code)
		assert.Equal(tt, "Cannot delete backup as restore is in progress", badRequest.Message)
	})

	t.Run("WhenDeleteBackupInternalReturnsOtherError", func(tt *testing.T) {
		ctx := context.Background()
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaInternalDeleteBackupUnderBackupVaultParams{
			BackupVaultId: "vault-id",
			BackupId:      "backup-id",
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
		}

		defer func() {
			utilParseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()
		utilParseAndValidateRegionAndZone = func(locationId string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "", nil
		}

		mockOrchestrator.EXPECT().GetBackupByExternalUUID(ctx, params.BackupVaultId, params.BackupId, params.ProjectNumber).Return(&datamodel.Backup{}, nil)
		mockOrchestrator.EXPECT().DeleteBackupInternal(ctx, mock.Anything).Return("", errors.New("failed to delete backup"))

		result, err := handler.V1betaInternalDeleteBackupUnderBackupVault(ctx, params)
		assert.NotNil(tt, err)
		assert.IsType(tt, &gcpgenserver.V1betaInternalDeleteBackupUnderBackupVaultInternalServerError{}, result)
		internalErr := result.(*gcpgenserver.V1betaInternalDeleteBackupUnderBackupVaultInternalServerError)
		assert.Equal(tt, float64(500), internalErr.Code)
		assert.Equal(tt, "failed to delete backup", internalErr.Message)
	})

	t.Run("WhenDeleteBackupInternalSuccessWithEmptyJobId", func(tt *testing.T) {
		ctx := context.Background()
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaInternalDeleteBackupUnderBackupVaultParams{
			BackupVaultId: "vault-id",
			BackupId:      "backup-id",
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
		}

		defer func() {
			utilParseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()
		utilParseAndValidateRegionAndZone = func(locationId string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "", nil
		}

		mockOrchestrator.EXPECT().GetBackupByExternalUUID(ctx, params.BackupVaultId, params.BackupId, params.ProjectNumber).Return(&datamodel.Backup{}, nil)
		mockOrchestrator.EXPECT().DeleteBackupInternal(ctx, mock.Anything).Return("", nil)

		result, err := handler.V1betaInternalDeleteBackupUnderBackupVault(ctx, params)
		assert.Nil(tt, err)
		assert.IsType(tt, &gcpgenserver.OperationV1beta{}, result)
		operation := result.(*gcpgenserver.OperationV1beta)
		assert.True(tt, operation.Done.Value)
		assert.True(tt, operation.Name.IsSet())
		assert.Contains(tt, operation.Name.Value, "/v1beta/projects/project-number/locations/us-east4/operations/")
	})

	t.Run("WhenDeleteBackupInternalSuccessWithNonEmptyJobId", func(tt *testing.T) {
		ctx := context.Background()
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaInternalDeleteBackupUnderBackupVaultParams{
			BackupVaultId: "vault-id",
			BackupId:      "backup-id",
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
		}

		defer func() {
			utilParseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()
		utilParseAndValidateRegionAndZone = func(locationId string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "", nil
		}

		mockOrchestrator.EXPECT().GetBackupByExternalUUID(ctx, params.BackupVaultId, params.BackupId, params.ProjectNumber).Return(&datamodel.Backup{}, nil)
		mockOrchestrator.EXPECT().DeleteBackupInternal(ctx, mock.Anything).Return("job-id-123", nil)

		result, err := handler.V1betaInternalDeleteBackupUnderBackupVault(ctx, params)
		assert.Nil(tt, err)
		assert.IsType(tt, &gcpgenserver.OperationV1beta{}, result)
		operation := result.(*gcpgenserver.OperationV1beta)
		assert.False(tt, operation.Done.Value)
		assert.True(tt, operation.Name.IsSet())
		expectedOperationID := "/v1beta/projects/project-number/locations/us-east4/operations/job-id-123"
		assert.Equal(tt, expectedOperationID, operation.Name.Value)
	})
}

func TestV1betaInternalCreateBackup(t *testing.T) {
	ctx := context.Background()

	t.Run("WhenInvalidLocationId", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		req := &gcpgenserver.InternalBackupCreateV1beta{
			ResourceId: "test-backup",
			BackupUUID: "backup-uuid-123",
			VolumeId:   "volume-uuid-456",
			VolumeName: "test-volume",
			Protocols:  []gcpgenserver.InternalBackupCreateV1betaProtocolsItem{gcpgenserver.InternalBackupCreateV1betaProtocolsItemNFSV3},
		}
		params := gcpgenserver.V1betaInternalCreateBackupParams{
			BackupVaultId:  "vault-123",
			ProjectNumber:  "project-123",
			LocationId:     "invalid-location",
			XCorrelationID: gcpgenserver.NewOptString("correlation-id"),
		}

		defer func() {
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "", "", &gcpgenserver.Error{
				Code:    400,
				Message: "Invalid location ID",
			}
		}

		resp, err := handler.V1betaInternalCreateBackup(ctx, req, params)
		assert.NoError(tt, err)
		assert.IsType(tt, &gcpgenserver.V1betaInternalCreateBackupBadRequest{}, resp)
		badReq := resp.(*gcpgenserver.V1betaInternalCreateBackupBadRequest)
		assert.Equal(tt, float64(400), badReq.Code)
		assert.Equal(tt, "Invalid location ID", badReq.Message)
	})

	t.Run("WhenCreateBackupInternalReturnsValidationError", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		req := &gcpgenserver.InternalBackupCreateV1beta{
			ResourceId: "test-backup",
			BackupUUID: "backup-uuid-123",
			VolumeId:   "volume-uuid-456",
			VolumeName: "test-volume",
			Protocols:  []gcpgenserver.InternalBackupCreateV1betaProtocolsItem{gcpgenserver.InternalBackupCreateV1betaProtocolsItemNFSV3},
		}
		params := gcpgenserver.V1betaInternalCreateBackupParams{
			BackupVaultId:  "vault-123",
			ProjectNumber:  "project-123",
			LocationId:     "us-east4",
			XCorrelationID: gcpgenserver.NewOptString("correlation-id"),
		}

		defer func() {
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "", nil
		}

		mockOrchestrator.EXPECT().CreateBackupInternal(ctx, mock.Anything).Return(nil, "", errors.NewUserInputValidationErr("Invalid volume UUID"))

		resp, err := handler.V1betaInternalCreateBackup(ctx, req, params)
		assert.NoError(tt, err)
		assert.IsType(tt, &gcpgenserver.V1betaInternalCreateBackupBadRequest{}, resp)
		badReq := resp.(*gcpgenserver.V1betaInternalCreateBackupBadRequest)
		assert.Equal(tt, float64(400), badReq.Code)
		assert.Equal(tt, "Invalid volume UUID", badReq.Message)
	})

	t.Run("WhenCreateBackupInternalReturnsNotFoundError", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		req := &gcpgenserver.InternalBackupCreateV1beta{
			ResourceId: "test-backup",
			BackupUUID: "backup-uuid-123",
			VolumeId:   "volume-uuid-456",
			VolumeName: "test-volume",
			Protocols:  []gcpgenserver.InternalBackupCreateV1betaProtocolsItem{gcpgenserver.InternalBackupCreateV1betaProtocolsItemNFSV3},
		}
		params := gcpgenserver.V1betaInternalCreateBackupParams{
			BackupVaultId:  "vault-123",
			ProjectNumber:  "project-123",
			LocationId:     "us-east4",
			XCorrelationID: gcpgenserver.NewOptString("correlation-id"),
		}

		defer func() {
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "", nil
		}

		mockOrchestrator.EXPECT().CreateBackupInternal(ctx, mock.Anything).Return(nil, "", errors.NewNotFoundErr("Backup vault not found", nil))

		resp, err := handler.V1betaInternalCreateBackup(ctx, req, params)
		assert.NoError(tt, err)
		assert.IsType(tt, &gcpgenserver.V1betaInternalCreateBackupBadRequest{}, resp)
		badReq := resp.(*gcpgenserver.V1betaInternalCreateBackupBadRequest)
		assert.Equal(tt, float64(400), badReq.Code)
	})

	t.Run("WhenCreateBackupInternalReturnsInternalError", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		req := &gcpgenserver.InternalBackupCreateV1beta{
			ResourceId: "test-backup",
			BackupUUID: "backup-uuid-123",
			VolumeId:   "volume-uuid-456",
			VolumeName: "test-volume",
			Protocols:  []gcpgenserver.InternalBackupCreateV1betaProtocolsItem{gcpgenserver.InternalBackupCreateV1betaProtocolsItemNFSV3},
		}
		params := gcpgenserver.V1betaInternalCreateBackupParams{
			BackupVaultId:  "vault-123",
			ProjectNumber:  "project-123",
			LocationId:     "us-east4",
			XCorrelationID: gcpgenserver.NewOptString("correlation-id"),
		}

		defer func() {
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "", nil
		}

		mockOrchestrator.EXPECT().CreateBackupInternal(ctx, mock.Anything).Return(nil, "", errors.New("Internal server error"))

		resp, err := handler.V1betaInternalCreateBackup(ctx, req, params)
		assert.Error(tt, err)
		assert.IsType(tt, &gcpgenserver.V1betaInternalCreateBackupInternalServerError{}, resp)
		internalErr := resp.(*gcpgenserver.V1betaInternalCreateBackupInternalServerError)
		assert.Equal(tt, float64(500), internalErr.Code)
		assert.Equal(tt, "Internal server error", internalErr.Message)
	})

	t.Run("WhenGetBackupByExternalUUIDFails", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		req := &gcpgenserver.InternalBackupCreateV1beta{
			ResourceId: "test-backup",
			BackupUUID: "backup-uuid-123",
			VolumeId:   "volume-uuid-456",
			VolumeName: "test-volume",
			Protocols:  []gcpgenserver.InternalBackupCreateV1betaProtocolsItem{gcpgenserver.InternalBackupCreateV1betaProtocolsItemNFSV3},
		}
		params := gcpgenserver.V1betaInternalCreateBackupParams{
			BackupVaultId:  "vault-123",
			ProjectNumber:  "project-123",
			LocationId:     "us-east4",
			XCorrelationID: gcpgenserver.NewOptString("correlation-id"),
		}

		defer func() {
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "", nil
		}

		mockOrchestrator.EXPECT().CreateBackupInternal(ctx, mock.Anything).Return(&models.Backup{}, "", nil)
		mockOrchestrator.EXPECT().GetBackupByExternalUUID(ctx, params.BackupVaultId, req.BackupUUID, params.ProjectNumber).Return(nil, errors.New("Database error"))

		resp, err := handler.V1betaInternalCreateBackup(ctx, req, params)
		assert.Error(tt, err)
		assert.IsType(tt, &gcpgenserver.V1betaInternalCreateBackupInternalServerError{}, resp)
		internalErr := resp.(*gcpgenserver.V1betaInternalCreateBackupInternalServerError)
		assert.Equal(tt, float64(500), internalErr.Code)
		assert.Equal(tt, "Database error", internalErr.Message)
	})

	t.Run("WhenSuccessSynchronousOperation", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		req := &gcpgenserver.InternalBackupCreateV1beta{
			ResourceId:  "test-backup",
			BackupUUID:  "backup-uuid-123",
			VolumeId:    "volume-uuid-456",
			VolumeName:  "test-volume",
			Protocols:   []gcpgenserver.InternalBackupCreateV1betaProtocolsItem{gcpgenserver.InternalBackupCreateV1betaProtocolsItemNFSV3},
			Description: gcpgenserver.NewOptString("Test backup description"),
		}
		params := gcpgenserver.V1betaInternalCreateBackupParams{
			BackupVaultId:  "vault-123",
			ProjectNumber:  "project-123",
			LocationId:     "us-east4",
			XCorrelationID: gcpgenserver.NewOptString("correlation-id"),
		}

		defer func() {
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "", nil
		}

		sourceRegionName := "us-east4"
		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{
				UUID: "vault-123",
			},
			Name:             "test-backup-vault",
			SourceRegionName: &sourceRegionName,
			BucketDetails:    datamodel.BucketDetailsArray{&datamodel.BucketDetails{BucketName: "test-bucket", SatisfiesPzi: true, SatisfiesPzs: true}},
		}
		backup := &datamodel.Backup{
			BaseModel: datamodel.BaseModel{
				UUID:      "backup-uuid-123",
				CreatedAt: time.Now(),
			},
			State:         datamodel.LifeCycleStateAvailable,
			Name:          "test-backup",
			VolumeUUID:    "volume-uuid-456",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:          "test-bucket",
				UseExistingSnapshot: false,
				RestoreVolumeCount:  0,
			},
			SizeInBytes:             int64(1000),
			LatestLogicalBackupSize: int64(500),
			Description:             "Test backup description",
			Type:                    "MANUAL",
		}

		mockOrchestrator.EXPECT().CreateBackupInternal(ctx, mock.Anything).Return(&models.Backup{}, "", nil)
		mockOrchestrator.EXPECT().GetBackupByExternalUUID(ctx, params.BackupVaultId, req.BackupUUID, params.ProjectNumber).Return(backup, nil)
		mockOrchestrator.EXPECT().UpdateBackupLatestLogicalBackupSizeByVolume(ctx, backup.VolumeUUID, backup.UUID).Return(nil)

		resp, err := handler.V1betaInternalCreateBackup(ctx, req, params)
		assert.NoError(tt, err)
		assert.IsType(tt, &gcpgenserver.InternalBackupV1beta{}, resp)
		internalBackup := resp.(*gcpgenserver.InternalBackupV1beta)
		assert.True(tt, internalBackup.ResourceId.Set)
		assert.Equal(tt, "test-backup", internalBackup.ResourceId.Value)
	})

	t.Run("WhenSuccessAsynchronousOperation", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		req := &gcpgenserver.InternalBackupCreateV1beta{
			ResourceId: "test-backup",
			BackupUUID: "backup-uuid-123",
			VolumeId:   "volume-uuid-456",
			VolumeName: "test-volume",
			Protocols:  []gcpgenserver.InternalBackupCreateV1betaProtocolsItem{gcpgenserver.InternalBackupCreateV1betaProtocolsItemNFSV3},
		}
		params := gcpgenserver.V1betaInternalCreateBackupParams{
			BackupVaultId:  "vault-123",
			ProjectNumber:  "project-123",
			LocationId:     "us-east4",
			XCorrelationID: gcpgenserver.NewOptString("correlation-id"),
		}

		defer func() {
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "", nil
		}

		sourceRegionName := "us-east4"
		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{
				UUID: "vault-123",
			},
			Name:             "test-backup-vault",
			SourceRegionName: &sourceRegionName,
			BucketDetails:    datamodel.BucketDetailsArray{&datamodel.BucketDetails{BucketName: "test-bucket", SatisfiesPzi: true, SatisfiesPzs: true}},
		}
		backup := &datamodel.Backup{
			BaseModel: datamodel.BaseModel{
				UUID:      "backup-uuid-123",
				CreatedAt: time.Now(),
			},
			State:         datamodel.LifeCycleStateAvailable,
			Name:          "test-backup",
			VolumeUUID:    "volume-uuid-456",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:          "test-bucket",
				UseExistingSnapshot: false,
				RestoreVolumeCount:  0,
			},
			SizeInBytes:             int64(1000),
			LatestLogicalBackupSize: int64(500),
			Description:             "Test backup description",
			Type:                    "MANUAL",
		}

		jobID := "job-uuid-123"
		mockOrchestrator.EXPECT().CreateBackupInternal(ctx, mock.Anything).Return(&models.Backup{}, jobID, nil)
		mockOrchestrator.EXPECT().GetBackupByExternalUUID(ctx, params.BackupVaultId, req.BackupUUID, params.ProjectNumber).Return(backup, nil)
		mockOrchestrator.EXPECT().UpdateBackupLatestLogicalBackupSizeByVolume(ctx, backup.VolumeUUID, backup.UUID).Return(nil)

		resp, err := handler.V1betaInternalCreateBackup(ctx, req, params)
		assert.NoError(tt, err)
		assert.IsType(tt, &gcpgenserver.OperationV1beta{}, resp)
		operation := resp.(*gcpgenserver.OperationV1beta)
		assert.True(tt, operation.Name.IsSet())
		assert.Contains(tt, operation.Name.Value, jobID)
		assert.False(tt, operation.Done.Value)
	})

	t.Run("WhenUpdateBackupLatestLogicalBackupSizeByVolumeFails", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		req := &gcpgenserver.InternalBackupCreateV1beta{
			ResourceId: "test-backup",
			BackupUUID: "backup-uuid-123",
			VolumeId:   "volume-uuid-456",
			VolumeName: "test-volume",
			Protocols:  []gcpgenserver.InternalBackupCreateV1betaProtocolsItem{gcpgenserver.InternalBackupCreateV1betaProtocolsItemNFSV3},
		}
		params := gcpgenserver.V1betaInternalCreateBackupParams{
			BackupVaultId:  "vault-123",
			ProjectNumber:  "project-123",
			LocationId:     "us-east4",
			XCorrelationID: gcpgenserver.NewOptString("correlation-id"),
		}

		defer func() {
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "", nil
		}

		sourceRegionName := "us-east4"
		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{
				UUID: "vault-123",
			},
			Name:             "test-backup-vault",
			SourceRegionName: &sourceRegionName,
			BucketDetails:    datamodel.BucketDetailsArray{&datamodel.BucketDetails{BucketName: "test-bucket", SatisfiesPzi: true, SatisfiesPzs: true}},
		}
		backup := &datamodel.Backup{
			BaseModel: datamodel.BaseModel{
				UUID:      "backup-uuid-123",
				CreatedAt: time.Now(),
			},
			State:         datamodel.LifeCycleStateAvailable,
			Name:          "test-backup",
			VolumeUUID:    "volume-uuid-456",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:          "test-bucket",
				UseExistingSnapshot: false,
				RestoreVolumeCount:  0,
			},
			SizeInBytes:             int64(1000),
			LatestLogicalBackupSize: int64(500),
			Description:             "Test backup description",
			Type:                    "MANUAL",
		}

		mockOrchestrator.EXPECT().CreateBackupInternal(ctx, mock.Anything).Return(&models.Backup{}, "", nil)
		mockOrchestrator.EXPECT().GetBackupByExternalUUID(ctx, params.BackupVaultId, req.BackupUUID, params.ProjectNumber).Return(backup, nil)
		mockOrchestrator.EXPECT().UpdateBackupLatestLogicalBackupSizeByVolume(ctx, backup.VolumeUUID, backup.UUID).Return(errors.New("Database update error"))

		resp, err := handler.V1betaInternalCreateBackup(ctx, req, params)
		assert.Error(tt, err)
		assert.IsType(tt, &gcpgenserver.V1betaInternalCreateBackupInternalServerError{}, resp)
		internalErr := resp.(*gcpgenserver.V1betaInternalCreateBackupInternalServerError)
		assert.Equal(tt, float64(500), internalErr.Code)
		assert.Contains(tt, err.Error(), "Database update error")
	})
}

func TestV1betaInternalUpdateBackup(t *testing.T) {
	ctx := context.Background()

	t.Run("WhenInvalidLocationId", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		req := &gcpgenserver.BackupUpdateV1beta{
			Description: "Updated description",
		}
		params := gcpgenserver.V1betaInternalUpdateBackupParams{
			BackupVaultId:  "vault-123",
			BackupId:       "backup-123",
			ProjectNumber:  "project-123",
			LocationId:     "invalid-location",
			XCorrelationID: gcpgenserver.NewOptString("correlation-id"),
		}

		defer func() {
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "", "", &gcpgenserver.Error{
				Code:    400,
				Message: "Invalid location ID",
			}
		}

		resp, err := handler.V1betaInternalUpdateBackup(ctx, req, params)
		assert.NoError(tt, err)
		assert.IsType(tt, &gcpgenserver.V1betaInternalUpdateBackupBadRequest{}, resp)
		badReq := resp.(*gcpgenserver.V1betaInternalUpdateBackupBadRequest)
		assert.Equal(tt, float64(400), badReq.Code)
		assert.Equal(tt, "Invalid location ID", badReq.Message)
	})

	t.Run("WhenUpdateBackupInternalReturnsValidationError", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		req := &gcpgenserver.BackupUpdateV1beta{
			Description: "Updated description",
		}
		params := gcpgenserver.V1betaInternalUpdateBackupParams{
			BackupVaultId:  "vault-123",
			BackupId:       "backup-123",
			ProjectNumber:  "project-123",
			LocationId:     "us-east4",
			XCorrelationID: gcpgenserver.NewOptString("correlation-id"),
		}

		defer func() {
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "", nil
		}

		mockOrchestrator.EXPECT().UpdateBackupInternal(ctx, mock.Anything).Return(nil, "", errors.NewUserInputValidationErr("Invalid backup UUID"))

		resp, err := handler.V1betaInternalUpdateBackup(ctx, req, params)
		assert.NoError(tt, err)
		assert.IsType(tt, &gcpgenserver.V1betaInternalUpdateBackupBadRequest{}, resp)
		badReq := resp.(*gcpgenserver.V1betaInternalUpdateBackupBadRequest)
		assert.Equal(tt, float64(400), badReq.Code)
		assert.Equal(tt, "Invalid backup UUID", badReq.Message)
	})

	t.Run("WhenUpdateBackupInternalReturnsNotFoundError", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		req := &gcpgenserver.BackupUpdateV1beta{
			Description: "Updated description",
		}
		params := gcpgenserver.V1betaInternalUpdateBackupParams{
			BackupVaultId:  "vault-123",
			BackupId:       "backup-123",
			ProjectNumber:  "project-123",
			LocationId:     "us-east4",
			XCorrelationID: gcpgenserver.NewOptString("correlation-id"),
		}

		defer func() {
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "", nil
		}

		mockOrchestrator.EXPECT().UpdateBackupInternal(ctx, mock.Anything).Return(nil, "", errors.NewNotFoundErr("Backup not found", nil))

		resp, err := handler.V1betaInternalUpdateBackup(ctx, req, params)
		assert.NoError(tt, err)
		assert.IsType(tt, &gcpgenserver.V1betaInternalUpdateBackupBadRequest{}, resp)
		badReq := resp.(*gcpgenserver.V1betaInternalUpdateBackupBadRequest)
		assert.Equal(tt, float64(400), badReq.Code)
	})

	t.Run("WhenUpdateBackupInternalReturnsInternalError", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		req := &gcpgenserver.BackupUpdateV1beta{
			Description: "Updated description",
		}
		params := gcpgenserver.V1betaInternalUpdateBackupParams{
			BackupVaultId:  "vault-123",
			BackupId:       "backup-123",
			ProjectNumber:  "project-123",
			LocationId:     "us-east4",
			XCorrelationID: gcpgenserver.NewOptString("correlation-id"),
		}

		defer func() {
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "", nil
		}

		mockOrchestrator.EXPECT().UpdateBackupInternal(ctx, mock.Anything).Return(nil, "", errors.New("Internal server error"))

		resp, err := handler.V1betaInternalUpdateBackup(ctx, req, params)
		assert.Error(tt, err)
		assert.IsType(tt, &gcpgenserver.V1betaInternalUpdateBackupInternalServerError{}, resp)
		internalErr := resp.(*gcpgenserver.V1betaInternalUpdateBackupInternalServerError)
		assert.Equal(tt, float64(500), internalErr.Code)
		assert.Equal(tt, "Internal server error", internalErr.Message)
	})

	t.Run("WhenGetBackupByExternalUUIDFails", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		req := &gcpgenserver.BackupUpdateV1beta{
			Description: "Updated description",
		}
		params := gcpgenserver.V1betaInternalUpdateBackupParams{
			BackupVaultId:  "vault-123",
			BackupId:       "backup-123",
			ProjectNumber:  "project-123",
			LocationId:     "us-east4",
			XCorrelationID: gcpgenserver.NewOptString("correlation-id"),
		}

		defer func() {
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "", nil
		}

		mockOrchestrator.EXPECT().UpdateBackupInternal(ctx, mock.Anything).Return(&models.Backup{}, "", nil)
		mockOrchestrator.EXPECT().GetBackupByExternalUUID(ctx, params.BackupVaultId, params.BackupId, params.ProjectNumber).Return(nil, errors.New("Database error"))

		resp, err := handler.V1betaInternalUpdateBackup(ctx, req, params)
		assert.Error(tt, err)
		assert.IsType(tt, &gcpgenserver.V1betaInternalUpdateBackupInternalServerError{}, resp)
		internalErr := resp.(*gcpgenserver.V1betaInternalUpdateBackupInternalServerError)
		assert.Equal(tt, float64(500), internalErr.Code)
		assert.Equal(tt, "Database error", internalErr.Message)
	})

	t.Run("WhenSuccessSynchronousOperation", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		req := &gcpgenserver.BackupUpdateV1beta{
			Description: "Updated description",
		}
		params := gcpgenserver.V1betaInternalUpdateBackupParams{
			BackupVaultId:  "vault-123",
			BackupId:       "backup-123",
			ProjectNumber:  "project-123",
			LocationId:     "us-east4",
			XCorrelationID: gcpgenserver.NewOptString("correlation-id"),
		}

		defer func() {
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "", nil
		}

		sourceRegionName := "us-east4"
		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{
				UUID: "vault-123",
			},
			Name:             "test-backup-vault",
			SourceRegionName: &sourceRegionName,
			BucketDetails:    datamodel.BucketDetailsArray{&datamodel.BucketDetails{BucketName: "test-bucket", SatisfiesPzi: true, SatisfiesPzs: true}},
		}
		backup := &datamodel.Backup{
			BaseModel: datamodel.BaseModel{
				UUID:      "backup-123",
				CreatedAt: time.Now(),
			},
			State:         datamodel.LifeCycleStateAvailable,
			Name:          "test-backup",
			VolumeUUID:    "volume-uuid-456",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:          "test-bucket",
				UseExistingSnapshot: false,
				RestoreVolumeCount:  0,
			},
			SizeInBytes:             int64(1000),
			LatestLogicalBackupSize: int64(500),
			Description:             "Updated description",
			Type:                    "MANUAL",
		}

		mockOrchestrator.EXPECT().UpdateBackupInternal(ctx, mock.Anything).Return(&models.Backup{}, "", nil)
		mockOrchestrator.EXPECT().GetBackupByExternalUUID(ctx, params.BackupVaultId, params.BackupId, params.ProjectNumber).Return(backup, nil)

		resp, err := handler.V1betaInternalUpdateBackup(ctx, req, params)
		assert.NoError(tt, err)
		assert.IsType(tt, &gcpgenserver.InternalBackupV1beta{}, resp)
		internalBackup := resp.(*gcpgenserver.InternalBackupV1beta)
		assert.True(tt, internalBackup.ResourceId.Set)
		assert.Equal(tt, "test-backup", internalBackup.ResourceId.Value)
	})

	t.Run("WhenSuccessAsynchronousOperation", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		req := &gcpgenserver.BackupUpdateV1beta{
			Description: "Updated description",
		}
		params := gcpgenserver.V1betaInternalUpdateBackupParams{
			BackupVaultId:  "vault-123",
			BackupId:       "backup-123",
			ProjectNumber:  "project-123",
			LocationId:     "us-east4",
			XCorrelationID: gcpgenserver.NewOptString("correlation-id"),
		}

		defer func() {
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "", nil
		}

		sourceRegionName := "us-east4"
		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{
				UUID: "vault-123",
			},
			Name:             "test-backup-vault",
			SourceRegionName: &sourceRegionName,
			BucketDetails:    datamodel.BucketDetailsArray{&datamodel.BucketDetails{BucketName: "test-bucket", SatisfiesPzi: true, SatisfiesPzs: true}},
		}
		backup := &datamodel.Backup{
			BaseModel: datamodel.BaseModel{
				UUID:      "backup-123",
				CreatedAt: time.Now(),
			},
			State:         datamodel.LifeCycleStateAvailable,
			Name:          "test-backup",
			VolumeUUID:    "volume-uuid-456",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:          "test-bucket",
				UseExistingSnapshot: false,
				RestoreVolumeCount:  0,
			},
			SizeInBytes:             int64(1000),
			LatestLogicalBackupSize: int64(500),
			Description:             "Updated description",
			Type:                    "MANUAL",
		}

		jobID := "job-uuid-123"
		mockOrchestrator.EXPECT().UpdateBackupInternal(ctx, mock.Anything).Return(&models.Backup{}, jobID, nil)
		mockOrchestrator.EXPECT().GetBackupByExternalUUID(ctx, params.BackupVaultId, params.BackupId, params.ProjectNumber).Return(backup, nil)

		resp, err := handler.V1betaInternalUpdateBackup(ctx, req, params)
		assert.NoError(tt, err)
		assert.IsType(tt, &gcpgenserver.OperationV1beta{}, resp)
		operation := resp.(*gcpgenserver.OperationV1beta)
		assert.True(tt, operation.Name.IsSet())
		assert.Contains(tt, operation.Name.Value, jobID)
		assert.False(tt, operation.Done.Value)
	})
}

func TestConvertInternalBackupVaultToDataModelFunction(t *testing.T) {
	t.Run("AllFieldsAreMapped", func(tt *testing.T) {
		req := &gcpgenserver.BackupVaultInternalV1beta{
			BackupVaultId:         "backup-vault-id",
			ResourceId:            "resource-id",
			AccountVendorId:       "project-number",
			BackupVaultType:       gcpgenserver.BackupVaultInternalV1betaBackupVaultTypeCROSSREGION,
			LifeCycleState:        gcpgenserver.BackupVaultInternalV1betaLifeCycleStateCREATING,
			Description:           gcpgenserver.NewOptString("description"),
			BackupRegion:          gcpgenserver.NewOptString("us-east1"),
			SourceRegion:          gcpgenserver.NewOptString("us-west1"),
			LifeCycleStateDetails: gcpgenserver.NewOptString("creating destination resources"),
			ImmutableAttributes: gcpgenserver.NewOptBackupVaultInternalV1betaImmutableAttributes(gcpgenserver.BackupVaultInternalV1betaImmutableAttributes{
				IsDailyBackupImmutable:                 gcpgenserver.NewOptBool(true),
				IsWeeklyBackupImmutable:                gcpgenserver.NewOptBool(true),
				IsMonthlyBackupImmutable:               gcpgenserver.NewOptBool(false),
				IsAdhocBackupImmutable:                 gcpgenserver.NewOptBool(true),
				BackupMinimumEnforcedRetentionDuration: gcpgenserver.NewOptInt(14),
			}),
			BucketDetails: []gcpgenserver.BackupVaultInternalV1betaBucketDetailsItem{
				{
					BucketName:          gcpgenserver.NewOptString("bucket-name"),
					ServiceAccountName:  gcpgenserver.NewOptString("service-account"),
					VendorSubnetId:      gcpgenserver.NewOptString("subnet-id"),
					TenantProjectNumber: gcpgenserver.NewOptString("tenant-project"),
					SatisfiesPzs:        gcpgenserver.NewOptBool(true),
					SatisfiesPzi:        gcpgenserver.NewOptBool(false),
				},
			},
			KmsConfigResourcePath:    gcpgenserver.NewOptString("projects/p/locations/l/kmsConfigs/c"),
			EncryptionState:          gcpgenserver.NewOptBackupVaultInternalV1betaEncryptionState(gcpgenserver.BackupVaultInternalV1betaEncryptionStateENCRYPTIONSTATECOMPLETED),
			BackupsPrimaryKeyVersion: gcpgenserver.NewOptString("projects/p/locations/l/keyRings/r/cryptoKeys/k/cryptoKeyVersions/1"),
		}

		result := _convertInternalBackupVaultToDataModel(req)

		assert.NotNil(tt, result)
		assert.Equal(tt, "resource-id", result.Name)
		assert.Equal(tt, "project-number", result.AccountVendorID)
		assert.Equal(tt, "CREATING", result.LifeCycleState)
		assert.Equal(tt, "CROSS_REGION", result.BackupVaultType)
		assert.Equal(tt, datamodel.ServiceTypeGCNV, result.ServiceType)

		assert.NotNil(tt, result.Description)
		assert.Equal(tt, "description", *result.Description)
		assert.NotNil(tt, result.BackupRegionName)
		assert.Equal(tt, "us-east1", *result.BackupRegionName)
		assert.NotNil(tt, result.SourceRegionName)
		assert.Equal(tt, "us-west1", *result.SourceRegionName)
		assert.Equal(tt, "creating destination resources", result.LifeCycleStateDetails)

		assert.NotNil(tt, result.ImmutableAttributes)
		assert.True(tt, result.ImmutableAttributes.IsDailyBackupImmutable)
		assert.True(tt, result.ImmutableAttributes.IsWeeklyBackupImmutable)
		assert.False(tt, result.ImmutableAttributes.IsMonthlyBackupImmutable)
		assert.True(tt, result.ImmutableAttributes.IsAdhocBackupImmutable)
		assert.NotNil(tt, result.ImmutableAttributes.BackupMinimumEnforcedRetentionDuration)
		assert.EqualValues(tt, 14, *result.ImmutableAttributes.BackupMinimumEnforcedRetentionDuration)

		assert.Len(tt, result.BucketDetails, 1)
		assert.Equal(tt, "bucket-name", result.BucketDetails[0].BucketName)
		assert.Equal(tt, "service-account", result.BucketDetails[0].ServiceAccountName)
		assert.Equal(tt, "subnet-id", result.BucketDetails[0].VendorSubnetID)
		assert.Equal(tt, "tenant-project", result.BucketDetails[0].TenantProjectNumber)
		assert.True(tt, result.BucketDetails[0].SatisfiesPzs)
		assert.False(tt, result.BucketDetails[0].SatisfiesPzi)

		assert.NotNil(tt, result.CmekAttributes)
		assert.NotNil(tt, result.CmekAttributes.KmsConfigResourcePath)
		assert.Equal(tt, "projects/p/locations/l/kmsConfigs/c", *result.CmekAttributes.KmsConfigResourcePath)
		assert.NotNil(tt, result.CmekAttributes.EncryptionState)
		assert.Equal(tt, "ENCRYPTION_STATE_COMPLETED", *result.CmekAttributes.EncryptionState)
		assert.NotNil(tt, result.CmekAttributes.BackupsPrimaryKeyVersion)
		assert.Equal(tt, "projects/p/locations/l/keyRings/r/cryptoKeys/k/cryptoKeyVersions/1", *result.CmekAttributes.BackupsPrimaryKeyVersion)
	})

	t.Run("OptionalFieldsUnsetRemainEmpty", func(tt *testing.T) {
		req := &gcpgenserver.BackupVaultInternalV1beta{
			ResourceId:      "resource-id",
			AccountVendorId: "project-number",
			BackupVaultType: gcpgenserver.BackupVaultInternalV1betaBackupVaultTypeINREGION,
			LifeCycleState:  gcpgenserver.BackupVaultInternalV1betaLifeCycleStateCREATING,
		}

		result := _convertInternalBackupVaultToDataModel(req)

		assert.NotNil(tt, result)
		assert.Nil(tt, result.Description)
		assert.Nil(tt, result.BackupRegionName)
		assert.Nil(tt, result.SourceRegionName)
		assert.Equal(tt, "", result.LifeCycleStateDetails)
		assert.Nil(tt, result.ImmutableAttributes)
		assert.Empty(tt, result.BucketDetails)
		assert.Nil(tt, result.CmekAttributes)
		assert.Equal(tt, datamodel.ServiceTypeGCNV, result.ServiceType)
	})

	t.Run("ServiceTypeCrossProjectIsPreserved", func(tt *testing.T) {
		req := &gcpgenserver.BackupVaultInternalV1beta{
			ResourceId:      "resource-id",
			AccountVendorId: "project-number",
			BackupVaultType: gcpgenserver.BackupVaultInternalV1betaBackupVaultTypeCROSSREGION,
			LifeCycleState:  gcpgenserver.BackupVaultInternalV1betaLifeCycleStateCREATING,
			ServiceType:     gcpgenserver.NewOptString(datamodel.ServiceTypeCrossProject),
		}

		result := _convertInternalBackupVaultToDataModel(req)

		assert.NotNil(tt, result)
		assert.Equal(tt, datamodel.ServiceTypeCrossProject, result.ServiceType)
	})

	t.Run("EmptyServiceTypeDefaultsToGCNV", func(tt *testing.T) {
		req := &gcpgenserver.BackupVaultInternalV1beta{
			ResourceId:      "resource-id",
			AccountVendorId: "project-number",
			BackupVaultType: gcpgenserver.BackupVaultInternalV1betaBackupVaultTypeINREGION,
			LifeCycleState:  gcpgenserver.BackupVaultInternalV1betaLifeCycleStateCREATING,
			ServiceType:     gcpgenserver.NewOptString(""),
		}

		result := _convertInternalBackupVaultToDataModel(req)

		assert.NotNil(tt, result)
		assert.Equal(tt, datamodel.ServiceTypeGCNV, result.ServiceType)
	})
}

func TestConvertDataModelToBackupVaultInternalServiceType(t *testing.T) {
	t.Run("CrossProjectServiceTypeIsEmitted", func(tt *testing.T) {
		bv := &datamodel.BackupVault{
			Name:            "resource-id",
			AccountVendorID: "project-number",
			BackupVaultType: "CROSS_REGION",
			LifeCycleState:  "READY",
			ServiceType:     datamodel.ServiceTypeCrossProject,
		}

		result := convertDataModelToBackupVaultInternal(bv)

		assert.True(tt, result.ServiceType.IsSet())
		assert.Equal(tt, datamodel.ServiceTypeCrossProject, result.ServiceType.Value)
	})

	t.Run("EmptyServiceTypeIsNotEmitted", func(tt *testing.T) {
		bv := &datamodel.BackupVault{
			Name:            "resource-id",
			AccountVendorID: "project-number",
			BackupVaultType: "IN_REGION",
			LifeCycleState:  "READY",
		}

		result := convertDataModelToBackupVaultInternal(bv)

		assert.False(tt, result.ServiceType.IsSet())
	})
}

func TestCreateInternalBackupParams(t *testing.T) {
	t.Run("WhenAllFieldsSet", func(tt *testing.T) {
		completionTime := time.Now()
		snapshotCreationTime := time.Now().Add(-1 * time.Hour)
		req := &gcpgenserver.InternalBackupCreateV1beta{
			ResourceId:               "test-backup",
			BackupUUID:               "backup-uuid-123",
			VolumeId:                 "volume-uuid-456",
			VolumeName:               "test-volume",
			Protocols:                []gcpgenserver.InternalBackupCreateV1betaProtocolsItem{gcpgenserver.InternalBackupCreateV1betaProtocolsItemNFSV3, gcpgenserver.InternalBackupCreateV1betaProtocolsItemSMB},
			Description:              gcpgenserver.NewOptString("Test description"),
			SnapshotId:               gcpgenserver.NewOptString("snapshot-id-123"),
			SnapshotName:             gcpgenserver.NewOptString("snapshot-name"),
			UseExistingSnapshot:      gcpgenserver.NewOptBool(true),
			BucketName:               gcpgenserver.NewOptString("bucket-name"),
			EndpointUuid:             gcpgenserver.NewOptString("endpoint-uuid"),
			IsRegionalHa:             gcpgenserver.NewOptBool(true),
			CompletionTime:           gcpgenserver.NewOptDateTime(completionTime),
			BackupPolicyName:         gcpgenserver.NewOptString("policy-name"),
			OntapVolumeStyle:         gcpgenserver.NewOptString("flexvol"),
			SourceVolumeZone:         gcpgenserver.NewOptString("us-east4-a"),
			ServiceAccountName:       gcpgenserver.NewOptString("sa-name"),
			SnapshotCreationTime:     gcpgenserver.NewOptDateTime(snapshotCreationTime),
			ConstituentCountOfBackup: gcpgenserver.NewOptInt32(5),
			BackupType:               gcpgenserver.NewOptInternalBackupCreateV1betaBackupType("MANUAL"),
		}
		params := gcpgenserver.V1betaInternalCreateBackupParams{
			BackupVaultId:  "vault-123",
			ProjectNumber:  "project-123",
			LocationId:     "us-east4",
			XCorrelationID: gcpgenserver.NewOptString("correlation-id"),
		}

		result := createInternalBackupParams(req, params)

		assert.Equal(tt, params.ProjectNumber, result.AccountName)
		assert.Equal(tt, params.BackupVaultId, result.BackupVaultID)
		assert.Equal(tt, req.VolumeId, result.VolumeUUID)
		assert.Equal(tt, req.ResourceId, result.BackupName)
		assert.Equal(tt, req.BackupUUID, result.BackupUUID)
		assert.Equal(tt, utils.BackupTypeMANUAL, result.BackupType)
		assert.Equal(tt, params.LocationId, result.LocationID)
		assert.Equal(tt, req.VolumeName, result.VolumeName)
		assert.Equal(tt, []string{"NFSV3", "SMB"}, result.Protocols)
		assert.Equal(tt, "Test description", result.Description)
		assert.Equal(tt, "snapshot-id-123", result.SnapshotID)
		assert.Equal(tt, "snapshot-name", result.SnapshotName)
		assert.True(tt, result.UseExistingSnapshot)
		assert.Equal(tt, "bucket-name", result.BucketName)
		assert.Equal(tt, "endpoint-uuid", result.EndpointUUID)
		assert.True(tt, result.IsRegionalHA)
		assert.Equal(tt, completionTime.Format(time.RFC3339), result.CompletionTime)
		assert.Equal(tt, "policy-name", result.BackupPolicyName)
		assert.Equal(tt, "flexvol", result.OntapVolumeStyle)
		assert.Equal(tt, "us-east4-a", result.SourceVolumeZone)
		assert.Equal(tt, "sa-name", result.ServiceAccountName)
		assert.Equal(tt, snapshotCreationTime.Format(time.RFC3339), result.SnapshotCreationTime)
		assert.Equal(tt, int32(5), result.ConstituentCountOfBackup)
		assert.Equal(tt, "correlation-id", result.XCorrelationID)
	})

	t.Run("WhenOptionalFieldsNotSet", func(tt *testing.T) {
		req := &gcpgenserver.InternalBackupCreateV1beta{
			ResourceId: "test-backup",
			BackupUUID: "backup-uuid-123",
			VolumeId:   "volume-uuid-456",
			VolumeName: "test-volume",
			Protocols:  []gcpgenserver.InternalBackupCreateV1betaProtocolsItem{gcpgenserver.InternalBackupCreateV1betaProtocolsItemNFSV3},
		}
		params := gcpgenserver.V1betaInternalCreateBackupParams{
			BackupVaultId:  "vault-123",
			ProjectNumber:  "project-123",
			LocationId:     "us-east4",
			XCorrelationID: gcpgenserver.OptString{},
		}

		result := createInternalBackupParams(req, params)

		assert.Equal(tt, params.ProjectNumber, result.AccountName)
		assert.Equal(tt, params.BackupVaultId, result.BackupVaultID)
		assert.Equal(tt, req.VolumeId, result.VolumeUUID)
		assert.Equal(tt, req.ResourceId, result.BackupName)
		assert.Equal(tt, req.BackupUUID, result.BackupUUID)
		assert.Equal(tt, "", result.BackupType)
		assert.Equal(tt, params.LocationId, result.LocationID)
		assert.Equal(tt, req.VolumeName, result.VolumeName)
		assert.Equal(tt, []string{"NFSV3"}, result.Protocols)
		assert.Equal(tt, "", result.Description)
		assert.Equal(tt, "", result.SnapshotID)
		assert.Equal(tt, "", result.SnapshotName)
		assert.False(tt, result.UseExistingSnapshot)
		assert.Equal(tt, "", result.BucketName)
		assert.Equal(tt, "", result.EndpointUUID)
		assert.False(tt, result.IsRegionalHA)
		assert.Equal(tt, "", result.CompletionTime)
		assert.Equal(tt, "", result.BackupPolicyName)
		assert.Equal(tt, "", result.OntapVolumeStyle)
		assert.Equal(tt, "", result.SourceVolumeZone)
		assert.Equal(tt, "", result.ServiceAccountName)
		assert.Equal(tt, "", result.SnapshotCreationTime)
		assert.Equal(tt, int32(0), result.ConstituentCountOfBackup)
		assert.Equal(tt, "", result.XCorrelationID)
	})

	t.Run("WhenProtocolsEmpty", func(tt *testing.T) {
		req := &gcpgenserver.InternalBackupCreateV1beta{
			ResourceId: "test-backup",
			BackupUUID: "backup-uuid-123",
			VolumeId:   "volume-uuid-456",
			VolumeName: "test-volume",
			Protocols:  []gcpgenserver.InternalBackupCreateV1betaProtocolsItem{},
		}
		params := gcpgenserver.V1betaInternalCreateBackupParams{
			BackupVaultId:  "vault-123",
			ProjectNumber:  "project-123",
			LocationId:     "us-east4",
			XCorrelationID: gcpgenserver.NewOptString("correlation-id"),
		}

		result := createInternalBackupParams(req, params)

		assert.NotNil(tt, result.Protocols)
		assert.Equal(tt, 0, len(result.Protocols))
	})

	t.Run("WhenAllProtocolTypes", func(tt *testing.T) {
		req := &gcpgenserver.InternalBackupCreateV1beta{
			ResourceId: "test-backup",
			BackupUUID: "backup-uuid-123",
			VolumeId:   "volume-uuid-456",
			VolumeName: "test-volume",
			Protocols: []gcpgenserver.InternalBackupCreateV1betaProtocolsItem{
				gcpgenserver.InternalBackupCreateV1betaProtocolsItemNFSV3,
				gcpgenserver.InternalBackupCreateV1betaProtocolsItemNFSV4,
				gcpgenserver.InternalBackupCreateV1betaProtocolsItemSMB,
				gcpgenserver.InternalBackupCreateV1betaProtocolsItemISCSI,
			},
		}
		params := gcpgenserver.V1betaInternalCreateBackupParams{
			BackupVaultId:  "vault-123",
			ProjectNumber:  "project-123",
			LocationId:     "us-east4",
			XCorrelationID: gcpgenserver.NewOptString("correlation-id"),
		}

		result := createInternalBackupParams(req, params)

		assert.Equal(tt, []string{"NFSV3", "NFSV4", "SMB", "ISCSI"}, result.Protocols)
	})

	t.Run("WhenIsOntapBackupTrue_SetsIsExpertModeVolume", func(tt *testing.T) {
		req := &gcpgenserver.InternalBackupCreateV1beta{
			ResourceId:    "test-backup",
			BackupUUID:    "backup-uuid-123",
			VolumeId:      "volume-uuid-456",
			VolumeName:    "test-volume",
			IsOntapBackup: gcpgenserver.NewOptBool(true),
		}
		params := gcpgenserver.V1betaInternalCreateBackupParams{
			BackupVaultId: "vault-123",
			ProjectNumber: "project-123",
			LocationId:    "us-east4",
		}

		result := createInternalBackupParams(req, params)

		assert.True(tt, result.IsExpertModeVolume)
	})

	t.Run("WhenIsOntapBackupFalse_IsExpertModeVolumeFalse", func(tt *testing.T) {
		req := &gcpgenserver.InternalBackupCreateV1beta{
			ResourceId:    "test-backup",
			BackupUUID:    "backup-uuid-123",
			VolumeId:      "volume-uuid-456",
			VolumeName:    "test-volume",
			IsOntapBackup: gcpgenserver.NewOptBool(false),
		}
		params := gcpgenserver.V1betaInternalCreateBackupParams{
			BackupVaultId: "vault-123",
			ProjectNumber: "project-123",
			LocationId:    "us-east4",
		}

		result := createInternalBackupParams(req, params)

		assert.False(tt, result.IsExpertModeVolume)
	})

	t.Run("WhenIsOntapBackupNotSet_IsExpertModeVolumeFalse", func(tt *testing.T) {
		req := &gcpgenserver.InternalBackupCreateV1beta{
			ResourceId: "test-backup",
			BackupUUID: "backup-uuid-123",
			VolumeId:   "volume-uuid-456",
			VolumeName: "test-volume",
			// IsOntapBackup not set
		}
		params := gcpgenserver.V1betaInternalCreateBackupParams{
			BackupVaultId: "vault-123",
			ProjectNumber: "project-123",
			LocationId:    "us-east4",
		}

		result := createInternalBackupParams(req, params)

		assert.False(tt, result.IsExpertModeVolume)
	})
}

func TestV1betaInternalCreateBackupVaultWithVCPOrchestration(t *testing.T) {
	origCVPHost := cvp.CVP_HOST
	origOrchestration := env.EnableBackupVaultVCPOrchestration
	defer func() {
		cvp.CVP_HOST = origCVPHost
		env.EnableBackupVaultVCPOrchestration = origOrchestration
	}()
	cvp.CVP_HOST = "localhost:8009"
	env.EnableBackupVaultVCPOrchestration = true

	t.Run("WhenSDECreateFails_ReturnsErrorAndDoesNotPersist", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		req := &gcpgenserver.BackupVaultInternalV1beta{
			BackupVaultId: "source-external-uuid",
			ResourceId:    "test-resource-id",
			SourceRegion:  gcpgenserver.NewOptString("us-east4"),
		}
		params := gcpgenserver.V1betaInternalCreateBackupVaultParams{
			ProjectNumber: "test-project",
			LocationId:    "us-west1",
		}

		originalCreateBVInSDE := createBackupVaultInSDE
		defer func() { createBackupVaultInSDE = originalCreateBVInSDE }()
		createBackupVaultInSDE = func(_ context.Context, _ *cvpmodels.BackupVaultCreateV1beta, _, _ string, _ log.Logger) (*backup_vault.V1betaCreateBackupVaultAccepted, gcpgenserver.V1betaCreateBackupVaultRes, error) {
			return nil, nil, errors.New("SDE create failed")
		}

		resp, err := handler.V1betaInternalCreateBackupVault(context.Background(), req, params)
		assert.Error(tt, err)
		result, ok := resp.(*gcpgenserver.V1betaInternalCreateBackupVaultInternalServerError)
		assert.True(tt, ok)
		assert.Equal(tt, float64(500), result.Code)
		assert.Contains(tt, result.Message, "Failed to create destination backup vault in SDE")
		mockOrchestrator.AssertNotCalled(tt, "CreateBackupVaultEntryInVCP", mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("WhenSDEReturnsErrorResponse_ReturnsInternalServerError", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		req := &gcpgenserver.BackupVaultInternalV1beta{
			BackupVaultId: "source-external-uuid",
			ResourceId:    "test-resource-id",
			SourceRegion:  gcpgenserver.NewOptString("us-east4"),
		}
		params := gcpgenserver.V1betaInternalCreateBackupVaultParams{
			ProjectNumber: "test-project",
			LocationId:    "us-west1",
		}

		originalCreateBVInSDE := createBackupVaultInSDE
		defer func() { createBackupVaultInSDE = originalCreateBVInSDE }()
		createBackupVaultInSDE = func(_ context.Context, _ *cvpmodels.BackupVaultCreateV1beta, _, _ string, _ log.Logger) (*backup_vault.V1betaCreateBackupVaultAccepted, gcpgenserver.V1betaCreateBackupVaultRes, error) {
			errRes := &gcpgenserver.V1betaCreateBackupVaultInternalServerError{
				Code:    500,
				Message: "SDE internal error",
			}
			return nil, errRes, nil
		}

		resp, err := handler.V1betaInternalCreateBackupVault(context.Background(), req, params)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "SDE create backup vault failed")
		result, ok := resp.(*gcpgenserver.V1betaInternalCreateBackupVaultInternalServerError)
		assert.True(tt, ok)
		assert.Equal(tt, float64(500), result.Code)
		assert.Contains(tt, result.Message, "Failed to create destination backup vault in SDE")
		mockOrchestrator.AssertNotCalled(tt, "CreateBackupVaultEntryInVCP", mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("WhenSDEReturnsBadRequest_ReturnsBadRequest", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		req := &gcpgenserver.BackupVaultInternalV1beta{
			BackupVaultId: "source-external-uuid",
			ResourceId:    "test-resource-id",
			SourceRegion:  gcpgenserver.NewOptString("us-east4"),
		}
		params := gcpgenserver.V1betaInternalCreateBackupVaultParams{
			ProjectNumber: "test-project",
			LocationId:    "us-west1",
		}

		originalCreateBVInSDE := createBackupVaultInSDE
		defer func() { createBackupVaultInSDE = originalCreateBVInSDE }()
		createBackupVaultInSDE = func(_ context.Context, _ *cvpmodels.BackupVaultCreateV1beta, _, _ string, _ log.Logger) (*backup_vault.V1betaCreateBackupVaultAccepted, gcpgenserver.V1betaCreateBackupVaultRes, error) {
			return nil, &gcpgenserver.V1betaCreateBackupVaultBadRequest{Code: 400, Message: "invalid vault config"}, nil
		}

		resp, err := handler.V1betaInternalCreateBackupVault(context.Background(), req, params)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "SDE create backup vault failed")
		result, ok := resp.(*gcpgenserver.V1betaInternalCreateBackupVaultBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, float64(400), result.Code)
		assert.Equal(tt, "invalid vault config", result.Message)
	})

	t.Run("WhenSDEReturnsConflict_ReturnsConflict", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		req := &gcpgenserver.BackupVaultInternalV1beta{
			BackupVaultId: "source-external-uuid",
			ResourceId:    "test-resource-id",
			SourceRegion:  gcpgenserver.NewOptString("us-east4"),
		}
		params := gcpgenserver.V1betaInternalCreateBackupVaultParams{
			ProjectNumber: "test-project",
			LocationId:    "us-west1",
		}

		originalCreateBVInSDE := createBackupVaultInSDE
		defer func() { createBackupVaultInSDE = originalCreateBVInSDE }()
		createBackupVaultInSDE = func(_ context.Context, _ *cvpmodels.BackupVaultCreateV1beta, _, _ string, _ log.Logger) (*backup_vault.V1betaCreateBackupVaultAccepted, gcpgenserver.V1betaCreateBackupVaultRes, error) {
			return nil, &gcpgenserver.V1betaCreateBackupVaultConflict{Code: 409, Message: "backup vault already exists"}, nil
		}

		resp, err := handler.V1betaInternalCreateBackupVault(context.Background(), req, params)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "SDE create backup vault failed")
		result, ok := resp.(*gcpgenserver.V1betaInternalCreateBackupVaultConflict)
		assert.True(tt, ok)
		assert.Equal(tt, float64(409), result.Code)
		assert.Equal(tt, "backup vault already exists", result.Message)
	})

	t.Run("WhenSDEReturnsForbidden_ReturnsForbidden", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		req := &gcpgenserver.BackupVaultInternalV1beta{
			BackupVaultId: "source-external-uuid",
			ResourceId:    "test-resource-id",
			SourceRegion:  gcpgenserver.NewOptString("us-east4"),
		}
		params := gcpgenserver.V1betaInternalCreateBackupVaultParams{
			ProjectNumber: "test-project",
			LocationId:    "us-west1",
		}

		originalCreateBVInSDE := createBackupVaultInSDE
		defer func() { createBackupVaultInSDE = originalCreateBVInSDE }()
		createBackupVaultInSDE = func(_ context.Context, _ *cvpmodels.BackupVaultCreateV1beta, _, _ string, _ log.Logger) (*backup_vault.V1betaCreateBackupVaultAccepted, gcpgenserver.V1betaCreateBackupVaultRes, error) {
			return nil, &gcpgenserver.V1betaCreateBackupVaultForbidden{Code: 403, Message: "operation not permitted"}, nil
		}

		resp, err := handler.V1betaInternalCreateBackupVault(context.Background(), req, params)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "SDE create backup vault failed")
		result, ok := resp.(*gcpgenserver.V1betaInternalCreateBackupVaultForbidden)
		assert.True(tt, ok)
		assert.Equal(tt, float64(403), result.Code)
		assert.Equal(tt, "operation not permitted", result.Message)
	})

	t.Run("WhenSDEReturnsUnauthorized_ReturnsUnauthorized", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		req := &gcpgenserver.BackupVaultInternalV1beta{
			BackupVaultId: "source-external-uuid",
			ResourceId:    "test-resource-id",
			SourceRegion:  gcpgenserver.NewOptString("us-east4"),
		}
		params := gcpgenserver.V1betaInternalCreateBackupVaultParams{
			ProjectNumber: "test-project",
			LocationId:    "us-west1",
		}

		originalCreateBVInSDE := createBackupVaultInSDE
		defer func() { createBackupVaultInSDE = originalCreateBVInSDE }()
		createBackupVaultInSDE = func(_ context.Context, _ *cvpmodels.BackupVaultCreateV1beta, _, _ string, _ log.Logger) (*backup_vault.V1betaCreateBackupVaultAccepted, gcpgenserver.V1betaCreateBackupVaultRes, error) {
			return nil, &gcpgenserver.V1betaCreateBackupVaultUnauthorized{Code: 401, Message: "missing credentials"}, nil
		}

		resp, err := handler.V1betaInternalCreateBackupVault(context.Background(), req, params)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "SDE create backup vault failed")
		result, ok := resp.(*gcpgenserver.V1betaInternalCreateBackupVaultUnauthorized)
		assert.True(tt, ok)
		assert.Equal(tt, float64(401), result.Code)
		assert.Equal(tt, "missing credentials", result.Message)
	})

	t.Run("WhenSDEReturnsUnprocessableEntity_ReturnsUnprocessableEntity", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		req := &gcpgenserver.BackupVaultInternalV1beta{
			BackupVaultId: "source-external-uuid",
			ResourceId:    "test-resource-id",
			SourceRegion:  gcpgenserver.NewOptString("us-east4"),
		}
		params := gcpgenserver.V1betaInternalCreateBackupVaultParams{
			ProjectNumber: "test-project",
			LocationId:    "us-west1",
		}

		originalCreateBVInSDE := createBackupVaultInSDE
		defer func() { createBackupVaultInSDE = originalCreateBVInSDE }()
		createBackupVaultInSDE = func(_ context.Context, _ *cvpmodels.BackupVaultCreateV1beta, _, _ string, _ log.Logger) (*backup_vault.V1betaCreateBackupVaultAccepted, gcpgenserver.V1betaCreateBackupVaultRes, error) {
			return nil, &gcpgenserver.V1betaCreateBackupVaultUnprocessableEntity{Code: 422, Message: "semantic validation failed"}, nil
		}

		resp, err := handler.V1betaInternalCreateBackupVault(context.Background(), req, params)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "SDE create backup vault failed")
		result, ok := resp.(*gcpgenserver.V1betaInternalCreateBackupVaultUnprocessableEntity)
		assert.True(tt, ok)
		assert.Equal(tt, float64(422), result.Code)
		assert.Equal(tt, "semantic validation failed", result.Message)
	})

	t.Run("WhenAllAttributesSet_PropagatesAllFieldsToSDE", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		retentionDuration := 30
		req := &gcpgenserver.BackupVaultInternalV1beta{
			BackupVaultId:            "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
			ResourceId:               "source-vault",
			Description:              gcpgenserver.NewOptString("my vault description"),
			BackupRegion:             gcpgenserver.NewOptString("us-west1"),
			SourceRegion:             gcpgenserver.NewOptString("us-east4"),
			KmsConfigResourcePath:    gcpgenserver.NewOptString("projects/p/locations/l/keyRings/kr/cryptoKeys/k"),
			BackupsPrimaryKeyVersion: gcpgenserver.NewOptString("projects/p/locations/l/keyRings/kr/cryptoKeys/k/cryptoKeyVersions/1"),
			ImmutableAttributes: gcpgenserver.NewOptBackupVaultInternalV1betaImmutableAttributes(
				gcpgenserver.BackupVaultInternalV1betaImmutableAttributes{
					IsDailyBackupImmutable:                 gcpgenserver.NewOptBool(true),
					IsWeeklyBackupImmutable:                gcpgenserver.NewOptBool(true),
					IsMonthlyBackupImmutable:               gcpgenserver.NewOptBool(false),
					IsAdhocBackupImmutable:                 gcpgenserver.NewOptBool(true),
					BackupMinimumEnforcedRetentionDuration: gcpgenserver.NewOptInt(retentionDuration),
				},
			),
		}
		params := gcpgenserver.V1betaInternalCreateBackupVaultParams{
			ProjectNumber: "test-project",
			LocationId:    "us-west1",
		}

		var capturedBody *cvpmodels.BackupVaultCreateV1beta
		originalCreateBVInSDE := createBackupVaultInSDE
		defer func() { createBackupVaultInSDE = originalCreateBVInSDE }()
		createBackupVaultInSDE = func(_ context.Context, body *cvpmodels.BackupVaultCreateV1beta, _, _ string, _ log.Logger) (*backup_vault.V1betaCreateBackupVaultAccepted, gcpgenserver.V1betaCreateBackupVaultRes, error) {
			capturedBody = body
			return nil, nil, errors.New("stop here")
		}

		_, _ = handler.V1betaInternalCreateBackupVault(context.Background(), req, params)

		assert.NotNil(tt, capturedBody)
		assert.NotNil(tt, capturedBody.Description)
		assert.Equal(tt, "my vault description", *capturedBody.Description)
		assert.NotNil(tt, capturedBody.BackupRegion)
		assert.Equal(tt, "us-west1", *capturedBody.BackupRegion)
		assert.NotNil(tt, capturedBody.SourceRegion)
		assert.Equal(tt, "us-east4", *capturedBody.SourceRegion)
		assert.NotNil(tt, capturedBody.KmsConfigResourcePath)
		assert.Equal(tt, "projects/p/locations/l/keyRings/kr/cryptoKeys/k", *capturedBody.KmsConfigResourcePath)
		assert.NotNil(tt, capturedBody.BackupsPrimaryKeyVersion)
		assert.Equal(tt, "projects/p/locations/l/keyRings/kr/cryptoKeys/k/cryptoKeyVersions/1", *capturedBody.BackupsPrimaryKeyVersion)
		assert.NotNil(tt, capturedBody.BackupRetentionPolicy)
		assert.True(tt, capturedBody.BackupRetentionPolicy.DailyBackupImmutable)
		assert.True(tt, capturedBody.BackupRetentionPolicy.WeeklyBackupImmutable)
		assert.False(tt, capturedBody.BackupRetentionPolicy.MonthlyBackupImmutable)
		assert.True(tt, capturedBody.BackupRetentionPolicy.ManualBackupImmutable)
		assert.NotNil(tt, capturedBody.BackupRetentionPolicy.BackupMinimumEnforcedRetentionDays)
		assert.Equal(tt, int64(retentionDuration), *capturedBody.BackupRetentionPolicy.BackupMinimumEnforcedRetentionDays)
		assert.NotNil(tt, capturedBody.ExternalUUID)
		assert.Equal(tt, "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", *capturedBody.ExternalUUID)
	})

	t.Run("WhenImmutableAttributesSetWithoutRetentionDuration_FlagsArePropagatedButRetentionDaysIsNil", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		req := &gcpgenserver.BackupVaultInternalV1beta{
			BackupVaultId: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
			ResourceId:    "source-vault",
			SourceRegion:  gcpgenserver.NewOptString("us-east4"),
			ImmutableAttributes: gcpgenserver.NewOptBackupVaultInternalV1betaImmutableAttributes(
				gcpgenserver.BackupVaultInternalV1betaImmutableAttributes{
					IsDailyBackupImmutable:   gcpgenserver.NewOptBool(true),
					IsWeeklyBackupImmutable:  gcpgenserver.NewOptBool(false),
					IsMonthlyBackupImmutable: gcpgenserver.NewOptBool(true),
					IsAdhocBackupImmutable:   gcpgenserver.NewOptBool(false),
					// BackupMinimumEnforcedRetentionDuration intentionally not set
				},
			),
		}
		params := gcpgenserver.V1betaInternalCreateBackupVaultParams{
			ProjectNumber: "test-project",
			LocationId:    "us-west1",
		}

		var capturedBody *cvpmodels.BackupVaultCreateV1beta
		originalCreateBVInSDE := createBackupVaultInSDE
		defer func() { createBackupVaultInSDE = originalCreateBVInSDE }()
		createBackupVaultInSDE = func(_ context.Context, body *cvpmodels.BackupVaultCreateV1beta, _, _ string, _ log.Logger) (*backup_vault.V1betaCreateBackupVaultAccepted, gcpgenserver.V1betaCreateBackupVaultRes, error) {
			capturedBody = body
			return nil, nil, errors.New("stop here")
		}

		_, _ = handler.V1betaInternalCreateBackupVault(context.Background(), req, params)

		assert.NotNil(tt, capturedBody)
		assert.NotNil(tt, capturedBody.BackupRetentionPolicy)
		assert.True(tt, capturedBody.BackupRetentionPolicy.DailyBackupImmutable)
		assert.False(tt, capturedBody.BackupRetentionPolicy.WeeklyBackupImmutable)
		assert.True(tt, capturedBody.BackupRetentionPolicy.MonthlyBackupImmutable)
		assert.False(tt, capturedBody.BackupRetentionPolicy.ManualBackupImmutable)
		assert.Nil(tt, capturedBody.BackupRetentionPolicy.BackupMinimumEnforcedRetentionDays)
	})

	t.Run("WhenAllAttributesSetAndSDESucceeds_PersistsInVCPAndReturnsBackupVault", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		req := &gcpgenserver.BackupVaultInternalV1beta{
			BackupVaultId:            "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
			ResourceId:               "source-vault",
			Description:              gcpgenserver.NewOptString("cross-region vault"),
			BackupRegion:             gcpgenserver.NewOptString("us-west1"),
			SourceRegion:             gcpgenserver.NewOptString("us-east4"),
			KmsConfigResourcePath:    gcpgenserver.NewOptString("projects/p/locations/l/keyRings/kr/cryptoKeys/k"),
			BackupsPrimaryKeyVersion: gcpgenserver.NewOptString("projects/p/locations/l/keyRings/kr/cryptoKeys/k/cryptoKeyVersions/1"),
			ImmutableAttributes: gcpgenserver.NewOptBackupVaultInternalV1betaImmutableAttributes(
				gcpgenserver.BackupVaultInternalV1betaImmutableAttributes{
					IsDailyBackupImmutable:                 gcpgenserver.NewOptBool(true),
					IsWeeklyBackupImmutable:                gcpgenserver.NewOptBool(true),
					IsMonthlyBackupImmutable:               gcpgenserver.NewOptBool(true),
					IsAdhocBackupImmutable:                 gcpgenserver.NewOptBool(true),
					BackupMinimumEnforcedRetentionDuration: gcpgenserver.NewOptInt(90),
				},
			),
		}
		params := gcpgenserver.V1betaInternalCreateBackupVaultParams{
			ProjectNumber: "test-project",
			LocationId:    "us-west1",
		}

		originalCreateBVInSDE := createBackupVaultInSDE
		defer func() { createBackupVaultInSDE = originalCreateBVInSDE }()
		createBackupVaultInSDE = func(_ context.Context, _ *cvpmodels.BackupVaultCreateV1beta, _, _ string, _ log.Logger) (*backup_vault.V1betaCreateBackupVaultAccepted, gcpgenserver.V1betaCreateBackupVaultRes, error) {
			return &backup_vault.V1betaCreateBackupVaultAccepted{}, nil, nil
		}

		now := time.Now()
		createdBackupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{
				UUID:      "generated-dest-uuid",
				CreatedAt: now,
				UpdatedAt: now,
			},
			Name:            "remote-source-vault",
			BackupVaultType: activities.CrossRegionBackupType,
			ExternalUUID:    nillable.GetStringPtr("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"),
		}
		mockOrchestrator.EXPECT().
			CreateBackupVaultEntryInVCP(mock.Anything, mock.Anything, mock.MatchedBy(func(p *commonparams.BackupVaultParams) bool {
				return p.OwnerID == "test-project" && p.Region == "us-west1"
			})).
			Return(createdBackupVault, nil)

		resp, err := handler.V1betaInternalCreateBackupVault(context.Background(), req, params)
		assert.NoError(tt, err)
		result, ok := resp.(*gcpgenserver.BackupVaultInternalV1beta)
		assert.True(tt, ok)
		assert.Equal(tt, "generated-dest-uuid", result.BackupVaultId)
		assert.Equal(tt, "remote-source-vault", result.ResourceId)
		assert.True(tt, result.ExternalUuid.Set)
		assert.Equal(tt, "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", result.ExternalUuid.Value)
	})
}

func TestV1betaInternalCreateBackupVault(t *testing.T) {
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()

	t.Run("WhenRequestIsNil", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		params := gcpgenserver.V1betaInternalCreateBackupVaultParams{
			ProjectNumber: "test-project",
			LocationId:    "us-central1",
		}

		resp, err := handler.V1betaInternalCreateBackupVault(context.Background(), nil, params)
		assert.Error(tt, err)
		assert.Equal(tt, "backupVaultId is required", err.Error())

		result, ok := resp.(*gcpgenserver.V1betaInternalCreateBackupVaultBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, float64(400), result.Code)
	})

	t.Run("WhenBackupVaultIdIsEmpty", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		req := &gcpgenserver.BackupVaultInternalV1beta{
			BackupVaultId: "",
		}
		params := gcpgenserver.V1betaInternalCreateBackupVaultParams{
			ProjectNumber: "test-project",
			LocationId:    "us-central1",
		}

		resp, err := handler.V1betaInternalCreateBackupVault(context.Background(), req, params)
		assert.Error(tt, err)
		assert.Equal(tt, "backupVaultId is required", err.Error())

		result, ok := resp.(*gcpgenserver.V1betaInternalCreateBackupVaultBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, float64(400), result.Code)
	})

	t.Run("WhenProjectNumberIsEmpty", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		req := &gcpgenserver.BackupVaultInternalV1beta{
			BackupVaultId: "test-backup-vault-id",
		}
		params := gcpgenserver.V1betaInternalCreateBackupVaultParams{
			ProjectNumber: "",
			LocationId:    "us-central1",
		}

		resp, err := handler.V1betaInternalCreateBackupVault(context.Background(), req, params)
		assert.Error(tt, err)
		assert.Equal(tt, "projectNumber is required", err.Error())

		result, ok := resp.(*gcpgenserver.V1betaInternalCreateBackupVaultBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, float64(400), result.Code)
	})

	t.Run("WhenLocationIdIsEmpty", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		req := &gcpgenserver.BackupVaultInternalV1beta{
			BackupVaultId: "test-backup-vault-id",
		}
		params := gcpgenserver.V1betaInternalCreateBackupVaultParams{
			ProjectNumber: "test-project",
			LocationId:    "",
		}

		resp, err := handler.V1betaInternalCreateBackupVault(context.Background(), req, params)
		assert.Error(tt, err)
		assert.Equal(tt, "locationId is required", err.Error())

		result, ok := resp.(*gcpgenserver.V1betaInternalCreateBackupVaultBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, float64(400), result.Code)
	})

	t.Run("WhenCVPListBackupVaultsReturnsError", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockCVPClient := backup_vault.NewMockClientService(tt)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		req := &gcpgenserver.BackupVaultInternalV1beta{
			BackupVaultId: "test-backup-vault-id",
			ResourceId:    "test-resource-id",
		}
		params := gcpgenserver.V1betaInternalCreateBackupVaultParams{
			ProjectNumber: "test-project",
			LocationId:    "us-central1",
		}

		// Mock CVP client
		mockCVPClient.EXPECT().V1betaListBackupVaults(mock.Anything).Return(nil, errors.New("CVP connection error"))
		cvpClient := &cvpapi.Cvp{BackupVault: mockCVPClient}
		originalCvpCreateClient := cvpCreateClient
		defer func() { cvpCreateClient = originalCvpCreateClient }()
		cvpCreateClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		resp, err := handler.V1betaInternalCreateBackupVault(context.Background(), req, params)
		assert.Error(tt, err)
		assert.Equal(tt, "CVP connection error", err.Error())

		result, ok := resp.(*gcpgenserver.V1betaInternalCreateBackupVaultInternalServerError)
		assert.True(tt, ok)
		assert.Equal(tt, float64(500), result.Code)
		assert.Contains(tt, result.Message, "Failed to list backup vaults from CVP")
	})

	t.Run("WhenCVPListBackupVaultsReturnsNotFoundErr", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockCVPClient := backup_vault.NewMockClientService(tt)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		req := &gcpgenserver.BackupVaultInternalV1beta{
			BackupVaultId: "test-backup-vault-id",
			ResourceId:    "test-resource-id",
		}
		params := gcpgenserver.V1betaInternalCreateBackupVaultParams{
			ProjectNumber: "test-project",
			LocationId:    "us-central1",
		}

		// Mock CVP client
		mockCVPClient.EXPECT().V1betaListBackupVaults(mock.Anything).Return(nil, errors.NewNotFoundErr("backup vaults", nil))
		cvpClient := &cvpapi.Cvp{BackupVault: mockCVPClient}
		originalCvpCreateClient := cvpCreateClient
		defer func() { cvpCreateClient = originalCvpCreateClient }()
		cvpCreateClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		resp, err := handler.V1betaInternalCreateBackupVault(context.Background(), req, params)
		assert.NoError(tt, err)

		result, ok := resp.(*gcpgenserver.V1betaInternalCreateBackupVaultBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, float64(404), result.Code)
		assert.Contains(tt, result.Message, "No backup vaults found in CVP")
	})

	t.Run("WhenCVPListBackupVaultsReturnsNilPayload", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockCVPClient := backup_vault.NewMockClientService(tt)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		req := &gcpgenserver.BackupVaultInternalV1beta{
			BackupVaultId: "test-backup-vault-id",
			ResourceId:    "test-resource-id",
		}
		params := gcpgenserver.V1betaInternalCreateBackupVaultParams{
			ProjectNumber: "test-project",
			LocationId:    "us-central1",
		}

		// Mock CVP client with nil payload
		mockResponse := &backup_vault.V1betaListBackupVaultsOK{
			Payload: nil,
		}
		mockCVPClient.EXPECT().V1betaListBackupVaults(mock.Anything).Return(mockResponse, nil)
		cvpClient := &cvpapi.Cvp{BackupVault: mockCVPClient}
		originalCvpCreateClient := cvpCreateClient
		defer func() { cvpCreateClient = originalCvpCreateClient }()
		cvpCreateClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		resp, err := handler.V1betaInternalCreateBackupVault(context.Background(), req, params)
		assert.NoError(tt, err)

		result, ok := resp.(*gcpgenserver.V1betaInternalCreateBackupVaultBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, float64(404), result.Code)
		assert.Equal(tt, "No backup vaults found in CVP", result.Message)
	})

	t.Run("WhenBackupVaultNotFoundInCVP", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockCVPClient := backup_vault.NewMockClientService(tt)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		req := &gcpgenserver.BackupVaultInternalV1beta{
			BackupVaultId: "test-backup-vault-id",
			ResourceId:    "test-resource-id",
		}
		params := gcpgenserver.V1betaInternalCreateBackupVaultParams{
			ProjectNumber: "test-project",
			LocationId:    "us-central1",
		}

		// Mock CVP client with vaults that don't match
		backupVaultType := "IN_REGION"
		mockResponse := &backup_vault.V1betaListBackupVaultsOK{
			Payload: &backup_vault.V1betaListBackupVaultsOKBody{
				BackupVaults: []*cvpmodels.BackupVaultV1beta{
					{
						BackupVaultID:     "other-vault-id",
						ResourceID:        nillable.GetStringPtr("other-resource-id"),
						BackupVaultType:   &backupVaultType,
						SourceBackupVault: nillable.GetStringPtr("other-source"),
					},
				},
			},
		}
		mockCVPClient.EXPECT().V1betaListBackupVaults(mock.Anything).Return(mockResponse, nil)
		cvpClient := &cvpapi.Cvp{BackupVault: mockCVPClient}
		originalCvpCreateClient := cvpCreateClient
		defer func() { cvpCreateClient = originalCvpCreateClient }()
		cvpCreateClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		resp, err := handler.V1betaInternalCreateBackupVault(context.Background(), req, params)
		assert.NoError(tt, err)

		result, ok := resp.(*gcpgenserver.V1betaInternalCreateBackupVaultBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, float64(404), result.Code)
		assert.Contains(tt, result.Message, "BackupVault test-backup-vault-id not found in CVP")
	})

	t.Run("WhenCreateBackupVaultEntryInVCPReturnsConflictErr", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockCVPClient := backup_vault.NewMockClientService(tt)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		req := &gcpgenserver.BackupVaultInternalV1beta{
			BackupVaultId:     "test-backup-vault-id",
			ResourceId:        "test-resource-id",
			SourceBackupVault: gcpgenserver.NewOptString("source-vault-id"),
		}
		params := gcpgenserver.V1betaInternalCreateBackupVaultParams{
			ProjectNumber: "test-project",
			LocationId:    "us-central1",
		}

		// Mock CVP client
		backupVaultType := activities.CrossRegionBackupType
		sourceBackupVault := "/projects/test-project/locations/us-west1/backupVaults/test-resource-id"
		mockResponse := &backup_vault.V1betaListBackupVaultsOK{
			Payload: &backup_vault.V1betaListBackupVaultsOKBody{
				BackupVaults: []*cvpmodels.BackupVaultV1beta{
					{
						BackupVaultID:     "test-backup-vault-id",
						ResourceID:        nillable.GetStringPtr("test-resource-id"),
						BackupVaultType:   &backupVaultType,
						SourceBackupVault: &sourceBackupVault,
					},
				},
			},
		}
		mockCVPClient.EXPECT().V1betaListBackupVaults(mock.Anything).Return(mockResponse, nil)
		cvpClient := &cvpapi.Cvp{BackupVault: mockCVPClient}
		originalCvpCreateClient := cvpCreateClient
		defer func() { cvpCreateClient = originalCvpCreateClient }()
		cvpCreateClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		// Mock ConvertToBackupVaultDataModel
		now := time.Now()
		backupVaultDataModel := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{
				UUID:      "test-uuid",
				CreatedAt: now,
				UpdatedAt: now,
			},
			Name:            "test-resource-id",
			BackupVaultType: activities.CrossRegionBackupType,
		}
		originalConvertFunc := _convertToBackupVaultDataModel
		defer func() { _convertToBackupVaultDataModel = originalConvertFunc }()
		_convertToBackupVaultDataModel = func(cvpBackupVault *cvpmodels.BackupVaultV1beta, locationId string) (*datamodel.BackupVault, error) {
			return backupVaultDataModel, nil
		}

		// Mock orchestrator to return conflict error
		mockOrchestrator.EXPECT().CreateBackupVaultEntryInVCP(mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.NewConflictErr("BackupVault already exists"))

		resp, err := handler.V1betaInternalCreateBackupVault(context.Background(), req, params)
		assert.NoError(tt, err)

		result, ok := resp.(*gcpgenserver.V1betaInternalCreateBackupVaultConflict)
		assert.True(tt, ok)
		assert.Equal(tt, float64(409), result.Code)
		assert.Equal(tt, "BackupVault already exists in VCP", result.Message)
	})

	t.Run("WhenCreateBackupVaultEntryInVCPReturnsOtherError", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockCVPClient := backup_vault.NewMockClientService(tt)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		req := &gcpgenserver.BackupVaultInternalV1beta{
			BackupVaultId:     "test-backup-vault-id",
			ResourceId:        "test-resource-id",
			SourceBackupVault: gcpgenserver.NewOptString("source-vault-id"),
		}
		params := gcpgenserver.V1betaInternalCreateBackupVaultParams{
			ProjectNumber: "test-project",
			LocationId:    "us-central1",
		}

		// Mock CVP client
		backupVaultType := activities.CrossRegionBackupType
		sourceBackupVault := "/projects/test-project/locations/us-west1/backupVaults/test-resource-id"
		mockResponse := &backup_vault.V1betaListBackupVaultsOK{
			Payload: &backup_vault.V1betaListBackupVaultsOKBody{
				BackupVaults: []*cvpmodels.BackupVaultV1beta{
					{
						BackupVaultID:     "test-backup-vault-id",
						ResourceID:        nillable.GetStringPtr("test-resource-id"),
						BackupVaultType:   &backupVaultType,
						SourceBackupVault: &sourceBackupVault,
					},
				},
			},
		}
		mockCVPClient.EXPECT().V1betaListBackupVaults(mock.Anything).Return(mockResponse, nil)
		cvpClient := &cvpapi.Cvp{BackupVault: mockCVPClient}
		originalCvpCreateClient := cvpCreateClient
		defer func() { cvpCreateClient = originalCvpCreateClient }()
		cvpCreateClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		// Mock ConvertToBackupVaultDataModel
		now := time.Now()
		backupVaultDataModel := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{
				UUID:      "test-uuid",
				CreatedAt: now,
				UpdatedAt: now,
			},
			Name:            "test-resource-id",
			BackupVaultType: activities.CrossRegionBackupType,
		}
		originalConvertFunc := _convertToBackupVaultDataModel
		defer func() { _convertToBackupVaultDataModel = originalConvertFunc }()
		_convertToBackupVaultDataModel = func(cvpBackupVault *cvpmodels.BackupVaultV1beta, locationId string) (*datamodel.BackupVault, error) {
			return backupVaultDataModel, nil
		}

		// Mock orchestrator to return error
		mockOrchestrator.EXPECT().CreateBackupVaultEntryInVCP(mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("database error"))

		resp, err := handler.V1betaInternalCreateBackupVault(context.Background(), req, params)
		assert.Error(tt, err)
		assert.Equal(tt, "database error", err.Error())

		result, ok := resp.(*gcpgenserver.V1betaInternalCreateBackupVaultInternalServerError)
		assert.True(tt, ok)
		assert.Equal(tt, float64(500), result.Code)
		assert.Equal(tt, "Failed to create BackupVault entry in VCP database", result.Message)
	})

	t.Run("WhenSuccessfulCreationWithoutBucketDetails", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockCVPClient := backup_vault.NewMockClientService(tt)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		req := &gcpgenserver.BackupVaultInternalV1beta{
			BackupVaultId:     "test-backup-vault-id",
			ResourceId:        "test-resource-id",
			SourceBackupVault: gcpgenserver.NewOptString("source-vault-id"),
		}
		params := gcpgenserver.V1betaInternalCreateBackupVaultParams{
			ProjectNumber: "test-project",
			LocationId:    "us-central1",
		}

		// Mock CVP client
		backupVaultType := activities.CrossRegionBackupType
		sourceBackupVault := "/projects/test-project/locations/us-west1/backupVaults/test-resource-id"
		mockResponse := &backup_vault.V1betaListBackupVaultsOK{
			Payload: &backup_vault.V1betaListBackupVaultsOKBody{
				BackupVaults: []*cvpmodels.BackupVaultV1beta{
					{
						BackupVaultID:     "test-backup-vault-id",
						ResourceID:        nillable.GetStringPtr("test-resource-id"),
						BackupVaultType:   &backupVaultType,
						SourceBackupVault: &sourceBackupVault,
					},
				},
			},
		}
		mockCVPClient.EXPECT().V1betaListBackupVaults(mock.Anything).Return(mockResponse, nil)
		cvpClient := &cvpapi.Cvp{BackupVault: mockCVPClient}
		originalCvpCreateClient := cvpCreateClient
		defer func() { cvpCreateClient = originalCvpCreateClient }()
		cvpCreateClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		// Mock ConvertToBackupVaultDataModel
		now := time.Now()
		backupVaultDataModel := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{
				UUID:      "test-uuid",
				CreatedAt: now,
				UpdatedAt: now,
			},
			Name:            "test-resource-id",
			BackupVaultType: activities.CrossRegionBackupType,
		}
		originalConvertFunc := _convertToBackupVaultDataModel
		defer func() { _convertToBackupVaultDataModel = originalConvertFunc }()
		_convertToBackupVaultDataModel = func(cvpBackupVault *cvpmodels.BackupVaultV1beta, locationId string) (*datamodel.BackupVault, error) {
			return backupVaultDataModel, nil
		}

		// Mock orchestrator to return created backup vault
		createdBackupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{
				UUID:      "test-uuid",
				CreatedAt: now,
				UpdatedAt: now,
			},
			Name:                       "test-resource-id",
			BackupVaultType:            activities.CrossRegionBackupType,
			ExternalUUID:               nillable.GetStringPtr("test-backup-vault-id"),
			CrossRegionBackupVaultName: nillable.GetStringPtr("source-vault-id"),
		}
		mockOrchestrator.EXPECT().CreateBackupVaultEntryInVCP(mock.Anything, mock.Anything, mock.Anything).Return(createdBackupVault, nil)

		resp, err := handler.V1betaInternalCreateBackupVault(context.Background(), req, params)
		assert.NoError(tt, err)

		result, ok := resp.(*gcpgenserver.BackupVaultInternalV1beta)
		assert.True(tt, ok)
		assert.Equal(tt, "test-uuid", result.BackupVaultId)
		assert.Equal(tt, "test-resource-id", result.ResourceId)
		assert.Equal(tt, gcpgenserver.BackupVaultInternalV1betaBackupVaultTypeCROSSREGION, result.BackupVaultType)
	})

	// Cross-region destination vault: CVP exposes a distinct resourceId in the backup region (e.g. ...-destination-xxxx)
	// while the internal request still carries the source vault ResourceId. VCP must persist Name = CVP resourceId for CCFE path alignment.
	t.Run("WhenSuccessfulCreationCrossRegionDestinationUsesCVPResourceIdAsVaultName", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockCVPClient := backup_vault.NewMockClientService(tt)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		const (
			sourceResourceID      = "crbtestbv001"
			destinationResourceID = "crbtestbv001-destination-ae0e"
			sourceVaultUUID       = "ae0e7884-ff74-617a-4df9-b4b72359bfaa"
			destinationVaultUUID  = "3850ea0b-2f86-ec69-712f-5c66233aa458"
		)

		req := &gcpgenserver.BackupVaultInternalV1beta{
			BackupVaultId:     sourceVaultUUID,
			ResourceId:        sourceResourceID,
			SourceBackupVault: gcpgenserver.NewOptString("projects/test-project/locations/us-central1/backupVaults/" + sourceResourceID),
		}
		params := gcpgenserver.V1betaInternalCreateBackupVaultParams{
			ProjectNumber: "test-project",
			LocationId:    "us-east4",
		}

		backupVaultType := activities.CrossRegionBackupType
		sourceBackupVaultPath := "/projects/test-project/locations/us-central1/backupVaults/" + sourceResourceID
		mockResponse := &backup_vault.V1betaListBackupVaultsOK{
			Payload: &backup_vault.V1betaListBackupVaultsOKBody{
				BackupVaults: []*cvpmodels.BackupVaultV1beta{
					{
						BackupVaultID:     destinationVaultUUID,
						ResourceID:        nillable.GetStringPtr(destinationResourceID),
						BackupVaultType:   &backupVaultType,
						SourceBackupVault: &sourceBackupVaultPath,
					},
				},
			},
		}
		mockCVPClient.EXPECT().V1betaListBackupVaults(mock.Anything).Return(mockResponse, nil)
		cvpClient := &cvpapi.Cvp{BackupVault: mockCVPClient}
		originalCvpCreateClient := cvpCreateClient
		defer func() { cvpCreateClient = originalCvpCreateClient }()
		cvpCreateClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		now := time.Now()
		created := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{
				UUID:      destinationVaultUUID,
				CreatedAt: now,
				UpdatedAt: now,
			},
			Name:                       destinationResourceID,
			BackupVaultType:            activities.CrossRegionBackupType,
			ExternalUUID:               nillable.GetStringPtr(sourceVaultUUID),
			CrossRegionBackupVaultName: nillable.GetStringPtr(sourceBackupVaultPath),
		}
		mockOrchestrator.EXPECT().CreateBackupVaultEntryInVCP(
			mock.Anything,
			mock.MatchedBy(func(bv *datamodel.BackupVault) bool {
				return bv != nil &&
					bv.Name == destinationResourceID &&
					bv.UUID == destinationVaultUUID &&
					bv.ExternalUUID != nil && *bv.ExternalUUID == sourceVaultUUID
			}),
			mock.Anything,
		).Return(created, nil)

		resp, err := handler.V1betaInternalCreateBackupVault(context.Background(), req, params)
		assert.NoError(tt, err)

		result, ok := resp.(*gcpgenserver.BackupVaultInternalV1beta)
		assert.True(tt, ok)
		assert.Equal(tt, destinationVaultUUID, result.BackupVaultId)
		assert.Equal(tt, destinationResourceID, result.ResourceId)
		assert.NotEqual(tt, sourceResourceID, result.ResourceId)
		assert.Equal(tt, gcpgenserver.BackupVaultInternalV1betaBackupVaultTypeCROSSREGION, result.BackupVaultType)
	})

	t.Run("WhenSuccessfulCreationWithBucketDetails", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		// ServiceType is the explicit signal for a cross-project vault. Bucket details alone
		// (e.g. TenantProjectNumber) must not be treated as a cross-project indicator - see
		// WhenPlainCrossRegionVaultWithBucketDetailsIsNotTreatedAsCrossProject below.
		req := &gcpgenserver.BackupVaultInternalV1beta{
			BackupVaultId:     "test-backup-vault-id",
			ResourceId:        "test-resource-id",
			SourceBackupVault: gcpgenserver.NewOptString("source-vault-id"),
			ServiceType:       gcpgenserver.NewOptString(datamodel.ServiceTypeCrossProject),
			BucketDetails: []gcpgenserver.BackupVaultInternalV1betaBucketDetailsItem{
				{
					BucketName:          gcpgenserver.NewOptString("test-bucket"),
					ServiceAccountName:  gcpgenserver.NewOptString("test-sa"),
					VendorSubnetId:      gcpgenserver.NewOptString("test-subnet"),
					TenantProjectNumber: gcpgenserver.NewOptString("test-tenant-project"),
					SatisfiesPzs:        gcpgenserver.NewOptBool(true),
					SatisfiesPzi:        gcpgenserver.NewOptBool(true),
				},
			},
		}
		params := gcpgenserver.V1betaInternalCreateBackupVaultParams{
			ProjectNumber: "test-project",
			LocationId:    "us-central1",
		}

		now := time.Now()
		createdBackupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{
				UUID:      "test-uuid",
				CreatedAt: now,
				UpdatedAt: now,
			},
			Name:                       "test-resource-id",
			BackupVaultType:            activities.CrossRegionBackupType,
			ExternalUUID:               nillable.GetStringPtr("test-backup-vault-id"),
			CrossRegionBackupVaultName: nillable.GetStringPtr("source-vault-id"),
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{
					BucketName:          "test-bucket",
					ServiceAccountName:  "test-sa",
					VendorSubnetID:      "test-subnet",
					TenantProjectNumber: "test-tenant-project",
					SatisfiesPzs:        true,
					SatisfiesPzi:        true,
				},
			},
		}
		mockOrchestrator.EXPECT().CreateBackupVaultEntryInVCP(mock.Anything, mock.Anything, mock.Anything).Return(createdBackupVault, nil)

		resp, err := handler.V1betaInternalCreateBackupVault(context.Background(), req, params)
		assert.NoError(tt, err)

		result, ok := resp.(*gcpgenserver.BackupVaultInternalV1beta)
		assert.True(tt, ok)
		assert.Equal(tt, "test-uuid", result.BackupVaultId)
		assert.Equal(tt, "test-resource-id", result.ResourceId)
		assert.Equal(tt, 1, len(result.BucketDetails))
		assert.Equal(tt, "test-bucket", result.BucketDetails[0].BucketName.Value)
		assert.Equal(tt, "test-sa", result.BucketDetails[0].ServiceAccountName.Value)
	})

	// Regression test: TenantProjectNumber is populated on every bucket (it is just the GCP
	// project number that owns the bucket - see CreateBucket), not only on cross-project vaults.
	// A plain CROSS_REGION vault (serviceType=GCNV) that happens to carry BucketDetails with a
	// TenantProjectNumber must still go through the normal CVP-backed destination lookup and
	// must not be persisted with ServiceType=CrossProject.
	t.Run("WhenPlainCrossRegionVaultWithBucketDetailsIsNotTreatedAsCrossProject", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockCVPClient := backup_vault.NewMockClientService(tt)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		req := &gcpgenserver.BackupVaultInternalV1beta{
			BackupVaultId:     "test-backup-vault-id",
			ResourceId:        "test-resource-id",
			SourceBackupVault: gcpgenserver.NewOptString("source-vault-id"),
			ServiceType:       gcpgenserver.NewOptString(datamodel.ServiceTypeGCNV),
			BucketDetails: []gcpgenserver.BackupVaultInternalV1betaBucketDetailsItem{
				{
					BucketName:          gcpgenserver.NewOptString("shared-vpc-bucket"),
					ServiceAccountName:  gcpgenserver.NewOptString("test-sa"),
					VendorSubnetId:      gcpgenserver.NewOptString("test-subnet"),
					TenantProjectNumber: gcpgenserver.NewOptString("622045561671"),
				},
			},
		}
		params := gcpgenserver.V1betaInternalCreateBackupVaultParams{
			ProjectNumber: "test-project",
			LocationId:    "us-central1",
		}

		backupVaultType := activities.CrossRegionBackupType
		sourceBackupVault := "/projects/test-project/locations/us-west1/backupVaults/test-resource-id"
		mockResponse := &backup_vault.V1betaListBackupVaultsOK{
			Payload: &backup_vault.V1betaListBackupVaultsOKBody{
				BackupVaults: []*cvpmodels.BackupVaultV1beta{
					{
						BackupVaultID:     "test-backup-vault-id",
						ResourceID:        nillable.GetStringPtr("test-resource-id"),
						BackupVaultType:   &backupVaultType,
						SourceBackupVault: &sourceBackupVault,
					},
				},
			},
		}
		mockCVPClient.EXPECT().V1betaListBackupVaults(mock.Anything).Return(mockResponse, nil)
		cvpClient := &cvpapi.Cvp{BackupVault: mockCVPClient}
		originalCvpCreateClient := cvpCreateClient
		defer func() { cvpCreateClient = originalCvpCreateClient }()
		cvpCreateClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		now := time.Now()
		var capturedBV *datamodel.BackupVault
		createdBackupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{
				UUID:      "test-uuid",
				CreatedAt: now,
				UpdatedAt: now,
			},
			Name:            "test-resource-id",
			BackupVaultType: activities.CrossRegionBackupType,
			ServiceType:     datamodel.ServiceTypeGCNV,
			ExternalUUID:    nillable.GetStringPtr("test-backup-vault-id"),
		}
		mockOrchestrator.EXPECT().
			CreateBackupVaultEntryInVCP(mock.Anything, mock.MatchedBy(func(bv *datamodel.BackupVault) bool {
				capturedBV = bv
				return true
			}), mock.Anything).
			Return(createdBackupVault, nil)

		resp, err := handler.V1betaInternalCreateBackupVault(context.Background(), req, params)
		assert.NoError(tt, err)

		result, ok := resp.(*gcpgenserver.BackupVaultInternalV1beta)
		assert.True(tt, ok)
		assert.Equal(tt, "test-uuid", result.BackupVaultId)

		// The vault must be resolved via the normal CVP-backed lookup (proving isCrossProjectVault
		// was false), and must be persisted with its real ServiceType, not CrossProject.
		mockCVPClient.AssertCalled(tt, "V1betaListBackupVaults", mock.Anything)
		assert.NotNil(tt, capturedBV)
		assert.Equal(tt, datamodel.ServiceTypeGCNV, capturedBV.ServiceType)
		assert.NotEqual(tt, datamodel.ServiceTypeCrossProject, capturedBV.ServiceType)
	})

	t.Run("WhenSuccessfulCreationWithCMEKAttributes", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockCVPClient := backup_vault.NewMockClientService(tt)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		kmsConfigPath := "projects/test-project/locations/us-central1/kmsConfigs/myconfig"
		encryptionState := "ENCRYPTION_STATE_COMPLETED"
		backupsPrimaryKeyVersion := "projects/test-project/locations/us-central1/keyRings/test-ring/cryptoKeys/test-key/cryptoKeyVersions/1"

		req := &gcpgenserver.BackupVaultInternalV1beta{
			BackupVaultId:            "test-backup-vault-id",
			ResourceId:               "test-resource-id",
			SourceBackupVault:        gcpgenserver.NewOptString("source-vault-id"),
			KmsConfigResourcePath:    gcpgenserver.NewOptString(kmsConfigPath),
			EncryptionState:          gcpgenserver.NewOptBackupVaultInternalV1betaEncryptionState(gcpgenserver.BackupVaultInternalV1betaEncryptionStateENCRYPTIONSTATECOMPLETED),
			BackupsPrimaryKeyVersion: gcpgenserver.NewOptString(backupsPrimaryKeyVersion),
		}
		params := gcpgenserver.V1betaInternalCreateBackupVaultParams{
			ProjectNumber: "test-project",
			LocationId:    "us-central1",
		}

		// Mock CVP client
		backupVaultType := activities.CrossRegionBackupType
		sourceBackupVault := "/projects/test-project/locations/us-west1/backupVaults/test-resource-id"
		mockResponse := &backup_vault.V1betaListBackupVaultsOK{
			Payload: &backup_vault.V1betaListBackupVaultsOKBody{
				BackupVaults: []*cvpmodels.BackupVaultV1beta{
					{
						BackupVaultID:            "test-backup-vault-id",
						ResourceID:               nillable.GetStringPtr("test-resource-id"),
						BackupVaultType:          &backupVaultType,
						SourceBackupVault:        &sourceBackupVault,
						KmsConfigResourcePath:    nillable.GetStringPtr(kmsConfigPath),
						EncryptionState:          nillable.GetStringPtr(encryptionState),
						BackupsPrimaryKeyVersion: nillable.GetStringPtr(backupsPrimaryKeyVersion),
					},
				},
			},
		}
		mockCVPClient.EXPECT().V1betaListBackupVaults(mock.Anything).Return(mockResponse, nil)
		cvpClient := &cvpapi.Cvp{BackupVault: mockCVPClient}
		originalCvpCreateClient := cvpCreateClient
		defer func() { cvpCreateClient = originalCvpCreateClient }()
		cvpCreateClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		// Mock ConvertToBackupVaultDataModel
		now := time.Now()
		backupVaultDataModel := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{
				UUID:      "test-uuid",
				CreatedAt: now,
				UpdatedAt: now,
			},
			Name:            "test-resource-id",
			BackupVaultType: activities.CrossRegionBackupType,
			CmekAttributes: &datamodel.CmekAttributes{
				KmsConfigResourcePath:    &kmsConfigPath,
				EncryptionState:          &encryptionState,
				BackupsPrimaryKeyVersion: &backupsPrimaryKeyVersion,
			},
		}
		originalConvertFunc := _convertToBackupVaultDataModel
		defer func() { _convertToBackupVaultDataModel = originalConvertFunc }()
		_convertToBackupVaultDataModel = func(cvpBackupVault *cvpmodels.BackupVaultV1beta, locationId string) (*datamodel.BackupVault, error) {
			return backupVaultDataModel, nil
		}

		// Mock orchestrator to return created backup vault with CMEK attributes
		createdBackupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{
				UUID:      "test-uuid",
				CreatedAt: now,
				UpdatedAt: now,
			},
			Name:                       "test-resource-id",
			BackupVaultType:            activities.CrossRegionBackupType,
			ExternalUUID:               nillable.GetStringPtr("test-backup-vault-id"),
			CrossRegionBackupVaultName: nillable.GetStringPtr("source-vault-id"),
			CmekAttributes: &datamodel.CmekAttributes{
				KmsConfigResourcePath:    &kmsConfigPath,
				EncryptionState:          &encryptionState,
				BackupsPrimaryKeyVersion: &backupsPrimaryKeyVersion,
			},
		}
		mockOrchestrator.EXPECT().CreateBackupVaultEntryInVCP(mock.Anything, mock.Anything, mock.Anything).Return(createdBackupVault, nil)

		resp, err := handler.V1betaInternalCreateBackupVault(context.Background(), req, params)
		assert.NoError(tt, err)

		result, ok := resp.(*gcpgenserver.BackupVaultInternalV1beta)
		assert.True(tt, ok)
		assert.Equal(tt, "test-uuid", result.BackupVaultId)
		assert.Equal(tt, "test-resource-id", result.ResourceId)
		assert.True(tt, result.KmsConfigResourcePath.IsSet())
		assert.Equal(tt, kmsConfigPath, result.KmsConfigResourcePath.Value)
		assert.True(tt, result.EncryptionState.IsSet())
		assert.Equal(tt, gcpgenserver.BackupVaultInternalV1betaEncryptionStateENCRYPTIONSTATECOMPLETED, result.EncryptionState.Value)
		assert.True(tt, result.BackupsPrimaryKeyVersion.IsSet())
		assert.Equal(tt, backupsPrimaryKeyVersion, result.BackupsPrimaryKeyVersion.Value)
	})

	t.Run("WhenSourceBackupVaultMatchesResourceId", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockCVPClient := backup_vault.NewMockClientService(tt)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		req := &gcpgenserver.BackupVaultInternalV1beta{
			BackupVaultId: "test-backup-vault-id",
			ResourceId:    "test-resource-id",
		}
		params := gcpgenserver.V1betaInternalCreateBackupVaultParams{
			ProjectNumber: "test-project",
			LocationId:    "us-central1",
		}

		// Mock CVP client with SourceBackupVault that ends with ResourceId
		backupVaultType := activities.CrossRegionBackupType
		sourceBackupVault := "/projects/test-project/locations/us-west1/backupVaults/test-resource-id"
		mockResponse := &backup_vault.V1betaListBackupVaultsOK{
			Payload: &backup_vault.V1betaListBackupVaultsOKBody{
				BackupVaults: []*cvpmodels.BackupVaultV1beta{
					{
						BackupVaultID:     "test-backup-vault-id",
						ResourceID:        nillable.GetStringPtr("test-resource-id"),
						BackupVaultType:   &backupVaultType,
						SourceBackupVault: &sourceBackupVault,
					},
				},
			},
		}
		mockCVPClient.EXPECT().V1betaListBackupVaults(mock.Anything).Return(mockResponse, nil)
		cvpClient := &cvpapi.Cvp{BackupVault: mockCVPClient}
		originalCvpCreateClient := cvpCreateClient
		defer func() { cvpCreateClient = originalCvpCreateClient }()
		cvpCreateClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		// Mock ConvertToBackupVaultDataModel
		now := time.Now()
		backupVaultDataModel := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{
				UUID:      "test-uuid",
				CreatedAt: now,
				UpdatedAt: now,
			},
			Name:            "test-resource-id",
			BackupVaultType: activities.CrossRegionBackupType,
		}
		originalConvertFunc := _convertToBackupVaultDataModel
		defer func() { _convertToBackupVaultDataModel = originalConvertFunc }()
		_convertToBackupVaultDataModel = func(cvpBackupVault *cvpmodels.BackupVaultV1beta, locationId string) (*datamodel.BackupVault, error) {
			return backupVaultDataModel, nil
		}

		// Mock orchestrator to return created backup vault
		createdBackupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{
				UUID:      "test-uuid",
				CreatedAt: now,
				UpdatedAt: now,
			},
			Name:            "test-resource-id",
			BackupVaultType: activities.CrossRegionBackupType,
			ExternalUUID:    nillable.GetStringPtr("test-backup-vault-id"),
		}
		mockOrchestrator.EXPECT().CreateBackupVaultEntryInVCP(mock.Anything, mock.Anything, mock.Anything).Return(createdBackupVault, nil)

		resp, err := handler.V1betaInternalCreateBackupVault(context.Background(), req, params)
		assert.NoError(tt, err)

		result, ok := resp.(*gcpgenserver.BackupVaultInternalV1beta)
		assert.True(tt, ok)
		assert.Equal(tt, "test-uuid", result.BackupVaultId)
		assert.Equal(tt, "test-resource-id", result.ResourceId)
	})

	t.Run("WhenBackupVaultTypeIsNilButSourceBackupVaultMatches", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockCVPClient := backup_vault.NewMockClientService(tt)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		req := &gcpgenserver.BackupVaultInternalV1beta{
			BackupVaultId: "test-backup-vault-id",
			ResourceId:    "test-resource-id",
		}
		params := gcpgenserver.V1betaInternalCreateBackupVaultParams{
			ProjectNumber: "test-project",
			LocationId:    "us-central1",
		}

		// Mock CVP client with nil BackupVaultType but matching SourceBackupVault
		sourceBackupVault := "/projects/test-project/locations/us-west1/backupVaults/test-resource-id"
		mockResponse := &backup_vault.V1betaListBackupVaultsOK{
			Payload: &backup_vault.V1betaListBackupVaultsOKBody{
				BackupVaults: []*cvpmodels.BackupVaultV1beta{
					{
						BackupVaultID:     "test-backup-vault-id",
						ResourceID:        nillable.GetStringPtr("test-resource-id"),
						BackupVaultType:   nil, // nil type should still match
						SourceBackupVault: &sourceBackupVault,
					},
				},
			},
		}
		mockCVPClient.EXPECT().V1betaListBackupVaults(mock.Anything).Return(mockResponse, nil)
		cvpClient := &cvpapi.Cvp{BackupVault: mockCVPClient}
		originalCvpCreateClient := cvpCreateClient
		defer func() { cvpCreateClient = originalCvpCreateClient }()
		cvpCreateClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		// Mock ConvertToBackupVaultDataModel
		now := time.Now()
		backupVaultDataModel := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{
				UUID:      "test-uuid",
				CreatedAt: now,
				UpdatedAt: now,
			},
			Name:            "test-resource-id",
			BackupVaultType: activities.CrossRegionBackupType,
		}
		originalConvertFunc := _convertToBackupVaultDataModel
		defer func() { _convertToBackupVaultDataModel = originalConvertFunc }()
		_convertToBackupVaultDataModel = func(cvpBackupVault *cvpmodels.BackupVaultV1beta, locationId string) (*datamodel.BackupVault, error) {
			return backupVaultDataModel, nil
		}

		// Mock orchestrator to return created backup vault
		createdBackupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{
				UUID:      "test-uuid",
				CreatedAt: now,
				UpdatedAt: now,
			},
			Name:            "test-resource-id",
			BackupVaultType: activities.CrossRegionBackupType,
			ExternalUUID:    nillable.GetStringPtr("test-backup-vault-id"),
		}
		mockOrchestrator.EXPECT().CreateBackupVaultEntryInVCP(mock.Anything, mock.Anything, mock.Anything).Return(createdBackupVault, nil)

		resp, err := handler.V1betaInternalCreateBackupVault(context.Background(), req, params)
		assert.NoError(tt, err)

		result, ok := resp.(*gcpgenserver.BackupVaultInternalV1beta)
		assert.True(tt, ok)
		assert.Equal(tt, "test-uuid", result.BackupVaultId)
	})
}

func TestV1betaInternalUpdateState(t *testing.T) {
	t.Run("WhenNotFoundError", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		notFoundErr := errors.NewNotFoundErr("volume replication", nil)
		mockOrchestrator.EXPECT().UpdateVolumeReplicationState(mock.Anything, mock.AnythingOfType("models.UpdateVolumeReplicationStateParams")).Return(nil, notFoundErr)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		req := &gcpgenserver.VolumeReplicationUpdateStateInternalV1beta{
			State:        gcpgenserver.NewOptString(datamodel.LifeCycleStateError),
			StateDetails: gcpgenserver.NewOptString("error details"),
		}
		params := gcpgenserver.V1betaInternalUpdateStateParams{
			ProjectNumber:       "project-123",
			LocationId:          "us-central1",
			VolumeReplicationId: "replication-123",
		}
		response, err := handler.V1betaInternalUpdateState(context.Background(), req, params)
		assert.NoError(tt, err)
		assert.IsType(tt, &gcpgenserver.V1betaInternalUpdateStateBadRequest{}, response)
		badRequestResp := response.(*gcpgenserver.V1betaInternalUpdateStateBadRequest)
		assert.Equal(tt, float64(400), badRequestResp.Code)
		assert.Contains(tt, badRequestResp.Message, "volume replication")
	})

	t.Run("WhenInternalServerError", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		internalErr := errors.New("database connection failed")
		mockOrchestrator.EXPECT().UpdateVolumeReplicationState(mock.Anything, mock.AnythingOfType("models.UpdateVolumeReplicationStateParams")).Return(nil, internalErr)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		req := &gcpgenserver.VolumeReplicationUpdateStateInternalV1beta{
			State:        gcpgenserver.NewOptString(datamodel.LifeCycleStateError),
			StateDetails: gcpgenserver.NewOptString("error details"),
		}
		params := gcpgenserver.V1betaInternalUpdateStateParams{
			ProjectNumber:       "project-456",
			LocationId:          "europe-west1",
			VolumeReplicationId: "replication-456",
		}
		response, err := handler.V1betaInternalUpdateState(context.Background(), req, params)
		assert.Error(tt, err)
		assert.Equal(tt, "database connection failed", err.Error())
		assert.IsType(tt, &gcpgenserver.V1betaInternalUpdateStateInternalServerError{}, response)
		internalServerResp := response.(*gcpgenserver.V1betaInternalUpdateStateInternalServerError)
		assert.Equal(tt, float64(500), internalServerResp.Code)
		assert.Equal(tt, "Internal server error", internalServerResp.Message)
	})

	t.Run("WhenSuccessful", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		volumeReplication := &models.VolumeReplication{
			BaseModel: models.BaseModel{
				UUID: "replication-789",
			},
			State:        datamodel.LifeCycleStateError,
			StateDetails: "deletion error details",
		}
		mockOrchestrator.EXPECT().UpdateVolumeReplicationState(mock.Anything, mock.AnythingOfType("models.UpdateVolumeReplicationStateParams")).Return(volumeReplication, nil)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		req := &gcpgenserver.VolumeReplicationUpdateStateInternalV1beta{
			State:        gcpgenserver.NewOptString(datamodel.LifeCycleStateError),
			StateDetails: gcpgenserver.NewOptString("error details"),
		}
		params := gcpgenserver.V1betaInternalUpdateStateParams{
			ProjectNumber:       "project-789",
			LocationId:          "asia-southeast1",
			VolumeReplicationId: "replication-789",
		}
		response, err := handler.V1betaInternalUpdateState(context.Background(), req, params)
		assert.NoError(tt, err)
		assert.IsType(tt, &gcpgenserver.VolumeReplicationUpdateStateInternalV1beta{}, response)
		updateStateResp := response.(*gcpgenserver.VolumeReplicationUpdateStateInternalV1beta)
		assert.True(tt, updateStateResp.State.IsSet())
		assert.Equal(tt, datamodel.LifeCycleStateError, updateStateResp.State.Value)
		assert.True(tt, updateStateResp.StateDetails.IsSet())
		assert.Equal(tt, "deletion error details", updateStateResp.StateDetails.Value)
	})
}

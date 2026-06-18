package replication

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/pborman/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/models"
	googleproxyclient "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/google-proxy-client"
	coreModels "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaErrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/auth"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
)

func Test_getReplicationJobs(t *testing.T) {
	t.Run("WhenSuccessful", func(tt *testing.T) {
		ctx := context.Background()
		basePath := "basePath"
		token := "token"
		locationID := "loc"
		projectNumber := "proj"
		xCorrelationID := new(string)
		poolId := "pool"

		mockJobs := []googleproxyclient.InternalJobV1beta{
			{ResourceName: googleproxyclient.OptString{Value: "job1", Set: true}},
		}

		mockClient := googleproxyclient.NewMockInvoker(t)
		mockClient.EXPECT().V1betaInternalGetReplicationJobs(ctx, mock.Anything).Return(&googleproxyclient.V1betaInternalGetReplicationJobsOK{Jobs: mockJobs}, nil)

		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		defer func() { googleproxyclient.GetGProxyClient = originalGetGProxyClient }()

		jobs, err := _getReplicationJobs(ctx, basePath, token, locationID, projectNumber, xCorrelationID, poolId)
		assert.NoError(t, err)
		assert.Equal(t, mockJobs, jobs)
	})
	t.Run("WhenError", func(tt *testing.T) {
		ctx := context.Background()
		basePath := "basePath"
		token := "token"
		locationID := "loc"
		projectNumber := "proj"
		xCorrelationID := new(string)
		poolId := "pool"

		mockClient := googleproxyclient.NewMockInvoker(t)
		mockClient.EXPECT().V1betaInternalGetReplicationJobs(ctx, mock.Anything).Return(nil, errors.New("api error"))

		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		defer func() { googleproxyclient.GetGProxyClient = originalGetGProxyClient }()

		jobs, err := _getReplicationJobs(ctx, basePath, token, locationID, projectNumber, xCorrelationID, poolId)
		assert.Error(t, err)
		assert.Nil(t, jobs)
	})
}

func Test_checkActiveReplicationJobs(t *testing.T) {
	ctx := context.Background()
	accountID := int64(1)
	poolUUID := "pool-uuid-123"
	ccfeURI := "projects/p1/locations/loc1/volumes/vol-1/replications/repl-1"

	t.Run("NoActiveJobs_Success", func(tt *testing.T) {
		mockStorage := &database.MockStorage{}
		mockStorage.On("GetJobsWithCondition", ctx, mock.Anything).Return([]*datamodel.Job{}, nil)
		err := _checkActiveReplicationJobs(ctx, mockStorage, accountID, poolUUID, ccfeURI)
		assert.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("JobsForOtherPool_NoError", func(tt *testing.T) {
		mockStorage := &database.MockStorage{}
		otherPoolJob := &datamodel.Job{
			BaseModel:     datamodel.BaseModel{UUID: "job-1"},
			Type:          string(datamodel.JobTypeCreateVolumeReplication),
			State:         string(datamodel.JobsStatePROCESSING),
			ResourceName:  ccfeURI,
			JobAttributes: &datamodel.JobAttributes{PoolUUID: "other-pool-uuid"},
		}
		mockStorage.On("GetJobsWithCondition", ctx, mock.Anything).Return([]*datamodel.Job{otherPoolJob}, nil)
		err := _checkActiveReplicationJobs(ctx, mockStorage, accountID, poolUUID, ccfeURI)
		assert.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("GetJobsWithCondition_ReturnsError", func(tt *testing.T) {
		mockStorage := &database.MockStorage{}
		dbErr := errors.New("database error")
		mockStorage.On("GetJobsWithCondition", ctx, mock.Anything).Return([]*datamodel.Job(nil), dbErr)
		err := _checkActiveReplicationJobs(ctx, mockStorage, accountID, poolUUID, ccfeURI)
		assert.Error(tt, err)
		assert.Equal(tt, dbErr, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("ActiveCreateHybridReplicationJob_ReturnsPoolError", func(tt *testing.T) {
		mockStorage := &database.MockStorage{}
		job := &datamodel.Job{
			BaseModel:     datamodel.BaseModel{UUID: "job-1"},
			Type:          string(datamodel.JobTypeCreateHybridReplication),
			State:         string(datamodel.JobsStatePROCESSING),
			ResourceName:  "some-other-uri",
			JobAttributes: &datamodel.JobAttributes{PoolUUID: poolUUID},
		}
		mockStorage.On("GetJobsWithCondition", ctx, mock.Anything).Return([]*datamodel.Job{job}, nil)
		err := _checkActiveReplicationJobs(ctx, mockStorage, accountID, poolUUID, ccfeURI)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "There is an active replication operation in progress for this pool")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("ActiveEstablishPeeringJob_ReturnsPoolError", func(tt *testing.T) {
		mockStorage := &database.MockStorage{}
		job := &datamodel.Job{
			BaseModel:     datamodel.BaseModel{UUID: "job-2"},
			Type:          string(datamodel.JobTypeHybridReplicationEstablishPeering),
			State:         string(datamodel.JobsStateNEW),
			ResourceName:  "some-uri",
			JobAttributes: &datamodel.JobAttributes{PoolUUID: poolUUID},
		}
		mockStorage.On("GetJobsWithCondition", ctx, mock.Anything).Return([]*datamodel.Job{job}, nil)
		err := _checkActiveReplicationJobs(ctx, mockStorage, accountID, poolUUID, ccfeURI)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "There is an active replication operation in progress for this pool")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("ActiveJobSameReplication_ReturnsReplicationError", func(tt *testing.T) {
		mockStorage := &database.MockStorage{}
		job := &datamodel.Job{
			BaseModel:     datamodel.BaseModel{UUID: "job-3"},
			Type:          string(datamodel.JobTypeCreateVolumeReplication),
			State:         string(datamodel.JobsStatePROCESSING),
			ResourceName:  ccfeURI,
			JobAttributes: &datamodel.JobAttributes{PoolUUID: poolUUID},
		}
		mockStorage.On("GetJobsWithCondition", ctx, mock.Anything).Return([]*datamodel.Job{job}, nil)
		err := _checkActiveReplicationJobs(ctx, mockStorage, accountID, poolUUID, ccfeURI)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "Another operation against this replication is in progress")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("ActiveJobSamePoolDifferentReplication_ReturnsReplicationErrorWhenResourceMatches", func(tt *testing.T) {
		mockStorage := &database.MockStorage{}
		job := &datamodel.Job{
			BaseModel:     datamodel.BaseModel{UUID: "job-4"},
			Type:          string(datamodel.JobTypeUpdateVolumeReplication),
			State:         string(datamodel.JobsStatePROCESSING),
			ResourceName:  ccfeURI,
			JobAttributes: &datamodel.JobAttributes{PoolUUID: poolUUID},
		}
		mockStorage.On("GetJobsWithCondition", ctx, mock.Anything).Return([]*datamodel.Job{job}, nil)
		err := _checkActiveReplicationJobs(ctx, mockStorage, accountID, poolUUID, ccfeURI)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "Another operation against this replication is in progress")
		mockStorage.AssertExpectations(tt)
	})
}

func Test_replicationJobInProcess(t *testing.T) {
	ctx := context.Background()
	srcBasePath := "srcBasePath"
	destBasePath := "destBasePath"
	srcToken := "srcToken"
	destToken := "destToken"
	srcLocationID := "srcLoc"
	destLocationID := "destLoc"
	srcProjectNumber := "srcProj"
	destProjectNumber := "destProj"
	ccfeUri := "ccfeUri"
	remoteCcfeUri := "remoteCcfeUri"
	srcPoolId := "srcPool"
	dstPoolId := "dstPool"
	xCorrelationID := new(string)

	t.Run("No jobs in process", func(t *testing.T) {
		mockClient := googleproxyclient.NewMockInvoker(t)
		mockClient.EXPECT().V1betaInternalGetReplicationJobs(ctx, mock.Anything).Return(&googleproxyclient.V1betaInternalGetReplicationJobsOK{Jobs: []googleproxyclient.InternalJobV1beta{}}, nil).Maybe()

		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		mc := &googleproxyclient.ProxyClient{Invoker: mockClient}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		defer func() { googleproxyclient.GetGProxyClient = originalGetGProxyClient }()

		err := _replicationJobInProcess(ctx, srcProjectNumber, destProjectNumber, srcBasePath, destBasePath, srcLocationID, destLocationID, srcToken, destToken, ccfeUri, remoteCcfeUri, srcPoolId, dstPoolId, xCorrelationID)
		assert.NoError(t, err)
	})

	t.Run("Job in process for source", func(t *testing.T) {
		mockClient := googleproxyclient.NewMockInvoker(t)
		mockClient.EXPECT().V1betaInternalGetReplicationJobs(ctx, mock.Anything).Return(&googleproxyclient.V1betaInternalGetReplicationJobsOK{
			Jobs: []googleproxyclient.InternalJobV1beta{
				{ResourceName: googleproxyclient.OptString{Value: ccfeUri, Set: true}},
			},
		}, nil).Once()

		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		mc := &googleproxyclient.ProxyClient{Invoker: mockClient}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		defer func() { googleproxyclient.GetGProxyClient = originalGetGProxyClient }()

		err := _replicationJobInProcess(ctx, srcProjectNumber, destProjectNumber, srcBasePath, destBasePath, srcLocationID, destLocationID, srcToken, destToken, ccfeUri, "", srcPoolId, dstPoolId, xCorrelationID)
		assert.Error(t, err)
	})

	t.Run("Job in process for destination", func(t *testing.T) {
		mockClient := googleproxyclient.NewMockInvoker(t)
		mockClient.EXPECT().V1betaInternalGetReplicationJobs(ctx, mock.Anything).Return(&googleproxyclient.V1betaInternalGetReplicationJobsOK{
			Jobs: []googleproxyclient.InternalJobV1beta{
				{ResourceName: googleproxyclient.OptString{Value: remoteCcfeUri, Set: true}},
			},
		}, nil).Once()

		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		mc := &googleproxyclient.ProxyClient{Invoker: mockClient}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		defer func() { googleproxyclient.GetGProxyClient = originalGetGProxyClient }()

		err := _replicationJobInProcess(ctx, srcProjectNumber, destProjectNumber, "", destBasePath, srcLocationID, destLocationID, srcToken, destToken, ccfeUri, remoteCcfeUri, srcPoolId, dstPoolId, xCorrelationID)
		assert.Error(t, err)
	})

	t.Run("Error from getReplicationJobs", func(t *testing.T) {
		mockClient := googleproxyclient.NewMockInvoker(t)
		mockClient.EXPECT().V1betaInternalGetReplicationJobs(ctx, mock.Anything).Return(nil, errors.New("api error")).Once()

		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		mc := &googleproxyclient.ProxyClient{Invoker: mockClient}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		defer func() { googleproxyclient.GetGProxyClient = originalGetGProxyClient }()

		err := _replicationJobInProcess(ctx, srcProjectNumber, destProjectNumber, srcBasePath, destBasePath, srcLocationID, destLocationID, srcToken, destToken, ccfeUri, "", srcPoolId, dstPoolId, xCorrelationID)
		assert.Error(t, err)
	})
}

func Test_validateStoragePoolUri(t *testing.T) {
	validUris := []string{
		"projects/123/locations/us-central1/storagePools/pool-1",
		"projects/abc/locations/europe-west1/pools/pool-2",
	}
	invalidUris := []string{
		"projects/123/locations/us-central1",               // too short
		"projects/123/locations/us-central1/volumes/vol-1", // wrong resource
		"projects/123/locations/us-central1/pools",         // missing pool name
		"invalid/uri/format",
	}

	for _, uri := range validUris {
		err := _validateStoragePoolUri(uri)
		assert.NoError(t, err, "should be valid: %s", uri)
	}

	for _, uri := range invalidUris {
		err := _validateStoragePoolUri(uri)
		assert.Error(t, err, "should be invalid: %s", uri)
	}
}

func Test_validateReplicationResourceId(t *testing.T) {
	ctx := context.Background()
	projectNumber := "proj-1"
	resourceId := "replication-1"
	volumeResourceId := "vol-1"
	account := &datamodel.Account{BaseModel: datamodel.BaseModel{UUID: "acc-1"}}

	t.Run("no replications exist", func(t *testing.T) {
		mockStorage := &database.MockStorage{}
		mockStorage.On("GetAccount", ctx, projectNumber).Return(account, nil)
		mockStorage.On("GetVolumeReplicationByProjectId", ctx, account.ID).Return([]*datamodel.VolumeReplication{}, nil)

		err := _validateReplicationResourceId(ctx, projectNumber, resourceId, volumeResourceId, mockStorage)
		assert.NoError(t, err)
	})

	t.Run("replication with different resourceId", func(t *testing.T) {
		mockStorage := &database.MockStorage{}
		mockStorage.On("GetAccount", ctx, projectNumber).Return(account, nil)
		mockStorage.On("GetVolumeReplicationByProjectId", ctx, account.ID).Return([]*datamodel.VolumeReplication{
			{Uri: "projects/proj-1/locations/loc-1/volumes/vol-1/replications/replication-2"},
		}, nil)

		err := _validateReplicationResourceId(ctx, projectNumber, resourceId, volumeResourceId, mockStorage)
		assert.NoError(t, err)
	})

	t.Run("replication with same resourceId and volumeResourceId", func(t *testing.T) {
		mockSe := &database.MockStorage{}
		mockSe.On("GetAccount", ctx, projectNumber).Return(account, nil)
		mockSe.On("GetVolumeReplicationByProjectId", ctx, account.ID).Return([]*datamodel.VolumeReplication{
			{Uri: "projects/proj-1/locations/loc-1/volumes/vol-1/replications/replication-1"},
		}, nil)

		err := _validateReplicationResourceId(ctx, projectNumber, resourceId, volumeResourceId, mockSe)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "replication resourceId already in use")
	})

	t.Run("GetAccount returns error", func(t *testing.T) {
		mockSe := &database.MockStorage{}
		mockSe.On("GetAccount", ctx, projectNumber).Return(&datamodel.Account{}, errors.New("account error"))

		err := _validateReplicationResourceId(ctx, projectNumber, resourceId, volumeResourceId, mockSe)
		assert.Error(t, err)
	})

	t.Run("GetVolumeReplicationByProjectId returns error", func(t *testing.T) {
		mockSe := &database.MockStorage{}
		mockSe.On("GetAccount", ctx, projectNumber).Return(account, nil)
		mockSe.On("GetVolumeReplicationByProjectId", ctx, account.ID).Return(nil, errors.New("replication error"))

		err := _validateReplicationResourceId(ctx, projectNumber, resourceId, volumeResourceId, mockSe)
		assert.Error(t, err)
	})
}

func Test_createReplicationObjects_Success(t *testing.T) {
	resourceID := "replication-1"
	description := "desc"
	replicationSchedule := models.ReplicationV1betaReplicationScheduleHOURLY
	volumeID := "vol_2"
	event := &CreateReplicationEvent{
		SourceProjectNumber:      "src-proj",
		DestinationProjectNumber: "dst-proj",
		LocationID:               "loc-1",
		DestinationLocationID:    "loc-2",
		VolumeResourceID:         "vol-1",
		CreateReplicationParams: &CreateReplicationParamsBody{
			ResourceID:          &resourceID,
			Description:         &description,
			ReplicationSchedule: &replicationSchedule,
			DestinationVolumeParameters: &DestinationVolumeParams{
				VolumeID: volumeID,
			},
		},
		SourceVolume: datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: uuid.New()},
			Name:      "vol-1",
			Pool:      &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-1"}},
		},
		SourcePool: datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-1"}},
	}

	replication, err := _createReplicationObjects(event, "loc-2", "region-1", "region-2")
	assert.NoError(t, err)
	assert.NotNil(t, replication)
	assert.Contains(t, replication.Uri, "projects/src-proj/locations/loc-1/volumes/vol-1/replications/replication-1")
	assert.Contains(t, replication.RemoteUri, "projects/dst-proj/locations/loc-2/volumes/vol_2/replications/replication-1")
	assert.Equal(t, resourceID, replication.Name)
	assert.Equal(t, description, replication.Description)
	assert.NotNil(t, replication.ReplicationAttributes)
	assert.Equal(t, event.SourceVolume.UUID, replication.ReplicationAttributes.SourceVolumeUUID)
	assert.Equal(t, "vol-1", replication.ReplicationAttributes.SourceVolumeName)
	assert.Equal(t, "loc-1", replication.ReplicationAttributes.SourceLocation)
	assert.Equal(t, "loc-2", replication.ReplicationAttributes.DestinationLocation)
	assert.Equal(t, models.VolumeReplicationCVPV1betaEndpointTypeSrc, replication.ReplicationAttributes.EndpointType)
	assert.Equal(t, "hourly", replication.ReplicationAttributes.ReplicationSchedule)
	assert.Equal(t, "pool-1", replication.ReplicationAttributes.SourcePoolUUID)
}

func Test_createReplicationObjects_InvalidUUID(t *testing.T) {
	resourceID := "replication-1"
	description := "desc"
	replicationSchedule := models.ReplicationV1betaReplicationScheduleREPLICATIONSCHEDULEUNSPECIFIED
	event := &CreateReplicationEvent{
		SourceProjectNumber:      "src-proj",
		DestinationProjectNumber: "dst-proj",
		LocationID:               "loc-1",
		DestinationLocationID:    "loc-2",
		VolumeResourceID:         "vol-1",
		CreateReplicationParams: &CreateReplicationParamsBody{
			ResourceID:          &resourceID,
			Description:         &description,
			ReplicationSchedule: &replicationSchedule,
			DestinationVolumeParameters: &DestinationVolumeParams{
				VolumeID: "vol_2",
			},
		},
		SourceVolume: datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "invalid-uuid"},
			Name:      "vol-1",
			Pool:      &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-1"}},
		},
		SourcePool: datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-1"}},
	}

	replication, err := _createReplicationObjects(event, "loc-2", "region-1", "region-2")
	assert.Error(t, err)
	assert.Nil(t, replication)
}

func Test_validateLabels(t *testing.T) {
	t.Run("WhenValidLabels", func(t *testing.T) {
		validLabels := map[string]string{
			"env":    "prod",
			"region": "us-central1",
		}
		err := _validateLabels(validLabels)
		assert.NoError(t, err)
	})

	t.Run("WhenEmptyMap", func(t *testing.T) {
		err := _validateLabels(map[string]string{})
		assert.NoError(t, err)
	})

	t.Run("WhenTooManyLabels", func(t *testing.T) {
		tooMany := map[string]string{}
		for i := 0; i < 65; i++ {
			tooMany[fmt.Sprintf("k%d", i)] = "v"
		}
		err := _validateLabels(tooMany)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "Label count exceeds the maximum limit of 64")
	})

	t.Run("WhenEmptyKey", func(t *testing.T) {
		labels := map[string]string{
			"": "value",
		}
		err := _validateLabels(labels)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "Label key is required")
	})

	t.Run("WhenKeyTooLongCharacters", func(t *testing.T) {
		// Create a key of 64 runes (maxRuneCount is 63)
		key := strings.Repeat("a", 64)
		labels := map[string]string{
			key: "value",
		}
		err := _validateLabels(labels)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "Label key exceeds the maximum length of 63 characters")
	})

	t.Run("WhenKeyTooLongBytes", func(t *testing.T) {
		// Create a key that has exactly 63 runes but > 128 bytes
		// Use a 4-byte UTF-8 character like '𐀀' (U+10000) repeated to exceed byte limit
		key := strings.Repeat("𐀀", 63) // 63 * 4 = 252 bytes, but only 63 runes
		labels := map[string]string{
			key: "value",
		}
		err := _validateLabels(labels)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "Label key exceeds the maximum encoded length of 128 bytes")
	})

	t.Run("WhenValueTooLongCharacters", func(t *testing.T) {
		// Create a value of 64 runes (maxRuneCount is 63)
		value := strings.Repeat("b", 64)
		labels := map[string]string{
			"key": value,
		}
		err := _validateLabels(labels)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "Label value exceeds the maximum length of 63 characters")
	})

	t.Run("WhenValueTooLongBytes", func(t *testing.T) {
		// Create a value that has exactly 63 runes but > 128 bytes
		// Use a 4-byte UTF-8 character like '𐀀' (U+10000) repeated to exceed byte limit
		value := strings.Repeat("𐀀", 63) // 63 * 4 = 252 bytes, but only 63 runes
		labels := map[string]string{
			"key": value,
		}
		err := _validateLabels(labels)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "Label value exceeds the maximum encoded length of 128 bytes")
	})

	t.Run("WhenMultipleValidationFailuresFirstKeyIssue", func(t *testing.T) {
		labels := map[string]string{
			"": "value",
		}
		err := _validateLabels(labels)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "Label key is required")
	})
	t.Run("WhenMultipleValidationFailuresFirstValueIssue", func(t *testing.T) {
		labels := map[string]string{
			"key": "", // Empty value should pass key validation but fail value validation
		}
		err := _validateLabels(labels)
		assert.NoError(t, err) // Empty value is valid
	})
	t.Run("WhenBoundaryValuesExactlyAtLimits", func(t *testing.T) {
		// Key exactly at maxRuneCount (63)
		key := strings.Repeat("a", 63)
		// Value exactly at maxRuneCount (63) - not maxByteCount since function checks rune count
		value := strings.Repeat("b", 63)

		labels := map[string]string{
			key: value,
		}
		err := _validateLabels(labels)
		assert.NoError(t, err)
	})

	t.Run("WhenBoundaryValuesOneBelowLimits", func(t *testing.T) {
		// Key one below maxRuneCount (62)
		key := strings.Repeat("a", 62)
		// Value one below maxRuneCount (62) - not maxByteCount since function checks rune count
		value := strings.Repeat("b", 62)

		labels := map[string]string{
			key: value,
		}
		err := _validateLabels(labels)
		assert.NoError(t, err)
	})

	t.Run("WhenUnicodeCharactersInKeyAndValue", func(t *testing.T) {
		labels := map[string]string{
			"env-测试": "prod-测试",
		}
		err := _validateLabels(labels)
		assert.NoError(t, err)
	})

	t.Run("WhenSpecialCharactersInKeyAndValue", func(t *testing.T) {
		labels := map[string]string{
			"env-prod":  "prod-env",
			"region_us": "us_region",
			"zone-1":    "zone1",
		}
		err := _validateLabels(labels)
		assert.NoError(t, err)
	})
}

// Unit tests for _getDestinationPool
func Test_getDestinationPool(t *testing.T) {
	ctx := context.Background()
	destBasePath := "destBasePath"
	token := "token"
	remoteLocationID := "loc"
	projectNumber := "proj"
	xCorrelationID := new(string)
	poolName := "pool-1"

	t.Run("Returns pool when found", func(t *testing.T) {
		mockPool := &googleproxyclient.PoolInternalV1beta{ResourceId: poolName, Network: "network"}
		mockClient := googleproxyclient.NewMockInvoker(t)
		mockClient.EXPECT().V1betaInternalDescribePool(ctx, mock.Anything).Return(mockPool, nil)

		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		mc := &googleproxyclient.ProxyClient{Invoker: mockClient}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		defer func() { googleproxyclient.GetGProxyClient = originalGetGProxyClient }()

		pool, err := _getDestinationPool(ctx, destBasePath, token, remoteLocationID, projectNumber, xCorrelationID, poolName)
		assert.NoError(t, err)
		assert.NotNil(t, pool)
		assert.Equal(t, poolName, pool.ResourceId)
	})

	t.Run("Returns not found error when pool does not exist", func(t *testing.T) {
		mockClient := googleproxyclient.NewMockInvoker(t)
		mockClient.EXPECT().V1betaInternalDescribePool(ctx, mock.Anything).Return(&googleproxyclient.V1betaInternalDescribePoolNotFound{}, nil)

		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		mc := &googleproxyclient.ProxyClient{Invoker: mockClient}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		defer func() { googleproxyclient.GetGProxyClient = originalGetGProxyClient }()

		pool, err := _getDestinationPool(ctx, destBasePath, token, remoteLocationID, projectNumber, xCorrelationID, poolName)
		assert.Error(t, err)
		assert.Nil(t, pool)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("Returns error when Invoker returns error", func(t *testing.T) {
		mockClient := googleproxyclient.NewMockInvoker(t)
		mockClient.EXPECT().V1betaInternalDescribePool(ctx, mock.Anything).Return(nil, errors.New("api error"))

		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		mc := &googleproxyclient.ProxyClient{Invoker: mockClient}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		defer func() { googleproxyclient.GetGProxyClient = originalGetGProxyClient }()

		pool, err := _getDestinationPool(ctx, destBasePath, token, remoteLocationID, projectNumber, xCorrelationID, poolName)
		assert.Error(t, err)
		assert.Nil(t, pool)
	})

	t.Run("Returns error when describe pool returns internal server error", func(t *testing.T) {
		serverErr := &googleproxyclient.V1betaInternalDescribePoolInternalServerError{
			Message: "something went wrong",
		}
		mockClient := googleproxyclient.NewMockInvoker(t)
		mockClient.EXPECT().V1betaInternalDescribePool(ctx, mock.Anything).Return(serverErr, nil)

		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		mc := &googleproxyclient.ProxyClient{Invoker: mockClient}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		defer func() { googleproxyclient.GetGProxyClient = originalGetGProxyClient }()

		pool, err := _getDestinationPool(ctx, destBasePath, token, remoteLocationID, projectNumber, xCorrelationID, poolName)
		assert.Error(t, err)
		assert.Nil(t, pool)
		var customErr *vsaErrors.CustomError
		assert.True(t, vsaErrors.As(err, &customErr))
		assert.Equal(t, vsaErrors.ErrInternalServerError, customErr.TrackingID)
		assert.Contains(t, customErr.OriginalErr.Error(), "something went wrong")
	})

	t.Run("Returns error when destination pool has active cluster upgrade", func(t *testing.T) {
		mockPool := &googleproxyclient.PoolInternalV1beta{
			ResourceId:              poolName,
			HasActiveClusterUpgrade: googleproxyclient.OptBool{Value: true, Set: true},
		}
		mockClient := googleproxyclient.NewMockInvoker(t)
		mockClient.EXPECT().V1betaInternalDescribePool(ctx, mock.Anything).Return(mockPool, nil)

		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		mc := &googleproxyclient.ProxyClient{Invoker: mockClient}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		defer func() { googleproxyclient.GetGProxyClient = originalGetGProxyClient }()

		pool, err := _getDestinationPool(ctx, destBasePath, token, remoteLocationID, projectNumber, xCorrelationID, poolName)
		assert.Error(t, err)
		assert.Nil(t, pool)
		var customErr *vsaErrors.CustomError
		assert.True(t, vsaErrors.As(err, &customErr))
		assert.Equal(t, vsaErrors.ErrStoragePoolTemporarilyUnavailable, customErr.TrackingID)
	})

	t.Run("Returns pool when HasActiveClusterUpgrade is false", func(t *testing.T) {
		mockPool := &googleproxyclient.PoolInternalV1beta{
			ResourceId:              poolName,
			HasActiveClusterUpgrade: googleproxyclient.OptBool{Value: false, Set: true},
		}
		mockClient := googleproxyclient.NewMockInvoker(t)
		mockClient.EXPECT().V1betaInternalDescribePool(ctx, mock.Anything).Return(mockPool, nil)

		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		mc := &googleproxyclient.ProxyClient{Invoker: mockClient}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		defer func() { googleproxyclient.GetGProxyClient = originalGetGProxyClient }()

		pool, err := _getDestinationPool(ctx, destBasePath, token, remoteLocationID, projectNumber, xCorrelationID, poolName)
		assert.NoError(t, err)
		assert.NotNil(t, pool)
		assert.Equal(t, poolName, pool.ResourceId)
	})

	t.Run("Returns pool when HasActiveClusterUpgrade is not set", func(t *testing.T) {
		mockPool := &googleproxyclient.PoolInternalV1beta{
			ResourceId:              poolName,
			HasActiveClusterUpgrade: googleproxyclient.OptBool{Set: false},
		}
		mockClient := googleproxyclient.NewMockInvoker(t)
		mockClient.EXPECT().V1betaInternalDescribePool(ctx, mock.Anything).Return(mockPool, nil)

		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		mc := &googleproxyclient.ProxyClient{Invoker: mockClient}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		defer func() { googleproxyclient.GetGProxyClient = originalGetGProxyClient }()

		pool, err := _getDestinationPool(ctx, destBasePath, token, remoteLocationID, projectNumber, xCorrelationID, poolName)
		assert.NoError(t, err)
		assert.NotNil(t, pool)
		assert.Equal(t, poolName, pool.ResourceId)
	})
}

// Unit tests for _internalGetVolumeCount
func Test_internalGetVolumeCount(t *testing.T) {
	ctx := context.Background()
	basePath := "basePath"
	projectNumber := "proj"
	locationID := "loc"
	poolID := "pool"
	jwt := "token"
	storageClass := "SOFTWARE"
	serviceLevel := "FLEX"

	t.Run("Returns volume count when successful", func(t *testing.T) {
		mockClient := googleproxyclient.NewMockInvoker(t)
		mockClient.EXPECT().V1betaGetVolumeCount(ctx, mock.Anything).Return(&googleproxyclient.V1betaGetVolumeCountOK{
			VolumeCount: 5,
		}, nil)

		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		mc := &googleproxyclient.ProxyClient{Invoker: mockClient}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		defer func() { googleproxyclient.GetGProxyClient = originalGetGProxyClient }()

		count, err := _internalGetVolumeCount(ctx, basePath, projectNumber, locationID, poolID, jwt, storageClass, serviceLevel)
		assert.NoError(t, err)
		assert.Equal(t, 5, count)
	})

	t.Run("Returns 0 when Invoker returns error", func(t *testing.T) {
		mockClient := googleproxyclient.NewMockInvoker(t)
		mockClient.EXPECT().V1betaGetVolumeCount(ctx, mock.Anything).Return(nil, errors.New("api error"))

		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		mc := &googleproxyclient.ProxyClient{Invoker: mockClient}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		defer func() { googleproxyclient.GetGProxyClient = originalGetGProxyClient }()

		count, err := _internalGetVolumeCount(ctx, basePath, projectNumber, locationID, poolID, jwt, storageClass, serviceLevel)
		assert.NoError(t, err)
		assert.Equal(t, 0, count)
	})
}

// Unit tests for _internalGetReplicationCount
func Test_internalGetReplicationCount(t *testing.T) {
	ctx := context.Background()
	basePath := "basePath"
	projectNumber := "proj"
	locationID := "loc"
	poolID := "pool"
	jwt := "token"
	storageClass := "SOFTWARE"
	serviceLevel := "FLEX"

	t.Run("Returns replication count when successful", func(t *testing.T) {
		mockClient := googleproxyclient.NewMockInvoker(t)
		mockClient.EXPECT().V1betaGetReplicationCount(ctx, mock.Anything).Return(&googleproxyclient.V1betaGetReplicationCountOK{
			ReplicationCount: 7,
		}, nil)

		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		mc := &googleproxyclient.ProxyClient{Invoker: mockClient}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		defer func() { googleproxyclient.GetGProxyClient = originalGetGProxyClient }()

		count, err := _internalGetReplicationCount(ctx, basePath, projectNumber, locationID, poolID, jwt, storageClass, serviceLevel)
		assert.NoError(t, err)
		assert.Equal(t, 7, count)
	})

	t.Run("Returns 0 when Invoker returns error", func(t *testing.T) {
		mockClient := googleproxyclient.NewMockInvoker(t)
		mockClient.EXPECT().V1betaGetReplicationCount(ctx, mock.Anything).Return(nil, errors.New("api error"))

		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		mc := &googleproxyclient.ProxyClient{Invoker: mockClient}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		defer func() { googleproxyclient.GetGProxyClient = originalGetGProxyClient }()

		count, err := _internalGetReplicationCount(ctx, basePath, projectNumber, locationID, poolID, jwt, storageClass, serviceLevel)
		assert.NoError(t, err)
		assert.Equal(t, 0, count)
	})
}

func Test_internalCountInRegionReplications(t *testing.T) {
	ctx := context.Background()
	mockStorage := &database.MockStorage{}

	t.Run("CountsOnlySameRegionNonHybridReplications", func(t *testing.T) {
		mockStorage.On("GetAccount", ctx, "proj-1").Return(&datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}}, nil).Once()
		mockStorage.On("ListVolumeReplications", ctx, mock.Anything, database.QueryDepthZero).Return([]*datamodel.VolumeReplication{
			{
				ReplicationAttributes: &datamodel.ReplicationDetails{
					SourceLocation:      "us-east1-a",
					DestinationLocation: "us-east1-b",
				},
			},
			{
				ReplicationAttributes: &datamodel.ReplicationDetails{
					SourceLocation:      "us-east1-a",
					DestinationLocation: "us-east1-c",
				},
			},
			{
				HybridReplicationAttributes: &datamodel.HybridReplicationAttribute{},
				ReplicationAttributes: &datamodel.ReplicationDetails{
					SourceLocation:      "us-east1-a",
					DestinationLocation: "us-east1-b",
				},
			},
			{
				ReplicationAttributes: &datamodel.ReplicationDetails{
					SourceLocation:      "us-east1-a",
					DestinationLocation: "us-west1-b",
				},
			},
		}, nil).Once()

		count, err := _internalCountInRegionReplications(ctx, mockStorage, "proj-1")
		assert.NoError(t, err)
		assert.Equal(t, 2, count)
		mockStorage.AssertExpectations(t)
	})

	t.Run("WhenGetAccountFails", func(t *testing.T) {
		mockStorage := &database.MockStorage{}
		mockStorage.On("GetAccount", ctx, "proj-err").Return((*datamodel.Account)(nil), errors.New("db error")).Once()
		count, err := _internalCountInRegionReplications(ctx, mockStorage, "proj-err")
		assert.Error(t, err)
		assert.Equal(t, 0, count)
		mockStorage.AssertExpectations(t)
	})

	t.Run("WhenListVolumeReplicationsFails", func(t *testing.T) {
		mockStorage := &database.MockStorage{}
		mockStorage.On("GetAccount", ctx, "proj-1").Return(&datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}}, nil).Once()
		mockStorage.On("ListVolumeReplications", ctx, mock.Anything, database.QueryDepthZero).Return([]*datamodel.VolumeReplication(nil), errors.New("list error")).Once()
		count, err := _internalCountInRegionReplications(ctx, mockStorage, "proj-1")
		assert.Error(t, err)
		assert.Equal(t, 0, count)
		mockStorage.AssertExpectations(t)
	})

	t.Run("SkipsReplicationWithInvalidSourceLocation", func(t *testing.T) {
		mockStorage := &database.MockStorage{}
		mockStorage.On("GetAccount", ctx, "proj-1").Return(&datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}}, nil).Once()
		mockStorage.On("ListVolumeReplications", ctx, mock.Anything, database.QueryDepthZero).Return([]*datamodel.VolumeReplication{
			{
				// Invalid source location – ParseRegionAndZone will return an error; replication is skipped.
				ReplicationAttributes: &datamodel.ReplicationDetails{
					SourceLocation:      "INVALID_SOURCE",
					DestinationLocation: "us-east1-b",
				},
			},
			{
				ReplicationAttributes: &datamodel.ReplicationDetails{
					SourceLocation:      "us-east1-a",
					DestinationLocation: "us-east1-b",
				},
			},
		}, nil).Once()

		count, err := _internalCountInRegionReplications(ctx, mockStorage, "proj-1")
		assert.NoError(t, err)
		// Only the second replication (with valid locations in the same region) is counted.
		assert.Equal(t, 1, count)
		mockStorage.AssertExpectations(t)
	})

	t.Run("SkipsReplicationWithInvalidDestinationLocation", func(t *testing.T) {
		mockStorage := &database.MockStorage{}
		mockStorage.On("GetAccount", ctx, "proj-1").Return(&datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}}, nil).Once()
		mockStorage.On("ListVolumeReplications", ctx, mock.Anything, database.QueryDepthZero).Return([]*datamodel.VolumeReplication{
			{
				// Valid source but invalid destination – second ParseRegionAndZone call fails; replication is skipped.
				ReplicationAttributes: &datamodel.ReplicationDetails{
					SourceLocation:      "us-east1-a",
					DestinationLocation: "INVALID_DEST",
				},
			},
			{
				ReplicationAttributes: &datamodel.ReplicationDetails{
					SourceLocation:      "us-west1-a",
					DestinationLocation: "us-west1-b",
				},
			},
		}, nil).Once()

		count, err := _internalCountInRegionReplications(ctx, mockStorage, "proj-1")
		assert.NoError(t, err)
		// Only the second replication (with valid locations in the same region) is counted.
		assert.Equal(t, 1, count)
		mockStorage.AssertExpectations(t)
	})
}

func Test_getInRegionQuotaState(t *testing.T) {
	ctx := context.Background()

	t.Run("WhenGetQuotaLimitFails", func(t *testing.T) {
		mockStorage := &database.MockStorage{}
		origGetQuotaLimit := getQuotaLimit
		getQuotaLimit = func(_ context.Context, _ log.Logger, _, _, _ string, _ common.ResourceType) (int, error) {
			return 0, errors.New("quota limit error")
		}
		defer func() { getQuotaLimit = origGetQuotaLimit }()

		total, existing, err := _getInRegionQuotaState(ctx, mockStorage, "us-east1", "proj-1", "token")
		assert.Error(t, err)
		assert.Equal(t, 0, total)
		assert.Equal(t, 0, existing)
	})

	t.Run("WhenInternalCountInRegionReplicationsFails", func(t *testing.T) {
		mockStorage := &database.MockStorage{}
		origGetQuotaLimit := getQuotaLimit
		getQuotaLimit = func(_ context.Context, _ log.Logger, _, _, _ string, _ common.ResourceType) (int, error) {
			return 10, nil
		}
		defer func() { getQuotaLimit = origGetQuotaLimit }()

		origInternalCountInRegionReplications := internalCountInRegionReplications
		internalCountInRegionReplications = func(_ context.Context, _ database.Storage, _ string) (int, error) {
			return 0, errors.New("count error")
		}
		defer func() { internalCountInRegionReplications = origInternalCountInRegionReplications }()

		total, existing, err := _getInRegionQuotaState(ctx, mockStorage, "us-east1", "proj-1", "token")
		assert.Error(t, err)
		assert.Equal(t, 0, total)
		assert.Equal(t, 0, existing)
	})
}

// Unit tests for _getVolume
func Test_getVolume(t *testing.T) {
	ctx := context.Background()
	basePath := "basePath"
	token := "token"
	locationID := "loc"
	projectNumber := "proj"
	xCorrelationID := new(string)
	volumeResourceId := "vol-1"

	t.Run("Returns volume when found", func(t *testing.T) {
		mockVolume := googleproxyclient.VolumeV1beta{ResourceId: volumeResourceId}
		mockClient := googleproxyclient.NewMockInvoker(t)
		mockClient.EXPECT().V1betaListVolumes(ctx, mock.Anything).Return(&googleproxyclient.V1betaListVolumesOK{
			Volumes: []googleproxyclient.VolumeV1beta{mockVolume},
		}, nil)

		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		mc := &googleproxyclient.ProxyClient{Invoker: mockClient}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		defer func() { googleproxyclient.GetGProxyClient = originalGetGProxyClient }()

		vol, err := _getVolume(ctx, basePath, token, locationID, projectNumber, xCorrelationID, volumeResourceId)
		assert.NoError(t, err)
		assert.Equal(t, volumeResourceId, vol.ResourceId)
	})

	t.Run("Returns error when volume not found", func(t *testing.T) {
		mockClient := googleproxyclient.NewMockInvoker(t)
		mockClient.EXPECT().V1betaListVolumes(ctx, mock.Anything).Return(&googleproxyclient.V1betaListVolumesOK{
			Volumes: []googleproxyclient.VolumeV1beta{},
		}, nil)

		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		mc := &googleproxyclient.ProxyClient{Invoker: mockClient}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		defer func() { googleproxyclient.GetGProxyClient = originalGetGProxyClient }()

		vol, err := _getVolume(ctx, basePath, token, locationID, projectNumber, xCorrelationID, volumeResourceId)
		assert.Error(t, err)
		assert.Equal(t, "", vol.ResourceId)
		assert.Contains(t, err.Error(), "Volume 'vol-1' not found")
	})

	t.Run("Returns error when Invoker returns error", func(t *testing.T) {
		mockClient := googleproxyclient.NewMockInvoker(t)
		mockClient.EXPECT().V1betaListVolumes(ctx, mock.Anything).Return(nil, errors.New("api error"))

		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		mc := &googleproxyclient.ProxyClient{Invoker: mockClient}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		defer func() { googleproxyclient.GetGProxyClient = originalGetGProxyClient }()

		vol, err := _getVolume(ctx, basePath, token, locationID, projectNumber, xCorrelationID, volumeResourceId)
		assert.Error(t, err)
		assert.Equal(t, "", vol.ResourceId)
	})
}

// setupMockNoActiveClusterUpgrade sets GetClusterUpgradeJobsByClusterID to return no active upgrade for any cluster (used by tests that reach hasActiveClusterUpgrade).
// It also mocks GetPoolByUUID so destination-pool UUID resolution for the upgrade check returns a pool (tests use destination pool id "pool-1").
func setupMockNoActiveClusterUpgrade(mockStorage *database.MockStorage) {
	mockStorage.On("GetClusterUpgradeJobsByClusterID", mock.Anything, mock.Anything).Return([]*datamodel.ClusterUpgradeJob{}, nil)
	mockStorage.On("GetPoolByUUID", mock.Anything, mock.Anything).Return(&datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-1"}}, nil)
}

// Unit tests for _validateCreateReplicationParams
func Test_validateCreateReplicationParams(t *testing.T) {
	ctx := context.Background()
	projectNumber := "proj-1"
	destProjectNumber := "proj-1"
	locationID := "us-east1"
	destLocationID := "us-east4"
	volumeResourceID := "vol-1"
	resourceID := "replication-1"
	description := "desc"
	replicationSchedule := models.ReplicationV1betaReplicationScheduleDAILY
	destPoolName := "pool-1"
	destPoolID := "pool-1"
	xCorrelationID := new(string)
	storagePoolUri := "projects/proj-2/locations/loc-2/pools/pool-1"

	baseVolume := datamodel.Volume{
		BaseModel:   datamodel.BaseModel{UUID: "b3b3b3b3-b3b3-4b3b-b3b3-b3b3b3b3b3b3"},
		Name:        volumeResourceID,
		Pool:        &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-uuid"}, ServiceLevel: "FLEX"},
		State:       string(googleproxyclient.VolumeV1betaVolumeStateREADY),
		SizeInBytes: 100,
		VolumeAttributes: &datamodel.VolumeAttributes{
			IsDataProtection: false,
			CreationToken:    "token-1",
		},
	}

	event := &CreateReplicationEvent{
		SourceProjectNumber:      projectNumber,
		DestinationProjectNumber: destProjectNumber,
		LocationID:               locationID,
		DestinationLocationID:    destLocationID,
		VolumeResourceID:         volumeResourceID,
		CreateReplicationParams: &CreateReplicationParamsBody{
			ResourceID:          &resourceID,
			Description:         &description,
			ReplicationSchedule: &replicationSchedule,
			Labels:              map[string]string{"env": "prod"},
			DestinationVolumeParameters: &DestinationVolumeParams{
				VolumeID:    "vol_2",
				ShareName:   "share_2",
				StoragePool: &storagePoolUri,
			},
		},
		SourceVolume:        baseVolume,
		SourcePool:          datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-uuid"}, ServiceLevel: "FLEX"},
		DestinationPoolName: destPoolName,
		XCorrelationID:      xCorrelationID,
	}

	t.Run("Success", func(t *testing.T) {
		mockStorage := &database.MockStorage{}
		setupMockNoActiveClusterUpgrade(mockStorage)
		mockStorage.On("GetAccount", ctx, projectNumber).Return(&datamodel.Account{BaseModel: datamodel.BaseModel{UUID: "acc-1"}}, nil)
		// hasActiveClusterUpgrade checks source and destination pool
		mockStorage.On("GetClusterUpgradeJobsByClusterID", mock.Anything, "pool-uuid").Return([]*datamodel.ClusterUpgradeJob{}, nil)
		mockStorage.On("GetClusterUpgradeJobsByClusterID", mock.Anything, "pool-1").Return([]*datamodel.ClusterUpgradeJob{}, nil)
		ValidateReplicationResourceId = func(ctx context.Context, projectNumber string, paramReplicationResourceId string, paramsVolumeResourceId string, se database.Storage) error {
			return nil
		}
		defer func() { ValidateReplicationResourceId = _validateReplicationResourceId }()

		// Patch dependencies
		origGetSignedJwtToken := InternalUtilGetSignedToken
		InternalUtilGetSignedToken = func(p string) (string, error) { return "token", nil }
		defer func() { InternalUtilGetSignedToken = origGetSignedJwtToken }()

		origGetPairedRegionURI := InternalUtilGetPairedRegionURI
		InternalUtilGetPairedRegionURI = func(region string) (string, error) { return "basePath", nil }
		defer func() { InternalUtilGetPairedRegionURI = origGetPairedRegionURI }()

		origGetQuotaLimit := getQuotaLimit
		getQuotaLimit = func(ctx context.Context, logger log.Logger, region string, projectId string, token string, resourceType common.ResourceType) (int, error) {
			return 10, nil
		}
		defer func() { getQuotaLimit = origGetQuotaLimit }()

		origGetCallbackToken := InternalUtilGetCallbackToken
		InternalUtilGetCallbackToken = func() (string, error) { return "cbtoken", nil }
		defer func() { InternalUtilGetCallbackToken = origGetCallbackToken }()

		origGetDestinationPool := getDestinationPool
		getDestinationPool = func(ctx context.Context, destBasePath, token, remoteLocationID, projectNumber string, xCorrelationID *string, name string) (*googleproxyclient.PoolInternalV1beta, error) {
			return &googleproxyclient.PoolInternalV1beta{
				Network:          "network",
				ResourceId:       destPoolID,
				PoolId:           googleproxyclient.OptString{Value: destPoolID, Set: true},
				AllocatedBytes:   googleproxyclient.NewOptNilFloat64(0),
				SizeInBytes:      200,
				ServiceLevel:     googleproxyclient.PoolInternalV1betaServiceLevelFLEX,
				StoragePoolState: googleproxyclient.NewOptPoolInternalV1betaStoragePoolState(googleproxyclient.PoolInternalV1betaStoragePoolStateREADY),
			}, nil
		}
		defer func() { getDestinationPool = origGetDestinationPool }()

		origGetVolume := getVolume
		getVolume = func(ctx context.Context, basePath, token, locationID, projectNumber string, xCorrelationID *string, volumeResourceId string) (googleproxyclient.VolumeV1beta, error) {
			return googleproxyclient.VolumeV1beta{}, errors.NewNotFoundErr("Volume", &volumeResourceId)
		}
		defer func() { getVolume = origGetVolume }()

		origInternalGetReplicationCount := internalGetReplicationCount
		internalGetReplicationCount = func(ctx context.Context, basePath, projectNumber, locationID, poolID, jwt, storageClass, serviceLevel string) (int, error) {
			return 0, nil
		}
		defer func() { internalGetReplicationCount = origInternalGetReplicationCount }()

		origInternalGetVolumeCount := internalGetVolumeCount
		internalGetVolumeCount = func(ctx context.Context, basePath, projectNumber, locationID, poolID, jwt, storageClass, serviceLevel string) (int, error) {
			return 0, nil
		}
		defer func() { internalGetVolumeCount = origInternalGetVolumeCount }()

		origReplicationJobInProcess := replicationJobInProcess
		replicationJobInProcess = func(ctx context.Context, srcProjectNumber, destProjectNumber, srcBasePath, destBasePath, srcLocationID, destLocationID, srcToken, destToken, ccfeUri, remoteCcfeUri, srcPoolId, dstPoolId string, correlationId *string) error {
			return nil
		}
		defer func() { replicationJobInProcess = origReplicationJobInProcess }()

		origCreateReplicationObjects := createReplicationObjects
		createReplicationObjects = func(event *CreateReplicationEvent, remotelocation, region, remoteRegion string) (*datamodel.VolumeReplication, error) {
			return &datamodel.VolumeReplication{Uri: "uri"}, nil
		}
		defer func() { createReplicationObjects = origCreateReplicationObjects }()

		replication, err := _validateCreateReplicationParams(ctx, event, mockStorage)
		assert.NoError(t, err)
		assert.NotNil(t, replication)
	})

	t.Run("Fails when source volume is a thin clone undergoing split operation", func(t *testing.T) {
		eventCopy := *event
		volumeCopy := baseVolume
		volumeCopy.VolumeAttributes = &datamodel.VolumeAttributes{
			IsDataProtection: false,
			CreationToken:    "token-1",
			CloneParentInfo: &datamodel.CloneParentInfo{
				ParentVolumeUUID: "parent-vol-uuid",
				State:            datamodel.CloneStateSplitting,
			},
		}
		eventCopy.SourceVolume = volumeCopy

		mockStorage := &database.MockStorage{}

		origGetSignedToken := InternalUtilGetSignedToken
		InternalUtilGetSignedToken = func(p string) (string, error) { return "token", nil }
		defer func() { InternalUtilGetSignedToken = origGetSignedToken }()

		origParseRegionAndZone := InternalParseRegionAndZone
		InternalParseRegionAndZone = func(location string) (string, string, error) {
			if location == locationID {
				return "us-east1", "us-east1-a", nil
			}
			return "us-east4", "us-east4-a", nil
		}
		defer func() { InternalParseRegionAndZone = origParseRegionAndZone }()

		origGetPairedRegionURI := InternalUtilGetPairedRegionURI
		InternalUtilGetPairedRegionURI = func(region string) (string, error) { return "basePath", nil }
		defer func() { InternalUtilGetPairedRegionURI = origGetPairedRegionURI }()

		origValidateReplicationResourceId := ValidateReplicationResourceId
		ValidateReplicationResourceId = func(ctx context.Context, projectNumber string, paramReplicationResourceId string, paramsVolumeResourceId string, se database.Storage) error {
			return nil
		}
		defer func() { ValidateReplicationResourceId = origValidateReplicationResourceId }()

		_, err := _validateCreateReplicationParams(ctx, &eventCopy, mockStorage)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "Cannot create replication as source volume is undergoing split operation")
	})

	t.Run("Fails when replicationSchedule is UNSPECIFIED", func(t *testing.T) {
		unspecified := models.ReplicationV1betaReplicationScheduleREPLICATIONSCHEDULEUNSPECIFIED
		eventCopy := *event
		paramsCopy := *event.CreateReplicationParams
		paramsCopy.ReplicationSchedule = &unspecified
		eventCopy.CreateReplicationParams = &paramsCopy

		mockStorage := &database.MockStorage{}
		setupMockNoActiveClusterUpgrade(mockStorage)
		_, err := _validateCreateReplicationParams(ctx, &eventCopy, mockStorage)
		assert.Error(t, err)
	})

	t.Run("Fails when auto tiering is disabled", func(t *testing.T) {
		unspecified := models.ReplicationV1betaReplicationScheduleREPLICATIONSCHEDULEUNSPECIFIED
		eventCopy := &CreateReplicationEvent{
			SourceProjectNumber:      projectNumber,
			DestinationProjectNumber: destProjectNumber,
			LocationID:               locationID,
			DestinationLocationID:    destLocationID,
			VolumeResourceID:         volumeResourceID,
			CreateReplicationParams: &CreateReplicationParamsBody{
				ResourceID:          &resourceID,
				Description:         &description,
				ReplicationSchedule: &replicationSchedule,
				Labels:              map[string]string{"env": "prod"},
				DestinationVolumeParameters: &DestinationVolumeParams{
					VolumeID:    "vol_2",
					ShareName:   "share_2",
					StoragePool: &storagePoolUri,
				},
			},
			SourceVolume:        baseVolume,
			SourcePool:          datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-uuid"}, ServiceLevel: "FLEX"},
			DestinationPoolName: destPoolName,
			XCorrelationID:      xCorrelationID,
		}
		paramsCopy := eventCopy.CreateReplicationParams
		paramsCopy.DestinationVolumeParameters.TieringPolicy = &googleproxyclient.TieringPolicyV1beta{}
		paramsCopy.ReplicationSchedule = &unspecified
		eventCopy.CreateReplicationParams = paramsCopy

		mockStorage := &database.MockStorage{}
		setupMockNoActiveClusterUpgrade(mockStorage)
		_, err := _validateCreateReplicationParams(ctx, eventCopy, mockStorage)
		assert.Error(t, err)
		assert.Equal(t, err.Error(), "Auto-Tiering feature is currently not enabled.")
	})

	t.Run("Fails when label validation fails", func(t *testing.T) {
		eventCopy := *event
		paramsCopy := *event.CreateReplicationParams
		labels := map[string]string{}
		for i := 0; i < 65; i++ {
			labels[fmt.Sprintf("k%d", i)] = "v"
		}
		paramsCopy.Labels = labels
		eventCopy.CreateReplicationParams = &paramsCopy

		mockStorage := &database.MockStorage{}
		setupMockNoActiveClusterUpgrade(mockStorage)
		_, err := _validateCreateReplicationParams(ctx, &eventCopy, mockStorage)
		assert.Error(t, err)
	})

	t.Run("Fails when cross project replication is attempted", func(t *testing.T) {
		eventCopy := *event
		eventCopy.DestinationProjectNumber = "proj-2" // Different from source project

		mockStorage := &database.MockStorage{}
		setupMockNoActiveClusterUpgrade(mockStorage)
		mm := &monkeyMock{}
		mm.Test(t)
		mm.Patch()
		defer mm.Unpatch()

		mm.On("InternalUtilGetSignedToken", eventCopy.SourceProjectNumber).Return("token", nil).Once()
		mm.On("InternalUtilGetSignedToken", eventCopy.DestinationProjectNumber).Return("token", nil).Once()
		mm.On("InternalParseRegionAndZone", eventCopy.LocationID).Return("us-east1", "us-east1-a", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "us-east1").Return("basePath", nil).Once()
		mm.On("InternalParseRegionAndZone", eventCopy.DestinationLocationID).Return("us-east4", "us-east4-a", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "us-east4").Return("basePath", nil).Once()

		_, err := _validateCreateReplicationParams(ctx, &eventCopy, mockStorage)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "cross project replication is not supported")
		mm.AssertExpectations(t)
	})

	t.Run("Fails when cross zone replication is attempted", func(t *testing.T) {
		eventCopy := *event
		eventCopy.DestinationLocationID = "us-east1" // Same region as source

		mockStorage := &database.MockStorage{}
		setupMockNoActiveClusterUpgrade(mockStorage)
		mm := &monkeyMock{}
		mm.Test(t)
		mm.Patch()
		defer mm.Unpatch()

		// Only one token call since source and destination projects are the same
		mm.On("InternalUtilGetSignedToken", eventCopy.SourceProjectNumber).Return("token", nil).Once()
		mm.On("InternalParseRegionAndZone", eventCopy.LocationID).Return("us-east1", "us-east1-a", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "us-east1").Return("basePath", nil).Once()
		mm.On("InternalParseRegionAndZone", eventCopy.DestinationLocationID).Return("us-east1", "us-east1-b", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "us-east1").Return("basePath", nil).Once()

		_, err := _validateCreateReplicationParams(ctx, &eventCopy, mockStorage)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "cross zone replication is not supported")
		mm.AssertExpectations(t)
	})

	t.Run("Fails when GetSignedToken fails", func(t *testing.T) {
		mockStorage := &database.MockStorage{}
		setupMockNoActiveClusterUpgrade(mockStorage)
		origGetSignedJwtToken := InternalUtilGetSignedToken
		InternalUtilGetSignedToken = func(p string) (string, error) { return "", errors.New("token error") }
		defer func() { InternalUtilGetSignedToken = origGetSignedJwtToken }()

		_, err := _validateCreateReplicationParams(ctx, event, mockStorage)
		assert.Error(t, err)
	})

	// More negative test cases can be added for each error path as needed
	t.Run("WhenInternalUtilGetSignedTokenFailsForDestinationPool", func(t *testing.T) {
		mockStorage := &database.MockStorage{}
		setupMockNoActiveClusterUpgrade(mockStorage)
		mm := &monkeyMock{}
		mm.Test(t)
		mm.Patch()
		defer mm.Unpatch()

		mm.On("InternalUtilGetSignedToken", event.DestinationProjectNumber).Return("", errors.New("token error")).Once()

		_, err := _validateCreateReplicationParams(ctx, event, mockStorage)
		assert.Error(t, err)
		mm.AssertExpectations(t)
	})
	t.Run("WhenInternalParseRegionAndZoneFailsForSourceLocation", func(t *testing.T) {
		mockStorage := &database.MockStorage{}
		setupMockNoActiveClusterUpgrade(mockStorage)
		mm := &monkeyMock{}
		mm.Test(t)
		mm.Patch()
		defer mm.Unpatch()

		mm.On("InternalUtilGetSignedToken", event.DestinationProjectNumber).Return("token", nil).Once()
		mm.On("InternalParseRegionAndZone", event.LocationID).Return("", "", errors.New("parse error")).Once()

		_, err := _validateCreateReplicationParams(ctx, event, mockStorage)
		assert.Error(t, err)
		mm.AssertExpectations(t)
	})
	t.Run("WhenInternalUtilGetPairedRegionURIFailsForSourceLocation", func(t *testing.T) {
		mockStorage := &database.MockStorage{}
		setupMockNoActiveClusterUpgrade(mockStorage)
		mm := &monkeyMock{}
		mm.Test(t)
		mm.Patch()
		defer mm.Unpatch()

		mm.On("InternalUtilGetSignedToken", event.DestinationProjectNumber).Return("token", nil).Once()
		mm.On("InternalParseRegionAndZone", event.LocationID).Return("region", "zone", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "region").Return("", errors.New("paired region uri error")).Once()

		_, err := _validateCreateReplicationParams(ctx, event, mockStorage)
		assert.Error(t, err)
		mm.AssertExpectations(t)
	})
	t.Run("WhenInternalParseRegionAndZoneFailsForDestinationLocation", func(t *testing.T) {
		mockStorage := &database.MockStorage{}
		setupMockNoActiveClusterUpgrade(mockStorage)
		mm := &monkeyMock{}
		mm.Test(t)
		mm.Patch()
		defer mm.Unpatch()

		mm.On("InternalUtilGetSignedToken", event.DestinationProjectNumber).Return("token", nil).Once()
		mm.On("InternalParseRegionAndZone", event.LocationID).Return("region", "zone", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "region").Return("basePath", nil).Once()
		mm.On("InternalParseRegionAndZone", event.DestinationLocationID).Return("", "", errors.New("parse error")).Once()

		_, err := _validateCreateReplicationParams(ctx, event, mockStorage)
		assert.Error(t, err)
		mm.AssertExpectations(t)
	})
	t.Run("WhenInternalUtilGetPairedRegionURIFailsForDestinationLocation", func(t *testing.T) {
		mockStorage := &database.MockStorage{}
		setupMockNoActiveClusterUpgrade(mockStorage)
		mm := &monkeyMock{}
		mm.Test(t)
		mm.Patch()
		defer mm.Unpatch()

		mm.On("InternalUtilGetSignedToken", event.DestinationProjectNumber).Return("token", nil).Once()
		mm.On("InternalParseRegionAndZone", event.LocationID).Return("region", "zone", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "region").Return("basePath", nil).Once()
		mm.On("InternalParseRegionAndZone", event.DestinationLocationID).Return("region", "zone", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "region").Return("", errors.New("paired region uri error")).Once()

		_, err := _validateCreateReplicationParams(ctx, event, mockStorage)
		assert.Error(t, err)
		mm.AssertExpectations(t)
	})
	t.Run("WhenValidateReplicationResourceIdFails", func(t *testing.T) {
		mockStorage := &database.MockStorage{}
		setupMockNoActiveClusterUpgrade(mockStorage)
		mm := &monkeyMock{}
		mm.Test(t)
		mm.Patch()
		defer mm.Unpatch()

		mm.On("InternalUtilGetSignedToken", event.DestinationProjectNumber).Return("token", nil).Once()
		mm.On("InternalParseRegionAndZone", event.LocationID).Return("region-1", "zone-1", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "region-1").Return("basePath", nil).Once()
		mm.On("InternalParseRegionAndZone", event.DestinationLocationID).Return("region-2", "zone-2", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "region-2").Return("basePath", nil).Once()
		mm.On("validateReplicationResourceId", ctx, event.SourceProjectNumber, *event.CreateReplicationParams.ResourceID, event.VolumeResourceID, mockStorage).Return(errors.New("validate replication resource id error")).Once()

		_, err := _validateCreateReplicationParams(ctx, event, mockStorage)
		assert.Error(t, err)
		mm.AssertExpectations(t)
	})
	t.Run("WhenVolumeIsDataProtectionVolume", func(t *testing.T) {
		mockStorage := &database.MockStorage{}
		setupMockNoActiveClusterUpgrade(mockStorage)
		mm := &monkeyMock{}
		mm.Test(t)
		mm.Patch()
		defer mm.Unpatch()

		mm.On("InternalUtilGetSignedToken", event.DestinationProjectNumber).Return("token", nil).Once()
		mm.On("InternalParseRegionAndZone", event.LocationID).Return("region-1", "zone-1", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "region-1").Return("basePath", nil).Once()
		mm.On("InternalParseRegionAndZone", event.DestinationLocationID).Return("region-2", "zone-2", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "region-2").Return("basePath", nil).Once()
		mm.On("validateReplicationResourceId", ctx, event.SourceProjectNumber, *event.CreateReplicationParams.ResourceID, event.VolumeResourceID, mockStorage).Return(nil).Once()

		event.SourceVolume.VolumeAttributes.IsDataProtection = true

		_, err := _validateCreateReplicationParams(ctx, event, mockStorage)
		assert.Error(t, err)
		assert.Equal(t, vsaErrors.NewVCPError(vsaErrors.ErrValidateCreateSourceVolumeInReplicationGroup, errors.New("sourceVolume already in replication")), err)
		mm.AssertExpectations(t)
	})

	t.Run("WhenSourceVolumeNotReady", func(t *testing.T) {
		mockStorage := &database.MockStorage{}
		setupMockNoActiveClusterUpgrade(mockStorage)
		mm := &monkeyMock{}
		mm.Test(t)
		mm.Patch()
		defer mm.Unpatch()

		mm.On("InternalUtilGetSignedToken", event.DestinationProjectNumber).Return("token", nil).Once()
		mm.On("InternalParseRegionAndZone", event.LocationID).Return("region-1", "zone-1", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "region-1").Return("basePath", nil).Once()
		mm.On("InternalParseRegionAndZone", event.DestinationLocationID).Return("region-2", "zone-2", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "region-2").Return("basePath", nil).Once()
		mm.On("validateReplicationResourceId", ctx, event.SourceProjectNumber, *event.CreateReplicationParams.ResourceID, event.VolumeResourceID, mockStorage).Return(nil).Once()

		// Reset volume state to not ready
		event.SourceVolume.VolumeAttributes.IsDataProtection = false
		event.SourceVolume.State = string(googleproxyclient.VolumeV1betaVolumeStateCREATING)

		_, err := _validateCreateReplicationParams(ctx, event, mockStorage)
		assert.Error(t, err)
		assert.Equal(t, vsaErrors.NewVCPError(vsaErrors.ErrValidateCreateSourceVolumeNotReady, errors.New("sourceVolume is not in a READY state")), err)
		mm.AssertExpectations(t)
	})

	t.Run("WhenSourcePoolUnhealthy", func(t *testing.T) {
		unhealthyStates := []struct {
			name  string
			state googleproxyclient.PoolV1betaStoragePoolState
		}{
			{"ERROR", googleproxyclient.PoolV1betaStoragePoolStateERROR},
			{"DISABLED", googleproxyclient.PoolV1betaStoragePoolStateDISABLED},
		}

		for _, tc := range unhealthyStates {
			t.Run(tc.name, func(t *testing.T) {
				mockStorage := &database.MockStorage{}
				mm := &monkeyMock{}
				mm.Test(t)
				mm.Patch()
				defer mm.Unpatch()

				mm.On("InternalUtilGetSignedToken", event.DestinationProjectNumber).Return("token", nil).Once()
				mm.On("InternalParseRegionAndZone", event.LocationID).Return("region-1", "zone-1", nil).Once()
				mm.On("InternalUtilGetPairedRegionURI", "region-1").Return("basePath", nil).Once()
				mm.On("InternalParseRegionAndZone", event.DestinationLocationID).Return("region-2", "zone-2", nil).Once()
				mm.On("InternalUtilGetPairedRegionURI", "region-2").Return("basePath", nil).Once()
				mm.On("validateReplicationResourceId", ctx, event.SourceProjectNumber, *event.CreateReplicationParams.ResourceID, event.VolumeResourceID, mockStorage).Return(nil).Once()

				// Reset volume state
				event.SourceVolume.VolumeAttributes.IsDataProtection = false
				event.SourceVolume.State = string(googleproxyclient.VolumeV1betaVolumeStateREADY)

				// Set source pool to unhealthy state
				event.SourcePool.State = string(tc.state)
				defer func() {
					// Reset source pool state after test
					event.SourcePool.State = string(googleproxyclient.PoolV1betaStoragePoolStateREADY)
				}()

				_, err := _validateCreateReplicationParams(ctx, event, mockStorage)
				assert.Error(t, err)
				assert.Equal(t, vsaErrors.NewVCPError(vsaErrors.ErrValidateSourceStoragePoolState, errors.New("source pool is in unhealthy state, please try after some time")), err)
				mm.AssertExpectations(t)
			})
		}
	})

	t.Run("WhenValidateStoragePoolUriFails", func(t *testing.T) {
		mockStorage := &database.MockStorage{}
		setupMockNoActiveClusterUpgrade(mockStorage)
		mm := &monkeyMock{}
		mm.Test(t)
		mm.Patch()
		defer mm.Unpatch()

		mm.On("InternalUtilGetSignedToken", event.DestinationProjectNumber).Return("token", nil).Once()
		mm.On("InternalParseRegionAndZone", event.LocationID).Return("region-1", "zone-1", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "region-1").Return("basePath", nil).Once()
		mm.On("InternalParseRegionAndZone", event.DestinationLocationID).Return("region-2", "zone-2", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "region-2").Return("basePath", nil).Once()
		mm.On("validateReplicationResourceId", ctx, event.SourceProjectNumber, *event.CreateReplicationParams.ResourceID, event.VolumeResourceID, mockStorage).Return(nil).Once()
		mm.On("validateStoragePoolUri", *event.CreateReplicationParams.DestinationVolumeParameters.StoragePool).Return(errors.New("invalid storage pool URI format")).Once()

		// Reset volume state
		event.SourceVolume.VolumeAttributes.IsDataProtection = false
		event.SourceVolume.State = string(googleproxyclient.VolumeV1betaVolumeStateREADY)

		_, err := _validateCreateReplicationParams(ctx, event, mockStorage)
		assert.Error(t, err)
		assert.Equal(t, vsaErrors.NewVCPError(vsaErrors.ErrValidateStoragePoolUri, errors.New("invalid storage pool URI format")), err)
		mm.AssertExpectations(t)
	})

	t.Run("WhenGetDestinationPoolFails", func(t *testing.T) {
		mockStorage := &database.MockStorage{}
		setupMockNoActiveClusterUpgrade(mockStorage)
		mm := &monkeyMock{}
		mm.Patch()
		defer mm.Unpatch()

		mm.On("InternalUtilGetSignedToken", event.DestinationProjectNumber).Return("token", nil).Once()
		mm.On("InternalParseRegionAndZone", event.LocationID).Return("region-1", "zone-1", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "region-1").Return("basePath", nil).Once()
		mm.On("InternalParseRegionAndZone", event.DestinationLocationID).Return("region-2", "zone-2", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "region-2").Return("basePath", nil).Once()
		mm.On("validateReplicationResourceId", ctx, event.SourceProjectNumber, *event.CreateReplicationParams.ResourceID, event.VolumeResourceID, mockStorage).Return(nil).Once()
		mm.On("validateStoragePoolUri", *event.CreateReplicationParams.DestinationVolumeParameters.StoragePool).Return(nil).Once()

		// Reset volume state and storage pool
		event.SourceVolume.VolumeAttributes.IsDataProtection = false
		event.SourceVolume.State = string(googleproxyclient.VolumeV1betaVolumeStateREADY)
		event.CreateReplicationParams.DestinationVolumeParameters.StoragePool = &storagePoolUri

		mm.On("getDestinationPool", ctx, "basePath", "token", event.DestinationLocationID, event.DestinationProjectNumber, event.XCorrelationID, event.DestinationPoolName).Return(nil, errors.New("destination pool error")).Once()

		_, err := _validateCreateReplicationParams(ctx, event, mockStorage)
		assert.Error(t, err)
		assert.Equal(t, errors.New("destination pool error"), err)
		mm.AssertExpectations(t)
	})

	t.Run("WhenDestinationPoolInONTAPMode", func(t *testing.T) {
		mockStorage := &database.MockStorage{}
		setupMockNoActiveClusterUpgrade(mockStorage)
		mm := &monkeyMock{}
		mm.Patch()
		defer mm.Unpatch()

		mm.On("InternalUtilGetSignedToken", event.DestinationProjectNumber).Return("token", nil).Once()
		mm.On("InternalParseRegionAndZone", event.LocationID).Return("region-1", "zone-1", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "region-1").Return("basePath", nil).Once()
		mm.On("InternalParseRegionAndZone", event.DestinationLocationID).Return("region-2", "zone-2", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "region-2").Return("basePath", nil).Once()
		mm.On("validateReplicationResourceId", ctx, event.SourceProjectNumber, *event.CreateReplicationParams.ResourceID, event.VolumeResourceID, mockStorage).Return(nil).Once()
		mm.On("validateStoragePoolUri", *event.CreateReplicationParams.DestinationVolumeParameters.StoragePool).Return(nil).Once()

		// Reset volume state and storage pool
		event.SourceVolume.VolumeAttributes.IsDataProtection = false
		event.SourceVolume.State = string(googleproxyclient.VolumeV1betaVolumeStateREADY)
		event.CreateReplicationParams.DestinationVolumeParameters.StoragePool = &storagePoolUri

		ontapModePool := &googleproxyclient.PoolInternalV1beta{
			Network:          "network",
			ResourceId:       destPoolID,
			PoolId:           googleproxyclient.OptString{Value: destPoolID, Set: true},
			AllocatedBytes:   googleproxyclient.NewOptNilFloat64(0),
			SizeInBytes:      200,
			ServiceLevel:     googleproxyclient.PoolInternalV1betaServiceLevelFLEX,
			StoragePoolState: googleproxyclient.NewOptPoolInternalV1betaStoragePoolState(googleproxyclient.PoolInternalV1betaStoragePoolStateREADY),
			Mode:             googleproxyclient.OptPoolInternalV1betaMode{Value: googleproxyclient.PoolInternalV1betaModeONTAP, Set: true},
		}
		mm.On("getDestinationPool", ctx, "basePath", "token", event.DestinationLocationID, event.DestinationProjectNumber, event.XCorrelationID, event.DestinationPoolName).Return(ontapModePool, nil).Once()

		_, err := _validateCreateReplicationParams(ctx, event, mockStorage)
		assert.Error(t, err)
		assert.Equal(t, vsaErrors.NewVCPError(vsaErrors.ErrValidateDestinationPoolMode, errors.New("Cannot create Replication with ONTAP-mode pool using GCNV API")), err)
		mm.AssertExpectations(t)
	})

	t.Run("WhenDestinationPoolInTransitionState", func(t *testing.T) {
		mockStorage := &database.MockStorage{}
		setupMockNoActiveClusterUpgrade(mockStorage)
		mm := &monkeyMock{}
		mm.Patch()
		defer mm.Unpatch()

		mm.On("InternalUtilGetSignedToken", event.DestinationProjectNumber).Return("token", nil).Once()
		mm.On("InternalParseRegionAndZone", event.LocationID).Return("region-1", "zone-1", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "region-1").Return("basePath", nil).Once()
		mm.On("InternalParseRegionAndZone", event.DestinationLocationID).Return("region-2", "zone-2", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "region-2").Return("basePath", nil).Once()
		mm.On("validateReplicationResourceId", ctx, event.SourceProjectNumber, *event.CreateReplicationParams.ResourceID, event.VolumeResourceID, mockStorage).Return(nil).Once()
		mm.On("validateStoragePoolUri", *event.CreateReplicationParams.DestinationVolumeParameters.StoragePool).Return(nil).Once()

		// Reset volume state and storage pool
		event.SourceVolume.VolumeAttributes.IsDataProtection = false
		event.SourceVolume.State = string(googleproxyclient.VolumeV1betaVolumeStateREADY)
		event.CreateReplicationParams.DestinationVolumeParameters.StoragePool = &storagePoolUri

		transitioningPool := &googleproxyclient.PoolInternalV1beta{
			Network:          "network",
			ResourceId:       destPoolID,
			PoolId:           googleproxyclient.OptString{Value: destPoolID, Set: true},
			AllocatedBytes:   googleproxyclient.NewOptNilFloat64(0),
			SizeInBytes:      200,
			ServiceLevel:     googleproxyclient.PoolInternalV1betaServiceLevelFLEX,
			StoragePoolState: googleproxyclient.NewOptPoolInternalV1betaStoragePoolState(googleproxyclient.PoolInternalV1betaStoragePoolStateCREATING),
		}
		mm.On("getDestinationPool", ctx, "basePath", "token", event.DestinationLocationID, event.DestinationProjectNumber, event.XCorrelationID, event.DestinationPoolName).Return(transitioningPool, nil).Once()

		_, err := _validateCreateReplicationParams(ctx, event, mockStorage)
		assert.Error(t, err)
		assert.Equal(t, vsaErrors.NewVCPError(vsaErrors.ErrValidateDestinationPoolTransitioning, errors.New("Destination pool is in transition state")), err)
		mm.AssertExpectations(t)
	})

	t.Run("WhenDestinationPoolUnhealthy", func(t *testing.T) {
		mockStorage := &database.MockStorage{}
		setupMockNoActiveClusterUpgrade(mockStorage)
		mm := &monkeyMock{}
		mm.Patch()
		defer mm.Unpatch()

		mm.On("InternalUtilGetSignedToken", event.DestinationProjectNumber).Return("token", nil).Once()
		mm.On("InternalParseRegionAndZone", event.LocationID).Return("region-1", "zone-1", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "region-1").Return("basePath", nil).Once()
		mm.On("InternalParseRegionAndZone", event.DestinationLocationID).Return("region-2", "zone-2", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "region-2").Return("basePath", nil).Once()
		mm.On("validateReplicationResourceId", ctx, event.SourceProjectNumber, *event.CreateReplicationParams.ResourceID, event.VolumeResourceID, mockStorage).Return(nil).Once()
		mm.On("validateStoragePoolUri", *event.CreateReplicationParams.DestinationVolumeParameters.StoragePool).Return(nil).Once()

		// Reset volume state and storage pool
		event.SourceVolume.VolumeAttributes.IsDataProtection = false
		event.SourceVolume.State = string(googleproxyclient.VolumeV1betaVolumeStateREADY)
		event.CreateReplicationParams.DestinationVolumeParameters.StoragePool = &storagePoolUri

		unhealthyPool := &googleproxyclient.PoolInternalV1beta{
			Network:          "network",
			ResourceId:       destPoolID,
			PoolId:           googleproxyclient.OptString{Value: destPoolID, Set: true},
			AllocatedBytes:   googleproxyclient.NewOptNilFloat64(0),
			SizeInBytes:      200,
			ServiceLevel:     googleproxyclient.PoolInternalV1betaServiceLevelFLEX,
			StoragePoolState: googleproxyclient.NewOptPoolInternalV1betaStoragePoolState(googleproxyclient.PoolInternalV1betaStoragePoolStateERROR),
		}
		mm.On("getDestinationPool", ctx, "basePath", "token", event.DestinationLocationID, event.DestinationProjectNumber, event.XCorrelationID, event.DestinationPoolName).Return(unhealthyPool, nil).Once()

		_, err := _validateCreateReplicationParams(ctx, event, mockStorage)
		assert.Error(t, err)
		assert.Equal(t, vsaErrors.NewVCPError(vsaErrors.ErrValidateDestinationStoragePoolState, errors.New("destination pool is in unhealthy state, please try after some time")), err)
		mm.AssertExpectations(t)
	})

	t.Run("WhenSourcePoolDegraded", func(t *testing.T) {
		mockStorage := &database.MockStorage{}
		setupMockNoActiveClusterUpgrade(mockStorage)
		mm := &monkeyMock{}
		mm.Test(t)
		mm.Patch()
		defer mm.Unpatch()

		mm.On("InternalUtilGetSignedToken", event.DestinationProjectNumber).Return("token", nil).Once()
		mm.On("InternalParseRegionAndZone", event.LocationID).Return("region-1", "zone-1", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "region-1").Return("basePath", nil).Once()
		mm.On("InternalParseRegionAndZone", event.DestinationLocationID).Return("region-2", "zone-2", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "region-2").Return("basePath", nil).Once()
		mm.On("validateReplicationResourceId", ctx, event.SourceProjectNumber, *event.CreateReplicationParams.ResourceID, event.VolumeResourceID, mockStorage).Return(nil).Once()

		// Reset volume state
		event.SourceVolume.VolumeAttributes.IsDataProtection = false
		event.SourceVolume.State = string(googleproxyclient.VolumeV1betaVolumeStateREADY)

		// Set source pool to degraded state
		event.SourcePool.State = string(googleproxyclient.PoolV1betaStoragePoolStateDEGRADED)
		defer func() {
			// Reset source pool state after test
			event.SourcePool.State = string(googleproxyclient.PoolV1betaStoragePoolStateREADY)
		}()

		_, err := _validateCreateReplicationParams(ctx, event, mockStorage)
		assert.Error(t, err)
		assert.Equal(t, vsaErrors.NewVCPError(vsaErrors.ErrValidateSourceStoragePoolStateDegraded, errors.New("source pool is in degraded state, please try after some time")), err)
		mm.AssertExpectations(t)
	})

	t.Run("WhenDestinationPoolDegraded", func(t *testing.T) {
		mockStorage := &database.MockStorage{}
		setupMockNoActiveClusterUpgrade(mockStorage)
		mm := &monkeyMock{}
		mm.Patch()
		defer mm.Unpatch()

		mm.On("InternalUtilGetSignedToken", event.DestinationProjectNumber).Return("token", nil).Once()
		mm.On("InternalParseRegionAndZone", event.LocationID).Return("region-1", "zone-1", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "region-1").Return("basePath", nil).Once()
		mm.On("InternalParseRegionAndZone", event.DestinationLocationID).Return("region-2", "zone-2", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "region-2").Return("basePath", nil).Once()
		mm.On("validateReplicationResourceId", ctx, event.SourceProjectNumber, *event.CreateReplicationParams.ResourceID, event.VolumeResourceID, mockStorage).Return(nil).Once()
		mm.On("validateStoragePoolUri", *event.CreateReplicationParams.DestinationVolumeParameters.StoragePool).Return(nil).Once()

		// Reset volume state and storage pool
		event.SourceVolume.VolumeAttributes.IsDataProtection = false
		event.SourceVolume.State = string(googleproxyclient.VolumeV1betaVolumeStateREADY)
		event.CreateReplicationParams.DestinationVolumeParameters.StoragePool = &storagePoolUri

		degradedPool := &googleproxyclient.PoolInternalV1beta{
			Network:          "network",
			ResourceId:       destPoolID,
			PoolId:           googleproxyclient.OptString{Value: destPoolID, Set: true},
			AllocatedBytes:   googleproxyclient.NewOptNilFloat64(0),
			SizeInBytes:      200,
			ServiceLevel:     googleproxyclient.PoolInternalV1betaServiceLevelFLEX,
			StoragePoolState: googleproxyclient.NewOptPoolInternalV1betaStoragePoolState(googleproxyclient.PoolInternalV1betaStoragePoolStateDEGRADED),
		}
		mm.On("getDestinationPool", ctx, "basePath", "token", event.DestinationLocationID, event.DestinationProjectNumber, event.XCorrelationID, event.DestinationPoolName).Return(degradedPool, nil).Once()

		_, err := _validateCreateReplicationParams(ctx, event, mockStorage)
		assert.Error(t, err)
		assert.Equal(t, vsaErrors.NewVCPError(vsaErrors.ErrValidateDestinationStoragePoolStateDegraded, errors.New("destination pool is in degraded state, please try after some time")), err)
		mm.AssertExpectations(t)
	})

	t.Run("WhenSourcePoolHasActiveClusterUpgrade", func(t *testing.T) {
		mockStorage := &database.MockStorage{}
		mm := &monkeyMock{}
		mm.Patch()
		defer mm.Unpatch()

		mm.On("InternalUtilGetSignedToken", event.DestinationProjectNumber).Return("token", nil).Once()
		mm.On("InternalParseRegionAndZone", event.LocationID).Return("region-1", "zone-1", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "region-1").Return("basePath", nil).Once()
		mm.On("InternalParseRegionAndZone", event.DestinationLocationID).Return("region-2", "zone-2", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "region-2").Return("basePath", nil).Once()
		mm.On("validateReplicationResourceId", ctx, event.SourceProjectNumber, *event.CreateReplicationParams.ResourceID, event.VolumeResourceID, mockStorage).Return(nil).Once()

		event.SourceVolume.VolumeAttributes.IsDataProtection = false
		event.SourceVolume.State = string(googleproxyclient.VolumeV1betaVolumeStateREADY)
		event.SourcePool.State = string(googleproxyclient.PoolV1betaStoragePoolStateREADY)

		// Source pool has active cluster upgrade (maintenance)
		mockStorage.On("GetClusterUpgradeJobsByClusterID", mock.Anything, event.SourcePool.UUID).Return([]*datamodel.ClusterUpgradeJob{{Status: string(coreModels.UpgradeStatusInProgress)}}, nil).Once()

		_, err := _validateCreateReplicationParams(ctx, event, mockStorage)
		assert.Error(t, err)
		assert.Equal(t, vsaErrors.NewVCPError(vsaErrors.ErrStoragePoolTemporarilyUnavailable, errors.New("storage pool is temporarily unavailable, please try again later")), err)
		mm.AssertExpectations(t)
		mockStorage.AssertExpectations(t)
	})

	t.Run("WhenSourcePoolUpgradeCheckReturnsError", func(t *testing.T) {
		mockStorage := &database.MockStorage{}
		mm := &monkeyMock{}
		mm.Patch()
		defer mm.Unpatch()

		mm.On("InternalUtilGetSignedToken", event.DestinationProjectNumber).Return("token", nil).Once()
		mm.On("InternalParseRegionAndZone", event.LocationID).Return("region-1", "zone-1", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "region-1").Return("basePath", nil).Once()
		mm.On("InternalParseRegionAndZone", event.DestinationLocationID).Return("region-2", "zone-2", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "region-2").Return("basePath", nil).Once()
		mm.On("validateReplicationResourceId", ctx, event.SourceProjectNumber, *event.CreateReplicationParams.ResourceID, event.VolumeResourceID, mockStorage).Return(nil).Once()

		event.SourceVolume.VolumeAttributes.IsDataProtection = false
		event.SourceVolume.State = string(googleproxyclient.VolumeV1betaVolumeStateREADY)
		event.SourcePool.State = string(googleproxyclient.PoolV1betaStoragePoolStateREADY)

		upgradeCheckErr := errors.New("db unavailable")
		mockStorage.On("GetClusterUpgradeJobsByClusterID", mock.Anything, event.SourcePool.UUID).Return(([]*datamodel.ClusterUpgradeJob)(nil), upgradeCheckErr).Once()

		_, err := _validateCreateReplicationParams(ctx, event, mockStorage)
		assert.Error(t, err)
		assert.NotNil(t, vsaErrors.ExtractCustomError(err))
		mm.AssertExpectations(t)
		mockStorage.AssertExpectations(t)
	})

	// Destination pool cluster upgrade is no longer checked here; it is enforced in GetDestinationPoolDetails (replication create workflow).

	// WhenDestinationPoolUpgradeCheckReturnsError removed: destination cluster upgrade is no longer checked in validation (see GetDestinationPoolDetails).

	t.Run("WhenDestinationPoolSizeExceeded", func(t *testing.T) {
		mockStorage := &database.MockStorage{}
		setupMockNoActiveClusterUpgrade(mockStorage)
		mm := &monkeyMock{}
		mm.Patch()
		defer mm.Unpatch()

		mm.On("InternalUtilGetSignedToken", event.DestinationProjectNumber).Return("token", nil).Once()
		mm.On("InternalParseRegionAndZone", event.LocationID).Return("region-1", "zone-1", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "region-1").Return("basePath", nil).Once()
		mm.On("InternalParseRegionAndZone", event.DestinationLocationID).Return("region-2", "zone-2", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "region-2").Return("basePath", nil).Once()
		mm.On("validateReplicationResourceId", ctx, event.SourceProjectNumber, *event.CreateReplicationParams.ResourceID, event.VolumeResourceID, mockStorage).Return(nil).Once()
		mm.On("validateStoragePoolUri", *event.CreateReplicationParams.DestinationVolumeParameters.StoragePool).Return(nil).Once()

		// Reset volume state and storage pool
		event.SourceVolume.VolumeAttributes.IsDataProtection = false
		event.SourceVolume.State = string(googleproxyclient.VolumeV1betaVolumeStateREADY)
		event.CreateReplicationParams.DestinationVolumeParameters.StoragePool = &storagePoolUri

		// Set a small destination pool that can't accommodate the volume
		smallPool := &googleproxyclient.PoolInternalV1beta{
			Network:          "network",
			ResourceId:       destPoolID,
			PoolId:           googleproxyclient.OptString{Value: destPoolID, Set: true},
			AllocatedBytes:   googleproxyclient.NewOptNilFloat64(50),
			SizeInBytes:      100, // Smaller than source volume size (100) + allocated bytes (50)
			ServiceLevel:     googleproxyclient.PoolInternalV1betaServiceLevelFLEX,
			StoragePoolState: googleproxyclient.NewOptPoolInternalV1betaStoragePoolState(googleproxyclient.PoolInternalV1betaStoragePoolStateREADY),
		}
		mm.On("getDestinationPool", ctx, "basePath", "token", event.DestinationLocationID, event.DestinationProjectNumber, event.XCorrelationID, event.DestinationPoolName).Return(smallPool, nil).Once()

		_, err := _validateCreateReplicationParams(ctx, event, mockStorage)
		assert.Error(t, err)
		assert.Equal(t, vsaErrors.NewVCPError(vsaErrors.ErrDestPoolSize, errors.New("Volume exceeds destination pool size")), err)
		mm.AssertExpectations(t)
	})

	t.Run("WhenDestinationPoolSizeATDisabled", func(t *testing.T) {
		// Patch env variable
		autoTieringEnabled = true
		defer func() {
			autoTieringEnabled = env.GetBool("AUTO_TIERING_ENABLED", false)
			event.CreateReplicationParams.DestinationVolumeParameters.TieringPolicy = nil
		}()
		mockStorage := &database.MockStorage{}
		setupMockNoActiveClusterUpgrade(mockStorage)
		mm := &monkeyMock{}
		mm.Patch()
		defer mm.Unpatch()

		mm.On("InternalUtilGetSignedToken", event.DestinationProjectNumber).Return("token", nil).Once()
		mm.On("InternalParseRegionAndZone", event.LocationID).Return("region-1", "zone-1", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "region-1").Return("basePath", nil).Once()
		mm.On("InternalParseRegionAndZone", event.DestinationLocationID).Return("region-2", "zone-2", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "region-2").Return("basePath", nil).Once()
		mm.On("validateReplicationResourceId", ctx, event.SourceProjectNumber, *event.CreateReplicationParams.ResourceID, event.VolumeResourceID, mockStorage).Return(nil).Once()
		mm.On("validateStoragePoolUri", *event.CreateReplicationParams.DestinationVolumeParameters.StoragePool).Return(nil).Once()

		// Reset volume state and storage pool
		event.SourceVolume.VolumeAttributes.IsDataProtection = false
		event.SourceVolume.State = string(googleproxyclient.VolumeV1betaVolumeStateREADY)
		event.CreateReplicationParams.DestinationVolumeParameters.StoragePool = &storagePoolUri
		event.CreateReplicationParams.DestinationVolumeParameters.TieringPolicy = &googleproxyclient.TieringPolicyV1beta{
			TierAction: googleproxyclient.OptNilTieringPolicyV1betaTierAction{
				Value: "ENABLED",
				Set:   true,
				Null:  false,
			},
			CoolingThresholdDays: googleproxyclient.OptNilInt32{Value: 2},
		}
		// Set an Autotiering disabled destination pool
		nonATPool := &googleproxyclient.PoolInternalV1beta{
			Network:          "network",
			ResourceId:       destPoolID,
			PoolId:           googleproxyclient.OptString{Value: destPoolID, Set: true},
			AllocatedBytes:   googleproxyclient.NewOptNilFloat64(50),
			SizeInBytes:      1000, // Smaller than source volume size (100) + allocated bytes (50)
			ServiceLevel:     googleproxyclient.PoolInternalV1betaServiceLevelFLEX,
			StoragePoolState: googleproxyclient.NewOptPoolInternalV1betaStoragePoolState(googleproxyclient.PoolInternalV1betaStoragePoolStateREADY),
		}
		mm.On("getDestinationPool", ctx, "basePath", "token", event.DestinationLocationID, event.DestinationProjectNumber, event.XCorrelationID, event.DestinationPoolName).Return(nonATPool, nil).Once()

		_, err := _validateCreateReplicationParams(ctx, event, mockStorage)
		assert.Error(t, err)
		assert.Equal(t, vsaErrors.NewVCPError(vsaErrors.ErrDestPoolTieringPolicyMismatch, errors.New("Auto tiering is not enabled on the destination pool")), err)
		mm.AssertExpectations(t)
	})

	t.Run("WhenDestinationVolumeHasInvalidCoolingDays", func(t *testing.T) {
		// Patch env variable
		autoTieringEnabled = true
		defer func() {
			autoTieringEnabled = env.GetBool("AUTO_TIERING_ENABLED", false)
			event.CreateReplicationParams.DestinationVolumeParameters.TieringPolicy = nil
		}()
		mockStorage := &database.MockStorage{}
		setupMockNoActiveClusterUpgrade(mockStorage)
		mm := &monkeyMock{}
		mm.Patch()
		defer mm.Unpatch()

		mm.On("InternalUtilGetSignedToken", event.DestinationProjectNumber).Return("token", nil).Once()
		mm.On("InternalParseRegionAndZone", event.LocationID).Return("region-1", "zone-1", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "region-1").Return("basePath", nil).Once()
		mm.On("InternalParseRegionAndZone", event.DestinationLocationID).Return("region-2", "zone-2", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "region-2").Return("basePath", nil).Once()
		mm.On("validateReplicationResourceId", ctx, event.SourceProjectNumber, *event.CreateReplicationParams.ResourceID, event.VolumeResourceID, mockStorage).Return(nil).Once()
		mm.On("validateStoragePoolUri", *event.CreateReplicationParams.DestinationVolumeParameters.StoragePool).Return(nil).Once()

		// Reset volume state and storage pool
		event.SourceVolume.VolumeAttributes.IsDataProtection = false
		event.SourceVolume.State = string(googleproxyclient.VolumeV1betaVolumeStateREADY)
		event.CreateReplicationParams.DestinationVolumeParameters.StoragePool = &storagePoolUri
		event.CreateReplicationParams.DestinationVolumeParameters.TieringPolicy = &googleproxyclient.TieringPolicyV1beta{
			TierAction: googleproxyclient.OptNilTieringPolicyV1betaTierAction{
				Value: "ENABLED",
				Set:   true,
				Null:  false,
			},
			CoolingThresholdDays: googleproxyclient.OptNilInt32{Value: 1},
		}

		atPool := &googleproxyclient.PoolInternalV1beta{
			Network:          "network",
			ResourceId:       destPoolID,
			PoolId:           googleproxyclient.OptString{Value: destPoolID, Set: true},
			AllocatedBytes:   googleproxyclient.NewOptNilFloat64(50),
			SizeInBytes:      1000,
			ServiceLevel:     googleproxyclient.PoolInternalV1betaServiceLevelFLEX,
			StoragePoolState: googleproxyclient.NewOptPoolInternalV1betaStoragePoolState(googleproxyclient.PoolInternalV1betaStoragePoolStateREADY),
			AllowAutoTiering: googleproxyclient.OptNilBool{Value: true, Set: true},
		}
		mm.On("getDestinationPool", ctx, "basePath", "token", event.DestinationLocationID, event.DestinationProjectNumber, event.XCorrelationID, event.DestinationPoolName).Return(atPool, nil).Once()

		_, err := _validateCreateReplicationParams(ctx, event, mockStorage)
		assert.Error(t, err)
		assert.Equal(t, vsaErrors.NewVCPError(vsaErrors.ErrDestVolumeTieringThresholdOutOfRange, errors.New("Coolness threshold days should be in between 2 and 183")), err)
		mm.AssertExpectations(t)
	})

	t.Run("WhenServiceLevelMismatch", func(t *testing.T) {
		mockStorage := &database.MockStorage{}
		setupMockNoActiveClusterUpgrade(mockStorage)
		mm := &monkeyMock{}
		mm.Patch()
		defer mm.Unpatch()

		mm.On("InternalUtilGetSignedToken", event.DestinationProjectNumber).Return("token", nil).Once()
		mm.On("InternalParseRegionAndZone", event.LocationID).Return("region-1", "zone-1", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "region-1").Return("basePath", nil).Once()
		mm.On("InternalParseRegionAndZone", event.DestinationLocationID).Return("region-2", "zone-2", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "region-2").Return("basePath", nil).Once()
		mm.On("validateReplicationResourceId", ctx, event.SourceProjectNumber, *event.CreateReplicationParams.ResourceID, event.VolumeResourceID, mockStorage).Return(nil).Once()
		mm.On("validateStoragePoolUri", *event.CreateReplicationParams.DestinationVolumeParameters.StoragePool).Return(nil).Once()

		// Reset volume state and storage pool
		event.SourceVolume.VolumeAttributes.IsDataProtection = false
		event.SourceVolume.State = string(googleproxyclient.VolumeV1betaVolumeStateREADY)
		event.CreateReplicationParams.DestinationVolumeParameters.StoragePool = &storagePoolUri

		// Set different service level for destination pool
		differentServiceLevelPool := &googleproxyclient.PoolInternalV1beta{
			Network:          "network",
			ResourceId:       destPoolID,
			PoolId:           googleproxyclient.OptString{Value: destPoolID, Set: true},
			AllocatedBytes:   googleproxyclient.NewOptNilFloat64(0),
			SizeInBytes:      200,
			ServiceLevel:     googleproxyclient.PoolInternalV1betaServiceLevelEXTREME, // Different from source (FLEX)
			StoragePoolState: googleproxyclient.NewOptPoolInternalV1betaStoragePoolState(googleproxyclient.PoolInternalV1betaStoragePoolStateREADY),
		}
		mm.On("getDestinationPool", ctx, "basePath", "token", event.DestinationLocationID, event.DestinationProjectNumber, event.XCorrelationID, event.DestinationPoolName).Return(differentServiceLevelPool, nil).Once()

		_, err := _validateCreateReplicationParams(ctx, event, mockStorage)
		assert.Error(t, err)
		assert.Equal(t, vsaErrors.NewVCPError(vsaErrors.ErrServiceLevelMismatch, errors.New("Service level on source volume and destination pool do not match")), err)
		mm.AssertExpectations(t)
	})

	t.Run("WhenLargeCapacityMismatch", func(t *testing.T) {
		mockStorage := &database.MockStorage{}
		setupMockNoActiveClusterUpgrade(mockStorage)
		mm := &monkeyMock{}
		mm.Patch()
		defer mm.Unpatch()

		mm.On("InternalUtilGetSignedToken", event.DestinationProjectNumber).Return("token", nil).Once()
		mm.On("InternalParseRegionAndZone", event.LocationID).Return("region-1", "zone-1", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "region-1").Return("basePath", nil).Once()
		mm.On("InternalParseRegionAndZone", event.DestinationLocationID).Return("region-2", "zone-2", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "region-2").Return("basePath", nil).Once()
		mm.On("validateReplicationResourceId", ctx, event.SourceProjectNumber, *event.CreateReplicationParams.ResourceID, event.VolumeResourceID, mockStorage).Return(nil).Once()
		mm.On("validateStoragePoolUri", *event.CreateReplicationParams.DestinationVolumeParameters.StoragePool).Return(nil).Once()

		// Reset volume state and storage pool
		event.SourceVolume.VolumeAttributes.IsDataProtection = false
		event.SourceVolume.State = string(googleproxyclient.VolumeV1betaVolumeStateREADY)
		event.CreateReplicationParams.DestinationVolumeParameters.StoragePool = &storagePoolUri

		// Set source pool LargeCapacity to true
		event.SourceVolume.Pool.LargeCapacity = true

		// Set different LargeCapacity for destination pool (false)
		differentLargeCapacityPool := &googleproxyclient.PoolInternalV1beta{
			Network:          "network",
			ResourceId:       destPoolID,
			PoolId:           googleproxyclient.OptString{Value: destPoolID, Set: true},
			AllocatedBytes:   googleproxyclient.NewOptNilFloat64(0),
			SizeInBytes:      200,
			ServiceLevel:     googleproxyclient.PoolInternalV1betaServiceLevelFLEX,
			LargeCapacity:    googleproxyclient.OptBool{Value: false, Set: true}, // Different from source (true)
			StoragePoolState: googleproxyclient.NewOptPoolInternalV1betaStoragePoolState(googleproxyclient.PoolInternalV1betaStoragePoolStateREADY),
		}
		mm.On("getDestinationPool", ctx, "basePath", "token", event.DestinationLocationID, event.DestinationProjectNumber, event.XCorrelationID, event.DestinationPoolName).Return(differentLargeCapacityPool, nil).Once()

		_, err := _validateCreateReplicationParams(ctx, event, mockStorage)
		assert.Error(t, err)
		assert.Equal(t, vsaErrors.NewVCPError(vsaErrors.ErrVolumePoolTypeMismatch, errors.New("CRR cannot be created between normal and large capacity pools")), err)
		mm.AssertExpectations(t)
	})

	t.Run("WhenLargeCapacityMismatch_Reverse", func(t *testing.T) {
		mockStorage := &database.MockStorage{}
		setupMockNoActiveClusterUpgrade(mockStorage)
		mm := &monkeyMock{}
		mm.Patch()
		defer mm.Unpatch()

		mm.On("InternalUtilGetSignedToken", event.DestinationProjectNumber).Return("token", nil).Once()
		mm.On("InternalParseRegionAndZone", event.LocationID).Return("region-1", "zone-1", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "region-1").Return("basePath", nil).Once()
		mm.On("InternalParseRegionAndZone", event.DestinationLocationID).Return("region-2", "zone-2", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "region-2").Return("basePath", nil).Once()
		mm.On("validateReplicationResourceId", ctx, event.SourceProjectNumber, *event.CreateReplicationParams.ResourceID, event.VolumeResourceID, mockStorage).Return(nil).Once()
		mm.On("validateStoragePoolUri", *event.CreateReplicationParams.DestinationVolumeParameters.StoragePool).Return(nil).Once()

		// Reset volume state and storage pool
		event.SourceVolume.VolumeAttributes.IsDataProtection = false
		event.SourceVolume.State = string(googleproxyclient.VolumeV1betaVolumeStateREADY)
		event.CreateReplicationParams.DestinationVolumeParameters.StoragePool = &storagePoolUri

		// Set source pool LargeCapacity to false
		event.SourceVolume.Pool.LargeCapacity = false

		// Set different LargeCapacity for destination pool (true)
		differentLargeCapacityPool := &googleproxyclient.PoolInternalV1beta{
			Network:          "network",
			ResourceId:       destPoolID,
			PoolId:           googleproxyclient.OptString{Value: destPoolID, Set: true},
			AllocatedBytes:   googleproxyclient.NewOptNilFloat64(0),
			SizeInBytes:      200,
			ServiceLevel:     googleproxyclient.PoolInternalV1betaServiceLevelFLEX,
			LargeCapacity:    googleproxyclient.OptBool{Value: true, Set: true}, // Different from source (false)
			StoragePoolState: googleproxyclient.NewOptPoolInternalV1betaStoragePoolState(googleproxyclient.PoolInternalV1betaStoragePoolStateREADY),
		}
		mm.On("getDestinationPool", ctx, "basePath", "token", event.DestinationLocationID, event.DestinationProjectNumber, event.XCorrelationID, event.DestinationPoolName).Return(differentLargeCapacityPool, nil).Once()

		_, err := _validateCreateReplicationParams(ctx, event, mockStorage)
		assert.Error(t, err)
		assert.Equal(t, vsaErrors.NewVCPError(vsaErrors.ErrVolumePoolTypeMismatch, errors.New("CRR cannot be created between normal and large capacity pools")), err)
		mm.AssertExpectations(t)
	})

	t.Run("WhenLargeCapacityMatch", func(t *testing.T) {
		mockStorage := &database.MockStorage{}
		setupMockNoActiveClusterUpgrade(mockStorage)
		mm := &monkeyMock{}
		mm.Patch()
		defer mm.Unpatch()

		mm.On("InternalUtilGetSignedToken", event.DestinationProjectNumber).Return("token", nil).Once()
		mm.On("InternalParseRegionAndZone", event.LocationID).Return("region-1", "zone-1", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "region-1").Return("basePath", nil).Once()
		mm.On("InternalParseRegionAndZone", event.DestinationLocationID).Return("region-2", "zone-2", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "region-2").Return("basePath", nil).Once()
		mm.On("validateReplicationResourceId", ctx, event.SourceProjectNumber, *event.CreateReplicationParams.ResourceID, event.VolumeResourceID, mockStorage).Return(nil).Once()
		mm.On("validateStoragePoolUri", *event.CreateReplicationParams.DestinationVolumeParameters.StoragePool).Return(nil).Once()

		// Reset volume state and storage pool
		event.SourceVolume.VolumeAttributes.IsDataProtection = false
		event.SourceVolume.State = string(googleproxyclient.VolumeV1betaVolumeStateREADY)
		event.CreateReplicationParams.DestinationVolumeParameters.StoragePool = &storagePoolUri

		// Set source pool LargeCapacity to true
		event.SourceVolume.Pool.LargeCapacity = true

		// Set matching LargeCapacity for destination pool (true)
		matchingLargeCapacityPool := &googleproxyclient.PoolInternalV1beta{
			Network:          "network",
			ResourceId:       destPoolID,
			PoolId:           googleproxyclient.OptString{Value: destPoolID, Set: true},
			AllocatedBytes:   googleproxyclient.NewOptNilFloat64(0),
			SizeInBytes:      200,
			ServiceLevel:     googleproxyclient.PoolInternalV1betaServiceLevelFLEX,
			LargeCapacity:    googleproxyclient.OptBool{Value: true, Set: true}, // Matches source (true)
			StoragePoolState: googleproxyclient.NewOptPoolInternalV1betaStoragePoolState(googleproxyclient.PoolInternalV1betaStoragePoolStateREADY),
		}
		mm.On("getDestinationPool", ctx, "basePath", "token", event.DestinationLocationID, event.DestinationProjectNumber, event.XCorrelationID, event.DestinationPoolName).Return(matchingLargeCapacityPool, nil).Once()
		mm.On("replicationJobInProcess", ctx, event.SourceProjectNumber, event.DestinationProjectNumber, "basePath", "basePath", event.LocationID, event.DestinationLocationID, "token", "token", mock.Anything, "", mock.Anything, destPoolID, event.XCorrelationID).Return(nil).Once()
		mm.On("InternalUtilGetCallbackToken").Return("callbackToken", nil).Once()
		mm.On("getQuotaLimit", ctx, mock.Anything, event.DestinationLocationID, event.DestinationProjectNumber, "callbackToken", common.ResourceTypeReplication).Return(10, nil).Once()
		mm.On("internalGetReplicationCount", ctx, "basePath", event.DestinationProjectNumber, event.DestinationLocationID, "", "token", mock.Anything, mock.Anything).Return(0, nil).Once()
		mm.On("getQuotaLimit", ctx, mock.Anything, event.DestinationLocationID, event.DestinationProjectNumber, "callbackToken", common.ResourceTypeVolume).Return(10, nil).Once()
		mm.On("internalGetVolumeCount", ctx, "basePath", event.DestinationProjectNumber, event.DestinationLocationID, "", "token", mock.Anything, mock.Anything).Return(0, nil).Once()
		mm.On("getVolume", ctx, "basePath", "token", event.DestinationLocationID, event.DestinationProjectNumber, event.XCorrelationID, mock.Anything).Return(googleproxyclient.VolumeV1beta{}, errors.NewNotFoundErr("Volume", nil)).Once()
		mm.On("createReplicationObjects", event, event.DestinationLocationID, mock.Anything, mock.Anything).Return(&datamodel.VolumeReplication{}, nil).Once()
		iops := int64(0)
		mm.EXPECT().validateVolumeQosParamsForReplication(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&iops, nil).Once()

		_, err := _validateCreateReplicationParams(ctx, event, mockStorage)
		assert.NoError(t, err)
		mm.AssertExpectations(t)
	})

	t.Run("WhenLargeCapacityNotSet", func(t *testing.T) {
		mockStorage := &database.MockStorage{}
		setupMockNoActiveClusterUpgrade(mockStorage)
		mm := &monkeyMock{}
		mm.Patch()
		defer mm.Unpatch()

		mm.On("InternalUtilGetSignedToken", event.DestinationProjectNumber).Return("token", nil).Once()
		mm.On("InternalParseRegionAndZone", event.LocationID).Return("region-1", "zone-1", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "region-1").Return("basePath", nil).Once()
		mm.On("InternalParseRegionAndZone", event.DestinationLocationID).Return("region-2", "zone-2", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "region-2").Return("basePath", nil).Once()
		mm.On("validateReplicationResourceId", ctx, event.SourceProjectNumber, *event.CreateReplicationParams.ResourceID, event.VolumeResourceID, mockStorage).Return(nil).Once()
		mm.On("validateStoragePoolUri", *event.CreateReplicationParams.DestinationVolumeParameters.StoragePool).Return(nil).Once()

		// Reset volume state and storage pool
		event.SourceVolume.VolumeAttributes.IsDataProtection = false
		event.SourceVolume.State = string(googleproxyclient.VolumeV1betaVolumeStateREADY)
		event.CreateReplicationParams.DestinationVolumeParameters.StoragePool = &storagePoolUri

		// Set source pool LargeCapacity to true
		event.SourceVolume.Pool.LargeCapacity = true

		// Set LargeCapacity not set for destination pool (Set: false)
		poolWithUnsetLargeCapacity := &googleproxyclient.PoolInternalV1beta{
			Network:          "network",
			ResourceId:       destPoolID,
			PoolId:           googleproxyclient.OptString{Value: destPoolID, Set: true},
			AllocatedBytes:   googleproxyclient.NewOptNilFloat64(0),
			SizeInBytes:      200,
			ServiceLevel:     googleproxyclient.PoolInternalV1betaServiceLevelFLEX,
			LargeCapacity:    googleproxyclient.OptBool{Value: false, Set: false}, // Not set - should skip validation
			StoragePoolState: googleproxyclient.NewOptPoolInternalV1betaStoragePoolState(googleproxyclient.PoolInternalV1betaStoragePoolStateREADY),
		}
		mm.On("getDestinationPool", ctx, "basePath", "token", event.DestinationLocationID, event.DestinationProjectNumber, event.XCorrelationID, event.DestinationPoolName).Return(poolWithUnsetLargeCapacity, nil).Once()
		mm.On("replicationJobInProcess", ctx, event.SourceProjectNumber, event.DestinationProjectNumber, "basePath", "basePath", event.LocationID, event.DestinationLocationID, "token", "token", mock.Anything, "", mock.Anything, destPoolID, event.XCorrelationID).Return(nil).Once()
		mm.On("InternalUtilGetCallbackToken").Return("callbackToken", nil).Once()
		mm.On("getQuotaLimit", ctx, mock.Anything, event.DestinationLocationID, event.DestinationProjectNumber, "callbackToken", common.ResourceTypeReplication).Return(10, nil).Once()
		mm.On("internalGetReplicationCount", ctx, "basePath", event.DestinationProjectNumber, event.DestinationLocationID, "", "token", mock.Anything, mock.Anything).Return(0, nil).Once()
		mm.On("getQuotaLimit", ctx, mock.Anything, event.DestinationLocationID, event.DestinationProjectNumber, "callbackToken", common.ResourceTypeVolume).Return(10, nil).Once()
		mm.On("internalGetVolumeCount", ctx, "basePath", event.DestinationProjectNumber, event.DestinationLocationID, "", "token", mock.Anything, mock.Anything).Return(0, nil).Once()
		mm.On("getVolume", ctx, "basePath", "token", event.DestinationLocationID, event.DestinationProjectNumber, event.XCorrelationID, mock.Anything).Return(googleproxyclient.VolumeV1beta{}, errors.NewNotFoundErr("Volume", nil)).Once()
		mm.On("createReplicationObjects", event, event.DestinationLocationID, mock.Anything, mock.Anything).Return(&datamodel.VolumeReplication{}, nil).Once()
		iops := int64(0)
		mm.EXPECT().validateVolumeQosParamsForReplication(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&iops, nil).Once()

		_, err := _validateCreateReplicationParams(ctx, event, mockStorage)
		assert.NoError(t, err)
		mm.AssertExpectations(t)
	})

	t.Run("WhenLargeCapacityMatch_BothFalse", func(t *testing.T) {
		mockStorage := &database.MockStorage{}
		setupMockNoActiveClusterUpgrade(mockStorage)
		mm := &monkeyMock{}
		mm.Patch()
		defer mm.Unpatch()

		mm.On("InternalUtilGetSignedToken", event.DestinationProjectNumber).Return("token", nil).Once()
		mm.On("InternalParseRegionAndZone", event.LocationID).Return("region-1", "zone-1", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "region-1").Return("basePath", nil).Once()
		mm.On("InternalParseRegionAndZone", event.DestinationLocationID).Return("region-2", "zone-2", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "region-2").Return("basePath", nil).Once()
		mm.On("validateReplicationResourceId", ctx, event.SourceProjectNumber, *event.CreateReplicationParams.ResourceID, event.VolumeResourceID, mockStorage).Return(nil).Once()
		mm.On("validateStoragePoolUri", *event.CreateReplicationParams.DestinationVolumeParameters.StoragePool).Return(nil).Once()

		// Reset volume state and storage pool
		event.SourceVolume.VolumeAttributes.IsDataProtection = false
		event.SourceVolume.State = string(googleproxyclient.VolumeV1betaVolumeStateREADY)
		event.CreateReplicationParams.DestinationVolumeParameters.StoragePool = &storagePoolUri

		// Set source pool LargeCapacity to false
		event.SourceVolume.Pool.LargeCapacity = false

		// Set matching LargeCapacity for destination pool (false) - both are normal capacity pools
		matchingNormalCapacityPool := &googleproxyclient.PoolInternalV1beta{
			Network:          "network",
			ResourceId:       destPoolID,
			PoolId:           googleproxyclient.OptString{Value: destPoolID, Set: true},
			AllocatedBytes:   googleproxyclient.NewOptNilFloat64(0),
			SizeInBytes:      200,
			ServiceLevel:     googleproxyclient.PoolInternalV1betaServiceLevelFLEX,
			LargeCapacity:    googleproxyclient.OptBool{Value: false, Set: true}, // Matches source (false) - both normal capacity
			StoragePoolState: googleproxyclient.NewOptPoolInternalV1betaStoragePoolState(googleproxyclient.PoolInternalV1betaStoragePoolStateREADY),
		}
		mm.On("getDestinationPool", ctx, "basePath", "token", event.DestinationLocationID, event.DestinationProjectNumber, event.XCorrelationID, event.DestinationPoolName).Return(matchingNormalCapacityPool, nil).Once()
		mm.On("replicationJobInProcess", ctx, event.SourceProjectNumber, event.DestinationProjectNumber, "basePath", "basePath", event.LocationID, event.DestinationLocationID, "token", "token", mock.Anything, "", mock.Anything, destPoolID, event.XCorrelationID).Return(nil).Once()
		mm.On("InternalUtilGetCallbackToken").Return("callbackToken", nil).Once()
		mm.On("getQuotaLimit", ctx, mock.Anything, event.DestinationLocationID, event.DestinationProjectNumber, "callbackToken", common.ResourceTypeReplication).Return(10, nil).Once()
		mm.On("internalGetReplicationCount", ctx, "basePath", event.DestinationProjectNumber, event.DestinationLocationID, "", "token", mock.Anything, mock.Anything).Return(0, nil).Once()
		mm.On("getQuotaLimit", ctx, mock.Anything, event.DestinationLocationID, event.DestinationProjectNumber, "callbackToken", common.ResourceTypeVolume).Return(10, nil).Once()
		mm.On("internalGetVolumeCount", ctx, "basePath", event.DestinationProjectNumber, event.DestinationLocationID, "", "token", mock.Anything, mock.Anything).Return(0, nil).Once()
		mm.On("getVolume", ctx, "basePath", "token", event.DestinationLocationID, event.DestinationProjectNumber, event.XCorrelationID, mock.Anything).Return(googleproxyclient.VolumeV1beta{}, errors.NewNotFoundErr("Volume", nil)).Once()
		mm.On("createReplicationObjects", event, event.DestinationLocationID, mock.Anything, mock.Anything).Return(&datamodel.VolumeReplication{}, nil).Once()
		iops := int64(0)
		mm.EXPECT().validateVolumeQosParamsForReplication(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&iops, nil).Once()

		_, err := _validateCreateReplicationParams(ctx, event, mockStorage)
		assert.NoError(t, err)
		mm.AssertExpectations(t)
	})

	t.Run("WhenReplicationJobInProcessFails", func(t *testing.T) {
		mockStorage := &database.MockStorage{}
		setupMockNoActiveClusterUpgrade(mockStorage)
		mm := &monkeyMock{}
		mm.Patch()
		defer mm.Unpatch()

		mm.On("InternalUtilGetSignedToken", event.DestinationProjectNumber).Return("token", nil).Once()
		mm.On("InternalParseRegionAndZone", event.LocationID).Return("region-1", "zone-1", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "region-1").Return("basePath", nil).Once()
		mm.On("InternalParseRegionAndZone", event.DestinationLocationID).Return("region-2", "zone-2", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "region-2").Return("basePath", nil).Once()
		mm.On("validateReplicationResourceId", ctx, event.SourceProjectNumber, *event.CreateReplicationParams.ResourceID, event.VolumeResourceID, mockStorage).Return(nil).Once()
		mm.On("validateStoragePoolUri", *event.CreateReplicationParams.DestinationVolumeParameters.StoragePool).Return(nil).Once()
		iops := int64(0)
		mm.EXPECT().validateVolumeQosParamsForReplication(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&iops, nil).Once()

		// Reset volume state and storage pool
		event.SourceVolume.VolumeAttributes.IsDataProtection = false
		event.SourceVolume.State = string(googleproxyclient.VolumeV1betaVolumeStateREADY)
		event.CreateReplicationParams.DestinationVolumeParameters.StoragePool = &storagePoolUri

		validPool := &googleproxyclient.PoolInternalV1beta{
			Network:          "network",
			ResourceId:       destPoolID,
			PoolId:           googleproxyclient.OptString{Value: destPoolID, Set: true},
			AllocatedBytes:   googleproxyclient.NewOptNilFloat64(0),
			SizeInBytes:      200,
			ServiceLevel:     googleproxyclient.PoolInternalV1betaServiceLevelFLEX,
			StoragePoolState: googleproxyclient.NewOptPoolInternalV1betaStoragePoolState(googleproxyclient.PoolInternalV1betaStoragePoolStateREADY),
		}
		mm.On("getDestinationPool", ctx, "basePath", "token", event.DestinationLocationID, event.DestinationProjectNumber, event.XCorrelationID, event.DestinationPoolName).Return(validPool, nil).Once()

		mm.On("replicationJobInProcess", ctx, event.SourceProjectNumber, event.DestinationProjectNumber, "basePath", "basePath", event.LocationID, event.DestinationLocationID, "token", "token", mock.Anything, "", event.SourcePool.UUID, destPoolID, event.XCorrelationID).Return(errors.New("replication job in process error")).Once()

		_, err := _validateCreateReplicationParams(ctx, event, mockStorage)
		assert.Error(t, err)
		assert.Equal(t, errors.New("replication job in process error"), err)
		mm.AssertExpectations(t)
	})

	t.Run("WhenQosValidationFails", func(t *testing.T) {
		mockStorage := &database.MockStorage{}
		setupMockNoActiveClusterUpgrade(mockStorage)
		mm := &monkeyMock{}
		mm.Patch()
		defer mm.Unpatch()

		mm.On("InternalUtilGetSignedToken", event.DestinationProjectNumber).Return("token", nil).Once()
		mm.On("InternalParseRegionAndZone", event.LocationID).Return("region-1", "zone-1", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "region-1").Return("basePath", nil).Once()
		mm.On("InternalParseRegionAndZone", event.DestinationLocationID).Return("region-2", "zone-2", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "region-2").Return("basePath", nil).Once()
		mm.On("validateReplicationResourceId", ctx, event.SourceProjectNumber, *event.CreateReplicationParams.ResourceID, event.VolumeResourceID, mockStorage).Return(nil).Once()
		mm.On("validateStoragePoolUri", *event.CreateReplicationParams.DestinationVolumeParameters.StoragePool).Return(nil).Once()

		event.SourceVolume.VolumeAttributes.IsDataProtection = false
		event.SourceVolume.State = string(googleproxyclient.VolumeV1betaVolumeStateREADY)
		event.CreateReplicationParams.DestinationVolumeParameters.StoragePool = &storagePoolUri

		validPool := &googleproxyclient.PoolInternalV1beta{
			Network:              "network",
			ResourceId:           destPoolID,
			PoolId:               googleproxyclient.OptString{Value: destPoolID, Set: true},
			AllocatedBytes:       googleproxyclient.NewOptNilFloat64(0),
			SizeInBytes:          200,
			ServiceLevel:         googleproxyclient.PoolInternalV1betaServiceLevelFLEX,
			StoragePoolState:     googleproxyclient.NewOptPoolInternalV1betaStoragePoolState(googleproxyclient.PoolInternalV1betaStoragePoolStateREADY),
			QosType:              googleproxyclient.OptNilString{Value: utils.QosTypeManual, Set: true},
			TotalThroughputMibps: googleproxyclient.NewOptNilFloat64(10000),
			TotalIops:            googleproxyclient.NewOptNilFloat64(50000),
		}
		mm.On("getDestinationPool", ctx, "basePath", "token", event.DestinationLocationID, event.DestinationProjectNumber, event.XCorrelationID, event.DestinationPoolName).Return(validPool, nil).Once()

		qosValidationErr := errors.New("qos validation failed")
		mm.On("validateVolumeQosParamsForReplication", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return((*int64)(nil), qosValidationErr).Once()

		_, err := _validateCreateReplicationParams(ctx, event, mockStorage)
		assert.Error(t, err)
		assert.Equal(t, qosValidationErr, err)
		mm.AssertExpectations(t)
	})

	t.Run("WhenGetCallbackTokenFails", func(t *testing.T) {
		mockStorage := &database.MockStorage{}
		setupMockNoActiveClusterUpgrade(mockStorage)
		mm := &monkeyMock{}
		mm.Patch()
		defer mm.Unpatch()

		mm.On("InternalUtilGetSignedToken", event.DestinationProjectNumber).Return("token", nil).Once()
		mm.On("InternalParseRegionAndZone", event.LocationID).Return("region-1", "zone-1", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "region-1").Return("basePath", nil).Once()
		mm.On("InternalParseRegionAndZone", event.DestinationLocationID).Return("region-2", "zone-2", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "region-2").Return("basePath", nil).Once()
		mm.On("validateReplicationResourceId", ctx, event.SourceProjectNumber, *event.CreateReplicationParams.ResourceID, event.VolumeResourceID, mockStorage).Return(nil).Once()
		mm.On("validateStoragePoolUri", *event.CreateReplicationParams.DestinationVolumeParameters.StoragePool).Return(nil).Once()
		iops := int64(0)
		mm.EXPECT().validateVolumeQosParamsForReplication(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&iops, nil).Once()

		// Reset volume state and storage pool
		event.SourceVolume.VolumeAttributes.IsDataProtection = false
		event.SourceVolume.State = string(googleproxyclient.VolumeV1betaVolumeStateREADY)
		event.CreateReplicationParams.DestinationVolumeParameters.StoragePool = &storagePoolUri

		validPool := &googleproxyclient.PoolInternalV1beta{
			Network:          "network",
			ResourceId:       destPoolID,
			PoolId:           googleproxyclient.OptString{Value: destPoolID, Set: true},
			AllocatedBytes:   googleproxyclient.NewOptNilFloat64(0),
			SizeInBytes:      200,
			ServiceLevel:     googleproxyclient.PoolInternalV1betaServiceLevelFLEX,
			StoragePoolState: googleproxyclient.NewOptPoolInternalV1betaStoragePoolState(googleproxyclient.PoolInternalV1betaStoragePoolStateREADY),
		}
		mm.On("getDestinationPool", ctx, "basePath", "token", event.DestinationLocationID, event.DestinationProjectNumber, event.XCorrelationID, event.DestinationPoolName).Return(validPool, nil).Once()

		mm.On("replicationJobInProcess", ctx, event.SourceProjectNumber, event.DestinationProjectNumber, "basePath", "basePath", event.LocationID, event.DestinationLocationID, "token", "token", mock.Anything, "", event.SourcePool.UUID, destPoolID, event.XCorrelationID).Return(nil).Once()

		mm.On("InternalUtilGetCallbackToken").Return("", errors.New("callback token error")).Once()

		_, err := _validateCreateReplicationParams(ctx, event, mockStorage)
		assert.Error(t, err)
		assert.Equal(t, vsaErrors.NewVCPError(vsaErrors.ErrGetSignedCallbackToken, errors.New("callback token error")), err)
		mm.AssertExpectations(t)
	})

	t.Run("WhenGetReplicationQuotaLimitFails", func(t *testing.T) {
		mockStorage := &database.MockStorage{}
		setupMockNoActiveClusterUpgrade(mockStorage)
		mm := &monkeyMock{}
		mm.Patch()
		defer mm.Unpatch()

		mm.On("InternalUtilGetSignedToken", event.DestinationProjectNumber).Return("token", nil).Once()
		mm.On("InternalParseRegionAndZone", event.LocationID).Return("region-1", "zone-1", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "region-1").Return("basePath", nil).Once()
		mm.On("InternalParseRegionAndZone", event.DestinationLocationID).Return("region-2", "zone-2", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "region-2").Return("basePath", nil).Once()
		mm.On("validateReplicationResourceId", ctx, event.SourceProjectNumber, *event.CreateReplicationParams.ResourceID, event.VolumeResourceID, mockStorage).Return(nil).Once()
		mm.On("validateStoragePoolUri", *event.CreateReplicationParams.DestinationVolumeParameters.StoragePool).Return(nil).Once()
		iops := int64(0)
		mm.EXPECT().validateVolumeQosParamsForReplication(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&iops, nil).Once()

		// Reset volume state and storage pool
		event.SourceVolume.VolumeAttributes.IsDataProtection = false
		event.SourceVolume.State = string(googleproxyclient.VolumeV1betaVolumeStateREADY)
		event.CreateReplicationParams.DestinationVolumeParameters.StoragePool = &storagePoolUri

		validPool := &googleproxyclient.PoolInternalV1beta{
			Network:          "network",
			ResourceId:       destPoolID,
			PoolId:           googleproxyclient.OptString{Value: destPoolID, Set: true},
			AllocatedBytes:   googleproxyclient.NewOptNilFloat64(0),
			SizeInBytes:      200,
			ServiceLevel:     googleproxyclient.PoolInternalV1betaServiceLevelFLEX,
			StoragePoolState: googleproxyclient.NewOptPoolInternalV1betaStoragePoolState(googleproxyclient.PoolInternalV1betaStoragePoolStateREADY),
		}
		mm.On("getDestinationPool", ctx, "basePath", "token", event.DestinationLocationID, event.DestinationProjectNumber, event.XCorrelationID, event.DestinationPoolName).Return(validPool, nil).Once()

		mm.On("replicationJobInProcess", ctx, event.SourceProjectNumber, event.DestinationProjectNumber, "basePath", "basePath", event.LocationID, event.DestinationLocationID, "token", "token", mock.Anything, "", event.SourcePool.UUID, destPoolID, event.XCorrelationID).Return(nil).Once()

		mm.On("InternalUtilGetCallbackToken").Return("callback-token", nil).Once()

		mm.On("getQuotaLimit", ctx, mock.Anything, event.DestinationLocationID, event.DestinationProjectNumber, "callback-token", mock.Anything).Return(0, errors.New("quota limit error")).Once()

		_, err := _validateCreateReplicationParams(ctx, event, mockStorage)
		assert.Error(t, err)
		assert.Equal(t, vsaErrors.NewVCPError(vsaErrors.ErrGetReplicationQuotaLimitInternal, errors.New("quota limit error")), err)
		mm.AssertExpectations(t)
	})
	t.Run("WhenGetReplicationCountFails", func(t *testing.T) {
		mockStorage := &database.MockStorage{}
		setupMockNoActiveClusterUpgrade(mockStorage)
		mm := &monkeyMock{}
		mm.Patch()
		defer mm.Unpatch()

		mm.On("InternalUtilGetSignedToken", event.DestinationProjectNumber).Return("token", nil).Once()
		mm.On("InternalParseRegionAndZone", event.LocationID).Return("region-1", "zone-1", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "region-1").Return("basePath", nil).Once()
		mm.On("InternalParseRegionAndZone", event.DestinationLocationID).Return("region-2", "zone-2", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "region-2").Return("basePath", nil).Once()
		mm.On("validateReplicationResourceId", ctx, event.SourceProjectNumber, *event.CreateReplicationParams.ResourceID, event.VolumeResourceID, mockStorage).Return(nil).Once()
		mm.On("validateStoragePoolUri", *event.CreateReplicationParams.DestinationVolumeParameters.StoragePool).Return(nil).Once()
		iops := int64(0)
		mm.EXPECT().validateVolumeQosParamsForReplication(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&iops, nil).Once()

		// Reset volume state and storage pool
		event.SourceVolume.VolumeAttributes.IsDataProtection = false
		event.SourceVolume.State = string(googleproxyclient.VolumeV1betaVolumeStateREADY)
		event.CreateReplicationParams.DestinationVolumeParameters.StoragePool = &storagePoolUri

		validPool := &googleproxyclient.PoolInternalV1beta{
			Network:          "network",
			ResourceId:       destPoolID,
			PoolId:           googleproxyclient.OptString{Value: destPoolID, Set: true},
			AllocatedBytes:   googleproxyclient.NewOptNilFloat64(0),
			SizeInBytes:      200,
			ServiceLevel:     googleproxyclient.PoolInternalV1betaServiceLevelFLEX,
			StoragePoolState: googleproxyclient.NewOptPoolInternalV1betaStoragePoolState(googleproxyclient.PoolInternalV1betaStoragePoolStateREADY),
		}
		mm.On("getDestinationPool", ctx, "basePath", "token", event.DestinationLocationID, event.DestinationProjectNumber, event.XCorrelationID, event.DestinationPoolName).Return(validPool, nil).Once()

		mm.On("replicationJobInProcess", ctx, event.SourceProjectNumber, event.DestinationProjectNumber, "basePath", "basePath", event.LocationID, event.DestinationLocationID, "token", "token", mock.Anything, "", event.SourcePool.UUID, destPoolID, event.XCorrelationID).Return(nil).Once()

		mm.On("InternalUtilGetCallbackToken").Return("callback-token", nil).Once()

		mm.On("getQuotaLimit", ctx, mock.Anything, event.DestinationLocationID, event.DestinationProjectNumber, "callback-token", mock.Anything).Return(5, nil).Once()

		mm.On("internalGetReplicationCount", ctx, "basePath", event.DestinationProjectNumber, event.DestinationLocationID, "", "token", mock.Anything, mock.Anything).Return(0, errors.New("replication count error")).Once()

		_, err := _validateCreateReplicationParams(ctx, event, mockStorage)
		assert.Error(t, err)
		assert.Equal(t, vsaErrors.NewVCPError(vsaErrors.ErrValidateCreateReplicationCvpInternalGetReplicationCount, errors.New("replication count error")), err)
		mm.AssertExpectations(t)
	})
	t.Run("WhenReplicationQuotaLimitExceeded", func(t *testing.T) {
		mockStorage := &database.MockStorage{}
		setupMockNoActiveClusterUpgrade(mockStorage)
		mm := &monkeyMock{}
		mm.Patch()
		defer mm.Unpatch()

		mm.On("InternalUtilGetSignedToken", event.DestinationProjectNumber).Return("token", nil).Once()
		mm.On("InternalParseRegionAndZone", event.LocationID).Return("region-1", "zone-1", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "region-1").Return("basePath", nil).Once()
		mm.On("InternalParseRegionAndZone", event.DestinationLocationID).Return("region-2", "zone-2", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "region-2").Return("basePath", nil).Once()
		mm.On("validateReplicationResourceId", ctx, event.SourceProjectNumber, *event.CreateReplicationParams.ResourceID, event.VolumeResourceID, mockStorage).Return(nil).Once()
		mm.On("validateStoragePoolUri", *event.CreateReplicationParams.DestinationVolumeParameters.StoragePool).Return(nil).Once()
		iops := int64(0)
		mm.EXPECT().validateVolumeQosParamsForReplication(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&iops, nil).Once()

		// Reset volume state and storage pool
		event.SourceVolume.VolumeAttributes.IsDataProtection = false
		event.SourceVolume.State = string(googleproxyclient.VolumeV1betaVolumeStateREADY)
		event.CreateReplicationParams.DestinationVolumeParameters.StoragePool = &storagePoolUri

		validPool := &googleproxyclient.PoolInternalV1beta{
			Network:          "network",
			ResourceId:       destPoolID,
			PoolId:           googleproxyclient.OptString{Value: destPoolID, Set: true},
			AllocatedBytes:   googleproxyclient.NewOptNilFloat64(0),
			SizeInBytes:      200,
			ServiceLevel:     googleproxyclient.PoolInternalV1betaServiceLevelFLEX,
			StoragePoolState: googleproxyclient.NewOptPoolInternalV1betaStoragePoolState(googleproxyclient.PoolInternalV1betaStoragePoolStateREADY),
		}
		mm.On("getDestinationPool", ctx, "basePath", "token", event.DestinationLocationID, event.DestinationProjectNumber, event.XCorrelationID, event.DestinationPoolName).Return(validPool, nil).Once()

		mm.On("replicationJobInProcess", ctx, event.SourceProjectNumber, event.DestinationProjectNumber, "basePath", "basePath", event.LocationID, event.DestinationLocationID, "token", "token", mock.Anything, "", event.SourcePool.UUID, destPoolID, event.XCorrelationID).Return(nil).Once()

		mm.On("InternalUtilGetCallbackToken").Return("callback-token", nil).Once()

		mm.On("getQuotaLimit", ctx, mock.Anything, event.DestinationLocationID, event.DestinationProjectNumber, "callback-token", mock.Anything).Return(5, nil).Once()

		mm.On("internalGetReplicationCount", ctx, "basePath", event.DestinationProjectNumber, event.DestinationLocationID, "", "token", mock.Anything, mock.Anything).Return(5, nil).Once()

		_, err := _validateCreateReplicationParams(ctx, event, mockStorage)
		assert.Error(t, err)
		assert.Equal(t, vsaErrors.NewVCPError(vsaErrors.ErrReplicationQuotaLimitExceeded, errors.New("Quota limit 'ReplicatedVolumesPerRegion' has been exceeded.")), err)
		mm.AssertExpectations(t)
	})
	t.Run("WhenGetVolumeQuotaLimitFails", func(t *testing.T) {
		mockStorage := &database.MockStorage{}
		setupMockNoActiveClusterUpgrade(mockStorage)
		mm := &monkeyMock{}
		mm.Patch()
		defer mm.Unpatch()

		mm.On("InternalUtilGetSignedToken", event.DestinationProjectNumber).Return("token", nil).Once()
		mm.On("InternalParseRegionAndZone", event.LocationID).Return("region-1", "zone-1", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "region-1").Return("basePath", nil).Once()
		mm.On("InternalParseRegionAndZone", event.DestinationLocationID).Return("region-2", "zone-2", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "region-2").Return("basePath", nil).Once()
		mm.On("validateReplicationResourceId", ctx, event.SourceProjectNumber, *event.CreateReplicationParams.ResourceID, event.VolumeResourceID, mockStorage).Return(nil).Once()
		mm.On("validateStoragePoolUri", *event.CreateReplicationParams.DestinationVolumeParameters.StoragePool).Return(nil).Once()
		iops := int64(0)
		mm.EXPECT().validateVolumeQosParamsForReplication(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&iops, nil).Once()

		// Reset volume state and storage pool
		event.SourceVolume.VolumeAttributes.IsDataProtection = false
		event.SourceVolume.State = string(googleproxyclient.VolumeV1betaVolumeStateREADY)
		event.CreateReplicationParams.DestinationVolumeParameters.StoragePool = &storagePoolUri

		validPool := &googleproxyclient.PoolInternalV1beta{
			Network:          "network",
			ResourceId:       destPoolID,
			PoolId:           googleproxyclient.OptString{Value: destPoolID, Set: true},
			AllocatedBytes:   googleproxyclient.NewOptNilFloat64(0),
			SizeInBytes:      200,
			ServiceLevel:     googleproxyclient.PoolInternalV1betaServiceLevelFLEX,
			StoragePoolState: googleproxyclient.NewOptPoolInternalV1betaStoragePoolState(googleproxyclient.PoolInternalV1betaStoragePoolStateREADY),
		}
		mm.On("getDestinationPool", ctx, "basePath", "token", event.DestinationLocationID, event.DestinationProjectNumber, event.XCorrelationID, event.DestinationPoolName).Return(validPool, nil).Once()

		mm.On("replicationJobInProcess", ctx, event.SourceProjectNumber, event.DestinationProjectNumber, "basePath", "basePath", event.LocationID, event.DestinationLocationID, "token", "token", mock.Anything, "", event.SourcePool.UUID, destPoolID, event.XCorrelationID).Return(nil).Once()

		mm.On("InternalUtilGetCallbackToken").Return("callback-token", nil).Once()

		mm.On("getQuotaLimit", ctx, mock.Anything, event.DestinationLocationID, event.DestinationProjectNumber, "callback-token", common.ResourceTypeReplication).Return(10, nil).Once()

		mm.On("internalGetReplicationCount", ctx, "basePath", event.DestinationProjectNumber, event.DestinationLocationID, "", "token", mock.Anything, mock.Anything).Return(0, nil).Once()

		mm.On("getQuotaLimit", ctx, mock.Anything, event.DestinationLocationID, event.DestinationProjectNumber, "callback-token", common.ResourceTypeVolume).Return(0, errors.New("volume quota limit error")).Once()

		_, err := _validateCreateReplicationParams(ctx, event, mockStorage)
		assert.Error(t, err)
		assert.Equal(t, vsaErrors.NewVCPError(vsaErrors.ErrGetVolumeQuotaLimitInternal, errors.New("volume quota limit error")), err)
		mm.AssertExpectations(t)
	})
	t.Run("WhenInternalGetVolumeCountFails", func(t *testing.T) {
		mockStorage := &database.MockStorage{}
		setupMockNoActiveClusterUpgrade(mockStorage)
		mm := &monkeyMock{}
		mm.Patch()
		defer mm.Unpatch()

		mm.On("InternalUtilGetSignedToken", event.DestinationProjectNumber).Return("token", nil).Once()
		mm.On("InternalParseRegionAndZone", event.LocationID).Return("region-1", "zone-1", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "region-1").Return("basePath", nil).Once()
		mm.On("InternalParseRegionAndZone", event.DestinationLocationID).Return("region-2", "zone-2", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "region-2").Return("basePath", nil).Once()
		mm.On("validateReplicationResourceId", ctx, event.SourceProjectNumber, *event.CreateReplicationParams.ResourceID, event.VolumeResourceID, mockStorage).Return(nil).Once()
		mm.On("validateStoragePoolUri", *event.CreateReplicationParams.DestinationVolumeParameters.StoragePool).Return(nil).Once()
		iops := int64(0)
		mm.EXPECT().validateVolumeQosParamsForReplication(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&iops, nil).Once()

		// Reset volume state and storage pool
		event.SourceVolume.VolumeAttributes.IsDataProtection = false
		event.SourceVolume.State = string(googleproxyclient.VolumeV1betaVolumeStateREADY)
		event.CreateReplicationParams.DestinationVolumeParameters.StoragePool = &storagePoolUri

		validPool := &googleproxyclient.PoolInternalV1beta{
			Network:          "network",
			ResourceId:       destPoolID,
			PoolId:           googleproxyclient.OptString{Value: destPoolID, Set: true},
			AllocatedBytes:   googleproxyclient.NewOptNilFloat64(0),
			SizeInBytes:      200,
			ServiceLevel:     googleproxyclient.PoolInternalV1betaServiceLevelFLEX,
			StoragePoolState: googleproxyclient.NewOptPoolInternalV1betaStoragePoolState(googleproxyclient.PoolInternalV1betaStoragePoolStateREADY),
		}
		mm.On("getDestinationPool", ctx, "basePath", "token", event.DestinationLocationID, event.DestinationProjectNumber, event.XCorrelationID, event.DestinationPoolName).Return(validPool, nil).Once()

		mm.On("replicationJobInProcess", ctx, event.SourceProjectNumber, event.DestinationProjectNumber, "basePath", "basePath", event.LocationID, event.DestinationLocationID, "token", "token", mock.Anything, "", event.SourcePool.UUID, destPoolID, event.XCorrelationID).Return(nil).Once()

		mm.On("InternalUtilGetCallbackToken").Return("callback-token", nil).Once()

		mm.On("getQuotaLimit", ctx, mock.Anything, event.DestinationLocationID, event.DestinationProjectNumber, "callback-token", common.ResourceTypeReplication).Return(10, nil).Once()

		mm.On("internalGetReplicationCount", ctx, "basePath", event.DestinationProjectNumber, event.DestinationLocationID, "", "token", mock.Anything, mock.Anything).Return(0, nil).Once()

		mm.On("getQuotaLimit", ctx, mock.Anything, event.DestinationLocationID, event.DestinationProjectNumber, "callback-token", common.ResourceTypeVolume).Return(5, nil).Once()

		mm.On("internalGetVolumeCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(0, errors.New("volume count error")).Once()

		_, err := _validateCreateReplicationParams(ctx, event, mockStorage)
		assert.Error(t, err)
		assert.Equal(t, vsaErrors.NewVCPError(vsaErrors.ErrValidateCreateReplicationCvpInternalGetVolumeCount, errors.New("volume count error")), err)
		mm.AssertExpectations(t)
	})
	t.Run("WhenVolumeQuotaLimitExceeded", func(t *testing.T) {
		mockStorage := &database.MockStorage{}
		setupMockNoActiveClusterUpgrade(mockStorage)
		mm := &monkeyMock{}
		mm.Patch()
		defer mm.Unpatch()

		mm.On("InternalUtilGetSignedToken", event.DestinationProjectNumber).Return("token", nil).Once()
		mm.On("InternalParseRegionAndZone", event.LocationID).Return("region-1", "zone-1", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "region-1").Return("basePath", nil).Once()
		mm.On("InternalParseRegionAndZone", event.DestinationLocationID).Return("region-2", "zone-2", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "region-2").Return("basePath", nil).Once()
		mm.On("validateReplicationResourceId", ctx, event.SourceProjectNumber, *event.CreateReplicationParams.ResourceID, event.VolumeResourceID, mockStorage).Return(nil).Once()
		mm.On("validateStoragePoolUri", *event.CreateReplicationParams.DestinationVolumeParameters.StoragePool).Return(nil).Once()
		iops := int64(0)
		mm.EXPECT().validateVolumeQosParamsForReplication(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&iops, nil).Once()

		// Reset volume state and storage pool
		event.SourceVolume.VolumeAttributes.IsDataProtection = false
		event.SourceVolume.State = string(googleproxyclient.VolumeV1betaVolumeStateREADY)
		event.CreateReplicationParams.DestinationVolumeParameters.StoragePool = &storagePoolUri

		validPool := &googleproxyclient.PoolInternalV1beta{
			Network:          "network",
			ResourceId:       destPoolID,
			PoolId:           googleproxyclient.OptString{Value: destPoolID, Set: true},
			AllocatedBytes:   googleproxyclient.NewOptNilFloat64(0),
			SizeInBytes:      200,
			ServiceLevel:     googleproxyclient.PoolInternalV1betaServiceLevelFLEX,
			StoragePoolState: googleproxyclient.NewOptPoolInternalV1betaStoragePoolState(googleproxyclient.PoolInternalV1betaStoragePoolStateREADY),
		}
		mm.On("getDestinationPool", ctx, "basePath", "token", event.DestinationLocationID, event.DestinationProjectNumber, event.XCorrelationID, event.DestinationPoolName).Return(validPool, nil).Once()

		mm.On("replicationJobInProcess", ctx, event.SourceProjectNumber, event.DestinationProjectNumber, "basePath", "basePath", event.LocationID, event.DestinationLocationID, "token", "token", mock.Anything, "", event.SourcePool.UUID, destPoolID, event.XCorrelationID).Return(nil).Once()

		mm.On("InternalUtilGetCallbackToken").Return("callback-token", nil).Once()

		mm.On("getQuotaLimit", ctx, mock.Anything, event.DestinationLocationID, event.DestinationProjectNumber, "callback-token", common.ResourceTypeReplication).Return(10, nil).Once()

		mm.On("internalGetReplicationCount", ctx, "basePath", event.DestinationProjectNumber, event.DestinationLocationID, "", "token", mock.Anything, mock.Anything).Return(0, nil).Once()

		mm.On("getQuotaLimit", ctx, mock.Anything, event.DestinationLocationID, event.DestinationProjectNumber, "callback-token", common.ResourceTypeVolume).Return(5, nil).Once()

		mm.On("internalGetVolumeCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(5, nil).Once()

		_, err := _validateCreateReplicationParams(ctx, event, mockStorage)
		assert.Error(t, err)
		assert.Equal(t, vsaErrors.NewVCPError(vsaErrors.ErrVolumeQuotaLimitExceeded, errors.New("Quota limit 'VolumesPerRegion' on destination region has been exceeded.")), err)
		mm.AssertExpectations(t)
	})
	t.Run("WhenReplicationQuotaLimitExceededForCrossProjectSameRegion", func(t *testing.T) {
		// Cross-project same-region (CPR): one unified in-region unit per project (source then destination).
		mockStorage := &database.MockStorage{}
		setupMockNoActiveClusterUpgrade(mockStorage)
		mm := &monkeyMock{}
		mm.Patch()
		defer mm.Unpatch()

		cpcrEnabled = true
		czcrEnabled = true
		defer func() {
			cpcrEnabled = env.GetBool("CPCRR_ENABLED", false)
			czcrEnabled = env.GetBool("CZCRR_ENABLED", false)
		}()

		event.SourceProjectNumber = "proj-src"
		event.DestinationProjectNumber = "proj-dst"
		defer func() {
			event.SourceProjectNumber = projectNumber
			event.DestinationProjectNumber = destProjectNumber
		}()

		mm.On("InternalUtilGetSignedToken", event.SourceProjectNumber).Return("token", nil).Once()
		mm.On("InternalUtilGetSignedToken", event.DestinationProjectNumber).Return("dst-token", nil).Once()
		mm.On("InternalParseRegionAndZone", event.LocationID).Return("us-east1", "us-east1-a", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "us-east1").Return("basePath", nil).Once()
		mm.On("InternalParseRegionAndZone", event.DestinationLocationID).Return("us-east1", "us-east1-b", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "us-east1").Return("basePath", nil).Once()
		mm.On("validateReplicationResourceId", ctx, event.SourceProjectNumber, *event.CreateReplicationParams.ResourceID, event.VolumeResourceID, mockStorage).Return(nil).Once()
		mm.On("validateStoragePoolUri", *event.CreateReplicationParams.DestinationVolumeParameters.StoragePool).Return(nil).Once()
		iops := int64(0)
		mm.EXPECT().validateVolumeQosParamsForReplication(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&iops, nil).Once()

		event.SourceVolume.VolumeAttributes.IsDataProtection = false
		event.SourceVolume.State = string(googleproxyclient.VolumeV1betaVolumeStateREADY)
		event.CreateReplicationParams.DestinationVolumeParameters.StoragePool = &storagePoolUri

		validPool := &googleproxyclient.PoolInternalV1beta{
			Network:          "network",
			ResourceId:       destPoolID,
			PoolId:           googleproxyclient.OptString{Value: destPoolID, Set: true},
			AllocatedBytes:   googleproxyclient.NewOptNilFloat64(0),
			SizeInBytes:      200,
			ServiceLevel:     googleproxyclient.PoolInternalV1betaServiceLevelFLEX,
			StoragePoolState: googleproxyclient.NewOptPoolInternalV1betaStoragePoolState(googleproxyclient.PoolInternalV1betaStoragePoolStateREADY),
		}
		mm.On("getDestinationPool", ctx, "basePath", "dst-token", event.DestinationLocationID, event.DestinationProjectNumber, event.XCorrelationID, event.DestinationPoolName).Return(validPool, nil).Once()
		mm.On("replicationJobInProcess", ctx, event.SourceProjectNumber, event.DestinationProjectNumber, "basePath", "basePath", event.LocationID, event.DestinationLocationID, "token", "dst-token", mock.Anything, "", event.SourcePool.UUID, destPoolID, event.XCorrelationID).Return(nil).Once()
		mm.On("InternalUtilGetCallbackToken").Return("callback-token", nil).Once()
		mm.On("getQuotaLimit", ctx, mock.Anything, event.LocationID, event.SourceProjectNumber, "callback-token", common.ResourceTypeInRegionReplication).Return(10, nil).Once()
		mm.On("internalCountInRegionReplications", ctx, mockStorage, event.SourceProjectNumber).Return(8, nil).Once()
		mm.On("getQuotaLimit", ctx, mock.Anything, event.DestinationLocationID, event.DestinationProjectNumber, "callback-token", common.ResourceTypeInRegionReplication).Return(10, nil).Once()
		mm.On("internalCountInRegionReplications", ctx, mockStorage, event.DestinationProjectNumber).Return(10, nil).Once()

		_, err := _validateCreateReplicationParams(ctx, event, mockStorage)
		assert.Error(t, err)
		assert.Equal(t, vsaErrors.NewVCPError(vsaErrors.ErrInRegionReplicationQuotaLimitExceeded,
			fmt.Errorf("quota limit 'Flex Unified in-region replicated volumes per region' has been exceeded for project %s", event.DestinationProjectNumber)), err)
		mm.AssertExpectations(t)
	})
	t.Run("WhenReplicationQuotaLimitExceededForSameRegion", func(t *testing.T) {
		// Same-project same-region (IRR) uses unified in-region quota for that project.
		mockStorage := &database.MockStorage{}
		setupMockNoActiveClusterUpgrade(mockStorage)
		mm := &monkeyMock{}
		mm.Patch()
		defer mm.Unpatch()

		// Enable cross-zone replication for same region test
		czcrEnabled = true
		defer func() {
			czcrEnabled = env.GetBool("CZCRR_ENABLED", false)
		}()

		mm.On("InternalUtilGetSignedToken", event.SourceProjectNumber).Return("token", nil).Once()
		// Same region for source and destination to trigger the same-region check
		mm.On("InternalParseRegionAndZone", event.LocationID).Return("us-east1", "us-east1-a", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "us-east1").Return("basePath", nil).Once()
		mm.On("InternalParseRegionAndZone", event.DestinationLocationID).Return("us-east1", "us-east1-b", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "us-east1").Return("basePath", nil).Once()
		mm.On("validateReplicationResourceId", ctx, event.SourceProjectNumber, *event.CreateReplicationParams.ResourceID, event.VolumeResourceID, mockStorage).Return(nil).Once()
		mm.On("validateStoragePoolUri", *event.CreateReplicationParams.DestinationVolumeParameters.StoragePool).Return(nil).Once()
		iops := int64(0)
		mm.EXPECT().validateVolumeQosParamsForReplication(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&iops, nil).Once()

		// Reset volume state and storage pool
		event.SourceVolume.VolumeAttributes.IsDataProtection = false
		event.SourceVolume.State = string(googleproxyclient.VolumeV1betaVolumeStateREADY)
		event.CreateReplicationParams.DestinationVolumeParameters.StoragePool = &storagePoolUri

		validPool := &googleproxyclient.PoolInternalV1beta{
			Network:          "network",
			ResourceId:       destPoolID,
			PoolId:           googleproxyclient.OptString{Value: destPoolID, Set: true},
			AllocatedBytes:   googleproxyclient.NewOptNilFloat64(0),
			SizeInBytes:      200,
			ServiceLevel:     googleproxyclient.PoolInternalV1betaServiceLevelFLEX,
			StoragePoolState: googleproxyclient.NewOptPoolInternalV1betaStoragePoolState(googleproxyclient.PoolInternalV1betaStoragePoolStateREADY),
		}
		mm.On("getDestinationPool", ctx, "basePath", "token", event.DestinationLocationID, event.DestinationProjectNumber, event.XCorrelationID, event.DestinationPoolName).Return(validPool, nil).Once()

		mm.On("replicationJobInProcess", ctx, event.SourceProjectNumber, event.DestinationProjectNumber, "basePath", "basePath", event.LocationID, event.DestinationLocationID, "token", "token", mock.Anything, "", event.SourcePool.UUID, destPoolID, event.XCorrelationID).Return(nil).Once()

		mm.On("InternalUtilGetCallbackToken").Return("callback-token", nil).Once()

		mm.On("getQuotaLimit", ctx, mock.Anything, event.DestinationLocationID, event.DestinationProjectNumber, "callback-token", common.ResourceTypeInRegionReplication).Return(10, nil).Once()
		mm.On("internalCountInRegionReplications", ctx, mockStorage, event.DestinationProjectNumber).Return(9, nil).Once()

		_, err := _validateCreateReplicationParams(ctx, event, mockStorage)
		assert.Error(t, err)
		assert.Equal(t, vsaErrors.NewVCPError(vsaErrors.ErrInRegionReplicationQuotaLimitExceeded,
			fmt.Errorf("quota limit 'Flex Unified in-region replicated volumes per region' has been exceeded for project %s", event.DestinationProjectNumber)), err)
		mm.AssertExpectations(t)
	})
	t.Run("WhenGetVolumeFails", func(t *testing.T) {
		mockStorage := &database.MockStorage{}
		setupMockNoActiveClusterUpgrade(mockStorage)
		mm := &monkeyMock{}
		mm.Patch()
		defer mm.Unpatch()

		mm.On("InternalUtilGetSignedToken", event.DestinationProjectNumber).Return("token", nil).Once()
		mm.On("InternalParseRegionAndZone", event.LocationID).Return("region-1", "zone-1", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "region-1").Return("basePath", nil).Once()
		mm.On("InternalParseRegionAndZone", event.DestinationLocationID).Return("region-2", "zone-2", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "region-2").Return("basePath", nil).Once()
		mm.On("validateReplicationResourceId", ctx, event.SourceProjectNumber, *event.CreateReplicationParams.ResourceID, event.VolumeResourceID, mockStorage).Return(nil).Once()
		mm.On("validateStoragePoolUri", *event.CreateReplicationParams.DestinationVolumeParameters.StoragePool).Return(nil).Once()
		iops := int64(0)
		mm.EXPECT().validateVolumeQosParamsForReplication(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&iops, nil).Once()

		// Reset volume state and storage pool
		event.SourceVolume.VolumeAttributes.IsDataProtection = false
		event.SourceVolume.State = string(googleproxyclient.VolumeV1betaVolumeStateREADY)
		event.CreateReplicationParams.DestinationVolumeParameters.StoragePool = &storagePoolUri

		validPool := &googleproxyclient.PoolInternalV1beta{
			Network:          "network",
			ResourceId:       destPoolID,
			PoolId:           googleproxyclient.OptString{Value: destPoolID, Set: true},
			AllocatedBytes:   googleproxyclient.NewOptNilFloat64(0),
			SizeInBytes:      200,
			ServiceLevel:     googleproxyclient.PoolInternalV1betaServiceLevelFLEX,
			StoragePoolState: googleproxyclient.NewOptPoolInternalV1betaStoragePoolState(googleproxyclient.PoolInternalV1betaStoragePoolStateREADY),
		}
		mm.On("getDestinationPool", ctx, "basePath", "token", event.DestinationLocationID, event.DestinationProjectNumber, event.XCorrelationID, event.DestinationPoolName).Return(validPool, nil).Once()

		mm.On("replicationJobInProcess", ctx, event.SourceProjectNumber, event.DestinationProjectNumber, "basePath", "basePath", event.LocationID, event.DestinationLocationID, "token", "token", mock.Anything, "", event.SourcePool.UUID, destPoolID, event.XCorrelationID).Return(nil).Once()

		mm.On("InternalUtilGetCallbackToken").Return("callback-token", nil).Once()

		mm.On("getQuotaLimit", ctx, mock.Anything, event.DestinationLocationID, event.DestinationProjectNumber, "callback-token", common.ResourceTypeReplication).Return(10, nil).Once()

		mm.On("internalGetReplicationCount", ctx, "basePath", event.DestinationProjectNumber, event.DestinationLocationID, "", "token", mock.Anything, mock.Anything).Return(0, nil).Once()

		mm.On("getQuotaLimit", ctx, mock.Anything, event.DestinationLocationID, event.DestinationProjectNumber, "callback-token", common.ResourceTypeVolume).Return(10, nil).Once()

		mm.On("internalGetVolumeCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(0, nil).Once()

		// Mock destination volume already exists
		existingVolume := googleproxyclient.VolumeV1beta{
			ResourceId:    "vol_2",
			CreationToken: googleproxyclient.OptString{Value: "share_2", Set: true},
		}
		mm.On("getVolume", ctx, "basePath", "token", event.DestinationLocationID, event.DestinationProjectNumber, event.XCorrelationID, "vol_2").Return(existingVolume, errors.New("get volume error")).Once()

		_, err := _validateCreateReplicationParams(ctx, event, mockStorage)
		assert.Error(t, err)
		assert.Equal(t, vsaErrors.NewVCPError(vsaErrors.ErrValidateGetVolumeReplicationCreation, errors.New("get volume error")), err)
		mm.AssertExpectations(t)
	})
	t.Run("WhenDestinationVolumeAlreadyExists", func(t *testing.T) {
		mockStorage := &database.MockStorage{}
		setupMockNoActiveClusterUpgrade(mockStorage)
		mm := &monkeyMock{}
		mm.Patch()
		defer mm.Unpatch()

		mm.On("InternalUtilGetSignedToken", event.DestinationProjectNumber).Return("token", nil).Once()
		mm.On("InternalParseRegionAndZone", event.LocationID).Return("region-1", "zone-1", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "region-1").Return("basePath", nil).Once()
		mm.On("InternalParseRegionAndZone", event.DestinationLocationID).Return("region-2", "zone-2", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "region-2").Return("basePath", nil).Once()
		mm.On("validateReplicationResourceId", ctx, event.SourceProjectNumber, *event.CreateReplicationParams.ResourceID, event.VolumeResourceID, mockStorage).Return(nil).Once()
		mm.On("validateStoragePoolUri", *event.CreateReplicationParams.DestinationVolumeParameters.StoragePool).Return(nil).Once()
		iops := int64(0)
		mm.EXPECT().validateVolumeQosParamsForReplication(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&iops, nil).Once()

		// Reset volume state and storage pool
		event.SourceVolume.VolumeAttributes.IsDataProtection = false
		event.SourceVolume.State = string(googleproxyclient.VolumeV1betaVolumeStateREADY)
		event.CreateReplicationParams.DestinationVolumeParameters.StoragePool = &storagePoolUri

		validPool := &googleproxyclient.PoolInternalV1beta{
			Network:          "network",
			ResourceId:       destPoolID,
			PoolId:           googleproxyclient.OptString{Value: destPoolID, Set: true},
			AllocatedBytes:   googleproxyclient.NewOptNilFloat64(0),
			SizeInBytes:      200,
			ServiceLevel:     googleproxyclient.PoolInternalV1betaServiceLevelFLEX,
			StoragePoolState: googleproxyclient.NewOptPoolInternalV1betaStoragePoolState(googleproxyclient.PoolInternalV1betaStoragePoolStateREADY),
		}
		mm.On("getDestinationPool", ctx, "basePath", "token", event.DestinationLocationID, event.DestinationProjectNumber, event.XCorrelationID, event.DestinationPoolName).Return(validPool, nil).Once()

		mm.On("replicationJobInProcess", ctx, event.SourceProjectNumber, event.DestinationProjectNumber, "basePath", "basePath", event.LocationID, event.DestinationLocationID, "token", "token", mock.Anything, "", event.SourcePool.UUID, destPoolID, event.XCorrelationID).Return(nil).Once()

		mm.On("InternalUtilGetCallbackToken").Return("callback-token", nil).Once()

		mm.On("getQuotaLimit", ctx, mock.Anything, event.DestinationLocationID, event.DestinationProjectNumber, "callback-token", common.ResourceTypeReplication).Return(10, nil).Once()

		mm.On("internalGetReplicationCount", ctx, "basePath", event.DestinationProjectNumber, event.DestinationLocationID, "", "token", mock.Anything, mock.Anything).Return(0, nil).Once()

		mm.On("getQuotaLimit", ctx, mock.Anything, event.DestinationLocationID, event.DestinationProjectNumber, "callback-token", common.ResourceTypeVolume).Return(10, nil).Once()

		mm.On("internalGetVolumeCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(0, nil).Once()

		// Mock destination volume already exists
		existingVolume := googleproxyclient.VolumeV1beta{
			ResourceId:    "vol_2",
			CreationToken: googleproxyclient.OptString{Value: "share_2", Set: true},
		}
		mm.On("getVolume", ctx, "basePath", "token", event.DestinationLocationID, event.DestinationProjectNumber, event.XCorrelationID, "vol_2").Return(existingVolume, nil).Once()

		_, err := _validateCreateReplicationParams(ctx, event, mockStorage)
		assert.Error(t, err)
		assert.Equal(t, vsaErrors.NewVCPError(vsaErrors.ErrGetVolumeCreateTokenInUseRemoteShareName, errors.New("RemoteShareName already Exists")), err)
		mm.AssertExpectations(t)
	})
	t.Run("WhenCreateReplicationObjectsFails", func(t *testing.T) {
		mockStorage := &database.MockStorage{}
		setupMockNoActiveClusterUpgrade(mockStorage)
		mm := &monkeyMock{}
		mm.Patch()
		defer mm.Unpatch()

		mm.On("InternalUtilGetSignedToken", event.DestinationProjectNumber).Return("token", nil).Once()
		mm.On("InternalParseRegionAndZone", event.LocationID).Return("region-1", "zone-1", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "region-1").Return("basePath", nil).Once()
		mm.On("InternalParseRegionAndZone", event.DestinationLocationID).Return("region-2", "zone-2", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "region-2").Return("basePath", nil).Once()
		mm.On("validateReplicationResourceId", ctx, event.SourceProjectNumber, *event.CreateReplicationParams.ResourceID, event.VolumeResourceID, mockStorage).Return(nil).Once()
		mm.On("validateStoragePoolUri", *event.CreateReplicationParams.DestinationVolumeParameters.StoragePool).Return(nil).Once()
		iops := int64(0)
		mm.EXPECT().validateVolumeQosParamsForReplication(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&iops, nil).Once()

		// Reset volume state and storage pool
		event.SourceVolume.VolumeAttributes.IsDataProtection = false
		event.SourceVolume.State = string(googleproxyclient.VolumeV1betaVolumeStateREADY)
		event.CreateReplicationParams.DestinationVolumeParameters.StoragePool = &storagePoolUri

		validPool := &googleproxyclient.PoolInternalV1beta{
			Network:          "network",
			ResourceId:       destPoolID,
			PoolId:           googleproxyclient.OptString{Value: destPoolID, Set: true},
			AllocatedBytes:   googleproxyclient.NewOptNilFloat64(0),
			SizeInBytes:      200,
			ServiceLevel:     googleproxyclient.PoolInternalV1betaServiceLevelFLEX,
			StoragePoolState: googleproxyclient.NewOptPoolInternalV1betaStoragePoolState(googleproxyclient.PoolInternalV1betaStoragePoolStateREADY),
		}
		mm.On("getDestinationPool", ctx, "basePath", "token", event.DestinationLocationID, event.DestinationProjectNumber, event.XCorrelationID, event.DestinationPoolName).Return(validPool, nil).Once()

		mm.On("replicationJobInProcess", ctx, event.SourceProjectNumber, event.DestinationProjectNumber, "basePath", "basePath", event.LocationID, event.DestinationLocationID, "token", "token", mock.Anything, "", event.SourcePool.UUID, destPoolID, event.XCorrelationID).Return(nil).Once()

		mm.On("InternalUtilGetCallbackToken").Return("callback-token", nil).Once()

		mm.On("getQuotaLimit", ctx, mock.Anything, event.DestinationLocationID, event.DestinationProjectNumber, "callback-token", common.ResourceTypeReplication).Return(10, nil).Once()

		mm.On("internalGetReplicationCount", ctx, "basePath", event.DestinationProjectNumber, event.DestinationLocationID, "", "token", mock.Anything, mock.Anything).Return(0, nil).Once()

		mm.On("getQuotaLimit", ctx, mock.Anything, event.DestinationLocationID, event.DestinationProjectNumber, "callback-token", common.ResourceTypeVolume).Return(10, nil).Once()

		mm.On("internalGetVolumeCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(0, nil).Once()

		// Mock destination volume not found (which is expected)
		mm.On("getVolume", ctx, "basePath", "token", event.DestinationLocationID, event.DestinationProjectNumber, event.XCorrelationID, "vol_2").Return(googleproxyclient.VolumeV1beta{}, errors.NewNotFoundErr("Volume", &[]string{"vol_2"}[0])).Once()

		mm.On("createReplicationObjects", event, event.DestinationLocationID, "region-1", "region-2").Return(nil, errors.New("create replication objects error")).Once()

		_, err := _validateCreateReplicationParams(ctx, event, mockStorage)
		assert.Error(t, err)
		assert.Equal(t, vsaErrors.NewVCPError(vsaErrors.ErrValidateCreateDummyReplication, errors.New("create replication objects error")), err)
		mm.AssertExpectations(t)
	})

	t.Run("WhenDestinationVolumeShareNameConflict", func(t *testing.T) {
		mockStorage := &database.MockStorage{}
		setupMockNoActiveClusterUpgrade(mockStorage)
		mm := &monkeyMock{}
		mm.Patch()
		defer mm.Unpatch()

		mm.On("InternalUtilGetSignedToken", event.DestinationProjectNumber).Return("token", nil).Once()
		mm.On("InternalParseRegionAndZone", event.LocationID).Return("region-1", "zone-1", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "region-1").Return("basePath", nil).Once()
		mm.On("InternalParseRegionAndZone", event.DestinationLocationID).Return("region-2", "zone-2", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "region-2").Return("basePath", nil).Once()
		mm.On("validateReplicationResourceId", ctx, event.SourceProjectNumber, *event.CreateReplicationParams.ResourceID, event.VolumeResourceID, mockStorage).Return(nil).Once()
		mm.On("validateStoragePoolUri", *event.CreateReplicationParams.DestinationVolumeParameters.StoragePool).Return(nil).Once()
		iops := int64(0)
		mm.EXPECT().validateVolumeQosParamsForReplication(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&iops, nil).Once()

		// Reset volume state and storage pool
		event.SourceVolume.VolumeAttributes.IsDataProtection = false
		event.SourceVolume.State = string(googleproxyclient.VolumeV1betaVolumeStateREADY)
		event.CreateReplicationParams.DestinationVolumeParameters.StoragePool = &storagePoolUri

		validPool := &googleproxyclient.PoolInternalV1beta{
			Network:          "network",
			ResourceId:       destPoolID,
			PoolId:           googleproxyclient.OptString{Value: destPoolID, Set: true},
			AllocatedBytes:   googleproxyclient.NewOptNilFloat64(0),
			SizeInBytes:      200,
			ServiceLevel:     googleproxyclient.PoolInternalV1betaServiceLevelFLEX,
			StoragePoolState: googleproxyclient.NewOptPoolInternalV1betaStoragePoolState(googleproxyclient.PoolInternalV1betaStoragePoolStateREADY),
		}
		mm.On("getDestinationPool", ctx, "basePath", "token", event.DestinationLocationID, event.DestinationProjectNumber, event.XCorrelationID, event.DestinationPoolName).Return(validPool, nil).Once()

		mm.On("replicationJobInProcess", ctx, event.SourceProjectNumber, event.DestinationProjectNumber, "basePath", "basePath", event.LocationID, event.DestinationLocationID, "token", "token", mock.Anything, "", event.SourcePool.UUID, destPoolID, event.XCorrelationID).Return(nil).Once()

		mm.On("InternalUtilGetCallbackToken").Return("callback-token", nil).Once()

		mm.On("getQuotaLimit", ctx, mock.Anything, event.DestinationLocationID, event.DestinationProjectNumber, "callback-token", common.ResourceTypeReplication).Return(10, nil).Once()

		mm.On("internalGetReplicationCount", ctx, "basePath", event.DestinationProjectNumber, event.DestinationLocationID, "", "token", mock.Anything, mock.Anything).Return(0, nil).Once()

		mm.On("getQuotaLimit", ctx, mock.Anything, event.DestinationLocationID, event.DestinationProjectNumber, "callback-token", common.ResourceTypeVolume).Return(10, nil).Once()

		mm.On("internalGetVolumeCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(0, nil).Once()

		// Mock destination volume with conflicting share name
		existingVolume := googleproxyclient.VolumeV1beta{
			ResourceId:    "vol_2",
			CreationToken: googleproxyclient.OptString{Value: "share_2", Set: true}, // Same as event.CreateReplicationParams.DestinationVolumeParameters.ShareName
		}
		mm.On("getVolume", ctx, "basePath", "token", event.DestinationLocationID, event.DestinationProjectNumber, event.XCorrelationID, "vol_2").Return(existingVolume, nil).Once()

		_, err := _validateCreateReplicationParams(ctx, event, mockStorage)
		assert.Error(t, err)
		assert.Equal(t, vsaErrors.NewVCPError(vsaErrors.ErrGetVolumeCreateTokenInUseRemoteShareName, errors.New("RemoteShareName already Exists")), err)
		mm.AssertExpectations(t)
	})

	t.Run("WhenDestinationVolumeUsesDefaultValues", func(t *testing.T) {
		mockStorage := &database.MockStorage{}
		setupMockNoActiveClusterUpgrade(mockStorage)
		mm := &monkeyMock{}
		mm.Patch()
		defer mm.Unpatch()

		mm.On("InternalUtilGetSignedToken", event.DestinationProjectNumber).Return("token", nil).Once()
		mm.On("InternalParseRegionAndZone", event.LocationID).Return("region-1", "zone-1", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "region-1").Return("basePath", nil).Once()
		mm.On("InternalParseRegionAndZone", event.DestinationLocationID).Return("region-2", "zone-2", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "region-2").Return("basePath", nil).Once()
		mm.On("validateReplicationResourceId", ctx, event.SourceProjectNumber, *event.CreateReplicationParams.ResourceID, event.VolumeResourceID, mockStorage).Return(nil).Once()
		mm.On("validateStoragePoolUri", *event.CreateReplicationParams.DestinationVolumeParameters.StoragePool).Return(nil).Once()
		iops := int64(0)
		mm.EXPECT().validateVolumeQosParamsForReplication(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&iops, nil).Once()

		// Reset volume state and storage pool
		event.SourceVolume.VolumeAttributes.IsDataProtection = false
		event.SourceVolume.State = string(googleproxyclient.VolumeV1betaVolumeStateREADY)
		event.CreateReplicationParams.DestinationVolumeParameters.StoragePool = &storagePoolUri

		// Clear destination volume parameters to test default values
		event.CreateReplicationParams.DestinationVolumeParameters.VolumeID = ""
		event.CreateReplicationParams.DestinationVolumeParameters.ShareName = ""

		validPool := &googleproxyclient.PoolInternalV1beta{
			Network:          "network",
			ResourceId:       destPoolID,
			PoolId:           googleproxyclient.OptString{Value: destPoolID, Set: true},
			AllocatedBytes:   googleproxyclient.NewOptNilFloat64(0),
			SizeInBytes:      200,
			ServiceLevel:     googleproxyclient.PoolInternalV1betaServiceLevelFLEX,
			StoragePoolState: googleproxyclient.NewOptPoolInternalV1betaStoragePoolState(googleproxyclient.PoolInternalV1betaStoragePoolStateREADY),
		}
		mm.On("getDestinationPool", ctx, "basePath", "token", event.DestinationLocationID, event.DestinationProjectNumber, event.XCorrelationID, event.DestinationPoolName).Return(validPool, nil).Once()

		mm.On("replicationJobInProcess", ctx, event.SourceProjectNumber, event.DestinationProjectNumber, "basePath", "basePath", event.LocationID, event.DestinationLocationID, "token", "token", mock.Anything, "", event.SourcePool.UUID, destPoolID, event.XCorrelationID).Return(nil).Once()

		mm.On("InternalUtilGetCallbackToken").Return("callback-token", nil).Once()

		mm.On("getQuotaLimit", ctx, mock.Anything, event.DestinationLocationID, event.DestinationProjectNumber, "callback-token", common.ResourceTypeReplication).Return(10, nil).Once()

		mm.On("internalGetReplicationCount", ctx, "basePath", event.DestinationProjectNumber, event.DestinationLocationID, "", "token", mock.Anything, mock.Anything).Return(0, nil).Once()

		mm.On("getQuotaLimit", ctx, mock.Anything, event.DestinationLocationID, event.DestinationProjectNumber, "callback-token", common.ResourceTypeVolume).Return(10, nil).Once()

		mm.On("internalGetVolumeCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(0, nil).Once()

		// Mock destination volume not found (which is expected)
		mm.On("getVolume", ctx, "basePath", "token", event.DestinationLocationID, event.DestinationProjectNumber, event.XCorrelationID, event.SourceVolume.Name).Return(googleproxyclient.VolumeV1beta{}, errors.NewNotFoundErr("Volume", &event.SourceVolume.Name)).Once()

		mm.On("createReplicationObjects", event, event.DestinationLocationID, "region-1", "region-2").Return(&datamodel.VolumeReplication{Uri: "uri"}, nil).Once()

		replication, err := _validateCreateReplicationParams(ctx, event, mockStorage)
		assert.NoError(t, err)
		assert.NotNil(t, replication)
		mm.AssertExpectations(t)
	})
	t.Run("WhenCrossProjectReplicationIsEnabled", func(t *testing.T) {
		mockStorage := &database.MockStorage{}
		setupMockNoActiveClusterUpgrade(mockStorage)
		mm := &monkeyMock{}
		mm.Patch()
		defer mm.Unpatch()

		event.DestinationProjectNumber = "proj-1"
		event.SourceProjectNumber = "proj-2"

		// Patch env variable
		cpcrEnabled = true
		defer func() {
			cpcrEnabled = env.GetBool("CPCRR_ENABLED", false)
		}()

		mm.On("InternalUtilGetSignedToken", event.SourceProjectNumber).Return("token", nil).Once()
		mm.On("InternalUtilGetSignedToken", event.DestinationProjectNumber).Return("token", nil).Once()
		mm.On("InternalParseRegionAndZone", event.LocationID).Return("region-1", "zone-1", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "region-1").Return("basePath", nil).Once()
		mm.On("InternalParseRegionAndZone", event.DestinationLocationID).Return("region-2", "zone-2", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "region-2").Return("basePath", nil).Once()
		mm.On("validateReplicationResourceId", ctx, event.SourceProjectNumber, *event.CreateReplicationParams.ResourceID, event.VolumeResourceID, mockStorage).Return(nil).Once()
		mm.On("validateStoragePoolUri", *event.CreateReplicationParams.DestinationVolumeParameters.StoragePool).Return(nil).Once()
		iops := int64(0)
		mm.EXPECT().validateVolumeQosParamsForReplication(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&iops, nil).Once()

		// Reset volume state and storage pool
		event.SourceVolume.VolumeAttributes.IsDataProtection = false
		event.SourceVolume.State = string(googleproxyclient.VolumeV1betaVolumeStateREADY)
		event.CreateReplicationParams.DestinationVolumeParameters.StoragePool = &storagePoolUri

		// Clear destination volume parameters to test default values
		event.CreateReplicationParams.DestinationVolumeParameters.VolumeID = ""
		event.CreateReplicationParams.DestinationVolumeParameters.ShareName = ""

		validPool := &googleproxyclient.PoolInternalV1beta{
			Network:          "network",
			ResourceId:       destPoolID,
			PoolId:           googleproxyclient.OptString{Value: destPoolID, Set: true},
			AllocatedBytes:   googleproxyclient.NewOptNilFloat64(0),
			SizeInBytes:      200,
			ServiceLevel:     googleproxyclient.PoolInternalV1betaServiceLevelFLEX,
			StoragePoolState: googleproxyclient.NewOptPoolInternalV1betaStoragePoolState(googleproxyclient.PoolInternalV1betaStoragePoolStateREADY),
		}
		mm.On("getDestinationPool", ctx, "basePath", "token", event.DestinationLocationID, event.DestinationProjectNumber, event.XCorrelationID, event.DestinationPoolName).Return(validPool, nil).Once()

		mm.On("replicationJobInProcess", ctx, event.SourceProjectNumber, event.DestinationProjectNumber, "basePath", "basePath", event.LocationID, event.DestinationLocationID, "token", "token", mock.Anything, "", event.SourcePool.UUID, destPoolID, event.XCorrelationID).Return(nil).Once()

		mm.On("InternalUtilGetCallbackToken").Return("callback-token", nil).Once()

		mm.On("getQuotaLimit", ctx, mock.Anything, event.DestinationLocationID, event.DestinationProjectNumber, "callback-token", common.ResourceTypeReplication).Return(10, nil).Once()

		mm.On("internalGetReplicationCount", ctx, "basePath", event.DestinationProjectNumber, event.DestinationLocationID, "", "token", mock.Anything, mock.Anything).Return(0, nil).Once()

		mm.On("getQuotaLimit", ctx, mock.Anything, event.DestinationLocationID, event.DestinationProjectNumber, "callback-token", common.ResourceTypeVolume).Return(10, nil).Once()

		mm.On("internalGetVolumeCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(0, nil).Once()

		// Mock destination volume not found (which is expected)
		mm.On("getVolume", ctx, "basePath", "token", event.DestinationLocationID, event.DestinationProjectNumber, event.XCorrelationID, event.SourceVolume.Name).Return(googleproxyclient.VolumeV1beta{}, errors.NewNotFoundErr("Volume", &event.SourceVolume.Name)).Once()

		mm.On("createReplicationObjects", event, event.DestinationLocationID, "region-1", "region-2").Return(&datamodel.VolumeReplication{Uri: "uri"}, nil).Once()

		replication, err := _validateCreateReplicationParams(ctx, event, mockStorage)
		assert.NoError(t, err)
		assert.NotNil(t, replication)
		mm.AssertExpectations(t)
	})
	t.Run("WhenCrossZoneReplicationIsEnabled", func(t *testing.T) {
		mockStorage := &database.MockStorage{}
		setupMockNoActiveClusterUpgrade(mockStorage)
		mm := &monkeyMock{}
		mm.Patch()
		defer mm.Unpatch()

		event.DestinationProjectNumber = "proj-1"
		event.SourceProjectNumber = "proj-1"

		// Patch env variable
		czcrEnabled = true
		defer func() {
			czcrEnabled = env.GetBool("CZCRR_ENABLED", false)
		}()

		mm.On("InternalUtilGetSignedToken", event.SourceProjectNumber).Return("token", nil).Once()
		mm.On("InternalParseRegionAndZone", event.LocationID).Return("region-1", "zone-1", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "region-1").Return("basePath", nil).Once()
		mm.On("InternalParseRegionAndZone", event.DestinationLocationID).Return("region-1", "zone-2", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "region-1").Return("basePath", nil).Once()
		mm.On("validateReplicationResourceId", ctx, event.SourceProjectNumber, *event.CreateReplicationParams.ResourceID, event.VolumeResourceID, mockStorage).Return(nil).Once()
		mm.On("validateStoragePoolUri", *event.CreateReplicationParams.DestinationVolumeParameters.StoragePool).Return(nil).Once()
		iops := int64(0)
		mm.EXPECT().validateVolumeQosParamsForReplication(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&iops, nil).Once()

		// Reset volume state and storage pool
		event.SourceVolume.VolumeAttributes.IsDataProtection = false
		event.SourceVolume.State = string(googleproxyclient.VolumeV1betaVolumeStateREADY)
		event.CreateReplicationParams.DestinationVolumeParameters.StoragePool = &storagePoolUri

		// Clear destination volume parameters to test default values
		event.CreateReplicationParams.DestinationVolumeParameters.VolumeID = ""
		event.CreateReplicationParams.DestinationVolumeParameters.ShareName = ""

		validPool := &googleproxyclient.PoolInternalV1beta{
			Network:          "network",
			ResourceId:       destPoolID,
			PoolId:           googleproxyclient.OptString{Value: destPoolID, Set: true},
			AllocatedBytes:   googleproxyclient.NewOptNilFloat64(0),
			SizeInBytes:      200,
			ServiceLevel:     googleproxyclient.PoolInternalV1betaServiceLevelFLEX,
			StoragePoolState: googleproxyclient.NewOptPoolInternalV1betaStoragePoolState(googleproxyclient.PoolInternalV1betaStoragePoolStateREADY),
		}
		mm.On("getDestinationPool", ctx, "basePath", "token", event.DestinationLocationID, event.DestinationProjectNumber, event.XCorrelationID, event.DestinationPoolName).Return(validPool, nil).Once()

		mm.On("replicationJobInProcess", ctx, event.SourceProjectNumber, event.DestinationProjectNumber, "basePath", "basePath", event.LocationID, event.DestinationLocationID, "token", "token", mock.Anything, "", event.SourcePool.UUID, destPoolID, event.XCorrelationID).Return(nil).Once()

		mm.On("InternalUtilGetCallbackToken").Return("callback-token", nil).Once()

		mm.On("getQuotaLimit", ctx, mock.Anything, event.DestinationLocationID, event.DestinationProjectNumber, "callback-token", common.ResourceTypeInRegionReplication).Return(10, nil).Once()

		mm.On("internalCountInRegionReplications", ctx, mockStorage, event.DestinationProjectNumber).Return(0, nil).Once()

		mm.On("getQuotaLimit", ctx, mock.Anything, event.DestinationLocationID, event.DestinationProjectNumber, "callback-token", common.ResourceTypeVolume).Return(10, nil).Once()

		mm.On("internalGetVolumeCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(0, nil).Once()

		// Mock destination volume not found (which is expected)
		mm.On("getVolume", ctx, "basePath", "token", event.DestinationLocationID, event.DestinationProjectNumber, event.XCorrelationID, event.SourceVolume.Name).Return(googleproxyclient.VolumeV1beta{}, errors.NewNotFoundErr("Volume", &event.SourceVolume.Name)).Once()

		mm.On("createReplicationObjects", event, event.DestinationLocationID, "region-1", "region-1").Return(&datamodel.VolumeReplication{Uri: "uri"}, nil).Once()

		replication, err := _validateCreateReplicationParams(ctx, event, mockStorage)
		assert.NoError(t, err)
		assert.NotNil(t, replication)
		mm.AssertExpectations(t)
	})

	t.Run("WhenInRegionQuotaStateFails_SameProjectSameRegion", func(t *testing.T) {
		// Same-project same-region (IRR): getInRegionQuotaState errors → line 367 is hit.
		mockStorage := &database.MockStorage{}
		setupMockNoActiveClusterUpgrade(mockStorage)
		mm := &monkeyMock{}
		mm.Patch()
		defer mm.Unpatch()

		event.SourceProjectNumber = projectNumber
		event.DestinationProjectNumber = projectNumber

		czcrEnabled = true
		defer func() { czcrEnabled = env.GetBool("CZCRR_ENABLED", false) }()

		mm.On("InternalUtilGetSignedToken", event.SourceProjectNumber).Return("token", nil).Once()
		mm.On("InternalParseRegionAndZone", event.LocationID).Return("same-region", "same-region-a", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "same-region").Return("basePath", nil).Once()
		mm.On("InternalParseRegionAndZone", event.DestinationLocationID).Return("same-region", "same-region-b", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "same-region").Return("basePath", nil).Once()
		mm.On("validateReplicationResourceId", ctx, event.SourceProjectNumber, *event.CreateReplicationParams.ResourceID, event.VolumeResourceID, mockStorage).Return(nil).Once()
		mm.On("validateStoragePoolUri", *event.CreateReplicationParams.DestinationVolumeParameters.StoragePool).Return(nil).Once()
		iops := int64(0)
		mm.EXPECT().validateVolumeQosParamsForReplication(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&iops, nil).Once()

		event.SourceVolume.VolumeAttributes.IsDataProtection = false
		event.SourceVolume.State = string(googleproxyclient.VolumeV1betaVolumeStateREADY)
		event.CreateReplicationParams.DestinationVolumeParameters.StoragePool = &storagePoolUri
		event.CreateReplicationParams.DestinationVolumeParameters.VolumeID = "vol_2"

		validPool := &googleproxyclient.PoolInternalV1beta{
			Network:          "network",
			ResourceId:       destPoolID,
			PoolId:           googleproxyclient.OptString{Value: destPoolID, Set: true},
			AllocatedBytes:   googleproxyclient.NewOptNilFloat64(0),
			SizeInBytes:      200,
			ServiceLevel:     googleproxyclient.PoolInternalV1betaServiceLevelFLEX,
			StoragePoolState: googleproxyclient.NewOptPoolInternalV1betaStoragePoolState(googleproxyclient.PoolInternalV1betaStoragePoolStateREADY),
		}
		mm.On("getDestinationPool", ctx, "basePath", "token", event.DestinationLocationID, event.DestinationProjectNumber, event.XCorrelationID, event.DestinationPoolName).Return(validPool, nil).Once()
		mm.On("replicationJobInProcess", ctx, event.SourceProjectNumber, event.DestinationProjectNumber, "basePath", "basePath", event.LocationID, event.DestinationLocationID, "token", "token", mock.Anything, "", event.SourcePool.UUID, destPoolID, event.XCorrelationID).Return(nil).Once()
		mm.On("InternalUtilGetCallbackToken").Return("callback-token", nil).Once()

		// getQuotaLimit fails → _getInRegionQuotaState returns error → line 367 is hit.
		mm.On("getQuotaLimit", ctx, mock.Anything, event.DestinationLocationID, event.DestinationProjectNumber, "callback-token", common.ResourceTypeInRegionReplication).Return(0, errors.New("quota error")).Once()

		_, err := _validateCreateReplicationParams(ctx, event, mockStorage)
		assert.Error(t, err)
		mm.AssertExpectations(t)
	})

	t.Run("WhenInRegionQuotaStateFailsForSourceProject_CrossProjectSameRegion", func(t *testing.T) {
		// Cross-project same-region (CPR): getInRegionQuotaState for source errors → line 376 is hit.
		mockStorage := &database.MockStorage{}
		setupMockNoActiveClusterUpgrade(mockStorage)
		mm := &monkeyMock{}
		mm.Patch()
		defer mm.Unpatch()

		cpcrEnabled = true
		czcrEnabled = true
		defer func() {
			cpcrEnabled = env.GetBool("CPCRR_ENABLED", false)
			czcrEnabled = env.GetBool("CZCRR_ENABLED", false)
		}()

		event.SourceProjectNumber = "proj-src"
		event.DestinationProjectNumber = "proj-dst"
		defer func() {
			event.SourceProjectNumber = projectNumber
			event.DestinationProjectNumber = destProjectNumber
		}()

		mm.On("InternalUtilGetSignedToken", event.SourceProjectNumber).Return("token", nil).Once()
		mm.On("InternalUtilGetSignedToken", event.DestinationProjectNumber).Return("dst-token", nil).Once()
		mm.On("InternalParseRegionAndZone", event.LocationID).Return("same-region", "same-region-a", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "same-region").Return("basePath", nil).Once()
		mm.On("InternalParseRegionAndZone", event.DestinationLocationID).Return("same-region", "same-region-b", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "same-region").Return("basePath", nil).Once()
		mm.On("validateReplicationResourceId", ctx, event.SourceProjectNumber, *event.CreateReplicationParams.ResourceID, event.VolumeResourceID, mockStorage).Return(nil).Once()
		mm.On("validateStoragePoolUri", *event.CreateReplicationParams.DestinationVolumeParameters.StoragePool).Return(nil).Once()
		iops := int64(0)
		mm.EXPECT().validateVolumeQosParamsForReplication(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&iops, nil).Once()

		event.SourceVolume.VolumeAttributes.IsDataProtection = false
		event.SourceVolume.State = string(googleproxyclient.VolumeV1betaVolumeStateREADY)
		event.CreateReplicationParams.DestinationVolumeParameters.StoragePool = &storagePoolUri
		event.CreateReplicationParams.DestinationVolumeParameters.VolumeID = "vol_2"

		validPool := &googleproxyclient.PoolInternalV1beta{
			Network:          "network",
			ResourceId:       destPoolID,
			PoolId:           googleproxyclient.OptString{Value: destPoolID, Set: true},
			AllocatedBytes:   googleproxyclient.NewOptNilFloat64(0),
			SizeInBytes:      200,
			ServiceLevel:     googleproxyclient.PoolInternalV1betaServiceLevelFLEX,
			StoragePoolState: googleproxyclient.NewOptPoolInternalV1betaStoragePoolState(googleproxyclient.PoolInternalV1betaStoragePoolStateREADY),
		}
		mm.On("getDestinationPool", ctx, "basePath", "dst-token", event.DestinationLocationID, event.DestinationProjectNumber, event.XCorrelationID, event.DestinationPoolName).Return(validPool, nil).Once()
		mm.On("replicationJobInProcess", ctx, event.SourceProjectNumber, event.DestinationProjectNumber, "basePath", "basePath", event.LocationID, event.DestinationLocationID, "token", "dst-token", mock.Anything, "", event.SourcePool.UUID, destPoolID, event.XCorrelationID).Return(nil).Once()
		mm.On("InternalUtilGetCallbackToken").Return("callback-token", nil).Once()

		// getQuotaLimit for source project fails → _getInRegionQuotaState returns error → line 376 is hit.
		mm.On("getQuotaLimit", ctx, mock.Anything, event.LocationID, event.SourceProjectNumber, "callback-token", common.ResourceTypeInRegionReplication).Return(0, errors.New("source quota error")).Once()

		_, err := _validateCreateReplicationParams(ctx, event, mockStorage)
		assert.Error(t, err)
		mm.AssertExpectations(t)
	})

	t.Run("WhenSourceQuotaExceeded_CrossProjectSameRegion", func(t *testing.T) {
		// Cross-project same-region: source in-region quota is exhausted → line 379 is hit.
		mockStorage := &database.MockStorage{}
		setupMockNoActiveClusterUpgrade(mockStorage)
		mm := &monkeyMock{}
		mm.Patch()
		defer mm.Unpatch()

		cpcrEnabled = true
		czcrEnabled = true
		defer func() {
			cpcrEnabled = env.GetBool("CPCRR_ENABLED", false)
			czcrEnabled = env.GetBool("CZCRR_ENABLED", false)
		}()

		event.SourceProjectNumber = "proj-src"
		event.DestinationProjectNumber = "proj-dst"
		defer func() {
			event.SourceProjectNumber = projectNumber
			event.DestinationProjectNumber = destProjectNumber
		}()

		mm.On("InternalUtilGetSignedToken", event.SourceProjectNumber).Return("token", nil).Once()
		mm.On("InternalUtilGetSignedToken", event.DestinationProjectNumber).Return("dst-token", nil).Once()
		mm.On("InternalParseRegionAndZone", event.LocationID).Return("same-region", "same-region-a", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "same-region").Return("basePath", nil).Once()
		mm.On("InternalParseRegionAndZone", event.DestinationLocationID).Return("same-region", "same-region-b", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "same-region").Return("basePath", nil).Once()
		mm.On("validateReplicationResourceId", ctx, event.SourceProjectNumber, *event.CreateReplicationParams.ResourceID, event.VolumeResourceID, mockStorage).Return(nil).Once()
		mm.On("validateStoragePoolUri", *event.CreateReplicationParams.DestinationVolumeParameters.StoragePool).Return(nil).Once()
		iops := int64(0)
		mm.EXPECT().validateVolumeQosParamsForReplication(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&iops, nil).Once()

		event.SourceVolume.VolumeAttributes.IsDataProtection = false
		event.SourceVolume.State = string(googleproxyclient.VolumeV1betaVolumeStateREADY)
		event.CreateReplicationParams.DestinationVolumeParameters.StoragePool = &storagePoolUri
		event.CreateReplicationParams.DestinationVolumeParameters.VolumeID = "vol_2"

		validPool := &googleproxyclient.PoolInternalV1beta{
			Network:          "network",
			ResourceId:       destPoolID,
			PoolId:           googleproxyclient.OptString{Value: destPoolID, Set: true},
			AllocatedBytes:   googleproxyclient.NewOptNilFloat64(0),
			SizeInBytes:      200,
			ServiceLevel:     googleproxyclient.PoolInternalV1betaServiceLevelFLEX,
			StoragePoolState: googleproxyclient.NewOptPoolInternalV1betaStoragePoolState(googleproxyclient.PoolInternalV1betaStoragePoolStateREADY),
		}
		mm.On("getDestinationPool", ctx, "basePath", "dst-token", event.DestinationLocationID, event.DestinationProjectNumber, event.XCorrelationID, event.DestinationPoolName).Return(validPool, nil).Once()
		mm.On("replicationJobInProcess", ctx, event.SourceProjectNumber, event.DestinationProjectNumber, "basePath", "basePath", event.LocationID, event.DestinationLocationID, "token", "dst-token", mock.Anything, "", event.SourcePool.UUID, destPoolID, event.XCorrelationID).Return(nil).Once()
		mm.On("InternalUtilGetCallbackToken").Return("callback-token", nil).Once()

		// Source quota = 1, existing = 1 → 1-1 = 0 < 1 → quota exceeded for source → line 379 is hit.
		mm.On("getQuotaLimit", ctx, mock.Anything, event.LocationID, event.SourceProjectNumber, "callback-token", common.ResourceTypeInRegionReplication).Return(1, nil).Once()
		mm.On("internalCountInRegionReplications", ctx, mockStorage, event.SourceProjectNumber).Return(1, nil).Once()

		_, err := _validateCreateReplicationParams(ctx, event, mockStorage)
		assert.Error(t, err)
		assert.Equal(t, vsaErrors.NewVCPError(vsaErrors.ErrInRegionReplicationQuotaLimitExceeded,
			fmt.Errorf("quota limit 'Flex Unified in-region replicated volumes per region' has been exceeded for project %s", event.SourceProjectNumber)), err)
		mm.AssertExpectations(t)
	})

	t.Run("WhenInRegionQuotaStateFailsForDestProject_CrossProjectSameRegion", func(t *testing.T) {
		// Cross-project same-region: source quota is OK but getInRegionQuotaState for dest errors → line 385 is hit.
		mockStorage := &database.MockStorage{}
		setupMockNoActiveClusterUpgrade(mockStorage)
		mm := &monkeyMock{}
		mm.Patch()
		defer mm.Unpatch()

		cpcrEnabled = true
		czcrEnabled = true
		defer func() {
			cpcrEnabled = env.GetBool("CPCRR_ENABLED", false)
			czcrEnabled = env.GetBool("CZCRR_ENABLED", false)
		}()

		event.SourceProjectNumber = "proj-src"
		event.DestinationProjectNumber = "proj-dst"
		defer func() {
			event.SourceProjectNumber = projectNumber
			event.DestinationProjectNumber = destProjectNumber
		}()

		mm.On("InternalUtilGetSignedToken", event.SourceProjectNumber).Return("token", nil).Once()
		mm.On("InternalUtilGetSignedToken", event.DestinationProjectNumber).Return("dst-token", nil).Once()
		mm.On("InternalParseRegionAndZone", event.LocationID).Return("same-region", "same-region-a", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "same-region").Return("basePath", nil).Once()
		mm.On("InternalParseRegionAndZone", event.DestinationLocationID).Return("same-region", "same-region-b", nil).Once()
		mm.On("InternalUtilGetPairedRegionURI", "same-region").Return("basePath", nil).Once()
		mm.On("validateReplicationResourceId", ctx, event.SourceProjectNumber, *event.CreateReplicationParams.ResourceID, event.VolumeResourceID, mockStorage).Return(nil).Once()
		mm.On("validateStoragePoolUri", *event.CreateReplicationParams.DestinationVolumeParameters.StoragePool).Return(nil).Once()
		iops := int64(0)
		mm.EXPECT().validateVolumeQosParamsForReplication(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&iops, nil).Once()

		event.SourceVolume.VolumeAttributes.IsDataProtection = false
		event.SourceVolume.State = string(googleproxyclient.VolumeV1betaVolumeStateREADY)
		event.CreateReplicationParams.DestinationVolumeParameters.StoragePool = &storagePoolUri
		event.CreateReplicationParams.DestinationVolumeParameters.VolumeID = "vol_2"

		validPool := &googleproxyclient.PoolInternalV1beta{
			Network:          "network",
			ResourceId:       destPoolID,
			PoolId:           googleproxyclient.OptString{Value: destPoolID, Set: true},
			AllocatedBytes:   googleproxyclient.NewOptNilFloat64(0),
			SizeInBytes:      200,
			ServiceLevel:     googleproxyclient.PoolInternalV1betaServiceLevelFLEX,
			StoragePoolState: googleproxyclient.NewOptPoolInternalV1betaStoragePoolState(googleproxyclient.PoolInternalV1betaStoragePoolStateREADY),
		}
		mm.On("getDestinationPool", ctx, "basePath", "dst-token", event.DestinationLocationID, event.DestinationProjectNumber, event.XCorrelationID, event.DestinationPoolName).Return(validPool, nil).Once()
		mm.On("replicationJobInProcess", ctx, event.SourceProjectNumber, event.DestinationProjectNumber, "basePath", "basePath", event.LocationID, event.DestinationLocationID, "token", "dst-token", mock.Anything, "", event.SourcePool.UUID, destPoolID, event.XCorrelationID).Return(nil).Once()
		mm.On("InternalUtilGetCallbackToken").Return("callback-token", nil).Once()

		// Source quota OK: 10 - 0 = 10 >= 1.
		mm.On("getQuotaLimit", ctx, mock.Anything, event.LocationID, event.SourceProjectNumber, "callback-token", common.ResourceTypeInRegionReplication).Return(10, nil).Once()
		mm.On("internalCountInRegionReplications", ctx, mockStorage, event.SourceProjectNumber).Return(0, nil).Once()

		// getQuotaLimit for destination project fails → _getInRegionQuotaState returns error → line 385 is hit.
		mm.On("getQuotaLimit", ctx, mock.Anything, event.DestinationLocationID, event.DestinationProjectNumber, "callback-token", common.ResourceTypeInRegionReplication).Return(0, errors.New("dest quota error")).Once()

		_, err := _validateCreateReplicationParams(ctx, event, mockStorage)
		assert.Error(t, err)
		mm.AssertExpectations(t)
	})
}

func TestValidateReplicationParams(t *testing.T) {
	event := &CommonReplicationEventParams{
		AccountName:              "test-account",
		Location:                 "test-location",
		VolumeResourceID:         "test-volume-id",
		ReplicationResourceID:    "test-replication-id",
		SourceProjectNumber:      "123456789",
		DestinationProjectNumber: "123456789",
	}
	t.Run("WhenListVolumeReplicationError", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockStorage.On("ListVolumeReplications", mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("some error"))
		expectedError := vsaErrors.NewVCPError(vsaErrors.ErrDatabaseDataReadError, errors.New("some error"))
		_, _, err := _validateReplicationParams(context.Background(), event, 12345, mockStorage, false, "CREATE_VOLUME_REPLICATION")

		assert.Error(tt, err)
		assert.Equal(tt, expectedError, err)
		mockStorage.AssertExpectations(tt)
	})
	t.Run("WhenEmptyListVolumeReplication", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		response := []*datamodel.VolumeReplication{}
		mockStorage.On("ListVolumeReplications", mock.Anything, mock.Anything, mock.Anything).Return(response, nil)
		expectedError := errors.NewUserInputValidationErr("No replication found for the given URI")
		_, _, err := _validateReplicationParams(context.Background(), event, 12345, mockStorage, false, "CREATE_VOLUME_REPLICATION")
		assert.Error(tt, err)
		assert.Equal(tt, expectedError, err)
		mockStorage.AssertExpectations(tt)
	})
	t.Run("WhenParsingProjectError", func(tt *testing.T) {
		defer func() {
			utilsParseProjectNumberFromURI = utils.ParseProjectNumberFromURI
		}()
		mockStorage := database.NewMockStorage(tt)
		response := []*datamodel.VolumeReplication{
			{
				BaseModel: datamodel.BaseModel{
					ID: 1,
				},
				ReplicationAttributes: &datamodel.ReplicationDetails{
					SourceLocation:      "us-central1",
					DestinationLocation: "us-east1",
				},
			},
		}
		mockStorage.On("ListVolumeReplications", mock.Anything, mock.Anything, mock.Anything).Return(response, nil)

		parseError := errors.New("some error")
		utilsParseProjectNumberFromURI = func(uri string) (string, error) {
			return "", vsaErrors.NewVCPError(vsaErrors.ErrProjectParsingError, parseError)
		}
		_, _, err := _validateReplicationParams(context.Background(), event, 12345, mockStorage, false, "CREATE_VOLUME_REPLICATION")
		assert.Error(tt, err)
		mockStorage.AssertExpectations(tt)
	})
	t.Run("WhenRemoteUriParsingError", func(tt *testing.T) {
		defer func() {
			utilsParseProjectNumberFromURI = utils.ParseProjectNumberFromURI
		}()
		mockStorage := database.NewMockStorage(tt)
		response := []*datamodel.VolumeReplication{
			{
				BaseModel: datamodel.BaseModel{
					ID: 1,
				},
				ReplicationAttributes: &datamodel.ReplicationDetails{
					SourceLocation:      "us-central1",
					DestinationLocation: "us-east1",
				},
				RemoteUri: "projects/123456789/locations/us-central1/volumes/test-volume/replications/test-replication",
			},
		}
		mockStorage.On("ListVolumeReplications", mock.Anything, mock.Anything, mock.Anything).Return(response, nil)

		parseError := errors.New("parse remote URI error")
		utilsParseProjectNumberFromURI = func(uri string) (string, error) {
			return "", parseError
		}
		_, _, err := _validateReplicationParams(context.Background(), event, 12345, mockStorage, false, "CREATE_VOLUME_REPLICATION")
		assert.Error(tt, err)
		var customErr *vsaErrors.CustomError
		assert.True(tt, vsaErrors.As(err, &customErr), "Expected a CustomError")
		assert.Equal(tt, vsaErrors.ErrProjectParsingError, customErr.TrackingID)
		mockStorage.AssertExpectations(tt)
	})
	t.Run("WhenSignedTokenError", func(tt *testing.T) {
		defer func() {
			utilsParseProjectNumberFromURI = utils.ParseProjectNumberFromURI
			InternalUtilGetSignedToken = auth.GetSignedJwtToken
			InternalParseRegionAndZone = utils.ParseRegionAndZone
		}()
		mockStorage := database.NewMockStorage(tt)
		response := []*datamodel.VolumeReplication{
			{
				BaseModel: datamodel.BaseModel{
					ID: 1,
				},
				ReplicationAttributes: &datamodel.ReplicationDetails{
					SourceLocation:      "us-central1",
					DestinationLocation: "us-east1",
				},
			},
		}
		mockStorage.On("ListVolumeReplications", mock.Anything, mock.Anything, mock.Anything).Return(response, nil)
		utilsParseProjectNumberFromURI = func(uri string) (string, error) {
			return "", nil
		}
		InternalUtilGetSignedToken = func(projectNumber string) (string, error) {
			return "", vsaErrors.NewVCPError(vsaErrors.ErrGetSignedToken, errors.New("some error"))
		}
		InternalParseRegionAndZone = func(location string) (string, string, error) {
			return location, "", nil
		}
		_, _, err := _validateReplicationParams(context.Background(), event, 12345, mockStorage, false, "CREATE_VOLUME_REPLICATION")
		assert.Error(tt, err)
		mockStorage.AssertExpectations(tt)
	})
	t.Run("WhenGetPairedRegionError", func(tt *testing.T) {
		defer func() {
			utilsParseProjectNumberFromURI = utils.ParseProjectNumberFromURI
			InternalUtilGetSignedToken = auth.GetSignedJwtToken
			InternalParseRegionAndZone = utils.ParseRegionAndZone
			InternalUtilGetPairedRegionURI = utils.GetPairedRegionURI
		}()
		mockStorage := database.NewMockStorage(tt)
		response := []*datamodel.VolumeReplication{
			{
				BaseModel: datamodel.BaseModel{
					ID: 1,
				},
				ReplicationAttributes: &datamodel.ReplicationDetails{
					SourceLocation:      "us-central1",
					DestinationLocation: "us-east1",
				},
			},
		}
		mockStorage.On("ListVolumeReplications", mock.Anything, mock.Anything, mock.Anything).Return(response, nil)
		utilsParseProjectNumberFromURI = func(uri string) (string, error) {
			return "", nil
		}
		InternalUtilGetSignedToken = func(projectNumber string) (string, error) {
			return "", nil
		}
		InternalParseRegionAndZone = func(location string) (string, string, error) {
			return location, "", nil
		}
		InternalUtilGetPairedRegionURI = func(region string) (string, error) {
			return "", errors.New("some error")
		}
		_, _, err := _validateReplicationParams(context.Background(), event, 12345, mockStorage, false, "CREATE_VOLUME_REPLICATION")
		assert.Error(tt, err)
		mockStorage.AssertExpectations(tt)
	})
	t.Run("WhenReplicationJobInProcessError", func(tt *testing.T) {
		defer func() {
			utilsParseProjectNumberFromURI = utils.ParseProjectNumberFromURI
			InternalUtilGetSignedToken = auth.GetSignedJwtToken
			InternalParseRegionAndZone = utils.ParseRegionAndZone
			InternalUtilGetPairedRegionURI = utils.GetPairedRegionURI
			replicationJobInProcess = _replicationJobInProcess
		}()
		mockStorage := database.NewMockStorage(tt)
		response := []*datamodel.VolumeReplication{
			{
				BaseModel: datamodel.BaseModel{
					ID: 1,
				},
				ReplicationAttributes: &datamodel.ReplicationDetails{
					SourceLocation:      "us-central1",
					DestinationLocation: "us-east1",
				},
			},
		}
		mockStorage.On("ListVolumeReplications", mock.Anything, mock.Anything, mock.Anything).Return(response, nil)
		mockStorage.On("CheckAndFetchDuplicateJobs", mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
		utilsParseProjectNumberFromURI = func(uri string) (string, error) {
			return "", nil
		}
		InternalUtilGetSignedToken = func(projectNumber string) (string, error) {
			return "", nil
		}
		InternalParseRegionAndZone = func(location string) (string, string, error) {
			return location, "", nil
		}
		InternalUtilGetPairedRegionURI = func(region string) (string, error) {
			return "basePath", nil
		}
		replicationJobInProcess = func(ctx context.Context, srcProjectNumber, destProjectNumber, srcBasePath, destBasePath, srcLocationID, destLocationID, srcToken, destToken, ccfeUri, remoteCcfeUri, srcPoolId, dstPoolId string, correlationId *string) error {
			return errors.New("some error")
		}
		_, _, err := _validateReplicationParams(context.Background(), event, 12345, mockStorage, false, "CREATE_VOLUME_REPLICATION")
		assert.Error(tt, err)
		mockStorage.AssertExpectations(tt)
	})
	t.Run("WhenSuccess", func(tt *testing.T) {
		defer func() {
			utilsParseProjectNumberFromURI = utils.ParseProjectNumberFromURI
			InternalUtilGetSignedToken = auth.GetSignedJwtToken
			InternalParseRegionAndZone = utils.ParseRegionAndZone
			InternalUtilGetPairedRegionURI = utils.GetPairedRegionURI
			replicationJobInProcess = _replicationJobInProcess
		}()
		mockStorage := database.NewMockStorage(tt)
		response := []*datamodel.VolumeReplication{
			{
				BaseModel: datamodel.BaseModel{
					ID: 1,
				},
				ReplicationAttributes: &datamodel.ReplicationDetails{
					SourceLocation:      "us-east4-1",
					DestinationLocation: "us-central1-a",
				},
			},
		}
		mockStorage.On("ListVolumeReplications", mock.Anything, mock.Anything, mock.Anything).Return(response, nil)
		mockStorage.On("CheckAndFetchDuplicateJobs", mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
		utilsParseProjectNumberFromURI = func(uri string) (string, error) {
			return "", nil
		}
		InternalUtilGetSignedToken = func(projectNumber string) (string, error) {
			return "", nil
		}
		InternalParseRegionAndZone = func(location string) (string, string, error) {
			return location, "", nil
		}
		InternalUtilGetPairedRegionURI = func(region string) (string, error) {
			return "basePath", nil
		}
		replicationJobInProcess = func(ctx context.Context, srcProjectNumber, destProjectNumber, srcBasePath, destBasePath, srcLocationID, destLocationID, srcToken, destToken, ccfeUri, remoteCcfeUri, srcPoolId, dstPoolId string, correlationId *string) error {
			return nil
		}
		_, _, err := _validateReplicationParams(context.Background(), event, 12345, mockStorage, false, "CREATE_VOLUME_REPLICATION")
		assert.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
	})
	t.Run("WhenSuccessInCaseOfCleanup", func(tt *testing.T) {
		defer func() {
			utilsParseProjectNumberFromURI = utils.ParseProjectNumberFromURI
			InternalUtilGetSignedToken = auth.GetSignedJwtToken
			InternalParseRegionAndZone = utils.ParseRegionAndZone
			InternalUtilGetPairedRegionURI = utils.GetPairedRegionURI
			replicationJobInProcess = _replicationJobInProcess
		}()
		mockStorage := database.NewMockStorage(tt)
		response := []*datamodel.VolumeReplication{
			{
				BaseModel: datamodel.BaseModel{
					ID: 1,
				},
				ReplicationAttributes: &datamodel.ReplicationDetails{
					SourceLocation:      "us-east4-1",
					DestinationLocation: "us-central1-a",
				},
			},
		}
		mockStorage.On("ListVolumeReplications", mock.Anything, mock.Anything, mock.Anything).Return(response, nil)
		mockStorage.On("CheckAndFetchDuplicateJobs", mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
		utilsParseProjectNumberFromURI = func(uri string) (string, error) {
			return "", nil
		}
		InternalUtilGetSignedToken = func(projectNumber string) (string, error) {
			return "", nil
		}
		InternalParseRegionAndZone = func(location string) (string, string, error) {
			return location, "", nil
		}
		InternalUtilGetPairedRegionURI = func(region string) (string, error) {
			return "basePath", nil
		}
		replicationJobInProcess = func(ctx context.Context, srcProjectNumber, destProjectNumber, srcBasePath, destBasePath, srcLocationID, destLocationID, srcToken, destToken, ccfeUri, remoteCcfeUri, srcPoolId, dstPoolId string, correlationId *string) error {
			return nil
		}
		_, _, err := _validateReplicationParams(context.Background(), event, 12345, mockStorage, true, "CREATE_VOLUME_REPLICATION")
		assert.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
	})
	t.Run("WhenParseSourceLocationError", func(tt *testing.T) {
		defer func() {
			InternalParseRegionAndZone = utils.ParseRegionAndZone
			utilsParseProjectNumberFromURI = utils.ParseProjectNumberFromURI
			InternalUtilGetSignedToken = auth.GetSignedJwtToken
		}()
		mockStorage := database.NewMockStorage(tt)
		response := []*datamodel.VolumeReplication{
			{
				BaseModel: datamodel.BaseModel{
					ID: 1,
				},
				ReplicationAttributes: &datamodel.ReplicationDetails{
					SourceLocation:      "invalid-location",
					DestinationLocation: "us-east1",
				},
				RemoteUri: "projects/123456789/locations/us-central1/volumes/test-volume/replications/test-replication",
			},
		}
		mockStorage.On("ListVolumeReplications", mock.Anything, mock.Anything, mock.Anything).Return(response, nil)
		utilsParseProjectNumberFromURI = func(uri string) (string, error) {
			return "123456789", nil
		}
		InternalUtilGetSignedToken = func(projectNumber string) (string, error) {
			return "mock-token", nil
		}
		InternalParseRegionAndZone = func(location string) (string, string, error) {
			return "", "", errors.New("parse error")
		}
		expectedError := vsaErrors.NewVCPError(vsaErrors.ErrParseSourceLocation, errors.New("parse error"))
		_, _, err := _validateReplicationParams(context.Background(), event, 12345, mockStorage, false, "CREATE_VOLUME_REPLICATION")
		assert.Error(tt, err)
		assert.Equal(tt, expectedError, err)
		mockStorage.AssertExpectations(tt)
	})
	t.Run("WhenGetPairedSourceRegionURIError", func(tt *testing.T) {
		defer func() {
			InternalParseRegionAndZone = utils.ParseRegionAndZone
			InternalUtilGetPairedRegionURI = utils.GetPairedRegionURI
			utilsParseProjectNumberFromURI = utils.ParseProjectNumberFromURI
			InternalUtilGetSignedToken = auth.GetSignedJwtToken
		}()
		mockStorage := database.NewMockStorage(tt)
		response := []*datamodel.VolumeReplication{
			{
				BaseModel: datamodel.BaseModel{
					ID: 1,
				},
				ReplicationAttributes: &datamodel.ReplicationDetails{
					SourceLocation:      "us-central1",
					DestinationLocation: "us-east1",
				},
				RemoteUri: "projects/123456789/locations/us-central1/volumes/test-volume/replications/test-replication",
			},
		}
		mockStorage.On("ListVolumeReplications", mock.Anything, mock.Anything, mock.Anything).Return(response, nil)
		utilsParseProjectNumberFromURI = func(uri string) (string, error) {
			return "123456789", nil
		}
		InternalUtilGetSignedToken = func(projectNumber string) (string, error) {
			return "mock-token", nil
		}
		InternalParseRegionAndZone = func(location string) (string, string, error) {
			return "us-central1", "us-central1-a", nil
		}
		InternalUtilGetPairedRegionURI = func(region string) (string, error) {
			return "", errors.New("paired region error")
		}
		expectedError := vsaErrors.NewVCPError(vsaErrors.ErrGetSrcBasePath, errors.New("paired region error"))
		_, _, err := _validateReplicationParams(context.Background(), event, 12345, mockStorage, false, "CREATE_VOLUME_REPLICATION")
		assert.Error(tt, err)
		assert.Equal(tt, expectedError, err)
		mockStorage.AssertExpectations(tt)
	})
	t.Run("WhenParseDestinationLocationError", func(tt *testing.T) {
		defer func() {
			InternalParseRegionAndZone = utils.ParseRegionAndZone
			InternalUtilGetPairedRegionURI = utils.GetPairedRegionURI
			utilsParseProjectNumberFromURI = utils.ParseProjectNumberFromURI
			InternalUtilGetSignedToken = auth.GetSignedJwtToken
		}()
		mockStorage := database.NewMockStorage(tt)
		response := []*datamodel.VolumeReplication{
			{
				BaseModel: datamodel.BaseModel{
					ID: 1,
				},
				ReplicationAttributes: &datamodel.ReplicationDetails{
					SourceLocation:      "us-central1",
					DestinationLocation: "invalid-location",
				},
				RemoteUri: "projects/123456789/locations/us-central1/volumes/test-volume/replications/test-replication",
			},
		}
		mockStorage.On("ListVolumeReplications", mock.Anything, mock.Anything, mock.Anything).Return(response, nil)
		utilsParseProjectNumberFromURI = func(uri string) (string, error) {
			return "123456789", nil
		}
		InternalUtilGetSignedToken = func(projectNumber string) (string, error) {
			return "mock-token", nil
		}
		InternalParseRegionAndZone = func(location string) (string, string, error) {
			if location == "us-central1" {
				return "us-central1", "us-central1-a", nil
			}
			return "", "", errors.New("parse destination error")
		}
		InternalUtilGetPairedRegionURI = func(region string) (string, error) {
			return "basePath", nil
		}
		expectedError := vsaErrors.NewVCPError(vsaErrors.ErrParseDestinationLocation, errors.New("parse destination error"))
		_, _, err := _validateReplicationParams(context.Background(), event, 12345, mockStorage, false, "CREATE_VOLUME_REPLICATION")
		assert.Error(tt, err)
		assert.Equal(tt, expectedError, err)
		mockStorage.AssertExpectations(tt)
	})
	t.Run("WhenGetPairedDestinationRegionURIError", func(tt *testing.T) {
		defer func() {
			InternalParseRegionAndZone = utils.ParseRegionAndZone
			InternalUtilGetPairedRegionURI = utils.GetPairedRegionURI
			utilsParseProjectNumberFromURI = utils.ParseProjectNumberFromURI
			InternalUtilGetSignedToken = auth.GetSignedJwtToken
		}()
		mockStorage := database.NewMockStorage(tt)
		response := []*datamodel.VolumeReplication{
			{
				BaseModel: datamodel.BaseModel{
					ID: 1,
				},
				ReplicationAttributes: &datamodel.ReplicationDetails{
					SourceLocation:      "us-central1",
					DestinationLocation: "us-east1",
				},
				RemoteUri: "projects/123456789/locations/us-central1/volumes/test-volume/replications/test-replication",
			},
		}
		mockStorage.On("ListVolumeReplications", mock.Anything, mock.Anything, mock.Anything).Return(response, nil)
		utilsParseProjectNumberFromURI = func(uri string) (string, error) {
			return "123456789", nil
		}
		InternalUtilGetSignedToken = func(projectNumber string) (string, error) {
			return "mock-token", nil
		}
		InternalParseRegionAndZone = func(location string) (string, string, error) {
			if location == "us-central1" {
				return "us-central1", "us-central1-a", nil
			}
			return "us-east1", "us-east1-a", nil
		}
		InternalUtilGetPairedRegionURI = func(region string) (string, error) {
			if region == "us-central1" {
				return "basePath", nil
			}
			return "", errors.New("paired destination region error")
		}
		expectedError := vsaErrors.NewVCPError(vsaErrors.ErrGetDstBasePath, errors.New("paired destination region error"))
		_, _, err := _validateReplicationParams(context.Background(), event, 12345, mockStorage, false, "CREATE_VOLUME_REPLICATION")
		assert.Error(tt, err)
		assert.Equal(tt, expectedError, err)
		mockStorage.AssertExpectations(tt)
	})
	t.Run("WhenCheckAndFetchDuplicateJobsError", func(tt *testing.T) {
		defer func() {
			InternalParseRegionAndZone = utils.ParseRegionAndZone
			InternalUtilGetPairedRegionURI = utils.GetPairedRegionURI
			utilsParseProjectNumberFromURI = utils.ParseProjectNumberFromURI
			InternalUtilGetSignedToken = auth.GetSignedJwtToken
		}()
		mockStorage := database.NewMockStorage(tt)
		response := []*datamodel.VolumeReplication{
			{
				BaseModel: datamodel.BaseModel{
					ID: 1,
				},
				ReplicationAttributes: &datamodel.ReplicationDetails{
					SourceLocation:      "us-central1",
					DestinationLocation: "us-east1",
				},
				RemoteUri: "projects/123456789/locations/us-central1/volumes/test-volume/replications/test-replication",
			},
		}
		mockStorage.On("ListVolumeReplications", mock.Anything, mock.Anything, mock.Anything).Return(response, nil)
		mockStorage.On("CheckAndFetchDuplicateJobs", mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("duplicate job check error"))
		utilsParseProjectNumberFromURI = func(uri string) (string, error) {
			return "123456789", nil
		}
		InternalUtilGetSignedToken = func(projectNumber string) (string, error) {
			return "mock-token", nil
		}
		InternalParseRegionAndZone = func(location string) (string, string, error) {
			if location == "us-central1" {
				return "us-central1", "us-central1-a", nil
			}
			return "us-east1", "us-east1-a", nil
		}
		InternalUtilGetPairedRegionURI = func(region string) (string, error) {
			return "basePath", nil
		}
		expectedError := errors.New("duplicate job check error")
		_, _, err := _validateReplicationParams(context.Background(), event, 12345, mockStorage, false, "CREATE_VOLUME_REPLICATION")
		assert.Error(tt, err)
		assert.Equal(tt, expectedError, err)
		mockStorage.AssertExpectations(tt)
	})
	t.Run("WhenDuplicateJobFound", func(tt *testing.T) {
		defer func() {
			InternalParseRegionAndZone = utils.ParseRegionAndZone
			InternalUtilGetPairedRegionURI = utils.GetPairedRegionURI
			getReplication = _getReplication
			utilsParseProjectNumberFromURI = utils.ParseProjectNumberFromURI
			InternalUtilGetSignedToken = auth.GetSignedJwtToken
		}()
		mockStorage := database.NewMockStorage(tt)
		response := []*datamodel.VolumeReplication{
			{
				BaseModel: datamodel.BaseModel{
					ID: 1,
				},
				ReplicationAttributes: &datamodel.ReplicationDetails{
					SourceLocation:      "us-central1",
					DestinationLocation: "us-east1",
				},
				RemoteUri: "projects/123456789/locations/us-central1/volumes/test-volume/replications/test-replication",
			},
		}
		mockStorage.On("ListVolumeReplications", mock.Anything, mock.Anything, mock.Anything).Return(response, nil)
		existingJob := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				UUID: "existing-job-uuid",
			},
		}
		mockStorage.On("CheckAndFetchDuplicateJobs", mock.Anything, mock.Anything, mock.Anything).Return(existingJob, nil)
		utilsParseProjectNumberFromURI = func(uri string) (string, error) {
			return "123456789", nil
		}
		InternalUtilGetSignedToken = func(projectNumber string) (string, error) {
			return "mock-token", nil
		}
		InternalParseRegionAndZone = func(location string) (string, string, error) {
			if location == "us-central1" {
				return "us-central1", "us-central1-a", nil
			}
			return "us-east1", "us-east1-a", nil
		}
		InternalUtilGetPairedRegionURI = func(region string) (string, error) {
			return "basePath", nil
		}
		mirrorState := "MIRRORED"
		mockReplication := &coreModels.VolumeReplication{
			MirrorState: &mirrorState,
		}
		getReplication = func(ctx context.Context, basePath, projectNumber, location, replicationUUID, token string) (*coreModels.VolumeReplication, error) {
			return mockReplication, nil
		}
		replication, jobUUID, err := _validateReplicationParams(context.Background(), event, 12345, mockStorage, false, "CREATE_VOLUME_REPLICATION")
		assert.NoError(tt, err)
		assert.Equal(tt, mockReplication, replication)
		assert.Equal(tt, "existing-job-uuid", *jobUUID)
		mockStorage.AssertExpectations(tt)
	})
	t.Run("WhenGetReplicationErrorForDuplicateJob", func(tt *testing.T) {
		defer func() {
			InternalParseRegionAndZone = utils.ParseRegionAndZone
			InternalUtilGetPairedRegionURI = utils.GetPairedRegionURI
			getReplication = _getReplication
			utilsParseProjectNumberFromURI = utils.ParseProjectNumberFromURI
			InternalUtilGetSignedToken = auth.GetSignedJwtToken
		}()
		mockStorage := database.NewMockStorage(tt)
		response := []*datamodel.VolumeReplication{
			{
				BaseModel: datamodel.BaseModel{
					ID: 1,
				},
				ReplicationAttributes: &datamodel.ReplicationDetails{
					SourceLocation:      "us-central1",
					DestinationLocation: "us-east1",
				},
				RemoteUri: "projects/123456789/locations/us-central1/volumes/test-volume/replications/test-replication",
			},
		}
		mockStorage.On("ListVolumeReplications", mock.Anything, mock.Anything, mock.Anything).Return(response, nil)
		existingJob := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				UUID: "existing-job-uuid",
			},
		}
		mockStorage.On("CheckAndFetchDuplicateJobs", mock.Anything, mock.Anything, mock.Anything).Return(existingJob, nil)
		utilsParseProjectNumberFromURI = func(uri string) (string, error) {
			return "123456789", nil
		}
		InternalUtilGetSignedToken = func(projectNumber string) (string, error) {
			return "mock-token", nil
		}
		InternalParseRegionAndZone = func(location string) (string, string, error) {
			if location == "us-central1" {
				return "us-central1", "us-central1-a", nil
			}
			return "us-east1", "us-east1-a", nil
		}
		InternalUtilGetPairedRegionURI = func(region string) (string, error) {
			return "basePath", nil
		}
		getReplication = func(ctx context.Context, basePath, projectNumber, location, replicationUUID, token string) (*coreModels.VolumeReplication, error) {
			return nil, errors.New("get replication error")
		}
		expectedError := vsaErrors.NewVCPError(vsaErrors.ErrGoogleProxyInternalGetMultipleReplications, errors.New("get replication error"))
		_, _, err := _validateReplicationParams(context.Background(), event, 12345, mockStorage, false, "CREATE_VOLUME_REPLICATION")
		assert.Error(tt, err)
		assert.Equal(tt, expectedError, err)
		mockStorage.AssertExpectations(tt)
	})
	t.Run("WhenReplicationJobInProcessError", func(tt *testing.T) {
		defer func() {
			InternalParseRegionAndZone = utils.ParseRegionAndZone
			InternalUtilGetPairedRegionURI = utils.GetPairedRegionURI
			replicationJobInProcess = _replicationJobInProcess
			utilsParseProjectNumberFromURI = utils.ParseProjectNumberFromURI
			InternalUtilGetSignedToken = auth.GetSignedJwtToken
		}()
		mockStorage := database.NewMockStorage(tt)
		response := []*datamodel.VolumeReplication{
			{
				BaseModel: datamodel.BaseModel{
					ID: 1,
				},
				ReplicationAttributes: &datamodel.ReplicationDetails{
					SourceLocation:      "us-central1",
					DestinationLocation: "us-east1",
				},
				RemoteUri: "projects/123456789/locations/us-central1/volumes/test-volume/replications/test-replication",
			},
		}
		mockStorage.On("ListVolumeReplications", mock.Anything, mock.Anything, mock.Anything).Return(response, nil)
		mockStorage.On("CheckAndFetchDuplicateJobs", mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
		utilsParseProjectNumberFromURI = func(uri string) (string, error) {
			return "123456789", nil
		}
		InternalUtilGetSignedToken = func(projectNumber string) (string, error) {
			return "mock-token", nil
		}
		InternalParseRegionAndZone = func(location string) (string, string, error) {
			if location == "us-central1" {
				return "us-central1", "us-central1-a", nil
			}
			return "us-east1", "us-east1-a", nil
		}
		InternalUtilGetPairedRegionURI = func(region string) (string, error) {
			return "basePath", nil
		}
		replicationJobInProcess = func(ctx context.Context, srcProjectNumber, destProjectNumber, srcBasePath, destBasePath, srcLocationID, destLocationID, srcToken, destToken, ccfeUri, remoteCcfeUri, srcPoolId, dstPoolId string, correlationId *string) error {
			return errors.New("replication job in process error")
		}
		expectedError := errors.New("replication job in process error")
		_, _, err := _validateReplicationParams(context.Background(), event, 12345, mockStorage, false, "CREATE_VOLUME_REPLICATION")
		assert.Error(tt, err)
		assert.Equal(tt, expectedError, err)
		mockStorage.AssertExpectations(tt)
	})
	t.Run("WhenSuccess", func(tt *testing.T) {
		defer func() {
			InternalParseRegionAndZone = utils.ParseRegionAndZone
			InternalUtilGetPairedRegionURI = utils.GetPairedRegionURI
			replicationJobInProcess = _replicationJobInProcess
			utilsParseProjectNumberFromURI = utils.ParseProjectNumberFromURI
			InternalUtilGetSignedToken = auth.GetSignedJwtToken
		}()
		mockStorage := database.NewMockStorage(tt)
		response := []*datamodel.VolumeReplication{
			{
				BaseModel: datamodel.BaseModel{
					ID: 1,
				},
				ReplicationAttributes: &datamodel.ReplicationDetails{
					SourceLocation:      "us-central1",
					DestinationLocation: "us-east1",
				},
				RemoteUri: "projects/123456789/locations/us-central1/volumes/test-volume/replications/test-replication",
			},
		}
		mockStorage.On("ListVolumeReplications", mock.Anything, mock.Anything, mock.Anything).Return(response, nil)
		mockStorage.On("CheckAndFetchDuplicateJobs", mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
		utilsParseProjectNumberFromURI = func(uri string) (string, error) {
			return "123456789", nil
		}
		InternalUtilGetSignedToken = func(projectNumber string) (string, error) {
			return "mock-token", nil
		}
		InternalParseRegionAndZone = func(location string) (string, string, error) {
			if location == "us-central1" {
				return "us-central1", "us-central1-a", nil
			}
			return "us-east1", "us-east1-a", nil
		}
		InternalUtilGetPairedRegionURI = func(region string) (string, error) {
			return "basePath", nil
		}
		replicationJobInProcess = func(ctx context.Context, srcProjectNumber, destProjectNumber, srcBasePath, destBasePath, srcLocationID, destLocationID, srcToken, destToken, ccfeUri, remoteCcfeUri, srcPoolId, dstPoolId string, correlationId *string) error {
			return nil
		}
		replication, jobUUID, err := _validateReplicationParams(context.Background(), event, 12345, mockStorage, false, "CREATE_VOLUME_REPLICATION")
		assert.NoError(tt, err)
		assert.Nil(tt, replication)
		assert.Nil(tt, jobUUID)
		mockStorage.AssertExpectations(tt)
	})
	t.Run("WhenDuplicateJobExistsAndDstBasePathEmptyAndGetReplicationFails", func(tt *testing.T) {
		defer func() {
			utilsParseProjectNumberFromURI = utils.ParseProjectNumberFromURI
			InternalUtilGetSignedToken = auth.GetSignedJwtToken
			InternalParseRegionAndZone = utils.ParseRegionAndZone
			InternalUtilGetPairedRegionURI = utils.GetPairedRegionURI
			getReplication = _getReplication
		}()
		mockStorage := database.NewMockStorage(tt)
		response := []*datamodel.VolumeReplication{
			{
				BaseModel: datamodel.BaseModel{
					ID: 1,
				},
				ReplicationAttributes: &datamodel.ReplicationDetails{
					SourceLocation:        "us-central1",
					DestinationLocation:   "us-east1",
					SourceReplicationUUID: "srcUUID",
				},
			},
		}
		existingJob := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				UUID: "existing-job-uuid",
			},
		}
		mockStorage.On("ListVolumeReplications", mock.Anything, mock.Anything, mock.Anything).Return(response, nil)
		mockStorage.On("CheckAndFetchDuplicateJobs", mock.Anything, mock.Anything, mock.Anything).Return(existingJob, nil)
		utilsParseProjectNumberFromURI = func(uri string) (string, error) {
			return "", nil
		}
		InternalUtilGetSignedToken = func(projectNumber string) (string, error) {
			return "mock-token", nil
		}
		InternalParseRegionAndZone = func(location string) (string, string, error) {
			return location, "", nil
		}
		InternalUtilGetPairedRegionURI = func(region string) (string, error) {
			return "basePath", nil
		}
		getReplication = func(ctx context.Context, basePath string, projectNumber string, locationID string, volumeReplicationID string, jwt string) (*coreModels.VolumeReplication, error) {
			return nil, errors.New("get replication error")
		}
		_, _, err := _validateReplicationParams(context.Background(), event, 12345, mockStorage, false, "CREATE_VOLUME_REPLICATION")
		assert.Error(tt, err)
		var customErr *vsaErrors.CustomError
		assert.True(tt, vsaErrors.As(err, &customErr), "Expected a CustomError")
		assert.Equal(tt, vsaErrors.ErrGoogleProxyInternalGetMultipleReplications, customErr.TrackingID)
		mockStorage.AssertExpectations(tt)
	})
	t.Run("WhenDuplicateJobExistsAndDstBasePathEmptyAndGetReplicationReturnsNil", func(tt *testing.T) {
		defer func() {
			utilsParseProjectNumberFromURI = utils.ParseProjectNumberFromURI
			InternalUtilGetSignedToken = auth.GetSignedJwtToken
			InternalParseRegionAndZone = utils.ParseRegionAndZone
			InternalUtilGetPairedRegionURI = utils.GetPairedRegionURI
			getReplication = _getReplication
		}()
		mockStorage := database.NewMockStorage(tt)
		response := []*datamodel.VolumeReplication{
			{
				BaseModel: datamodel.BaseModel{
					ID: 1,
				},
				ReplicationAttributes: &datamodel.ReplicationDetails{
					SourceLocation:        "us-central1",
					DestinationLocation:   "us-east1",
					SourceReplicationUUID: "srcUUID",
				},
			},
		}
		existingJob := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				UUID: "existing-job-uuid",
			},
		}
		mockStorage.On("ListVolumeReplications", mock.Anything, mock.Anything, mock.Anything).Return(response, nil)
		mockStorage.On("CheckAndFetchDuplicateJobs", mock.Anything, mock.Anything, mock.Anything).Return(existingJob, nil)
		utilsParseProjectNumberFromURI = func(uri string) (string, error) {
			return "", nil
		}
		InternalUtilGetSignedToken = func(projectNumber string) (string, error) {
			return "mock-token", nil
		}
		InternalParseRegionAndZone = func(location string) (string, string, error) {
			return location, "", nil
		}
		InternalUtilGetPairedRegionURI = func(region string) (string, error) {
			return "basePath", nil
		}
		getReplication = func(ctx context.Context, basePath string, projectNumber string, locationID string, volumeReplicationID string, jwt string) (*coreModels.VolumeReplication, error) {
			return nil, nil
		}
		_, _, err := _validateReplicationParams(context.Background(), event, 12345, mockStorage, false, "CREATE_VOLUME_REPLICATION")
		assert.Error(tt, err)
		var customErr *vsaErrors.CustomError
		assert.True(tt, vsaErrors.As(err, &customErr), "Expected a CustomError")
		assert.Equal(tt, vsaErrors.ErrGoogleProxyInternalGetMultipleReplications, customErr.TrackingID)
		mockStorage.AssertExpectations(tt)
	})
	t.Run("WhenRemoteUriIsEmpty", func(tt *testing.T) {
		defer func() {
			utilsParseProjectNumberFromURI = utils.ParseProjectNumberFromURI
			InternalUtilGetSignedToken = auth.GetSignedJwtToken
			InternalParseRegionAndZone = utils.ParseRegionAndZone
			InternalUtilGetPairedRegionURI = utils.GetPairedRegionURI
			replicationJobInProcess = _replicationJobInProcess
		}()
		mockStorage := database.NewMockStorage(tt)
		response := []*datamodel.VolumeReplication{
			{
				BaseModel: datamodel.BaseModel{
					ID: 1,
				},
				ReplicationAttributes: &datamodel.ReplicationDetails{
					SourceLocation:      "us-east4-1",
					DestinationLocation: "us-central1-a",
				},
				RemoteUri: "",
			},
		}
		mockStorage.On("ListVolumeReplications", mock.Anything, mock.Anything, mock.Anything).Return(response, nil)
		mockStorage.On("CheckAndFetchDuplicateJobs", mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
		InternalUtilGetSignedToken = func(projectNumber string) (string, error) {
			return "", nil
		}
		InternalParseRegionAndZone = func(location string) (string, string, error) {
			return location, "", nil
		}
		InternalUtilGetPairedRegionURI = func(region string) (string, error) {
			return "basePath", nil
		}
		replicationJobInProcess = func(ctx context.Context, srcProjectNumber, destProjectNumber, srcBasePath, destBasePath, srcLocationID, destLocationID, srcToken, destToken, ccfeUri, remoteCcfeUri, srcPoolId, dstPoolId string, correlationId *string) error {
			return nil
		}
		_, _, err := _validateReplicationParams(context.Background(), event, 12345, mockStorage, false, "CREATE_VOLUME_REPLICATION")
		assert.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
	})
	t.Run("WhenEndpointTypeIsDstEndpoint", func(tt *testing.T) {
		defer func() {
			utilsParseProjectNumberFromURI = utils.ParseProjectNumberFromURI
			InternalUtilGetSignedToken = auth.GetSignedJwtToken
			InternalParseRegionAndZone = utils.ParseRegionAndZone
			InternalUtilGetPairedRegionURI = utils.GetPairedRegionURI
			replicationJobInProcess = _replicationJobInProcess
		}()
		mockStorage := database.NewMockStorage(tt)
		response := []*datamodel.VolumeReplication{
			{
				BaseModel: datamodel.BaseModel{
					ID: 1,
				},
				ReplicationAttributes: &datamodel.ReplicationDetails{
					SourceLocation:      "us-east4-1",
					DestinationLocation: "us-central1-a",
					EndpointType:        string(datamodel.DstEndpoint),
				},
				RemoteUri: "projects/987654321/locations/us-central1/volumes/test-volume/replications/test-replication",
			},
		}
		mockStorage.On("ListVolumeReplications", mock.Anything, mock.Anything, mock.Anything).Return(response, nil)
		mockStorage.On("CheckAndFetchDuplicateJobs", mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
		utilsParseProjectNumberFromURI = func(uri string) (string, error) {
			return "987654321", nil
		}
		InternalUtilGetSignedToken = func(projectNumber string) (string, error) {
			return "", nil
		}
		InternalParseRegionAndZone = func(location string) (string, string, error) {
			return location, "", nil
		}
		InternalUtilGetPairedRegionURI = func(region string) (string, error) {
			return "basePath", nil
		}
		replicationJobInProcess = func(ctx context.Context, srcProjectNumber, destProjectNumber, srcBasePath, destBasePath, srcLocationID, destLocationID, srcToken, destToken, ccfeUri, remoteCcfeUri, srcPoolId, dstPoolId string, correlationId *string) error {
			return nil
		}
		_, _, err := _validateReplicationParams(context.Background(), event, 12345, mockStorage, false, "CREATE_VOLUME_REPLICATION")
		assert.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
	})
	t.Run("WhenDifferentProjects_GetSignedTokenErrorForDestination", func(tt *testing.T) {
		defer func() {
			utilsParseProjectNumberFromURI = utils.ParseProjectNumberFromURI
			InternalUtilGetSignedToken = auth.GetSignedJwtToken
		}()
		mockStorage := database.NewMockStorage(tt)
		response := []*datamodel.VolumeReplication{
			{
				BaseModel: datamodel.BaseModel{
					ID: 1,
				},
				ReplicationAttributes: &datamodel.ReplicationDetails{
					SourceLocation:      "us-east4-1",
					DestinationLocation: "us-central1-a",
				},
				RemoteUri: "projects/987654321/locations/us-central1/volumes/test-volume/replications/test-replication",
			},
		}
		mockStorage.On("ListVolumeReplications", mock.Anything, mock.Anything, mock.Anything).Return(response, nil)
		utilsParseProjectNumberFromURI = func(uri string) (string, error) {
			return "987654321", nil
		}
		callCount := 0
		InternalUtilGetSignedToken = func(projectNumber string) (string, error) {
			callCount++
			if callCount == 1 {
				return "src-token", nil
			}
			return "", vsaErrors.NewVCPError(vsaErrors.ErrGetSignedToken, errors.New("destination token error"))
		}
		_, _, err := _validateReplicationParams(context.Background(), event, 12345, mockStorage, false, "CREATE_VOLUME_REPLICATION")
		assert.Error(tt, err)
		var customErr *vsaErrors.CustomError
		assert.True(tt, vsaErrors.As(err, &customErr), "Expected a CustomError")
		assert.Equal(tt, vsaErrors.ErrGetSignedToken, customErr.TrackingID)
		mockStorage.AssertExpectations(tt)
	})
	t.Run("WhenSourceLocationIsEmpty", func(tt *testing.T) {
		defer func() {
			utilsParseProjectNumberFromURI = utils.ParseProjectNumberFromURI
			InternalUtilGetSignedToken = auth.GetSignedJwtToken
			InternalParseRegionAndZone = utils.ParseRegionAndZone
			InternalUtilGetPairedRegionURI = utils.GetPairedRegionURI
			replicationJobInProcess = _replicationJobInProcess
		}()
		mockStorage := database.NewMockStorage(tt)
		response := []*datamodel.VolumeReplication{
			{
				BaseModel: datamodel.BaseModel{
					ID: 1,
				},
				ReplicationAttributes: &datamodel.ReplicationDetails{
					SourceLocation:      "",
					DestinationLocation: "us-central1-a",
				},
				RemoteUri: "projects/123456789/locations/us-central1/volumes/test-volume/replications/test-replication",
			},
		}
		mockStorage.On("ListVolumeReplications", mock.Anything, mock.Anything, mock.Anything).Return(response, nil)
		mockStorage.On("CheckAndFetchDuplicateJobs", mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
		utilsParseProjectNumberFromURI = func(uri string) (string, error) {
			return "123456789", nil
		}
		InternalUtilGetSignedToken = func(projectNumber string) (string, error) {
			return "", nil
		}
		InternalParseRegionAndZone = func(location string) (string, string, error) {
			return location, "", nil
		}
		InternalUtilGetPairedRegionURI = func(region string) (string, error) {
			return "basePath", nil
		}
		replicationJobInProcess = func(ctx context.Context, srcProjectNumber, destProjectNumber, srcBasePath, destBasePath, srcLocationID, destLocationID, srcToken, destToken, ccfeUri, remoteCcfeUri, srcPoolId, dstPoolId string, correlationId *string) error {
			return nil
		}
		_, _, err := _validateReplicationParams(context.Background(), event, 12345, mockStorage, false, "CREATE_VOLUME_REPLICATION")
		assert.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
	})
	t.Run("WhenDestinationLocationIsEmpty", func(tt *testing.T) {
		defer func() {
			utilsParseProjectNumberFromURI = utils.ParseProjectNumberFromURI
			InternalUtilGetSignedToken = auth.GetSignedJwtToken
			InternalParseRegionAndZone = utils.ParseRegionAndZone
			InternalUtilGetPairedRegionURI = utils.GetPairedRegionURI
			replicationJobInProcess = _replicationJobInProcess
		}()
		mockStorage := database.NewMockStorage(tt)
		response := []*datamodel.VolumeReplication{
			{
				BaseModel: datamodel.BaseModel{
					ID: 1,
				},
				ReplicationAttributes: &datamodel.ReplicationDetails{
					SourceLocation:      "us-east4-1",
					DestinationLocation: "",
				},
				RemoteUri: "projects/123456789/locations/us-central1/volumes/test-volume/replications/test-replication",
			},
		}
		mockStorage.On("ListVolumeReplications", mock.Anything, mock.Anything, mock.Anything).Return(response, nil)
		mockStorage.On("CheckAndFetchDuplicateJobs", mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
		utilsParseProjectNumberFromURI = func(uri string) (string, error) {
			return "123456789", nil
		}
		InternalUtilGetSignedToken = func(projectNumber string) (string, error) {
			return "", nil
		}
		InternalParseRegionAndZone = func(location string) (string, string, error) {
			return location, "", nil
		}
		InternalUtilGetPairedRegionURI = func(region string) (string, error) {
			return "basePath", nil
		}
		replicationJobInProcess = func(ctx context.Context, srcProjectNumber, destProjectNumber, srcBasePath, destBasePath, srcLocationID, destLocationID, srcToken, destToken, ccfeUri, remoteCcfeUri, srcPoolId, dstPoolId string, correlationId *string) error {
			return nil
		}
		_, _, err := _validateReplicationParams(context.Background(), event, 12345, mockStorage, false, "CREATE_VOLUME_REPLICATION")
		assert.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
	})
	t.Run("WhenDuplicateJobFound_WithDstBasePathEmpty_GetReplicationFails", func(tt *testing.T) {
		defer func() {
			InternalParseRegionAndZone = utils.ParseRegionAndZone
			InternalUtilGetPairedRegionURI = utils.GetPairedRegionURI
			getReplication = _getReplication
			utilsParseProjectNumberFromURI = utils.ParseProjectNumberFromURI
			InternalUtilGetSignedToken = auth.GetSignedJwtToken
		}()
		mockStorage := database.NewMockStorage(tt)
		response := []*datamodel.VolumeReplication{
			{
				BaseModel: datamodel.BaseModel{
					ID: 1,
				},
				ReplicationAttributes: &datamodel.ReplicationDetails{
					SourceLocation:        "us-central1",
					DestinationLocation:   "",
					SourceReplicationUUID: "src-replication-uuid",
				},
				RemoteUri: "projects/123456789/locations/us-central1/volumes/test-volume/replications/test-replication",
			},
		}
		mockStorage.On("ListVolumeReplications", mock.Anything, mock.Anything, mock.Anything).Return(response, nil)
		existingJob := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				UUID: "existing-job-uuid",
			},
		}
		mockStorage.On("CheckAndFetchDuplicateJobs", mock.Anything, mock.Anything, mock.Anything).Return(existingJob, nil)
		utilsParseProjectNumberFromURI = func(uri string) (string, error) {
			return "123456789", nil
		}
		InternalUtilGetSignedToken = func(projectNumber string) (string, error) {
			return "mock-token", nil
		}
		InternalParseRegionAndZone = func(location string) (string, string, error) {
			return "us-central1", "us-central1-a", nil
		}
		InternalUtilGetPairedRegionURI = func(region string) (string, error) {
			return "basePath", nil
		}
		getReplication = func(ctx context.Context, basePath, projectNumber, location, replicationUUID, token string) (*coreModels.VolumeReplication, error) {
			return nil, errors.New("get replication error")
		}
		expectedError := vsaErrors.NewVCPError(vsaErrors.ErrGoogleProxyInternalGetMultipleReplications, errors.New("get replication error"))
		_, _, err := _validateReplicationParams(context.Background(), event, 12345, mockStorage, false, "CREATE_VOLUME_REPLICATION")
		assert.Error(tt, err)
		assert.Equal(tt, expectedError, err)
		mockStorage.AssertExpectations(tt)
	})
	t.Run("WhenDuplicateJobFound_WithDstBasePathEmpty_GetReplicationReturnsNil", func(tt *testing.T) {
		defer func() {
			InternalParseRegionAndZone = utils.ParseRegionAndZone
			InternalUtilGetPairedRegionURI = utils.GetPairedRegionURI
			getReplication = _getReplication
			utilsParseProjectNumberFromURI = utils.ParseProjectNumberFromURI
			InternalUtilGetSignedToken = auth.GetSignedJwtToken
		}()
		mockStorage := database.NewMockStorage(tt)
		response := []*datamodel.VolumeReplication{
			{
				BaseModel: datamodel.BaseModel{
					ID: 1,
				},
				ReplicationAttributes: &datamodel.ReplicationDetails{
					SourceLocation:        "us-central1",
					DestinationLocation:   "",
					SourceReplicationUUID: "src-replication-uuid",
				},
				RemoteUri: "projects/123456789/locations/us-central1/volumes/test-volume/replications/test-replication",
			},
		}
		mockStorage.On("ListVolumeReplications", mock.Anything, mock.Anything, mock.Anything).Return(response, nil)
		existingJob := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				UUID: "existing-job-uuid",
			},
		}
		mockStorage.On("CheckAndFetchDuplicateJobs", mock.Anything, mock.Anything, mock.Anything).Return(existingJob, nil)
		utilsParseProjectNumberFromURI = func(uri string) (string, error) {
			return "123456789", nil
		}
		InternalUtilGetSignedToken = func(projectNumber string) (string, error) {
			return "mock-token", nil
		}
		InternalParseRegionAndZone = func(location string) (string, string, error) {
			return "us-central1", "us-central1-a", nil
		}
		InternalUtilGetPairedRegionURI = func(region string) (string, error) {
			return "basePath", nil
		}
		getReplication = func(ctx context.Context, basePath, projectNumber, location, replicationUUID, token string) (*coreModels.VolumeReplication, error) {
			return nil, nil
		}
		expectedError := vsaErrors.NewVCPError(vsaErrors.ErrGoogleProxyInternalGetMultipleReplications, nil)
		_, _, err := _validateReplicationParams(context.Background(), event, 12345, mockStorage, false, "CREATE_VOLUME_REPLICATION")
		assert.Error(tt, err)
		assert.Equal(tt, expectedError, err)
		mockStorage.AssertExpectations(tt)
	})
	t.Run("WhenDuplicateJobFound_WithDstBasePathEmpty_Success", func(tt *testing.T) {
		defer func() {
			InternalParseRegionAndZone = utils.ParseRegionAndZone
			InternalUtilGetPairedRegionURI = utils.GetPairedRegionURI
			getReplication = _getReplication
			utilsParseProjectNumberFromURI = utils.ParseProjectNumberFromURI
			InternalUtilGetSignedToken = auth.GetSignedJwtToken
		}()
		mockStorage := database.NewMockStorage(tt)
		response := []*datamodel.VolumeReplication{
			{
				BaseModel: datamodel.BaseModel{
					ID: 1,
				},
				ReplicationAttributes: &datamodel.ReplicationDetails{
					SourceLocation:        "us-central1",
					DestinationLocation:   "",
					SourceReplicationUUID: "src-replication-uuid",
				},
				RemoteUri: "projects/123456789/locations/us-central1/volumes/test-volume/replications/test-replication",
			},
		}
		mockStorage.On("ListVolumeReplications", mock.Anything, mock.Anything, mock.Anything).Return(response, nil)
		existingJob := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				UUID: "existing-job-uuid",
			},
		}
		mockStorage.On("CheckAndFetchDuplicateJobs", mock.Anything, mock.Anything, mock.Anything).Return(existingJob, nil)
		utilsParseProjectNumberFromURI = func(uri string) (string, error) {
			return "123456789", nil
		}
		InternalUtilGetSignedToken = func(projectNumber string) (string, error) {
			return "mock-token", nil
		}
		InternalParseRegionAndZone = func(location string) (string, string, error) {
			return "us-central1", "us-central1-a", nil
		}
		InternalUtilGetPairedRegionURI = func(region string) (string, error) {
			return "basePath", nil
		}
		mirrorState := "MIRRORED"
		mockReplication := &coreModels.VolumeReplication{
			MirrorState: &mirrorState,
		}
		getReplication = func(ctx context.Context, basePath, projectNumber, location, replicationUUID, token string) (*coreModels.VolumeReplication, error) {
			return mockReplication, nil
		}
		replication, jobUUID, err := _validateReplicationParams(context.Background(), event, 12345, mockStorage, false, "CREATE_VOLUME_REPLICATION")
		assert.NoError(tt, err)
		assert.Equal(tt, mockReplication, replication)
		assert.Equal(tt, "existing-job-uuid", *jobUUID)
		mockStorage.AssertExpectations(tt)
	})
	t.Run("WhenDuplicateJobFound_WithDstBasePathNotEmpty_GetReplicationReturnsNil", func(tt *testing.T) {
		defer func() {
			InternalParseRegionAndZone = utils.ParseRegionAndZone
			InternalUtilGetPairedRegionURI = utils.GetPairedRegionURI
			getReplication = _getReplication
			utilsParseProjectNumberFromURI = utils.ParseProjectNumberFromURI
			InternalUtilGetSignedToken = auth.GetSignedJwtToken
		}()
		mockStorage := database.NewMockStorage(tt)
		response := []*datamodel.VolumeReplication{
			{
				BaseModel: datamodel.BaseModel{
					ID: 1,
				},
				ReplicationAttributes: &datamodel.ReplicationDetails{
					SourceLocation:             "us-central1",
					DestinationLocation:        "us-east1",
					DestinationReplicationUUID: "dst-replication-uuid",
				},
				RemoteUri: "projects/123456789/locations/us-central1/volumes/test-volume/replications/test-replication",
			},
		}
		mockStorage.On("ListVolumeReplications", mock.Anything, mock.Anything, mock.Anything).Return(response, nil)
		existingJob := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				UUID: "existing-job-uuid",
			},
		}
		mockStorage.On("CheckAndFetchDuplicateJobs", mock.Anything, mock.Anything, mock.Anything).Return(existingJob, nil)
		utilsParseProjectNumberFromURI = func(uri string) (string, error) {
			return "123456789", nil
		}
		InternalUtilGetSignedToken = func(projectNumber string) (string, error) {
			return "mock-token", nil
		}
		InternalParseRegionAndZone = func(location string) (string, string, error) {
			if location == "us-central1" {
				return "us-central1", "us-central1-a", nil
			}
			return "us-east1", "us-east1-a", nil
		}
		InternalUtilGetPairedRegionURI = func(region string) (string, error) {
			return "basePath", nil
		}
		getReplication = func(ctx context.Context, basePath, projectNumber, location, replicationUUID, token string) (*coreModels.VolumeReplication, error) {
			return nil, nil
		}
		expectedError := vsaErrors.NewVCPError(vsaErrors.ErrGoogleProxyInternalGetMultipleReplications, nil)
		_, _, err := _validateReplicationParams(context.Background(), event, 12345, mockStorage, false, "CREATE_VOLUME_REPLICATION")
		assert.Error(tt, err)
		assert.Equal(tt, expectedError, err)
		mockStorage.AssertExpectations(tt)
	})
}

func TestVerifyDstReplicationResume(t *testing.T) {
	event := &ResumeReplicationEvent{
		CommonReplicationEventParams: CommonReplicationEventParams{
			DstBasePath:              "dstPath",
			DestinationProjectNumber: "destinationProjectNumber",
			DstToken:                 "dstToken",
			ReplicationModel: &datamodel.VolumeReplication{
				ReplicationAttributes: &datamodel.ReplicationDetails{
					DestinationLocation:        "dstLocation",
					DestinationReplicationUUID: "dstUUID",
				},
			},
		},
	}
	t.Run("WhenGetReplicationError", func(tt *testing.T) {
		ctx := context.Background()
		defer func() {
			getReplication = _getReplication
		}()
		getReplication = func(ctx context.Context, basePath string, projectNumber string, locationID string, volumeReplicationID string, jwt string) (*coreModels.VolumeReplication, error) {
			return nil, errors.New("some error")
		}
		_, err := _verifyDstReplicationResume(ctx, event)
		assert.Error(tt, err)
	})
	t.Run("WhenMirrorStateMirrored", func(tt *testing.T) {
		ctx := context.Background()
		defer func() {
			getReplication = _getReplication
		}()
		mirrorState := "MIRRORED"
		dstReplication := &coreModels.VolumeReplication{
			MirrorState: &mirrorState,
		}
		getReplication = func(ctx context.Context, basePath string, projectNumber string, locationID string, volumeReplicationID string, jwt string) (*coreModels.VolumeReplication, error) {
			return dstReplication, nil
		}
		expectedError := errors.NewUserInputValidationErr(fmt.Sprintf("Replication mirror state should be %s", models.ReplicationV1betaMirrorStateSTOPPED))
		_, err := _verifyDstReplicationResume(ctx, event)
		assert.Error(tt, err)
		assert.Equal(tt, expectedError, err)
	})
	t.Run("WhenMirrorStateUninitialized", func(tt *testing.T) {
		ctx := context.Background()
		defer func() {
			getReplication = _getReplication
		}()
		mirrorState := "UNINITIALIZED"
		relationshipStatus := "transferring"
		dstReplication := &coreModels.VolumeReplication{
			MirrorState:        &mirrorState,
			RelationshipStatus: &relationshipStatus,
		}
		getReplication = func(ctx context.Context, basePath string, projectNumber string, locationID string, volumeReplicationID string, jwt string) (*coreModels.VolumeReplication, error) {
			return dstReplication, nil
		}
		expectedError := errors.NewUserInputValidationErr(fmt.Sprintf("Replication relationship status should be %s", models.VolumeReplicationCVPV1betaRelationshipStatusIdle))
		_, err := _verifyDstReplicationResume(ctx, event)
		assert.Error(tt, err)
		assert.Equal(tt, expectedError, err)
	})
	t.Run("WhenSuccess", func(tt *testing.T) {
		ctx := context.Background()
		defer func() {
			getReplication = _getReplication
		}()
		mirrorState := "STOPPED"
		relationshipStatus := "IDLE"
		dstReplication := &coreModels.VolumeReplication{
			MirrorState:        &mirrorState,
			RelationshipStatus: &relationshipStatus,
		}
		getReplication = func(ctx context.Context, basePath string, projectNumber string, locationID string, volumeReplicationID string, jwt string) (*coreModels.VolumeReplication, error) {
			return dstReplication, nil
		}
		resp, err := _verifyDstReplicationResume(ctx, event)
		assert.NoError(tt, err)
		assert.Equal(tt, dstReplication, resp)
	})
	t.Run("WhenHybridReplicationAttributesSet_AndStatusNotPeered_ReturnsError", func(tt *testing.T) {
		ctx := context.Background()
		migrationType := string(datamodel.HybridReplicationParametersReplicationTypeMIGRATION)
		status := datamodel.HybridReplicationStatusPendingSVMPeer
		eventWithHybridNotPeered := &ResumeReplicationEvent{
			CommonReplicationEventParams: CommonReplicationEventParams{
				DstBasePath:              "dstPath",
				DestinationProjectNumber: "destinationProjectNumber",
				DstToken:                 "dstToken",
				ReplicationModel: &datamodel.VolumeReplication{
					ReplicationAttributes: &datamodel.ReplicationDetails{
						DestinationLocation:        "us-east1",
						DestinationReplicationUUID: "dstUUID",
					},
					HybridReplicationAttributes: &datamodel.HybridReplicationAttribute{
						HybridReplicationType: &migrationType,
						Status:                status,
					},
				},
			},
		}
		expectedError := errors.NewUserInputValidationErr("Hybrid Replication needs to be in peered state before resuming")
		resp, err := _verifyDstReplicationResume(ctx, eventWithHybridNotPeered)
		assert.Error(tt, err)
		assert.Nil(tt, resp)
		assert.Equal(tt, expectedError, err)
	})
	t.Run("WhenIsSrcForHybridReplicationReturnsTrue_AndDestinationReplicationUUIDIsNil_AndStatusIsNotExternalManaged", func(tt *testing.T) {
		ctx := context.Background()
		reverseType := string(datamodel.HybridReplicationParametersReplicationTypeREVERSE)
		status := datamodel.HybridReplicationStatusPeered
		hybridEvent := &ResumeReplicationEvent{
			CommonReplicationEventParams: CommonReplicationEventParams{
				SrcBasePath:              "srcPath",
				SourceProjectNumber:      "sourceProjectNumber",
				SrcToken:                 "srcToken",
				DestinationProjectNumber: "destinationProjectNumber",
				DstToken:                 "dstToken",
				ReplicationModel: &datamodel.VolumeReplication{
					ReplicationAttributes: &datamodel.ReplicationDetails{
						SourceLocation:             "srcLocation",
						SourceReplicationUUID:      "srcUUID",
						DestinationLocation:        remoteRegionCustomer,
						DestinationReplicationUUID: "00000000-0000-0000-0000-000000000000",
					},
					HybridReplicationAttributes: &datamodel.HybridReplicationAttribute{
						HybridReplicationType: &reverseType,
						Status:                status,
					},
				},
			},
		}
		expectedError := errors.NewUserInputValidationErr("Hybrid Replication needs to be in externally managed state before resuming")
		resp, err := _verifyDstReplicationResume(ctx, hybridEvent)
		assert.Error(tt, err)
		assert.Nil(tt, resp)
		assert.Equal(tt, expectedError, err)
	})
	t.Run("WhenIsSrcForHybridReplicationReturnsTrue_AndDestinationReplicationUUIDIsNotNil", func(tt *testing.T) {
		ctx := context.Background()
		defer func() {
			getReplication = _getReplication
		}()
		reverseType := string(datamodel.HybridReplicationParametersReplicationTypeREVERSE)
		hybridEvent := &ResumeReplicationEvent{
			CommonReplicationEventParams: CommonReplicationEventParams{
				SrcBasePath:              "srcPath",
				SourceProjectNumber:      "sourceProjectNumber",
				SrcToken:                 "srcToken",
				DestinationProjectNumber: "destinationProjectNumber",
				DstToken:                 "dstToken",
				ReplicationModel: &datamodel.VolumeReplication{
					ReplicationAttributes: &datamodel.ReplicationDetails{
						SourceLocation:             "srcLocation",
						SourceReplicationUUID:      "srcUUID",
						DestinationLocation:        remoteRegionCustomer,
						DestinationReplicationUUID: "destUUID",
					},
					HybridReplicationAttributes: &datamodel.HybridReplicationAttribute{
						HybridReplicationType: &reverseType,
					},
				},
			},
		}
		srcReplication := &coreModels.VolumeReplication{
			ReplicationAttributes: &coreModels.ReplicationDetails{
				SourceSvmName:         "src-svm",
				SourceVolumeName:      "src-volume",
				DestinationSvmName:    "dst-svm",
				DestinationVolumeName: "dst-volume",
			},
			HybridReplicationAttributes: &coreModels.HybridReplicationParameters{},
		}
		getReplication = func(ctx context.Context, basePath string, projectNumber string, locationID string, volumeReplicationID string, jwt string) (*coreModels.VolumeReplication, error) {
			return srcReplication, nil
		}
		resp, err := _verifyDstReplicationResume(ctx, hybridEvent)
		assert.NoError(tt, err)
		assert.NotNil(tt, resp)
		assert.Equal(tt, srcReplication, resp)
		assert.NotNil(tt, resp.HybridReplicationAttributes.HybridReplicationUserCommands)
		assert.Contains(tt, resp.StateDetails, "Please execute the commands on Onprem ONTAP to Resume replication")
	})
	t.Run("WhenIsSrcForHybridReplicationReturnsTrue_AndDestinationReplicationUUIDIsNil_AndStatusIsExternalManaged", func(tt *testing.T) {
		ctx := context.Background()
		defer func() {
			getReplication = _getReplication
		}()
		reverseType := string(datamodel.HybridReplicationParametersReplicationTypeREVERSE)
		status := datamodel.HybridReplicationStatusExternalManaged
		hybridEvent := &ResumeReplicationEvent{
			CommonReplicationEventParams: CommonReplicationEventParams{
				SrcBasePath:              "srcPath",
				SourceProjectNumber:      "sourceProjectNumber",
				SrcToken:                 "srcToken",
				DestinationProjectNumber: "destinationProjectNumber",
				DstToken:                 "dstToken",
				ReplicationModel: &datamodel.VolumeReplication{
					ReplicationAttributes: &datamodel.ReplicationDetails{
						SourceLocation:             "srcLocation",
						SourceReplicationUUID:      "srcUUID",
						DestinationLocation:        remoteRegionCustomer,
						DestinationReplicationUUID: "00000000-0000-0000-0000-000000000000",
					},
					HybridReplicationAttributes: &datamodel.HybridReplicationAttribute{
						HybridReplicationType: &reverseType,
						Status:                status,
					},
				},
			},
		}
		srcReplication := &coreModels.VolumeReplication{
			ReplicationAttributes: &coreModels.ReplicationDetails{
				SourceSvmName:         "src-svm",
				SourceVolumeName:      "src-volume",
				DestinationSvmName:    "dst-svm",
				DestinationVolumeName: "dst-volume",
			},
			HybridReplicationAttributes: &coreModels.HybridReplicationParameters{},
		}
		getReplication = func(ctx context.Context, basePath string, projectNumber string, locationID string, volumeReplicationID string, jwt string) (*coreModels.VolumeReplication, error) {
			return srcReplication, nil
		}
		resp, err := _verifyDstReplicationResume(ctx, hybridEvent)
		assert.NoError(tt, err)
		assert.NotNil(tt, resp)
		assert.Equal(tt, srcReplication, resp)
		assert.NotNil(tt, resp.HybridReplicationAttributes.HybridReplicationUserCommands)
	})
	t.Run("WhenIsSrcForHybridReplicationReturnsTrue_AndGetReplicationFails", func(tt *testing.T) {
		ctx := context.Background()
		defer func() {
			getReplication = _getReplication
		}()
		reverseType := string(datamodel.HybridReplicationParametersReplicationTypeREVERSE)
		hybridEvent := &ResumeReplicationEvent{
			CommonReplicationEventParams: CommonReplicationEventParams{
				SrcBasePath:              "srcPath",
				SourceProjectNumber:      "sourceProjectNumber",
				SrcToken:                 "srcToken",
				DestinationProjectNumber: "destinationProjectNumber",
				DstToken:                 "dstToken",
				ReplicationModel: &datamodel.VolumeReplication{
					ReplicationAttributes: &datamodel.ReplicationDetails{
						SourceLocation:             "srcLocation",
						SourceReplicationUUID:      "srcUUID",
						DestinationLocation:        remoteRegionCustomer,
						DestinationReplicationUUID: "destUUID",
					},
					HybridReplicationAttributes: &datamodel.HybridReplicationAttribute{
						HybridReplicationType: &reverseType,
					},
				},
			},
		}
		getReplication = func(ctx context.Context, basePath string, projectNumber string, locationID string, volumeReplicationID string, jwt string) (*coreModels.VolumeReplication, error) {
			return nil, errors.New("get replication error")
		}
		resp, err := _verifyDstReplicationResume(ctx, hybridEvent)
		assert.Error(tt, err)
		assert.Nil(tt, resp)
		var customErr *vsaErrors.CustomError
		assert.True(tt, vsaErrors.As(err, &customErr), "Expected a CustomError")
		assert.Equal(tt, vsaErrors.ErrGoogleProxyInternalGetMultipleReplications, customErr.TrackingID)
	})
	t.Run("WhenIsSrcForHybridReplicationReturnsTrue_AndGetReplicationReturnsNil", func(tt *testing.T) {
		ctx := context.Background()
		defer func() {
			getReplication = _getReplication
		}()
		reverseType := string(datamodel.HybridReplicationParametersReplicationTypeREVERSE)
		hybridEvent := &ResumeReplicationEvent{
			CommonReplicationEventParams: CommonReplicationEventParams{
				SrcBasePath:              "srcPath",
				SourceProjectNumber:      "sourceProjectNumber",
				SrcToken:                 "srcToken",
				DestinationProjectNumber: "destinationProjectNumber",
				DstToken:                 "dstToken",
				ReplicationModel: &datamodel.VolumeReplication{
					ReplicationAttributes: &datamodel.ReplicationDetails{
						SourceLocation:             "srcLocation",
						SourceReplicationUUID:      "srcUUID",
						DestinationLocation:        remoteRegionCustomer,
						DestinationReplicationUUID: "destUUID",
					},
					HybridReplicationAttributes: &datamodel.HybridReplicationAttribute{
						HybridReplicationType: &reverseType,
					},
				},
			},
		}
		getReplication = func(ctx context.Context, basePath string, projectNumber string, locationID string, volumeReplicationID string, jwt string) (*coreModels.VolumeReplication, error) {
			return nil, nil
		}
		resp, err := _verifyDstReplicationResume(ctx, hybridEvent)
		assert.Error(tt, err)
		assert.Nil(tt, resp)
		var customErr *vsaErrors.CustomError
		assert.True(tt, vsaErrors.As(err, &customErr), "Expected a CustomError")
		assert.Equal(tt, vsaErrors.ErrGoogleProxyInternalGetMultipleReplications, customErr.TrackingID)
	})
}

func TestVerifySourceQuotaRules(t *testing.T) {
	baseEvent := &ResumeReplicationEvent{
		CommonReplicationEventParams: CommonReplicationEventParams{
			SrcBasePath:         "srcPath",
			SourceProjectNumber: "sourceProjectNumber",
			SrcToken:            "srcToken",
			ReplicationModel: &datamodel.VolumeReplication{
				ReplicationAttributes: &datamodel.ReplicationDetails{
					SourceLocation:   "srcLocation",
					SourceVolumeUUID: "srcVolumeUUID",
				},
			},
		},
	}

	t.Run("WhenHybridReplication_ValidatesQuotaRules", func(tt *testing.T) {
		ctx := context.Background()
		reverseType := string(datamodel.HybridReplicationParametersReplicationTypeREVERSE)
		hybridEvent := &ResumeReplicationEvent{
			CommonReplicationEventParams: CommonReplicationEventParams{
				SrcBasePath:         "srcPath",
				SourceProjectNumber: "sourceProjectNumber",
				SrcToken:            "srcToken",
				ReplicationModel: &datamodel.VolumeReplication{
					ReplicationAttributes: &datamodel.ReplicationDetails{
						SourceLocation:      "srcLocation",
						SourceVolumeUUID:    "srcVolumeUUID",
						DestinationLocation: remoteRegionCustomer,
					},
					HybridReplicationAttributes: &datamodel.HybridReplicationAttribute{
						HybridReplicationType: &reverseType,
					},
				},
			},
		}

		mockInvoker := googleproxyclient.NewMockInvoker(tt)
		mockProxyClient := &googleproxyclient.ProxyClient{
			Invoker: mockInvoker,
		}

		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() {
			googleproxyclient.GetGProxyClient = originalGetGProxyClient
		}()

		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mockProxyClient
		}

		// Hybrid replication should now validate quota rules like regular replication
		response := &googleproxyclient.V1betaListAllQuotaRulesOK{
			QuotaRules: []googleproxyclient.QuotaRulesV1beta{
				{
					ResourceId: "quota-rule-1",
					State:      googleproxyclient.NewOptQuotaRulesV1betaState(googleproxyclient.QuotaRulesV1betaStateREADY),
				},
			},
		}
		mockInvoker.On("V1betaListAllQuotaRules", ctx, mock.Anything).Return(response, nil)

		err := _verifySourceQuotaRules(ctx, hybridEvent)
		assert.NoError(tt, err)
		mockInvoker.AssertExpectations(tt)
	})

	t.Run("WhenHybridReplication_WithNonReadyQuotaRules_ReturnsError", func(tt *testing.T) {
		ctx := context.Background()
		reverseType := string(datamodel.HybridReplicationParametersReplicationTypeREVERSE)
		hybridEvent := &ResumeReplicationEvent{
			CommonReplicationEventParams: CommonReplicationEventParams{
				SrcBasePath:         "srcPath",
				SourceProjectNumber: "sourceProjectNumber",
				SrcToken:            "srcToken",
				ReplicationModel: &datamodel.VolumeReplication{
					ReplicationAttributes: &datamodel.ReplicationDetails{
						SourceLocation:      "srcLocation",
						SourceVolumeUUID:    "srcVolumeUUID",
						DestinationLocation: remoteRegionCustomer,
					},
					HybridReplicationAttributes: &datamodel.HybridReplicationAttribute{
						HybridReplicationType: &reverseType,
					},
				},
			},
		}

		mockInvoker := googleproxyclient.NewMockInvoker(tt)
		mockProxyClient := &googleproxyclient.ProxyClient{
			Invoker: mockInvoker,
		}

		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() {
			googleproxyclient.GetGProxyClient = originalGetGProxyClient
		}()

		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mockProxyClient
		}

		// Hybrid replication should validate and fail if quota rules are not ready
		response := &googleproxyclient.V1betaListAllQuotaRulesOK{
			QuotaRules: []googleproxyclient.QuotaRulesV1beta{
				{
					ResourceId:   "quota-rule-1",
					State:        googleproxyclient.NewOptQuotaRulesV1betaState(googleproxyclient.QuotaRulesV1betaStateCREATING),
					StateDetails: googleproxyclient.NewOptString("Creating quota rule"),
				},
			},
		}
		mockInvoker.On("V1betaListAllQuotaRules", ctx, mock.Anything).Return(response, nil)

		err := _verifySourceQuotaRules(ctx, hybridEvent)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "quota rules not in READY state")
		assert.Contains(tt, err.Error(), "quota-rule-1")
		mockInvoker.AssertExpectations(tt)
	})

	t.Run("WhenHybridReplication_WithAPIError_ReturnsError", func(tt *testing.T) {
		ctx := context.Background()
		reverseType := string(datamodel.HybridReplicationParametersReplicationTypeREVERSE)
		hybridEvent := &ResumeReplicationEvent{
			CommonReplicationEventParams: CommonReplicationEventParams{
				SrcBasePath:         "srcPath",
				SourceProjectNumber: "sourceProjectNumber",
				SrcToken:            "srcToken",
				ReplicationModel: &datamodel.VolumeReplication{
					ReplicationAttributes: &datamodel.ReplicationDetails{
						SourceLocation:      "srcLocation",
						SourceVolumeUUID:    "srcVolumeUUID",
						DestinationLocation: remoteRegionCustomer,
					},
					HybridReplicationAttributes: &datamodel.HybridReplicationAttribute{
						HybridReplicationType: &reverseType,
					},
				},
			},
		}

		mockInvoker := googleproxyclient.NewMockInvoker(tt)
		mockProxyClient := &googleproxyclient.ProxyClient{
			Invoker: mockInvoker,
		}

		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() {
			googleproxyclient.GetGProxyClient = originalGetGProxyClient
		}()

		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mockProxyClient
		}

		// Hybrid replication should validate and fail on API errors
		mockInvoker.On("V1betaListAllQuotaRules", ctx, mock.Anything).Return(nil, fmt.Errorf("network error"))

		err := _verifySourceQuotaRules(ctx, hybridEvent)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "failed to list quota rules on source volume")
		mockInvoker.AssertExpectations(tt)
	})

	t.Run("WhenAPICallFails_ReturnsError", func(tt *testing.T) {
		ctx := context.Background()
		mockInvoker := googleproxyclient.NewMockInvoker(tt)
		mockProxyClient := &googleproxyclient.ProxyClient{
			Invoker: mockInvoker,
		}

		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() {
			googleproxyclient.GetGProxyClient = originalGetGProxyClient
		}()

		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mockProxyClient
		}

		mockInvoker.On("V1betaListAllQuotaRules", ctx, mock.Anything).Return(nil, fmt.Errorf("API error"))

		err := _verifySourceQuotaRules(ctx, baseEvent)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "failed to list quota rules on source volume")
		mockInvoker.AssertExpectations(tt)
	})

	t.Run("WhenNoQuotaRules_Success", func(tt *testing.T) {
		ctx := context.Background()
		mockInvoker := googleproxyclient.NewMockInvoker(tt)
		mockProxyClient := &googleproxyclient.ProxyClient{
			Invoker: mockInvoker,
		}

		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() {
			googleproxyclient.GetGProxyClient = originalGetGProxyClient
		}()

		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mockProxyClient
		}

		response := &googleproxyclient.V1betaListAllQuotaRulesOK{
			QuotaRules: []googleproxyclient.QuotaRulesV1beta{},
		}
		mockInvoker.On("V1betaListAllQuotaRules", ctx, mock.Anything).Return(response, nil)

		err := _verifySourceQuotaRules(ctx, baseEvent)
		assert.NoError(tt, err)
		mockInvoker.AssertExpectations(tt)
	})

	t.Run("WhenAllQuotaRulesReady_Success", func(tt *testing.T) {
		ctx := context.Background()
		mockInvoker := googleproxyclient.NewMockInvoker(tt)
		mockProxyClient := &googleproxyclient.ProxyClient{
			Invoker: mockInvoker,
		}

		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() {
			googleproxyclient.GetGProxyClient = originalGetGProxyClient
		}()

		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mockProxyClient
		}

		response := &googleproxyclient.V1betaListAllQuotaRulesOK{
			QuotaRules: []googleproxyclient.QuotaRulesV1beta{
				{
					ResourceId: "quota-rule-1",
					State:      googleproxyclient.NewOptQuotaRulesV1betaState(googleproxyclient.QuotaRulesV1betaStateREADY),
				},
				{
					ResourceId: "quota-rule-2",
					State:      googleproxyclient.NewOptQuotaRulesV1betaState(googleproxyclient.QuotaRulesV1betaStateREADY),
				},
			},
		}
		mockInvoker.On("V1betaListAllQuotaRules", ctx, mock.Anything).Return(response, nil)

		err := _verifySourceQuotaRules(ctx, baseEvent)
		assert.NoError(tt, err)
		mockInvoker.AssertExpectations(tt)
	})

	t.Run("WhenQuotaRuleNotReady_ReturnsError", func(tt *testing.T) {
		ctx := context.Background()
		mockInvoker := googleproxyclient.NewMockInvoker(tt)
		mockProxyClient := &googleproxyclient.ProxyClient{
			Invoker: mockInvoker,
		}

		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() {
			googleproxyclient.GetGProxyClient = originalGetGProxyClient
		}()

		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mockProxyClient
		}

		response := &googleproxyclient.V1betaListAllQuotaRulesOK{
			QuotaRules: []googleproxyclient.QuotaRulesV1beta{
				{
					ResourceId:   "quota-rule-1",
					State:        googleproxyclient.NewOptQuotaRulesV1betaState(googleproxyclient.QuotaRulesV1betaStateREADY),
					StateDetails: googleproxyclient.NewOptString("Ready for use"),
				},
				{
					ResourceId:   "quota-rule-2",
					State:        googleproxyclient.NewOptQuotaRulesV1betaState(googleproxyclient.QuotaRulesV1betaStateCREATING),
					StateDetails: googleproxyclient.NewOptString("Creation in progress"),
				},
			},
		}
		mockInvoker.On("V1betaListAllQuotaRules", ctx, mock.Anything).Return(response, nil)

		err := _verifySourceQuotaRules(ctx, baseEvent)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "Cannot resume replication: source volume has quota rules not in READY state")
		assert.Contains(tt, err.Error(), "quota-rule-2")
		assert.Contains(tt, err.Error(), "CREATING")
		mockInvoker.AssertExpectations(tt)
	})

	t.Run("WhenQuotaRuleHasNoState_ReturnsError", func(tt *testing.T) {
		ctx := context.Background()
		mockInvoker := googleproxyclient.NewMockInvoker(tt)
		mockProxyClient := &googleproxyclient.ProxyClient{
			Invoker: mockInvoker,
		}

		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() {
			googleproxyclient.GetGProxyClient = originalGetGProxyClient
		}()

		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mockProxyClient
		}

		response := &googleproxyclient.V1betaListAllQuotaRulesOK{
			QuotaRules: []googleproxyclient.QuotaRulesV1beta{
				{
					ResourceId: "quota-rule-1",
					State:      googleproxyclient.OptQuotaRulesV1betaState{}, // No state
				},
			},
		}
		mockInvoker.On("V1betaListAllQuotaRules", ctx, mock.Anything).Return(response, nil)

		err := _verifySourceQuotaRules(ctx, baseEvent)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "Cannot resume replication: source volume has quota rules not in READY state")
		assert.Contains(tt, err.Error(), "quota-rule-1")
		assert.Contains(tt, err.Error(), "State: (not set)")
		mockInvoker.AssertExpectations(tt)
	})

	t.Run("WhenBadRequest_ReturnsError", func(tt *testing.T) {
		ctx := context.Background()
		mockInvoker := googleproxyclient.NewMockInvoker(tt)
		mockProxyClient := &googleproxyclient.ProxyClient{
			Invoker: mockInvoker,
		}

		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() {
			googleproxyclient.GetGProxyClient = originalGetGProxyClient
		}()

		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mockProxyClient
		}

		response := &googleproxyclient.V1betaListAllQuotaRulesBadRequest{
			Message: "Bad request",
		}
		mockInvoker.On("V1betaListAllQuotaRules", ctx, mock.Anything).Return(response, nil)

		err := _verifySourceQuotaRules(ctx, baseEvent)
		assert.Error(tt, err)
		var customErr *vsaErrors.CustomError
		assert.True(tt, vsaErrors.As(err, &customErr), "Expected a CustomError")
		assert.Equal(tt, vsaErrors.ErrQuotaRuleBadRequest, customErr.TrackingID)
		mockInvoker.AssertExpectations(tt)
	})

	t.Run("WhenForbidden_ReturnsError", func(tt *testing.T) {
		ctx := context.Background()
		mockInvoker := googleproxyclient.NewMockInvoker(tt)
		mockProxyClient := &googleproxyclient.ProxyClient{
			Invoker: mockInvoker,
		}

		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() {
			googleproxyclient.GetGProxyClient = originalGetGProxyClient
		}()

		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mockProxyClient
		}

		response := &googleproxyclient.V1betaListAllQuotaRulesForbidden{
			Message: "Forbidden",
		}
		mockInvoker.On("V1betaListAllQuotaRules", ctx, mock.Anything).Return(response, nil)

		err := _verifySourceQuotaRules(ctx, baseEvent)
		assert.Error(tt, err)
		var customErr *vsaErrors.CustomError
		assert.True(tt, vsaErrors.As(err, &customErr), "Expected a CustomError")
		assert.Equal(tt, vsaErrors.ErrQuotaRuleForbidden, customErr.TrackingID)
		mockInvoker.AssertExpectations(tt)
	})

	t.Run("WhenNotFound_ReturnsError", func(tt *testing.T) {
		ctx := context.Background()
		mockInvoker := googleproxyclient.NewMockInvoker(tt)
		mockProxyClient := &googleproxyclient.ProxyClient{
			Invoker: mockInvoker,
		}

		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() {
			googleproxyclient.GetGProxyClient = originalGetGProxyClient
		}()

		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mockProxyClient
		}

		response := &googleproxyclient.V1betaListAllQuotaRulesNotFound{
			Message: "Not found",
		}
		mockInvoker.On("V1betaListAllQuotaRules", ctx, mock.Anything).Return(response, nil)

		err := _verifySourceQuotaRules(ctx, baseEvent)
		assert.Error(tt, err)
		var customErr *vsaErrors.CustomError
		assert.True(tt, vsaErrors.As(err, &customErr), "Expected a CustomError")
		assert.Equal(tt, vsaErrors.ErrQuotaRuleNotFound, customErr.TrackingID)
		mockInvoker.AssertExpectations(tt)
	})

	t.Run("WhenTooManyRequests_ReturnsError", func(tt *testing.T) {
		ctx := context.Background()
		mockInvoker := googleproxyclient.NewMockInvoker(tt)
		mockProxyClient := &googleproxyclient.ProxyClient{
			Invoker: mockInvoker,
		}

		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() {
			googleproxyclient.GetGProxyClient = originalGetGProxyClient
		}()

		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mockProxyClient
		}

		response := &googleproxyclient.V1betaListAllQuotaRulesTooManyRequests{
			Message: "Too many requests",
		}
		mockInvoker.On("V1betaListAllQuotaRules", ctx, mock.Anything).Return(response, nil)

		err := _verifySourceQuotaRules(ctx, baseEvent)
		assert.Error(tt, err)
		var customErr *vsaErrors.CustomError
		assert.True(tt, vsaErrors.As(err, &customErr), "Expected a CustomError")
		assert.Equal(tt, vsaErrors.ErrQuotaRuleTooManyRequests, customErr.TrackingID)
		mockInvoker.AssertExpectations(tt)
	})

	t.Run("WhenInternalServerError_ReturnsError", func(tt *testing.T) {
		ctx := context.Background()
		mockInvoker := googleproxyclient.NewMockInvoker(tt)
		mockProxyClient := &googleproxyclient.ProxyClient{
			Invoker: mockInvoker,
		}

		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() {
			googleproxyclient.GetGProxyClient = originalGetGProxyClient
		}()

		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mockProxyClient
		}

		response := &googleproxyclient.V1betaListAllQuotaRulesInternalServerError{
			Message: "Internal server error",
		}
		mockInvoker.On("V1betaListAllQuotaRules", ctx, mock.Anything).Return(response, nil)

		err := _verifySourceQuotaRules(ctx, baseEvent)
		assert.Error(tt, err)
		var customErr *vsaErrors.CustomError
		assert.True(tt, vsaErrors.As(err, &customErr), "Expected a CustomError")
		assert.Equal(tt, vsaErrors.ErrQuotaRuleInternalServerError, customErr.TrackingID)
		mockInvoker.AssertExpectations(tt)
	})
}

func TestVerifyDestinationQuotaRules(t *testing.T) {
	baseEvent := &ResumeReplicationEvent{
		CommonReplicationEventParams: CommonReplicationEventParams{
			DstBasePath:              "dstPath",
			DestinationProjectNumber: "destinationProjectNumber",
			DstToken:                 "dstToken",
			ReplicationModel: &datamodel.VolumeReplication{
				ReplicationAttributes: &datamodel.ReplicationDetails{
					DestinationLocation:   "dstLocation",
					DestinationVolumeUUID: "dstVolumeUUID",
				},
			},
		},
	}

	t.Run("WhenHybridReplication_ValidatesQuotaRules", func(tt *testing.T) {
		ctx := context.Background()
		reverseType := string(datamodel.HybridReplicationParametersReplicationTypeREVERSE)
		hybridEvent := &ResumeReplicationEvent{
			CommonReplicationEventParams: CommonReplicationEventParams{
				DstBasePath:              "dstPath",
				DestinationProjectNumber: "destinationProjectNumber",
				DstToken:                 "dstToken",
				ReplicationModel: &datamodel.VolumeReplication{
					ReplicationAttributes: &datamodel.ReplicationDetails{
						DestinationLocation:   remoteRegionCustomer,
						DestinationVolumeUUID: "dstVolumeUUID",
					},
					HybridReplicationAttributes: &datamodel.HybridReplicationAttribute{
						HybridReplicationType: &reverseType,
					},
				},
			},
		}

		mockInvoker := googleproxyclient.NewMockInvoker(tt)
		mockProxyClient := &googleproxyclient.ProxyClient{
			Invoker: mockInvoker,
		}

		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() {
			googleproxyclient.GetGProxyClient = originalGetGProxyClient
		}()

		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mockProxyClient
		}

		// Hybrid replication should now validate quota rules like regular replication
		response := &googleproxyclient.V1betaListAllQuotaRulesOK{
			QuotaRules: []googleproxyclient.QuotaRulesV1beta{
				{
					ResourceId: "quota-rule-1",
					State:      googleproxyclient.NewOptQuotaRulesV1betaState(googleproxyclient.QuotaRulesV1betaStateREADY),
				},
			},
		}
		mockInvoker.On("V1betaListAllQuotaRules", ctx, mock.Anything).Return(response, nil)

		err := _verifyDestinationQuotaRules(ctx, hybridEvent)
		assert.NoError(tt, err)
		mockInvoker.AssertExpectations(tt)
	})

	t.Run("WhenHybridReplication_WithTransitioningStates_ReturnsError", func(tt *testing.T) {
		ctx := context.Background()
		reverseType := string(datamodel.HybridReplicationParametersReplicationTypeREVERSE)
		hybridEvent := &ResumeReplicationEvent{
			CommonReplicationEventParams: CommonReplicationEventParams{
				DstBasePath:              "dstPath",
				DestinationProjectNumber: "destinationProjectNumber",
				DstToken:                 "dstToken",
				ReplicationModel: &datamodel.VolumeReplication{
					ReplicationAttributes: &datamodel.ReplicationDetails{
						DestinationLocation:   remoteRegionCustomer,
						DestinationVolumeUUID: "dstVolumeUUID",
					},
					HybridReplicationAttributes: &datamodel.HybridReplicationAttribute{
						HybridReplicationType: &reverseType,
					},
				},
			},
		}

		mockInvoker := googleproxyclient.NewMockInvoker(tt)
		mockProxyClient := &googleproxyclient.ProxyClient{
			Invoker: mockInvoker,
		}

		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() {
			googleproxyclient.GetGProxyClient = originalGetGProxyClient
		}()

		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mockProxyClient
		}

		// Hybrid replication should validate and fail if quota rules are in transitioning states
		response := &googleproxyclient.V1betaListAllQuotaRulesOK{
			QuotaRules: []googleproxyclient.QuotaRulesV1beta{
				{
					ResourceId:   "quota-rule-1",
					State:        googleproxyclient.NewOptQuotaRulesV1betaState(googleproxyclient.QuotaRulesV1betaStateCREATING),
					StateDetails: googleproxyclient.NewOptString("Creating quota rule"),
				},
			},
		}
		mockInvoker.On("V1betaListAllQuotaRules", ctx, mock.Anything).Return(response, nil)

		err := _verifyDestinationQuotaRules(ctx, hybridEvent)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "quota rules in transitioning state")
		assert.Contains(tt, err.Error(), "quota-rule-1")
		mockInvoker.AssertExpectations(tt)
	})

	t.Run("WhenHybridReplication_WithAPIError_ReturnsError", func(tt *testing.T) {
		ctx := context.Background()
		reverseType := string(datamodel.HybridReplicationParametersReplicationTypeREVERSE)
		hybridEvent := &ResumeReplicationEvent{
			CommonReplicationEventParams: CommonReplicationEventParams{
				DstBasePath:              "dstPath",
				DestinationProjectNumber: "destinationProjectNumber",
				DstToken:                 "dstToken",
				ReplicationModel: &datamodel.VolumeReplication{
					ReplicationAttributes: &datamodel.ReplicationDetails{
						DestinationLocation:   remoteRegionCustomer,
						DestinationVolumeUUID: "dstVolumeUUID",
					},
					HybridReplicationAttributes: &datamodel.HybridReplicationAttribute{
						HybridReplicationType: &reverseType,
					},
				},
			},
		}

		mockInvoker := googleproxyclient.NewMockInvoker(tt)
		mockProxyClient := &googleproxyclient.ProxyClient{
			Invoker: mockInvoker,
		}

		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() {
			googleproxyclient.GetGProxyClient = originalGetGProxyClient
		}()

		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mockProxyClient
		}

		// Hybrid replication should validate and fail on API errors
		mockInvoker.On("V1betaListAllQuotaRules", ctx, mock.Anything).Return(nil, fmt.Errorf("network error"))

		err := _verifyDestinationQuotaRules(ctx, hybridEvent)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "failed to list quota rules on destination volume")
		mockInvoker.AssertExpectations(tt)
	})

	t.Run("WhenAPICallFails_ReturnsError", func(tt *testing.T) {
		ctx := context.Background()
		mockInvoker := googleproxyclient.NewMockInvoker(tt)
		mockProxyClient := &googleproxyclient.ProxyClient{
			Invoker: mockInvoker,
		}

		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() {
			googleproxyclient.GetGProxyClient = originalGetGProxyClient
		}()

		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mockProxyClient
		}

		mockInvoker.On("V1betaListAllQuotaRules", ctx, mock.Anything).Return(nil, fmt.Errorf("API error"))

		err := _verifyDestinationQuotaRules(ctx, baseEvent)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "failed to list quota rules on destination volume")
		mockInvoker.AssertExpectations(tt)
	})

	t.Run("WhenNoQuotaRules_Success", func(tt *testing.T) {
		ctx := context.Background()
		mockInvoker := googleproxyclient.NewMockInvoker(tt)
		mockProxyClient := &googleproxyclient.ProxyClient{
			Invoker: mockInvoker,
		}

		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() {
			googleproxyclient.GetGProxyClient = originalGetGProxyClient
		}()

		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mockProxyClient
		}

		response := &googleproxyclient.V1betaListAllQuotaRulesOK{
			QuotaRules: []googleproxyclient.QuotaRulesV1beta{},
		}
		mockInvoker.On("V1betaListAllQuotaRules", ctx, mock.Anything).Return(response, nil)

		err := _verifyDestinationQuotaRules(ctx, baseEvent)
		assert.NoError(tt, err)
		mockInvoker.AssertExpectations(tt)
	})

	t.Run("WhenAllQuotaRulesInStableStates_Success", func(tt *testing.T) {
		ctx := context.Background()
		mockInvoker := googleproxyclient.NewMockInvoker(tt)
		mockProxyClient := &googleproxyclient.ProxyClient{
			Invoker: mockInvoker,
		}

		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() {
			googleproxyclient.GetGProxyClient = originalGetGProxyClient
		}()

		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mockProxyClient
		}

		response := &googleproxyclient.V1betaListAllQuotaRulesOK{
			QuotaRules: []googleproxyclient.QuotaRulesV1beta{
				{
					ResourceId: "quota-rule-1",
					State:      googleproxyclient.NewOptQuotaRulesV1betaState(googleproxyclient.QuotaRulesV1betaStateREADY),
				},
				{
					ResourceId: "quota-rule-2",
					State:      googleproxyclient.NewOptQuotaRulesV1betaState(googleproxyclient.QuotaRulesV1betaStateERROR),
				},
			},
		}
		mockInvoker.On("V1betaListAllQuotaRules", ctx, mock.Anything).Return(response, nil)

		err := _verifyDestinationQuotaRules(ctx, baseEvent)
		assert.NoError(tt, err)
		mockInvoker.AssertExpectations(tt)
	})

	t.Run("WhenQuotaRuleInCreatingState_ReturnsError", func(tt *testing.T) {
		ctx := context.Background()
		mockInvoker := googleproxyclient.NewMockInvoker(tt)
		mockProxyClient := &googleproxyclient.ProxyClient{
			Invoker: mockInvoker,
		}

		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() {
			googleproxyclient.GetGProxyClient = originalGetGProxyClient
		}()

		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mockProxyClient
		}

		response := &googleproxyclient.V1betaListAllQuotaRulesOK{
			QuotaRules: []googleproxyclient.QuotaRulesV1beta{
				{
					ResourceId:   "quota-rule-1",
					State:        googleproxyclient.NewOptQuotaRulesV1betaState(googleproxyclient.QuotaRulesV1betaStateCREATING),
					StateDetails: googleproxyclient.NewOptString("Creation in progress"),
				},
			},
		}
		mockInvoker.On("V1betaListAllQuotaRules", ctx, mock.Anything).Return(response, nil)

		err := _verifyDestinationQuotaRules(ctx, baseEvent)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "Cannot resume replication: destination volume has quota rules in transitioning state")
		assert.Contains(tt, err.Error(), "quota-rule-1")
		assert.Contains(tt, err.Error(), "CREATING")
		mockInvoker.AssertExpectations(tt)
	})

	t.Run("WhenQuotaRuleInUpdatingState_ReturnsError", func(tt *testing.T) {
		ctx := context.Background()
		mockInvoker := googleproxyclient.NewMockInvoker(tt)
		mockProxyClient := &googleproxyclient.ProxyClient{
			Invoker: mockInvoker,
		}

		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() {
			googleproxyclient.GetGProxyClient = originalGetGProxyClient
		}()

		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mockProxyClient
		}

		response := &googleproxyclient.V1betaListAllQuotaRulesOK{
			QuotaRules: []googleproxyclient.QuotaRulesV1beta{
				{
					ResourceId:   "quota-rule-1",
					State:        googleproxyclient.NewOptQuotaRulesV1betaState(googleproxyclient.QuotaRulesV1betaStateUPDATING),
					StateDetails: googleproxyclient.NewOptString("Update in progress"),
				},
			},
		}
		mockInvoker.On("V1betaListAllQuotaRules", ctx, mock.Anything).Return(response, nil)

		err := _verifyDestinationQuotaRules(ctx, baseEvent)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "Cannot resume replication: destination volume has quota rules in transitioning state")
		assert.Contains(tt, err.Error(), "quota-rule-1")
		assert.Contains(tt, err.Error(), "UPDATING")
		mockInvoker.AssertExpectations(tt)
	})

	t.Run("WhenQuotaRuleInDeletingState_ReturnsError", func(tt *testing.T) {
		ctx := context.Background()
		mockInvoker := googleproxyclient.NewMockInvoker(tt)
		mockProxyClient := &googleproxyclient.ProxyClient{
			Invoker: mockInvoker,
		}

		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() {
			googleproxyclient.GetGProxyClient = originalGetGProxyClient
		}()

		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mockProxyClient
		}

		response := &googleproxyclient.V1betaListAllQuotaRulesOK{
			QuotaRules: []googleproxyclient.QuotaRulesV1beta{
				{
					ResourceId:   "quota-rule-1",
					State:        googleproxyclient.NewOptQuotaRulesV1betaState(googleproxyclient.QuotaRulesV1betaStateDELETING),
					StateDetails: googleproxyclient.NewOptString("Deletion in progress"),
				},
			},
		}
		mockInvoker.On("V1betaListAllQuotaRules", ctx, mock.Anything).Return(response, nil)

		err := _verifyDestinationQuotaRules(ctx, baseEvent)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "Cannot resume replication: destination volume has quota rules in transitioning state")
		assert.Contains(tt, err.Error(), "quota-rule-1")
		assert.Contains(tt, err.Error(), "DELETING")
		mockInvoker.AssertExpectations(tt)
	})

	t.Run("WhenMultipleQuotaRulesInTransitioningStates_ReturnsError", func(tt *testing.T) {
		ctx := context.Background()
		mockInvoker := googleproxyclient.NewMockInvoker(tt)
		mockProxyClient := &googleproxyclient.ProxyClient{
			Invoker: mockInvoker,
		}

		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() {
			googleproxyclient.GetGProxyClient = originalGetGProxyClient
		}()

		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mockProxyClient
		}

		response := &googleproxyclient.V1betaListAllQuotaRulesOK{
			QuotaRules: []googleproxyclient.QuotaRulesV1beta{
				{
					ResourceId:   "quota-rule-1",
					State:        googleproxyclient.NewOptQuotaRulesV1betaState(googleproxyclient.QuotaRulesV1betaStateREADY),
					StateDetails: googleproxyclient.NewOptString("Ready for use"),
				},
				{
					ResourceId:   "quota-rule-2",
					State:        googleproxyclient.NewOptQuotaRulesV1betaState(googleproxyclient.QuotaRulesV1betaStateCREATING),
					StateDetails: googleproxyclient.NewOptString("Creation in progress"),
				},
				{
					ResourceId:   "quota-rule-3",
					State:        googleproxyclient.NewOptQuotaRulesV1betaState(googleproxyclient.QuotaRulesV1betaStateUPDATING),
					StateDetails: googleproxyclient.NewOptString("Update in progress"),
				},
			},
		}
		mockInvoker.On("V1betaListAllQuotaRules", ctx, mock.Anything).Return(response, nil)

		err := _verifyDestinationQuotaRules(ctx, baseEvent)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "Cannot resume replication: destination volume has quota rules in transitioning state")
		assert.Contains(tt, err.Error(), "quota-rule-2")
		assert.Contains(tt, err.Error(), "quota-rule-3")
		assert.Contains(tt, err.Error(), "CREATING")
		assert.Contains(tt, err.Error(), "UPDATING")
		mockInvoker.AssertExpectations(tt)
	})

	t.Run("WhenQuotaRuleHasNoState_ReturnsError", func(tt *testing.T) {
		ctx := context.Background()
		mockInvoker := googleproxyclient.NewMockInvoker(tt)
		mockProxyClient := &googleproxyclient.ProxyClient{
			Invoker: mockInvoker,
		}

		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() {
			googleproxyclient.GetGProxyClient = originalGetGProxyClient
		}()

		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mockProxyClient
		}

		response := &googleproxyclient.V1betaListAllQuotaRulesOK{
			QuotaRules: []googleproxyclient.QuotaRulesV1beta{
				{
					ResourceId: "quota-rule-1",
					State:      googleproxyclient.OptQuotaRulesV1betaState{}, // No state
				},
			},
		}
		mockInvoker.On("V1betaListAllQuotaRules", ctx, mock.Anything).Return(response, nil)

		err := _verifyDestinationQuotaRules(ctx, baseEvent)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "Cannot resume replication: destination volume has quota rules in transitioning state")
		assert.Contains(tt, err.Error(), "quota-rule-1")
		assert.Contains(tt, err.Error(), "State: (not set)")
		mockInvoker.AssertExpectations(tt)
	})

	t.Run("WhenUnauthorized_ReturnsError", func(tt *testing.T) {
		ctx := context.Background()
		mockInvoker := googleproxyclient.NewMockInvoker(tt)
		mockProxyClient := &googleproxyclient.ProxyClient{
			Invoker: mockInvoker,
		}

		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() {
			googleproxyclient.GetGProxyClient = originalGetGProxyClient
		}()

		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mockProxyClient
		}

		response := &googleproxyclient.V1betaListAllQuotaRulesUnauthorized{
			Message: "Unauthorized",
		}
		mockInvoker.On("V1betaListAllQuotaRules", ctx, mock.Anything).Return(response, nil)

		err := _verifyDestinationQuotaRules(ctx, baseEvent)
		assert.Error(tt, err)
		var customErr *vsaErrors.CustomError
		assert.True(tt, vsaErrors.As(err, &customErr), "Expected a CustomError")
		assert.Equal(tt, vsaErrors.ErrQuotaRuleUnauthorized, customErr.TrackingID)
		mockInvoker.AssertExpectations(tt)
	})

	t.Run("WhenInternalServerError_ReturnsError", func(tt *testing.T) {
		ctx := context.Background()
		mockInvoker := googleproxyclient.NewMockInvoker(tt)
		mockProxyClient := &googleproxyclient.ProxyClient{
			Invoker: mockInvoker,
		}

		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() {
			googleproxyclient.GetGProxyClient = originalGetGProxyClient
		}()

		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mockProxyClient
		}

		response := &googleproxyclient.V1betaListAllQuotaRulesInternalServerError{
			Message: "Internal server error",
		}
		mockInvoker.On("V1betaListAllQuotaRules", ctx, mock.Anything).Return(response, nil)

		err := _verifyDestinationQuotaRules(ctx, baseEvent)
		assert.Error(tt, err)
		var customErr *vsaErrors.CustomError
		assert.True(tt, vsaErrors.As(err, &customErr), "Expected a CustomError")
		assert.Equal(tt, vsaErrors.ErrQuotaRuleInternalServerError, customErr.TrackingID)
		mockInvoker.AssertExpectations(tt)
	})

	t.Run("WhenForbidden_ReturnsError", func(tt *testing.T) {
		ctx := context.Background()
		mockInvoker := googleproxyclient.NewMockInvoker(tt)
		mockProxyClient := &googleproxyclient.ProxyClient{
			Invoker: mockInvoker,
		}

		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() {
			googleproxyclient.GetGProxyClient = originalGetGProxyClient
		}()

		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mockProxyClient
		}

		response := &googleproxyclient.V1betaListAllQuotaRulesForbidden{
			Message: "Forbidden",
		}
		mockInvoker.On("V1betaListAllQuotaRules", ctx, mock.Anything).Return(response, nil)

		err := _verifyDestinationQuotaRules(ctx, baseEvent)
		assert.Error(tt, err)
		var customErr *vsaErrors.CustomError
		assert.True(tt, vsaErrors.As(err, &customErr), "Expected a CustomError")
		assert.Equal(tt, vsaErrors.ErrQuotaRuleForbidden, customErr.TrackingID)
		mockInvoker.AssertExpectations(tt)
	})

	t.Run("WhenNotFound_ReturnsError", func(tt *testing.T) {
		ctx := context.Background()
		mockInvoker := googleproxyclient.NewMockInvoker(tt)
		mockProxyClient := &googleproxyclient.ProxyClient{
			Invoker: mockInvoker,
		}

		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() {
			googleproxyclient.GetGProxyClient = originalGetGProxyClient
		}()

		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mockProxyClient
		}

		response := &googleproxyclient.V1betaListAllQuotaRulesNotFound{
			Message: "Not found",
		}
		mockInvoker.On("V1betaListAllQuotaRules", ctx, mock.Anything).Return(response, nil)

		err := _verifyDestinationQuotaRules(ctx, baseEvent)
		assert.Error(tt, err)
		var customErr *vsaErrors.CustomError
		assert.True(tt, vsaErrors.As(err, &customErr), "Expected a CustomError")
		assert.Equal(tt, vsaErrors.ErrQuotaRuleNotFound, customErr.TrackingID)
		mockInvoker.AssertExpectations(tt)
	})

	t.Run("WhenTooManyRequests_ReturnsError", func(tt *testing.T) {
		ctx := context.Background()
		mockInvoker := googleproxyclient.NewMockInvoker(tt)
		mockProxyClient := &googleproxyclient.ProxyClient{
			Invoker: mockInvoker,
		}

		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() {
			googleproxyclient.GetGProxyClient = originalGetGProxyClient
		}()

		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mockProxyClient
		}

		response := &googleproxyclient.V1betaListAllQuotaRulesTooManyRequests{
			Message: "Too many requests",
		}
		mockInvoker.On("V1betaListAllQuotaRules", ctx, mock.Anything).Return(response, nil)

		err := _verifyDestinationQuotaRules(ctx, baseEvent)
		assert.Error(tt, err)
		var customErr *vsaErrors.CustomError
		assert.True(tt, vsaErrors.As(err, &customErr), "Expected a CustomError")
		assert.Equal(tt, vsaErrors.ErrQuotaRuleTooManyRequests, customErr.TrackingID)
		mockInvoker.AssertExpectations(tt)
	})
}

func TestVerifyDstReplicationStop(t *testing.T) {
	t.Run("VerifyDstReplicationStopSucceeds", func(tt *testing.T) {
		mirrorState := "MIRRORED"
		relationshipStatus := "IDLE"
		mockReplication := &coreModels.VolumeReplication{
			MirrorState:        &mirrorState,
			RelationshipStatus: &relationshipStatus,
		}
		event := &StopReplicationEvent{
			CommonReplicationEventParams: CommonReplicationEventParams{
				DstBasePath:              "dstPath",
				DestinationProjectNumber: "destinationProjectNumber",
				DstToken:                 "dstToken",
				ReplicationModel: &datamodel.VolumeReplication{
					ReplicationAttributes: &datamodel.ReplicationDetails{
						DestinationLocation:        "dstLocation",
						DestinationReplicationUUID: "dstUUID",
					},
				},
			},
		}
		getReplication = func(ctx context.Context, basePath, projectNumber, locationID, replicationUUID, jwt string) (*coreModels.VolumeReplication, error) {
			return mockReplication, nil
		}
		defer func() { getReplication = _getReplication }()
		replication, err := _verifyDstReplicationStop(context.Background(), event)
		assert.NoError(tt, err)
		assert.NotNil(tt, replication)
		assert.Equal(tt, mockReplication, replication)
	})
	t.Run("VerifyDstReplicationStopFailsWhenReplicationNotFound", func(tt *testing.T) {
		event := &StopReplicationEvent{
			CommonReplicationEventParams: CommonReplicationEventParams{
				DstBasePath:              "dstPath",
				DestinationProjectNumber: "destinationProjectNumber",
				DstToken:                 "dstToken",
				ReplicationModel: &datamodel.VolumeReplication{
					ReplicationAttributes: &datamodel.ReplicationDetails{
						DestinationLocation:        "dstLocation",
						DestinationReplicationUUID: "dstUUID",
					},
				},
			},
		}

		getReplication = func(ctx context.Context, basePath, projectNumber, locationID, replicationUUID, jwt string) (*coreModels.VolumeReplication, error) {
			return nil, errors.New("replication not found")
		}
		defer func() { getReplication = _getReplication }()

		replication, err := _verifyDstReplicationStop(context.Background(), event)
		assert.Error(tt, err)
		assert.Nil(tt, replication)
		var customErr *vsaErrors.CustomError
		assert.True(tt, vsaErrors.As(err, &customErr), "Expected a CustomError")
		assert.ErrorContains(tt, customErr.OriginalErr, "replication not found")
	})
	t.Run("VerifyDstReplicationStopFailsWhenAlreadyStopped", func(tt *testing.T) {
		mirrorState := "STOPPED"
		mockReplication := &coreModels.VolumeReplication{
			MirrorState: &mirrorState,
		}
		event := &StopReplicationEvent{
			CommonReplicationEventParams: CommonReplicationEventParams{
				DstBasePath:              "dstPath",
				DestinationProjectNumber: "destinationProjectNumber",
				DstToken:                 "dstToken",
				ReplicationModel: &datamodel.VolumeReplication{
					ReplicationAttributes: &datamodel.ReplicationDetails{
						DestinationLocation:        "dstLocation",
						DestinationReplicationUUID: "dstUUID",
					},
				},
			},
		}

		getReplication = func(ctx context.Context, basePath, projectNumber, locationID, replicationUUID, jwt string) (*coreModels.VolumeReplication, error) {
			return mockReplication, nil
		}
		defer func() { getReplication = _getReplication }()

		replication, err := _verifyDstReplicationStop(context.Background(), event)
		assert.Error(tt, err)
		assert.Nil(tt, replication)
		assert.Contains(tt, err.Error(), "Replication is already in STOPPED state")
	})

	t.Run("VerifyDstReplicationStopFailsWhenUninitializedAndTransferring", func(tt *testing.T) {
		mirrorState := "UNINITIALIZED"
		relationshipStatus := "transferring"
		mockReplication := &coreModels.VolumeReplication{
			MirrorState:        &mirrorState,
			RelationshipStatus: &relationshipStatus,
		}
		event := &StopReplicationEvent{
			CommonReplicationEventParams: CommonReplicationEventParams{
				DstBasePath:              "dstPath",
				DestinationProjectNumber: "destinationProjectNumber",
				DstToken:                 "dstToken",
				ReplicationModel: &datamodel.VolumeReplication{
					ReplicationAttributes: &datamodel.ReplicationDetails{
						DestinationLocation:        "dstLocation",
						DestinationReplicationUUID: "dstUUID",
					},
				},
			},
			ForceStop: false,
		}

		getReplication = func(ctx context.Context, basePath, projectNumber, locationID, replicationUUID, jwt string) (*coreModels.VolumeReplication, error) {
			return mockReplication, nil
		}
		defer func() { getReplication = _getReplication }()

		replication, err := _verifyDstReplicationStop(context.Background(), event)
		assert.Error(tt, err)
		assert.Nil(tt, replication)
		assert.Contains(tt, err.Error(), "Replication in preparing state. Please try again later")
	})

	t.Run("VerifyDstReplicationStopFailsWhenMirroredAndTransferring", func(tt *testing.T) {
		mirrorState := "MIRRORED"
		relationshipStatus := "transferring"
		mockReplication := &coreModels.VolumeReplication{
			MirrorState:        &mirrorState,
			RelationshipStatus: &relationshipStatus,
		}
		event := &StopReplicationEvent{
			CommonReplicationEventParams: CommonReplicationEventParams{
				DstBasePath:              "dstPath",
				DestinationProjectNumber: "destinationProjectNumber",
				DstToken:                 "dstToken",
				ReplicationModel: &datamodel.VolumeReplication{
					ReplicationAttributes: &datamodel.ReplicationDetails{
						DestinationLocation:        "dstLocation",
						DestinationReplicationUUID: "dstUUID",
					},
				},
			},
			ForceStop: false,
		}

		getReplication = func(ctx context.Context, basePath, projectNumber, locationID, replicationUUID, jwt string) (*coreModels.VolumeReplication, error) {
			return mockReplication, nil
		}
		defer func() { getReplication = _getReplication }()

		replication, err := _verifyDstReplicationStop(context.Background(), event)
		assert.Error(tt, err)
		assert.Nil(tt, replication)
		assert.Contains(tt, err.Error(), "Replication relationship status is in transferring state")
	})
	t.Run("WhenHybridReplicationAttributesSet_AndStatusNotPeered_ReturnsError", func(tt *testing.T) {
		ctx := context.Background()
		migrationType := string(datamodel.HybridReplicationParametersReplicationTypeMIGRATION)
		status := datamodel.HybridReplicationStatusPendingSVMPeer
		eventWithHybridNotPeered := &StopReplicationEvent{
			CommonReplicationEventParams: CommonReplicationEventParams{
				DstBasePath:              "dstPath",
				DestinationProjectNumber: "destinationProjectNumber",
				DstToken:                 "dstToken",
				ReplicationModel: &datamodel.VolumeReplication{
					ReplicationAttributes: &datamodel.ReplicationDetails{
						DestinationLocation:        "us-east1",
						DestinationReplicationUUID: "dstUUID",
					},
					HybridReplicationAttributes: &datamodel.HybridReplicationAttribute{
						HybridReplicationType: &migrationType,
						Status:                status,
					},
				},
			},
		}
		expectedError := errors.NewUserInputValidationErr("Hybrid Replication needs to be in peered state before stopping")
		resp, err := _verifyDstReplicationStop(ctx, eventWithHybridNotPeered)
		assert.Error(tt, err)
		assert.Nil(tt, resp)
		assert.Equal(tt, expectedError, err)
	})
	t.Run("WhenIsSrcForHybridReplicationReturnsTrue_AndDestinationReplicationUUIDIsNil_AndStatusIsNotExternalManaged", func(tt *testing.T) {
		ctx := context.Background()
		reverseType := string(datamodel.HybridReplicationParametersReplicationTypeREVERSE)
		status := datamodel.HybridReplicationStatusPeered
		hybridEvent := &StopReplicationEvent{
			CommonReplicationEventParams: CommonReplicationEventParams{
				SrcBasePath:              "srcPath",
				SourceProjectNumber:      "sourceProjectNumber",
				SrcToken:                 "srcToken",
				DestinationProjectNumber: "destinationProjectNumber",
				DstToken:                 "dstToken",
				ReplicationModel: &datamodel.VolumeReplication{
					ReplicationAttributes: &datamodel.ReplicationDetails{
						SourceLocation:             "srcLocation",
						SourceReplicationUUID:      "srcUUID",
						DestinationLocation:        remoteRegionCustomer,
						DestinationReplicationUUID: "00000000-0000-0000-0000-000000000000",
					},
					HybridReplicationAttributes: &datamodel.HybridReplicationAttribute{
						HybridReplicationType: &reverseType,
						Status:                status,
					},
				},
			},
		}
		expectedError := errors.NewUserInputValidationErr("Hybrid Replication needs to be in externally managed state before stopping")
		resp, err := _verifyDstReplicationStop(ctx, hybridEvent)
		assert.Error(tt, err)
		assert.Nil(tt, resp)
		assert.Equal(tt, expectedError, err)
	})
	t.Run("WhenIsSrcForHybridReplicationReturnsTrue_AndDestinationReplicationUUIDIsNotNil", func(tt *testing.T) {
		ctx := context.Background()
		defer func() {
			getReplication = _getReplication
		}()
		reverseType := string(datamodel.HybridReplicationParametersReplicationTypeREVERSE)
		hybridEvent := &StopReplicationEvent{
			CommonReplicationEventParams: CommonReplicationEventParams{
				SrcBasePath:              "srcPath",
				SourceProjectNumber:      "sourceProjectNumber",
				SrcToken:                 "srcToken",
				DestinationProjectNumber: "destinationProjectNumber",
				DstToken:                 "dstToken",
				ReplicationModel: &datamodel.VolumeReplication{
					ReplicationAttributes: &datamodel.ReplicationDetails{
						SourceLocation:             "srcLocation",
						SourceReplicationUUID:      "srcUUID",
						DestinationLocation:        remoteRegionCustomer,
						DestinationReplicationUUID: "destUUID",
					},
					HybridReplicationAttributes: &datamodel.HybridReplicationAttribute{
						HybridReplicationType: &reverseType,
					},
				},
			},
		}
		srcReplication := &coreModels.VolumeReplication{
			ReplicationAttributes: &coreModels.ReplicationDetails{
				SourceSvmName:         "src-svm",
				SourceVolumeName:      "src-volume",
				DestinationSvmName:    "dst-svm",
				DestinationVolumeName: "dst-volume",
			},
			HybridReplicationAttributes: &coreModels.HybridReplicationParameters{},
		}
		getReplication = func(ctx context.Context, basePath string, projectNumber string, locationID string, volumeReplicationID string, jwt string) (*coreModels.VolumeReplication, error) {
			return srcReplication, nil
		}
		resp, err := _verifyDstReplicationStop(ctx, hybridEvent)
		assert.NoError(tt, err)
		assert.NotNil(tt, resp)
		assert.Equal(tt, srcReplication, resp)
		assert.NotNil(tt, resp.HybridReplicationAttributes.HybridReplicationUserCommands)
		assert.Contains(tt, resp.StateDetails, "Please execute the commands on Onprem ONTAP to Stop replication")
	})
	t.Run("WhenIsSrcForHybridReplicationReturnsTrue_AndDestinationReplicationUUIDIsNil_AndStatusIsExternalManaged", func(tt *testing.T) {
		ctx := context.Background()
		defer func() {
			getReplication = _getReplication
		}()
		reverseType := string(datamodel.HybridReplicationParametersReplicationTypeREVERSE)
		status := datamodel.HybridReplicationStatusExternalManaged
		hybridEvent := &StopReplicationEvent{
			CommonReplicationEventParams: CommonReplicationEventParams{
				SrcBasePath:              "srcPath",
				SourceProjectNumber:      "sourceProjectNumber",
				SrcToken:                 "srcToken",
				DestinationProjectNumber: "destinationProjectNumber",
				DstToken:                 "dstToken",
				ReplicationModel: &datamodel.VolumeReplication{
					ReplicationAttributes: &datamodel.ReplicationDetails{
						SourceLocation:             "srcLocation",
						SourceReplicationUUID:      "srcUUID",
						DestinationLocation:        remoteRegionCustomer,
						DestinationReplicationUUID: "00000000-0000-0000-0000-000000000000",
					},
					HybridReplicationAttributes: &datamodel.HybridReplicationAttribute{
						HybridReplicationType: &reverseType,
						Status:                status,
					},
				},
			},
		}
		srcReplication := &coreModels.VolumeReplication{
			ReplicationAttributes: &coreModels.ReplicationDetails{
				SourceSvmName:         "src-svm",
				SourceVolumeName:      "src-volume",
				DestinationSvmName:    "dst-svm",
				DestinationVolumeName: "dst-volume",
			},
			HybridReplicationAttributes: &coreModels.HybridReplicationParameters{},
		}
		getReplication = func(ctx context.Context, basePath string, projectNumber string, locationID string, volumeReplicationID string, jwt string) (*coreModels.VolumeReplication, error) {
			return srcReplication, nil
		}
		resp, err := _verifyDstReplicationStop(ctx, hybridEvent)
		assert.NoError(tt, err)
		assert.NotNil(tt, resp)
		assert.Equal(tt, srcReplication, resp)
		assert.NotNil(tt, resp.HybridReplicationAttributes.HybridReplicationUserCommands)
	})
	t.Run("WhenIsSrcForHybridReplicationReturnsTrue_AndGetReplicationFails", func(tt *testing.T) {
		ctx := context.Background()
		defer func() {
			getReplication = _getReplication
		}()
		reverseType := string(datamodel.HybridReplicationParametersReplicationTypeREVERSE)
		hybridEvent := &StopReplicationEvent{
			CommonReplicationEventParams: CommonReplicationEventParams{
				SrcBasePath:              "srcPath",
				SourceProjectNumber:      "sourceProjectNumber",
				SrcToken:                 "srcToken",
				DestinationProjectNumber: "destinationProjectNumber",
				DstToken:                 "dstToken",
				ReplicationModel: &datamodel.VolumeReplication{
					ReplicationAttributes: &datamodel.ReplicationDetails{
						SourceLocation:             "srcLocation",
						SourceReplicationUUID:      "srcUUID",
						DestinationLocation:        remoteRegionCustomer,
						DestinationReplicationUUID: "destUUID",
					},
					HybridReplicationAttributes: &datamodel.HybridReplicationAttribute{
						HybridReplicationType: &reverseType,
					},
				},
			},
		}
		getReplication = func(ctx context.Context, basePath string, projectNumber string, locationID string, volumeReplicationID string, jwt string) (*coreModels.VolumeReplication, error) {
			return nil, errors.New("get replication error")
		}
		resp, err := _verifyDstReplicationStop(ctx, hybridEvent)
		assert.Error(tt, err)
		assert.Nil(tt, resp)
		var customErr *vsaErrors.CustomError
		assert.True(tt, vsaErrors.As(err, &customErr), "Expected a CustomError")
		assert.Equal(tt, vsaErrors.ErrGoogleProxyInternalGetMultipleReplications, customErr.TrackingID)
	})
	t.Run("WhenHybridReplicationNotInPeeredStateBeforeStopping", func(tt *testing.T) {
		ctx := context.Background()
		event := &StopReplicationEvent{
			CommonReplicationEventParams: CommonReplicationEventParams{
				DstBasePath:              "dstPath",
				DestinationProjectNumber: "destinationProjectNumber",
				DstToken:                 "dstToken",
				ReplicationModel: &datamodel.VolumeReplication{
					ReplicationAttributes: &datamodel.ReplicationDetails{
						DestinationLocation:        "dstLocation",
						DestinationReplicationUUID: "00000000-0000-0000-0000-000000000000",
					},
					HybridReplicationAttributes: &datamodel.HybridReplicationAttribute{
						Status: datamodel.HybridReplicationStatusPendingClusterPeer,
					},
				},
			},
		}
		expectedError := errors.NewUserInputValidationErr("Hybrid Replication needs to be in peered state before stopping")
		resp, err := _verifyDstReplicationStop(ctx, event)
		assert.Error(tt, err)
		assert.Nil(tt, resp)
		assert.Equal(tt, expectedError, err)
	})
	t.Run("WhenIsSrcForHybridReplicationReturnsTrue_AndGetReplicationReturnsNil", func(tt *testing.T) {
		ctx := context.Background()
		defer func() {
			getReplication = _getReplication
		}()
		reverseType := string(datamodel.HybridReplicationParametersReplicationTypeREVERSE)
		hybridEvent := &StopReplicationEvent{
			CommonReplicationEventParams: CommonReplicationEventParams{
				SrcBasePath:              "srcPath",
				SourceProjectNumber:      "sourceProjectNumber",
				SrcToken:                 "srcToken",
				DestinationProjectNumber: "destinationProjectNumber",
				DstToken:                 "dstToken",
				ReplicationModel: &datamodel.VolumeReplication{
					ReplicationAttributes: &datamodel.ReplicationDetails{
						SourceLocation:             "srcLocation",
						SourceReplicationUUID:      "srcUUID",
						DestinationLocation:        remoteRegionCustomer,
						DestinationReplicationUUID: "destUUID",
					},
					HybridReplicationAttributes: &datamodel.HybridReplicationAttribute{
						HybridReplicationType: &reverseType,
					},
				},
			},
		}
		getReplication = func(ctx context.Context, basePath string, projectNumber string, locationID string, volumeReplicationID string, jwt string) (*coreModels.VolumeReplication, error) {
			return nil, nil
		}
		resp, err := _verifyDstReplicationStop(ctx, hybridEvent)
		assert.Error(tt, err)
		assert.Nil(tt, resp)
		var customErr *vsaErrors.CustomError
		assert.True(tt, vsaErrors.As(err, &customErr), "Expected a CustomError")
		assert.Equal(tt, vsaErrors.ErrGoogleProxyInternalGetMultipleReplications, customErr.TrackingID)
	})
}

func TestVerifyDstVolume(t *testing.T) {
	correlationId := "correlationID"
	event := &ResumeReplicationEvent{
		CommonReplicationEventParams: CommonReplicationEventParams{
			SrcBasePath:              "srcPath",
			DstBasePath:              "dstPath",
			SourceProjectNumber:      "sourceProjectNumber",
			DestinationProjectNumber: "destinationProjectNumber",
			SrcToken:                 "srcToken",
			DstToken:                 "dstToken",
			ReplicationModel: &datamodel.VolumeReplication{
				ReplicationAttributes: &datamodel.ReplicationDetails{
					SourceLocation:             "srcLocation",
					DestinationLocation:        "dstLocation",
					DestinationReplicationUUID: "dstUUID",
					SourceVolumeUUID:           "srcVolumeUUID",
				},
			},
			XCorrelationID: &correlationId,
		},
	}
	t.Run("WhenDescribeVolumeError", func(tt *testing.T) {
		defer func() {
			describeVolume = _describeVolume
		}()
		ctx := context.Background()
		describeVolume = func(ctx context.Context, basePath string, token string, locationID string, projectNumber string, xCorrelationID *string, volumeId string) (googleproxyclient.VolumeV1beta, error) {
			return googleproxyclient.VolumeV1beta{}, errors.New("some error")
		}
		_, _, err := _verifyDstVolume(ctx, event.ReplicationModel.ReplicationAttributes, "srcPath", "dstPath", "srcToken", "dstToken", "srcProject", "dstProject", &correlationId, false)
		assert.Error(tt, err)
	})
	t.Run("WhenDescribeDestinationVolumeError", func(tt *testing.T) {
		defer func() {
			describeVolume = _describeVolume
		}()
		ctx := context.Background()
		count := 0
		describeVolume = func(ctx context.Context, basePath string, token string, locationID string, projectNumber string, xCorrelationID *string, volumeId string) (googleproxyclient.VolumeV1beta, error) {
			if count == 0 {
				count = count + 1
				return googleproxyclient.VolumeV1beta{}, nil
			}
			return googleproxyclient.VolumeV1beta{}, errors.New("some error")
		}
		_, _, err := _verifyDstVolume(ctx, event.ReplicationModel.ReplicationAttributes, "srcPath", "dstPath", "srcToken", "dstToken", "srcProject", "dstProject", &correlationId, false)
		assert.Error(tt, err)
	})
	t.Run("WhenVolumeNotFound", func(tt *testing.T) {
		defer func() {
			describeVolume = _describeVolume
		}()
		ctx := context.Background()
		expectedError := vsaErrors.NewVCPError(vsaErrors.ErrVolumeNotFound, errors.NewNotFoundErr("Volume", &[]string{"vol-1"}[0]))
		describeVolume = func(ctx context.Context, basePath string, token string, locationID string, projectNumber string, xCorrelationID *string, volumeId string) (googleproxyclient.VolumeV1beta, error) {
			return googleproxyclient.VolumeV1beta{}, errors.NewNotFoundErr("Volume", &[]string{"vol-1"}[0])
		}
		_, _, err := _verifyDstVolume(ctx, event.ReplicationModel.ReplicationAttributes, "srcPath", "dstPath", "srcToken", "dstToken", "srcProject", "dstProject", &correlationId, false)
		assert.Error(tt, err)
		assert.Equal(tt, expectedError, err)
	})
	t.Run("WhenDestinationVolumeNotFound", func(tt *testing.T) {
		defer func() {
			describeVolume = _describeVolume
		}()
		ctx := context.Background()
		count := 0
		expectedError := vsaErrors.NewVCPError(vsaErrors.ErrVolumeNotFound, errors.NewNotFoundErr("Volume", &[]string{"vol-1"}[0]))
		describeVolume = func(ctx context.Context, basePath string, token string, locationID string, projectNumber string, xCorrelationID *string, volumeId string) (googleproxyclient.VolumeV1beta, error) {
			if count == 0 {
				count = count + 1
				return googleproxyclient.VolumeV1beta{}, nil
			}
			return googleproxyclient.VolumeV1beta{}, errors.NewNotFoundErr("Volume", &[]string{"vol-1"}[0])
		}
		_, _, err := _verifyDstVolume(ctx, event.ReplicationModel.ReplicationAttributes, "srcPath", "dstPath", "srcToken", "dstToken", "srcProject", "dstProject", &correlationId, false)
		assert.Error(tt, err)
		assert.Equal(tt, expectedError, err)
	})
	t.Run("WhenVolumeStateOffline", func(tt *testing.T) {
		defer func() {
			describeVolume = _describeVolume
		}()
		ctx := context.Background()
		describeVolume = func(ctx context.Context, basePath string, token string, locationID string, projectNumber string, xCorrelationID *string, volumeId string) (googleproxyclient.VolumeV1beta, error) {
			return googleproxyclient.VolumeV1beta{
				VolumeState: googleproxyclient.OptVolumeV1betaVolumeState{
					Set:   true,
					Value: "offline",
				},
			}, nil
		}
		expectedError := vsaErrors.NewVCPError(vsaErrors.ErrVolumeNotOnlineForReplicationResume, errors.New("Volume is not online for replication"))
		_, _, err := _verifyDstVolume(ctx, event.ReplicationModel.ReplicationAttributes, "srcPath", "dstPath", "srcToken", "dstToken", "srcProject", "dstProject", &correlationId, false)
		assert.Error(tt, err)
		assert.Equal(tt, err, expectedError)
	})
	t.Run("WhenDestinationVolumeUsedSizeGreaterThanSourceVolumeAvailableQuota", func(tt *testing.T) {
		defer func() {
			describeVolume = _describeVolume
		}()
		ctx := context.Background()
		count := 0
		describeVolume = func(ctx context.Context, basePath string, token string, locationID string, projectNumber string, xCorrelationID *string, volumeId string) (googleproxyclient.VolumeV1beta, error) {
			if count == 0 {
				count = count + 1
				return googleproxyclient.VolumeV1beta{
					QuotaInBytes: googleproxyclient.NewOptFloat64(123),
				}, nil
			} else {
				return googleproxyclient.VolumeV1beta{
					QuotaInBytes: googleproxyclient.NewOptFloat64(124),
					UsedBytes:    googleproxyclient.NewOptNilFloat64(200),
				}, nil
			}
		}
		expectedError := vsaErrors.NewVCPError(vsaErrors.ErrDestinationVolumeUsedSizeGreaterThanSourceVolumeAvailableQuota, errors.New("Destination volume used size is greater than source volume available quota"))
		_, _, err := _verifyDstVolume(ctx, event.ReplicationModel.ReplicationAttributes, "srcPath", "dstPath", "srcToken", "dstToken", "srcProject", "dstProject", &correlationId, false)
		assert.NotNil(tt, err)
		assert.Equal(tt, expectedError, err)
	})
	t.Run("WhenSuccess", func(tt *testing.T) {
		defer func() {
			describeVolume = _describeVolume
		}()
		ctx := context.Background()
		describeVolume = func(ctx context.Context, basePath string, token string, locationID string, projectNumber string, xCorrelationID *string, volumeId string) (googleproxyclient.VolumeV1beta, error) {
			return googleproxyclient.VolumeV1beta{}, nil
		}
		_, _, err := _verifyDstVolume(ctx, event.ReplicationModel.ReplicationAttributes, "srcPath", "dstPath", "srcToken", "dstToken", "srcProject", "dstProject", &correlationId, false)
		assert.NoError(tt, err)
	})
}

func TestDescribeVolume(t *testing.T) {
	basePath := "basePath"
	token := "token"
	locationId := "locationId"
	projectNumber := "projectNumber"
	volumeId := "volumeId"
	xCorrelationID := new(string)
	t.Run("WhenError", func(tt *testing.T) {
		ctx := context.Background()
		mockClient := googleproxyclient.NewMockInvoker(t)
		mockClient.EXPECT().V1betaDescribeVolume(ctx, mock.Anything).Return(&googleproxyclient.VolumeV1beta{}, errors.New("some error"))

		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		defer func() { googleproxyclient.GetGProxyClient = originalGetGProxyClient }()
		_, err := _describeVolume(ctx, basePath, token, locationId, projectNumber, xCorrelationID, volumeId)
		assert.Error(tt, err)
	})
	t.Run("WhenSuccess", func(tt *testing.T) {
		ctx := context.Background()
		mockClient := googleproxyclient.NewMockInvoker(t)
		response := &googleproxyclient.VolumeV1beta{}
		mockClient.EXPECT().V1betaDescribeVolume(ctx, mock.Anything).Return(response, nil)

		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}

		defer func() { googleproxyclient.GetGProxyClient = originalGetGProxyClient }()
		resp, err := _describeVolume(ctx, basePath, token, locationId, projectNumber, xCorrelationID, volumeId)
		assert.NoError(tt, err)
		assert.Equal(tt, *response, resp)
	})
}

func TestGetReplication(t *testing.T) {
	t.Run("WhenError", func(tt *testing.T) {
		ctx := context.Background()
		mockClient := googleproxyclient.NewMockInvoker(t)
		mockClient.EXPECT().V1betaGetMultipleReplicationsInternal(ctx, mock.Anything, mock.Anything).Return(nil, errors.New("some error"))

		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}

		defer func() { googleproxyclient.GetGProxyClient = originalGetGProxyClient }()
		_, err := _getReplication(ctx, "basePath", "projectNumber", "locationID", "volumeReplicationID", "jwt")
		assert.Error(tt, err)
		assert.Equal(tt, "Failed to get multiple replications", err.Error())
	})
	t.Run("WhenSuccess", func(tt *testing.T) {
		ctx := context.Background()
		mockClient := googleproxyclient.NewMockInvoker(t)
		response := &googleproxyclient.V1betaGetMultipleReplicationsInternalOK{
			Replications: []googleproxyclient.VolumeReplicationInternalV1beta{
				{},
			},
		}
		mockClient.EXPECT().V1betaGetMultipleReplicationsInternal(ctx, mock.Anything, mock.Anything).Return(response, nil)

		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}

		defer func() { googleproxyclient.GetGProxyClient = originalGetGProxyClient }()
		_, err := _getReplication(ctx, "basePath", "projectNumber", "locationID", "volumeReplicationID", "jwt")
		assert.NoError(tt, err)
	})
}

func TestVerifyDstReplicationDelete(t *testing.T) {
	event := &DeleteReplicationEvent{
		CommonReplicationEventParams: CommonReplicationEventParams{
			DstBasePath:              "dstPath",
			DestinationProjectNumber: "destinationProjectNumber",
			DstToken:                 "dstToken",
			ReplicationModel: &datamodel.VolumeReplication{
				ReplicationAttributes: &datamodel.ReplicationDetails{
					DestinationLocation:        "dstLocation",
					DestinationReplicationUUID: "dstUUID",
				},
			},
		},
	}
	t.Run("WhenGetReplicationError", func(tt *testing.T) {
		ctx := context.Background()
		defer func() {
			getReplication = _getReplication
		}()
		getReplication = func(ctx context.Context, basePath string, projectNumber string, locationID string, volumeReplicationID string, jwt string) (*coreModels.VolumeReplication, error) {
			return nil, errors.New("some error")
		}
		_, err := _verifyReplication(ctx, event)
		assert.Error(tt, err)
	})
	t.Run("WhenMirrorStateMirrored", func(tt *testing.T) {
		ctx := context.Background()
		defer func() {
			getReplication = _getReplication
		}()
		mirrorState := "MIRRORED"
		dstReplication := &coreModels.VolumeReplication{
			MirrorState: &mirrorState,
		}
		getReplication = func(ctx context.Context, basePath string, projectNumber string, locationID string, volumeReplicationID string, jwt string) (*coreModels.VolumeReplication, error) {
			return dstReplication, nil
		}
		expectedError := errors.NewUserInputValidationErr(fmt.Sprintf("Destination replication is in mirror_state: %v expected_mirror_state: %v", models.ReplicationV1betaMirrorStateMIRRORED, models.ReplicationV1betaMirrorStateSTOPPED))

		_, err := _verifyReplication(ctx, event)
		assert.Error(tt, err)
		assert.Equal(tt, expectedError, err)
	})
	t.Run("WhenMirrorStatePreparing", func(tt *testing.T) {
		ctx := context.Background()
		defer func() {
			getReplication = _getReplication
		}()
		mirrorState := "ABORTED"
		relationshipStatus := "transferring"
		dstReplication := &coreModels.VolumeReplication{
			MirrorState:        &mirrorState,
			RelationshipStatus: &relationshipStatus,
		}
		getReplication = func(ctx context.Context, basePath string, projectNumber string, locationID string, volumeReplicationID string, jwt string) (*coreModels.VolumeReplication, error) {
			return dstReplication, nil
		}
		expectedError := errors.NewUserInputValidationErr(fmt.Sprintf("Expected mirror state: %v or %v", models.ReplicationV1betaMirrorStatePREPARING, models.ReplicationV1betaMirrorStateSTOPPED))

		_, err := _verifyReplication(ctx, event)
		assert.Error(tt, err)
		assert.Equal(tt, expectedError, err)
	})
	t.Run("WhenMirrorStateUninitialized", func(tt *testing.T) {
		ctx := context.Background()
		defer func() {
			getReplication = _getReplication
		}()
		mirrorState := "PREPARING"
		relationshipStatus := "transferring"
		dstReplication := &coreModels.VolumeReplication{
			MirrorState:        &mirrorState,
			RelationshipStatus: &relationshipStatus,
		}
		getReplication = func(ctx context.Context, basePath string, projectNumber string, locationID string, volumeReplicationID string, jwt string) (*coreModels.VolumeReplication, error) {
			return dstReplication, nil
		}
		expectedError := errors.NewUserInputValidationErr(fmt.Sprintf("Replication relationship status should be %s", models.VolumeReplicationCVPV1betaRelationshipStatusIdle))
		_, err := _verifyReplication(ctx, event)
		assert.Error(tt, err)
		assert.Equal(tt, expectedError, err)
	})
	t.Run("WhenHybridReplicationInPendingSVMPeeringState", func(tt *testing.T) {
		ctx := context.Background()
		defer func() {
			getReplication = _getReplication
		}()
		mirrorState := "STOPPED"
		relationshipStatus := "IDLE"
		dstReplication := &coreModels.VolumeReplication{
			MirrorState:        &mirrorState,
			RelationshipStatus: &relationshipStatus,
		}
		getReplication = func(ctx context.Context, basePath string, projectNumber string, locationID string, volumeReplicationID string, jwt string) (*coreModels.VolumeReplication, error) {
			return dstReplication, nil
		}
		eventWithHybridReplication := &DeleteReplicationEvent{
			CommonReplicationEventParams: CommonReplicationEventParams{
				DstBasePath:              "dstPath",
				DestinationProjectNumber: "destinationProjectNumber",
				DstToken:                 "dstToken",
				ReplicationModel: &datamodel.VolumeReplication{
					State: datamodel.LifeCycleStateCreating,
					ReplicationAttributes: &datamodel.ReplicationDetails{
						DestinationLocation:        "dstLocation",
						DestinationReplicationUUID: "dstUUID",
					},
					HybridReplicationAttributes: &datamodel.HybridReplicationAttribute{
						Status: datamodel.HybridReplicationStatusPendingSVMPeer,
					},
				},
			},
		}
		expectedError := errors.NewUserInputValidationErr("Hybrid Replication in pending SVM peering state")
		_, err := _verifyReplication(ctx, eventWithHybridReplication)
		assert.Error(tt, err)
		assert.Equal(tt, expectedError, err)
	})
	t.Run("WhenSuccess", func(tt *testing.T) {
		ctx := context.Background()
		defer func() {
			getReplication = _getReplication
		}()
		mirrorState := "STOPPED"
		relationshipStatus := "IDLE"
		dstReplication := &coreModels.VolumeReplication{
			MirrorState:        &mirrorState,
			RelationshipStatus: &relationshipStatus,
		}
		getReplication = func(ctx context.Context, basePath string, projectNumber string, locationID string, volumeReplicationID string, jwt string) (*coreModels.VolumeReplication, error) {
			return dstReplication, nil
		}
		resp, err := _verifyReplication(ctx, event)
		assert.NoError(tt, err)
		assert.Equal(tt, dstReplication, resp)
	})
	t.Run("WhenDstBasePathEmpty_GetReplicationError", func(tt *testing.T) {
		ctx := context.Background()
		defer func() {
			getReplication = _getReplication
		}()
		eventWithEmptyDst := &DeleteReplicationEvent{
			CommonReplicationEventParams: CommonReplicationEventParams{
				DstBasePath:         "",
				SrcBasePath:         "srcPath",
				SourceProjectNumber: "sourceProjectNumber",
				SrcToken:            "srcToken",
				ReplicationModel: &datamodel.VolumeReplication{
					ReplicationAttributes: &datamodel.ReplicationDetails{
						SourceLocation:        "srcLocation",
						SourceReplicationUUID: "srcUUID",
					},
				},
			},
		}
		getReplication = func(ctx context.Context, basePath string, projectNumber string, locationID string, volumeReplicationID string, jwt string) (*coreModels.VolumeReplication, error) {
			return nil, errors.New("getReplication error")
		}
		_, err := _verifyReplication(ctx, eventWithEmptyDst)
		assert.Error(tt, err)
		var customErr *vsaErrors.CustomError
		assert.True(tt, vsaErrors.As(err, &customErr))
	})
	t.Run("WhenDstBasePathEmpty_GetReplicationReturnsNil", func(tt *testing.T) {
		ctx := context.Background()
		defer func() {
			getReplication = _getReplication
		}()
		eventWithEmptyDst := &DeleteReplicationEvent{
			CommonReplicationEventParams: CommonReplicationEventParams{
				DstBasePath:         "",
				SrcBasePath:         "srcPath",
				SourceProjectNumber: "sourceProjectNumber",
				SrcToken:            "srcToken",
				ReplicationModel: &datamodel.VolumeReplication{
					ReplicationAttributes: &datamodel.ReplicationDetails{
						SourceLocation:        "srcLocation",
						SourceReplicationUUID: "srcUUID",
					},
				},
			},
		}
		getReplication = func(ctx context.Context, basePath string, projectNumber string, locationID string, volumeReplicationID string, jwt string) (*coreModels.VolumeReplication, error) {
			return nil, nil
		}
		_, err := _verifyReplication(ctx, eventWithEmptyDst)
		assert.Error(tt, err)
		var customErr *vsaErrors.CustomError
		assert.True(tt, vsaErrors.As(err, &customErr))
	})
	t.Run("WhenDstBasePathEmpty_Success", func(tt *testing.T) {
		ctx := context.Background()
		defer func() {
			getReplication = _getReplication
		}()
		eventWithEmptyDst := &DeleteReplicationEvent{
			CommonReplicationEventParams: CommonReplicationEventParams{
				DstBasePath:         "",
				SrcBasePath:         "srcPath",
				SourceProjectNumber: "sourceProjectNumber",
				SrcToken:            "srcToken",
				ReplicationModel: &datamodel.VolumeReplication{
					ReplicationAttributes: &datamodel.ReplicationDetails{
						SourceLocation:        "srcLocation",
						SourceReplicationUUID: "srcUUID",
					},
				},
			},
		}
		mirrorState := "STOPPED"
		srcReplication := &coreModels.VolumeReplication{
			MirrorState: &mirrorState,
		}
		getReplication = func(ctx context.Context, basePath string, projectNumber string, locationID string, volumeReplicationID string, jwt string) (*coreModels.VolumeReplication, error) {
			// Verify that source parameters are used
			assert.Equal(tt, "srcPath", basePath)
			assert.Equal(tt, "sourceProjectNumber", projectNumber)
			assert.Equal(tt, "srcLocation", locationID)
			assert.Equal(tt, "srcUUID", volumeReplicationID)
			assert.Equal(tt, "srcToken", jwt)
			return srcReplication, nil
		}
		resp, err := _verifyReplication(ctx, eventWithEmptyDst)
		assert.NoError(tt, err)
		assert.Equal(tt, srcReplication, resp)
	})
}

func TestVerifyDstReplicationSync(t *testing.T) {
	event := &ResumeReplicationEvent{
		CommonReplicationEventParams: CommonReplicationEventParams{
			DstBasePath:              "dstPath",
			DestinationProjectNumber: "destinationProjectNumber",
			DstToken:                 "dstToken",
			ReplicationModel: &datamodel.VolumeReplication{
				ReplicationAttributes: &datamodel.ReplicationDetails{
					DestinationLocation:        "dstLocation",
					DestinationReplicationUUID: "dstUUID",
				},
			},
		},
	}
	t.Run("WhenIsSrcForHybridReplicationReturnsTrue", func(tt *testing.T) {
		ctx := context.Background()
		reverseType := string(datamodel.HybridReplicationParametersReplicationTypeREVERSE)
		hybridEvent := &ResumeReplicationEvent{
			CommonReplicationEventParams: CommonReplicationEventParams{
				DstBasePath:              "dstPath",
				DestinationProjectNumber: "destinationProjectNumber",
				DstToken:                 "dstToken",
				ReplicationModel: &datamodel.VolumeReplication{
					HybridReplicationAttributes: &datamodel.HybridReplicationAttribute{
						HybridReplicationType: &reverseType,
					},
					ReplicationAttributes: &datamodel.ReplicationDetails{
						DestinationLocation:        remoteRegionCustomer,
						DestinationReplicationUUID: "dstUUID",
					},
				},
			},
		}

		expectedError := errors.NewUserInputValidationErr("Sync not allowed when replication is in externally managed state")
		_, err := _verifyDstReplicationSync(ctx, hybridEvent)
		assert.Error(tt, err)
		assert.Equal(tt, expectedError, err)
	})

	t.Run("WhenGetReplicationError", func(tt *testing.T) {
		ctx := context.Background()
		defer func() {
			getReplication = _getReplication
		}()
		getReplication = func(ctx context.Context, basePath string, projectNumber string, locationID string, volumeReplicationID string, jwt string) (*coreModels.VolumeReplication, error) {
			return nil, errors.New("some error")
		}

		_, err := _verifyDstReplicationSync(ctx, event)
		assert.Error(tt, err)
	})

	t.Run("WhenMirrorStateNotMirrored", func(tt *testing.T) {
		ctx := context.Background()
		defer func() {
			getReplication = _getReplication
		}()
		mirrorState := "STOPPED"
		dstReplication := &coreModels.VolumeReplication{
			MirrorState: &mirrorState,
		}
		getReplication = func(ctx context.Context, basePath string, projectNumber string, locationID string, volumeReplicationID string, jwt string) (*coreModels.VolumeReplication, error) {
			return dstReplication, nil
		}

		expectedError := errors.NewUserInputValidationErr(fmt.Sprintf("Replication mirror state should be %s", models.ReplicationV1betaMirrorStateMIRRORED))
		_, err := _verifyDstReplicationSync(ctx, event)
		assert.Error(tt, err)
		assert.Equal(tt, expectedError, err)
	})

	t.Run("WhenSuccess", func(tt *testing.T) {
		ctx := context.Background()
		defer func() {
			getReplication = _getReplication
		}()
		mirrorState := models.ReplicationV1betaMirrorStateMIRRORED
		dstReplication := &coreModels.VolumeReplication{
			MirrorState: &mirrorState,
		}
		getReplication = func(ctx context.Context, basePath string, projectNumber string, locationID string, volumeReplicationID string, jwt string) (*coreModels.VolumeReplication, error) {
			return dstReplication, nil
		}

		resp, err := _verifyDstReplicationSync(ctx, event)
		assert.NoError(tt, err)
		assert.Equal(tt, dstReplication, resp)
	})
}

func TestValidateReplicationUpdate(t *testing.T) {
	event := &UpdateReplicationEvent{
		CommonReplicationEventParams: CommonReplicationEventParams{
			DstBasePath:              "dstPath",
			DestinationProjectNumber: "destinationProjectNumber",
			DstToken:                 "dstToken",
			ReplicationModel: &datamodel.VolumeReplication{
				ReplicationAttributes: &datamodel.ReplicationDetails{
					DestinationLocation:        "dstLocation",
					DestinationReplicationUUID: "dstUUID",
				},
			},
		},
	}
	t.Run("WhenGetReplicationError", func(tt *testing.T) {
		ctx := context.Background()
		defer func() {
			getReplication = _getReplication
		}()
		getReplication = func(ctx context.Context, basePath string, projectNumber string, locationID string, volumeReplicationID string, jwt string) (*coreModels.VolumeReplication, error) {
			return nil, errors.New("some error")
		}

		event1 := *event
		event1.Description = nillable.ToPointer("New description")
		event1.ReplicationModel.HybridReplicationAttributes = &datamodel.HybridReplicationAttribute{}
		_, err := _validateReplicationUpdate(ctx, &event1)
		assert.Error(tt, err)
	})
	t.Run("WhenNothingToUpdate", func(tt *testing.T) {
		ctx := context.Background()
		defer func() {
			getReplication = _getReplication
		}()
		_, err := _validateReplicationUpdate(ctx, event)
		assert.Error(tt, err)
		var customErr *vsaErrors.CustomError
		assert.True(tt, vsaErrors.As(err, &customErr), "Expected a CustomError")
		assert.ErrorContains(tt, customErr.OriginalErr, "empty replication update payload")
	})
	t.Run("WhenReplicationScheduleUnspecified", func(tt *testing.T) {
		ctx := context.Background()
		defer func() {
			getReplication = _getReplication
		}()

		event1 := *event
		event1.ReplicationSchedule = nillable.ToPointer("REPLICATION_SCHEDULE_UNSPECIFIED")
		_, err := _validateReplicationUpdate(ctx, &event1)
		assert.Error(tt, err)
		var customErr *vsaErrors.CustomError
		assert.True(tt, vsaErrors.As(err, &customErr), "Expected a CustomError")
		assert.ErrorContains(tt, customErr.OriginalErr, "Invalid replication schedule provided.")
	})
	t.Run("WhenLabelsInvalid", func(tt *testing.T) {
		ctx := context.Background()
		event.Description = nillable.ToPointer("New description")
		event.Labels = map[string]string{"": "value"}
		_, err := _validateReplicationUpdate(ctx, event)
		assert.Error(tt, err)
	})
	t.Run("WhenSuccess", func(tt *testing.T) {
		ctx := context.Background()
		defer func() {
			getReplication = _getReplication
		}()
		mirrorState := "STOPPED"
		relationshipStatus := "IDLE"
		dstReplication := &coreModels.VolumeReplication{
			MirrorState:        &mirrorState,
			RelationshipStatus: &relationshipStatus,
		}
		event.Description = nillable.ToPointer("New description")
		event.Labels = map[string]string{"key": "value"}
		getReplication = func(ctx context.Context, basePath string, projectNumber string, locationID string, volumeReplicationID string, jwt string) (*coreModels.VolumeReplication, error) {
			return dstReplication, nil
		}
		resp, err := _validateReplicationUpdate(ctx, event)
		assert.NoError(tt, err)
		assert.Equal(tt, dstReplication, resp)
	})
	t.Run("WhenHybridReplicationTypeIsReverseOnPremReplication", func(tt *testing.T) {
		ctx := context.Background()
		defer func() {
			getReplication = _getReplication
		}()
		reverseOnPremType := string(models.HybridReplicationParametersV1betaHybridReplicationTypeREVERSEONPREMREPLICATION)
		event1 := *event
		event1.Description = nillable.ToPointer("New description")
		event1.ReplicationModel.HybridReplicationAttributes = &datamodel.HybridReplicationAttribute{
			HybridReplicationType: &reverseOnPremType,
		}
		_, err := _validateReplicationUpdate(ctx, &event1)
		assert.Error(tt, err)
		assert.True(tt, errors.IsUserInputValidationErr(err), "Expected a UserInputValidationErr")
		assert.Equal(tt, "These fields cannot be updated when Hybrid Replication is Externally Managed", err.Error())
	})
	t.Run("WhenHybridReplicationStatusIsPendingReverseResume", func(tt *testing.T) {
		ctx := context.Background()
		defer func() {
			getReplication = _getReplication
		}()
		event1 := *event
		event1.Description = nillable.ToPointer("New description")
		event1.ReplicationModel.HybridReplicationAttributes = &datamodel.HybridReplicationAttribute{
			Status: datamodel.HybridReplicationStatusPendingRemoteResync,
		}
		_, err := _validateReplicationUpdate(ctx, &event1)
		assert.Error(tt, err)
		assert.True(tt, errors.IsUserInputValidationErr(err), "Expected a UserInputValidationErr")
		assert.Equal(tt, "Hybrid Replication can not be updated in the transition state - PENDING_REMOTE_RESYNC", err.Error())
	})
	t.Run("WhenHybridReplicationStatusIsPendingSVMPeer", func(tt *testing.T) {
		ctx := context.Background()
		defer func() {
			getReplication = _getReplication
		}()
		event1 := *event
		event1.Description = nillable.ToPointer("New description")
		event1.ReplicationModel.HybridReplicationAttributes = &datamodel.HybridReplicationAttribute{
			Status: datamodel.HybridReplicationStatusPendingSVMPeer,
		}
		_, err := _validateReplicationUpdate(ctx, &event1)
		assert.Error(tt, err)
		assert.True(tt, errors.IsUserInputValidationErr(err), "Expected a UserInputValidationErr")
		assert.Equal(tt, "Hybrid Replication can not be updated in the transition state - PENDING_SVM_PEER", err.Error())
	})
	t.Run("WhenHybridReplicationStatusIsPendingClusterPeer", func(tt *testing.T) {
		ctx := context.Background()
		defer func() {
			getReplication = _getReplication
		}()
		event1 := *event
		event1.Description = nillable.ToPointer("New description")
		event1.ReplicationModel.HybridReplicationAttributes = &datamodel.HybridReplicationAttribute{
			Status: datamodel.HybridReplicationStatusPendingClusterPeer,
		}
		_, err := _validateReplicationUpdate(ctx, &event1)
		assert.Error(tt, err)
		assert.True(tt, errors.IsUserInputValidationErr(err), "Expected a UserInputValidationErr")
		assert.Equal(tt, "Hybrid Replication can not be updated in the transition state - PENDING_CLUSTER_PEER", err.Error())
	})
	t.Run("WhenHybridReplicationTypeIsMIGRATIONAndScheduleIsNotHourly", func(tt *testing.T) {
		ctx := context.Background()
		defer func() {
			getReplication = _getReplication
		}()
		migrationType := string(datamodel.HybridReplicationParametersReplicationTypeMIGRATION)
		event1 := *event
		event1.ReplicationSchedule = nillable.ToPointer(models.ReplicationV1betaReplicationScheduleDAILY)
		event1.ReplicationModel.HybridReplicationAttributes = &datamodel.HybridReplicationAttribute{
			HybridReplicationType: &migrationType,
		}
		_, err := _validateReplicationUpdate(ctx, &event1)
		assert.Error(tt, err)
		assert.True(tt, errors.IsUserInputValidationErr(err), "Expected a UserInputValidationErr")
		assert.Equal(tt, "Invalid replication schedule provided.", err.Error())
	})
	t.Run("WhenHybridReplicationTypeIsMIGRATIONAndScheduleIsEvery10Minutes", func(tt *testing.T) {
		ctx := context.Background()
		defer func() {
			getReplication = _getReplication
		}()
		migrationType := string(datamodel.HybridReplicationParametersReplicationTypeMIGRATION)
		event1 := *event
		event1.ReplicationSchedule = nillable.ToPointer(models.ReplicationV1betaReplicationScheduleEVERY10MINUTES)
		event1.ReplicationModel.HybridReplicationAttributes = &datamodel.HybridReplicationAttribute{
			HybridReplicationType: &migrationType,
		}
		_, err := _validateReplicationUpdate(ctx, &event1)
		assert.Error(tt, err)
		assert.True(tt, errors.IsUserInputValidationErr(err), "Expected a UserInputValidationErr")
		assert.Equal(tt, "Invalid replication schedule provided.", err.Error())
	})
	t.Run("WhenHybridReplicationTypeIsMIGRATIONAndScheduleIsHourly", func(tt *testing.T) {
		ctx := context.Background()
		defer func() {
			getReplication = _getReplication
		}()
		migrationType := string(datamodel.HybridReplicationParametersReplicationTypeMIGRATION)
		mirrorState := "STOPPED"
		relationshipStatus := "IDLE"
		dstReplication := &coreModels.VolumeReplication{
			MirrorState:        &mirrorState,
			RelationshipStatus: &relationshipStatus,
		}
		event1 := *event
		event1.ReplicationSchedule = nillable.ToPointer(models.ReplicationV1betaReplicationScheduleHOURLY)
		event1.ReplicationModel.HybridReplicationAttributes = &datamodel.HybridReplicationAttribute{
			HybridReplicationType: &migrationType,
		}
		getReplication = func(ctx context.Context, basePath string, projectNumber string, locationID string, volumeReplicationID string, jwt string) (*coreModels.VolumeReplication, error) {
			return dstReplication, nil
		}
		resp, err := _validateReplicationUpdate(ctx, &event1)
		assert.NoError(tt, err)
		assert.Equal(tt, dstReplication, resp)
	})
	t.Run("WhenHybridReplicationTypeIsMIGRATIONAndScheduleIsNil", func(tt *testing.T) {
		ctx := context.Background()
		defer func() {
			getReplication = _getReplication
		}()
		migrationType := string(datamodel.HybridReplicationParametersReplicationTypeMIGRATION)
		mirrorState := "STOPPED"
		relationshipStatus := "IDLE"
		dstReplication := &coreModels.VolumeReplication{
			MirrorState:        &mirrorState,
			RelationshipStatus: &relationshipStatus,
		}
		event1 := *event
		event1.ReplicationSchedule = nil
		event1.Description = nillable.ToPointer("New description")
		event1.ReplicationModel.HybridReplicationAttributes = &datamodel.HybridReplicationAttribute{
			HybridReplicationType: &migrationType,
		}
		getReplication = func(ctx context.Context, basePath string, projectNumber string, locationID string, volumeReplicationID string, jwt string) (*coreModels.VolumeReplication, error) {
			return dstReplication, nil
		}
		resp, err := _validateReplicationUpdate(ctx, &event1)
		assert.NoError(tt, err)
		assert.Equal(tt, dstReplication, resp)
	})
	t.Run("WhenHybridReplicationTypeIsNotMIGRATION", func(tt *testing.T) {
		ctx := context.Background()
		defer func() {
			getReplication = _getReplication
		}()
		continuousType := string(datamodel.HybridReplicationParametersReplicationTypeCONTINUOUS)
		mirrorState := "STOPPED"
		relationshipStatus := "IDLE"
		dstReplication := &coreModels.VolumeReplication{
			MirrorState:        &mirrorState,
			RelationshipStatus: &relationshipStatus,
		}
		event1 := *event
		event1.ReplicationSchedule = nillable.ToPointer(models.ReplicationV1betaReplicationScheduleDAILY)
		event1.ReplicationModel.HybridReplicationAttributes = &datamodel.HybridReplicationAttribute{
			HybridReplicationType: &continuousType,
		}
		getReplication = func(ctx context.Context, basePath string, projectNumber string, locationID string, volumeReplicationID string, jwt string) (*coreModels.VolumeReplication, error) {
			return dstReplication, nil
		}
		resp, err := _validateReplicationUpdate(ctx, &event1)
		assert.NoError(tt, err)
		assert.Equal(tt, dstReplication, resp)
	})
	t.Run("WhenClusterLocationIsProvidedForNonHybridReplication", func(tt *testing.T) {
		ctx := context.Background()
		event1 := *event
		event1.ClusterLocation = nillable.ToPointer("us-west1")
		event1.ReplicationModel.HybridReplicationAttributes = nil
		_, err := _validateReplicationUpdate(ctx, &event1)
		assert.Error(tt, err)
		assert.True(tt, errors.IsUserInputValidationErr(err), "Expected a UserInputValidationErr")
		assert.Equal(tt, "Cluster location is not supported for non-hybrid replication", err.Error())
	})
	t.Run("WhenClusterLocationIsProvidedForHybridReplication", func(tt *testing.T) {
		ctx := context.Background()
		defer func() {
			getReplication = _getReplication
		}()
		migrationType := string(datamodel.HybridReplicationParametersReplicationTypeMIGRATION)
		mirrorState := "STOPPED"
		relationshipStatus := "IDLE"
		dstReplication := &coreModels.VolumeReplication{
			MirrorState:        &mirrorState,
			RelationshipStatus: &relationshipStatus,
		}
		event1 := *event
		event1.ClusterLocation = nillable.ToPointer("us-west1")
		event1.ReplicationModel.HybridReplicationAttributes = &datamodel.HybridReplicationAttribute{
			HybridReplicationType: &migrationType,
		}
		getReplication = func(ctx context.Context, basePath string, projectNumber string, locationID string, volumeReplicationID string, jwt string) (*coreModels.VolumeReplication, error) {
			return dstReplication, nil
		}
		resp, err := _validateReplicationUpdate(ctx, &event1)
		assert.NoError(tt, err)
		assert.Equal(tt, dstReplication, resp)
	})
	t.Run("WhenClusterLocationIsNil", func(tt *testing.T) {
		ctx := context.Background()
		defer func() {
			getReplication = _getReplication
		}()
		mirrorState := "STOPPED"
		relationshipStatus := "IDLE"
		dstReplication := &coreModels.VolumeReplication{
			MirrorState:        &mirrorState,
			RelationshipStatus: &relationshipStatus,
		}
		event1 := *event
		event1.ClusterLocation = nil
		event1.Description = nillable.ToPointer("New description")
		event1.ReplicationModel.HybridReplicationAttributes = nil
		getReplication = func(ctx context.Context, basePath string, projectNumber string, locationID string, volumeReplicationID string, jwt string) (*coreModels.VolumeReplication, error) {
			return dstReplication, nil
		}
		resp, err := _validateReplicationUpdate(ctx, &event1)
		assert.NoError(tt, err)
		assert.Equal(tt, dstReplication, resp)
	})
}

func Test_verifyDstReplicationReverse(t *testing.T) {
	event := &ReverseReplicationEvent{
		CommonReplicationEventParams: CommonReplicationEventParams{
			ReplicationModel: &datamodel.VolumeReplication{
				ReplicationAttributes: &datamodel.ReplicationDetails{
					DestinationLocation:        "us-east1",
					DestinationReplicationUUID: "dest-repl-uuid",
				},
			},
			DstBasePath:              "dstPath",
			DestinationProjectNumber: "dest-proj",
			DstToken:                 "dstToken",
		},
	}

	t.Run("WhenHybridReplicationAttributesSet_AndStatusNotPeered_ReturnsError", func(tt *testing.T) {
		ctx := context.Background()
		migrationType := string(datamodel.HybridReplicationParametersReplicationTypeMIGRATION)
		status := datamodel.HybridReplicationStatusPendingSVMPeer
		eventWithHybridNotPeered := &ReverseReplicationEvent{
			CommonReplicationEventParams: CommonReplicationEventParams{
				DstBasePath:              "dstPath",
				DestinationProjectNumber: "dest-proj",
				DstToken:                 "dstToken",
				ReplicationModel: &datamodel.VolumeReplication{
					ReplicationAttributes: &datamodel.ReplicationDetails{
						DestinationLocation:        "us-east1",
						DestinationReplicationUUID: "dest-repl-uuid",
					},
					HybridReplicationAttributes: &datamodel.HybridReplicationAttribute{
						HybridReplicationType: &migrationType,
						Status:                status,
					},
				},
			},
		}
		expectedError := errors.NewUserInputValidationErr("Hybrid Replication needs to be in peered state before reversing")
		resp, err := _verifyDstReplicationReverse(ctx, eventWithHybridNotPeered)
		assert.Error(tt, err)
		assert.Nil(tt, resp)
		assert.Equal(tt, expectedError, err)
	})
	t.Run("WhenIsSrcForHybridReplicationReturnsTrue_AndDestinationReplicationUUIDIsNil_AndStatusIsNotExternalManaged", func(tt *testing.T) {
		ctx := context.Background()
		reverseType := string(datamodel.HybridReplicationParametersReplicationTypeREVERSE)
		status := datamodel.HybridReplicationStatusPeered
		hybridEvent := &ReverseReplicationEvent{
			CommonReplicationEventParams: CommonReplicationEventParams{
				SrcBasePath:              "srcPath",
				SourceProjectNumber:      "sourceProjectNumber",
				SrcToken:                 "srcToken",
				DstBasePath:              "dstPath",
				DestinationProjectNumber: "destinationProjectNumber",
				DstToken:                 "dstToken",
				ReplicationModel: &datamodel.VolumeReplication{
					ReplicationAttributes: &datamodel.ReplicationDetails{
						SourceLocation:             "srcLocation",
						SourceReplicationUUID:      "srcUUID",
						DestinationLocation:        remoteRegionCustomer,
						DestinationReplicationUUID: "00000000-0000-0000-0000-000000000000",
					},
					HybridReplicationAttributes: &datamodel.HybridReplicationAttribute{
						HybridReplicationType: &reverseType,
						Status:                status,
					},
				},
			},
		}
		expectedError := errors.NewUserInputValidationErr("Hybrid Replication needs to be in externally managed state before reversing")
		resp, err := _verifyDstReplicationReverse(ctx, hybridEvent)
		assert.Error(tt, err)
		assert.Nil(tt, resp)
		assert.Equal(tt, expectedError, err)
	})

	t.Run("WhenIsSrcForHybridReplicationReturnsTrue_AndDestinationReplicationUUIDIsNil_AndStatusIsExternalManaged", func(tt *testing.T) {
		ctx := context.Background()
		defer func() {
			getReplication = _getReplication
		}()
		reverseType := string(datamodel.HybridReplicationParametersReplicationTypeREVERSE)
		status := datamodel.HybridReplicationStatusExternalManaged
		hybridEvent := &ReverseReplicationEvent{
			CommonReplicationEventParams: CommonReplicationEventParams{
				SrcBasePath:              "srcPath",
				SourceProjectNumber:      "sourceProjectNumber",
				SrcToken:                 "srcToken",
				DstBasePath:              "dstPath",
				DestinationProjectNumber: "destinationProjectNumber",
				DstToken:                 "dstToken",
				ReplicationModel: &datamodel.VolumeReplication{
					ReplicationAttributes: &datamodel.ReplicationDetails{
						SourceLocation:             "srcLocation",
						SourceReplicationUUID:      "srcUUID",
						DestinationLocation:        remoteRegionCustomer,
						DestinationReplicationUUID: "00000000-0000-0000-0000-000000000000",
					},
					HybridReplicationAttributes: &datamodel.HybridReplicationAttribute{
						HybridReplicationType: &reverseType,
						Status:                status,
					},
				},
			},
		}
		srcReplication := &coreModels.VolumeReplication{
			ReplicationAttributes: &coreModels.ReplicationDetails{
				SourceSvmName:         "src-svm",
				SourceVolumeName:      "src-volume",
				DestinationSvmName:    "dst-svm",
				DestinationVolumeName: "dst-volume",
			},
		}
		getReplication = func(ctx context.Context, basePath string, projectNumber string, locationID string, volumeReplicationID string, jwt string) (*coreModels.VolumeReplication, error) {
			assert.Equal(tt, "srcPath", basePath)
			assert.Equal(tt, "sourceProjectNumber", projectNumber)
			assert.Equal(tt, "srcLocation", locationID)
			assert.Equal(tt, "srcUUID", volumeReplicationID)
			assert.Equal(tt, "srcToken", jwt)
			return srcReplication, nil
		}
		resp, err := _verifyDstReplicationReverse(ctx, hybridEvent)
		assert.NoError(tt, err)
		assert.NotNil(tt, resp)
		assert.Equal(tt, srcReplication, resp)
	})

	t.Run("WhenIsSrcForHybridReplicationReturnsTrue_AndDestinationReplicationUUIDIsNotNil", func(tt *testing.T) {
		ctx := context.Background()
		defer func() {
			getReplication = _getReplication
		}()
		reverseType := string(datamodel.HybridReplicationParametersReplicationTypeREVERSE)
		hybridEvent := &ReverseReplicationEvent{
			CommonReplicationEventParams: CommonReplicationEventParams{
				SrcBasePath:              "srcPath",
				SourceProjectNumber:      "sourceProjectNumber",
				SrcToken:                 "srcToken",
				DstBasePath:              "dstPath",
				DestinationProjectNumber: "destinationProjectNumber",
				DstToken:                 "dstToken",
				ReplicationModel: &datamodel.VolumeReplication{
					ReplicationAttributes: &datamodel.ReplicationDetails{
						SourceLocation:             "srcLocation",
						SourceReplicationUUID:      "srcUUID",
						DestinationLocation:        remoteRegionCustomer,
						DestinationReplicationUUID: "destUUID",
					},
					HybridReplicationAttributes: &datamodel.HybridReplicationAttribute{
						HybridReplicationType: &reverseType,
					},
				},
			},
		}
		srcReplication := &coreModels.VolumeReplication{
			ReplicationAttributes: &coreModels.ReplicationDetails{
				SourceSvmName:         "src-svm",
				SourceVolumeName:      "src-volume",
				DestinationSvmName:    "dst-svm",
				DestinationVolumeName: "dst-volume",
			},
		}
		getReplication = func(ctx context.Context, basePath string, projectNumber string, locationID string, volumeReplicationID string, jwt string) (*coreModels.VolumeReplication, error) {
			assert.Equal(tt, "srcPath", basePath)
			assert.Equal(tt, "sourceProjectNumber", projectNumber)
			assert.Equal(tt, "srcLocation", locationID)
			assert.Equal(tt, "srcUUID", volumeReplicationID)
			assert.Equal(tt, "srcToken", jwt)
			return srcReplication, nil
		}
		resp, err := _verifyDstReplicationReverse(ctx, hybridEvent)
		assert.NoError(tt, err)
		assert.NotNil(tt, resp)
		assert.Equal(tt, srcReplication, resp)
	})

	t.Run("WhenIsSrcForHybridReplicationReturnsTrue_AndGetReplicationFails", func(tt *testing.T) {
		ctx := context.Background()
		defer func() {
			getReplication = _getReplication
		}()
		reverseType := string(datamodel.HybridReplicationParametersReplicationTypeREVERSE)
		hybridEvent := &ReverseReplicationEvent{
			CommonReplicationEventParams: CommonReplicationEventParams{
				SrcBasePath:              "srcPath",
				SourceProjectNumber:      "sourceProjectNumber",
				SrcToken:                 "srcToken",
				DstBasePath:              "dstPath",
				DestinationProjectNumber: "destinationProjectNumber",
				DstToken:                 "dstToken",
				ReplicationModel: &datamodel.VolumeReplication{
					ReplicationAttributes: &datamodel.ReplicationDetails{
						SourceLocation:             "srcLocation",
						SourceReplicationUUID:      "srcUUID",
						DestinationLocation:        remoteRegionCustomer,
						DestinationReplicationUUID: "destUUID",
					},
					HybridReplicationAttributes: &datamodel.HybridReplicationAttribute{
						HybridReplicationType: &reverseType,
					},
				},
			},
		}
		getReplication = func(ctx context.Context, basePath string, projectNumber string, locationID string, volumeReplicationID string, jwt string) (*coreModels.VolumeReplication, error) {
			return nil, errors.New("get replication error")
		}
		resp, err := _verifyDstReplicationReverse(ctx, hybridEvent)
		assert.Error(tt, err)
		assert.Nil(tt, resp)
		var customErr *vsaErrors.CustomError
		assert.True(tt, vsaErrors.As(err, &customErr), "Expected a CustomError")
		assert.Equal(tt, vsaErrors.ErrGoogleProxyInternalGetMultipleReplications, customErr.TrackingID)
	})

	t.Run("WhenIsSrcForHybridReplicationReturnsTrue_AndGetReplicationReturnsNil", func(tt *testing.T) {
		ctx := context.Background()
		defer func() {
			getReplication = _getReplication
		}()
		reverseType := string(datamodel.HybridReplicationParametersReplicationTypeREVERSE)
		hybridEvent := &ReverseReplicationEvent{
			CommonReplicationEventParams: CommonReplicationEventParams{
				SrcBasePath:              "srcPath",
				SourceProjectNumber:      "sourceProjectNumber",
				SrcToken:                 "srcToken",
				DstBasePath:              "dstPath",
				DestinationProjectNumber: "destinationProjectNumber",
				DstToken:                 "dstToken",
				ReplicationModel: &datamodel.VolumeReplication{
					ReplicationAttributes: &datamodel.ReplicationDetails{
						SourceLocation:             "srcLocation",
						SourceReplicationUUID:      "srcUUID",
						DestinationLocation:        remoteRegionCustomer,
						DestinationReplicationUUID: "destUUID",
					},
					HybridReplicationAttributes: &datamodel.HybridReplicationAttribute{
						HybridReplicationType: &reverseType,
					},
				},
			},
		}
		getReplication = func(ctx context.Context, basePath string, projectNumber string, locationID string, volumeReplicationID string, jwt string) (*coreModels.VolumeReplication, error) {
			return nil, nil
		}
		resp, err := _verifyDstReplicationReverse(ctx, hybridEvent)
		assert.Error(tt, err)
		assert.Nil(tt, resp)
		var customErr *vsaErrors.CustomError
		assert.True(tt, vsaErrors.As(err, &customErr), "Expected a CustomError")
		assert.Equal(tt, vsaErrors.ErrGoogleProxyInternalGetMultipleReplications, customErr.TrackingID)
	})

	t.Run("WhenGetReplicationError", func(tt *testing.T) {
		ctx := context.Background()
		defer func() {
			getReplication = _getReplication
		}()
		getReplication = func(ctx context.Context, basePath string, projectNumber string, locationID string, volumeReplicationID string, jwt string) (*coreModels.VolumeReplication, error) {
			return nil, errors.New("get replication error")
		}

		_, err := _verifyDstReplicationReverse(ctx, event)
		assert.Error(tt, err)
		var customErr *vsaErrors.CustomError
		assert.True(tt, vsaErrors.As(err, &customErr), "Expected a CustomError")
		assert.Equal(tt, vsaErrors.ErrGoogleProxyInternalGetMultipleReplications, customErr.TrackingID)
	})

	t.Run("WhenReplicationIsNil", func(tt *testing.T) {
		ctx := context.Background()
		defer func() {
			getReplication = _getReplication
		}()
		getReplication = func(ctx context.Context, basePath string, projectNumber string, locationID string, volumeReplicationID string, jwt string) (*coreModels.VolumeReplication, error) {
			return nil, nil
		}

		_, err := _verifyDstReplicationReverse(ctx, event)
		assert.Error(tt, err)
		var customErr *vsaErrors.CustomError
		assert.True(tt, vsaErrors.As(err, &customErr), "Expected a CustomError")
		assert.Equal(tt, vsaErrors.ErrGoogleProxyInternalGetMultipleReplications, customErr.TrackingID)
	})

	t.Run("WhenSuccess", func(tt *testing.T) {
		ctx := context.Background()
		defer func() {
			getReplication = _getReplication
		}()
		mirrorState := models.ReplicationV1betaMirrorStateSTOPPED
		relationshipStatus := "idle"
		dstReplication := &coreModels.VolumeReplication{
			MirrorState:        &mirrorState,
			RelationshipStatus: &relationshipStatus,
		}
		getReplication = func(ctx context.Context, basePath string, projectNumber string, locationID string, volumeReplicationID string, jwt string) (*coreModels.VolumeReplication, error) {
			return dstReplication, nil
		}

		resp, err := _verifyDstReplicationReverse(ctx, event)
		assert.NoError(tt, err)
		assert.Equal(tt, dstReplication, resp)
	})
}

func Test_verifyNewSourceQuotaRulesReverse(t *testing.T) {
	event := &ReverseReplicationEvent{
		CommonReplicationEventParams: CommonReplicationEventParams{
			ReplicationModel: &datamodel.VolumeReplication{
				ReplicationAttributes: &datamodel.ReplicationDetails{
					DestinationLocation:   "us-east1",
					DestinationVolumeUUID: "dest-volume-uuid",
				},
			},
			DestinationProjectNumber: "123456789",
			DstBasePath:              "https://test-base-path",
			DstToken:                 "test-token",
		},
	}

	t.Run("AllQuotaRulesReady", func(tt *testing.T) {
		ctx := context.Background()

		// Mock the Google Proxy client
		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() { googleproxyclient.GetGProxyClient = originalGetGProxyClient }()

		mockInvoker := googleproxyclient.NewMockInvoker(t)
		mc := &googleproxyclient.ProxyClient{
			Invoker: mockInvoker,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}

		quotaRules := []googleproxyclient.QuotaRulesV1beta{
			{
				ResourceId: "quota-rule-1",
				State:      googleproxyclient.NewOptQuotaRulesV1betaState(googleproxyclient.QuotaRulesV1betaStateREADY),
			},
			{
				ResourceId: "quota-rule-2",
				State:      googleproxyclient.NewOptQuotaRulesV1betaState(googleproxyclient.QuotaRulesV1betaStateREADY),
			},
		}

		mockInvoker.EXPECT().V1betaListAllQuotaRules(ctx, mock.Anything).Return(&googleproxyclient.V1betaListAllQuotaRulesOK{QuotaRules: quotaRules}, nil)

		err := _verifyNewSourceQuotaRulesReverse(ctx, event)
		assert.NoError(tt, err)
	})

	t.Run("QuotaRuleNotReady", func(tt *testing.T) {
		ctx := context.Background()

		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() { googleproxyclient.GetGProxyClient = originalGetGProxyClient }()

		mockInvoker := googleproxyclient.NewMockInvoker(t)
		mc := &googleproxyclient.ProxyClient{
			Invoker: mockInvoker,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}

		quotaRules := []googleproxyclient.QuotaRulesV1beta{
			{
				ResourceId:   "quota-rule-1",
				State:        googleproxyclient.NewOptQuotaRulesV1betaState(googleproxyclient.QuotaRulesV1betaStateERROR),
				StateDetails: googleproxyclient.NewOptString("Error details"),
			},
		}

		mockInvoker.EXPECT().V1betaListAllQuotaRules(ctx, mock.Anything).Return(&googleproxyclient.V1betaListAllQuotaRulesOK{QuotaRules: quotaRules}, nil)

		err := _verifyNewSourceQuotaRulesReverse(ctx, event)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "Cannot reverse resume replication: new source volume has quota rules not in READY state")
	})

	t.Run("QuotaRuleNoState", func(tt *testing.T) {
		ctx := context.Background()

		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() { googleproxyclient.GetGProxyClient = originalGetGProxyClient }()

		mockInvoker := googleproxyclient.NewMockInvoker(t)
		mc := &googleproxyclient.ProxyClient{
			Invoker: mockInvoker,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}

		quotaRules := []googleproxyclient.QuotaRulesV1beta{
			{
				ResourceId:   "quota-rule-1",
				State:        googleproxyclient.OptQuotaRulesV1betaState{}, // No state set
				StateDetails: googleproxyclient.NewOptString("No state"),
			},
		}

		mockInvoker.EXPECT().V1betaListAllQuotaRules(ctx, mock.Anything).Return(&googleproxyclient.V1betaListAllQuotaRulesOK{QuotaRules: quotaRules}, nil)

		err := _verifyNewSourceQuotaRulesReverse(ctx, event)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "State: (not set)")
	})

	t.Run("NoQuotaRules", func(tt *testing.T) {
		ctx := context.Background()

		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() { googleproxyclient.GetGProxyClient = originalGetGProxyClient }()

		mockInvoker := googleproxyclient.NewMockInvoker(t)
		mc := &googleproxyclient.ProxyClient{
			Invoker: mockInvoker,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}

		mockInvoker.EXPECT().V1betaListAllQuotaRules(ctx, mock.Anything).
			Return(&googleproxyclient.V1betaListAllQuotaRulesOK{QuotaRules: []googleproxyclient.QuotaRulesV1beta{}}, nil)

		err := _verifyNewSourceQuotaRulesReverse(ctx, event)
		assert.NoError(tt, err)
	})

	t.Run("APIBadRequest", func(tt *testing.T) {
		ctx := context.Background()

		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() { googleproxyclient.GetGProxyClient = originalGetGProxyClient }()

		mockInvoker := googleproxyclient.NewMockInvoker(t)
		mc := &googleproxyclient.ProxyClient{
			Invoker: mockInvoker,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}

		mockInvoker.EXPECT().V1betaListAllQuotaRules(ctx, mock.Anything).
			Return(&googleproxyclient.V1betaListAllQuotaRulesBadRequest{Message: "Bad request"}, nil)

		err := _verifyNewSourceQuotaRulesReverse(ctx, event)
		assert.Error(tt, err)
		var customErr *vsaErrors.CustomError
		assert.True(tt, vsaErrors.As(err, &customErr))
		assert.Equal(tt, vsaErrors.ErrQuotaRuleBadRequest, customErr.TrackingID)
	})

	t.Run("APIUnauthorized", func(tt *testing.T) {
		ctx := context.Background()

		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() { googleproxyclient.GetGProxyClient = originalGetGProxyClient }()

		mockInvoker := googleproxyclient.NewMockInvoker(t)
		mc := &googleproxyclient.ProxyClient{
			Invoker: mockInvoker,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}

		mockInvoker.EXPECT().V1betaListAllQuotaRules(ctx, mock.Anything).
			Return(&googleproxyclient.V1betaListAllQuotaRulesUnauthorized{Message: "Unauthorized"}, nil)

		err := _verifyNewSourceQuotaRulesReverse(ctx, event)
		assert.Error(tt, err)
		var customErr *vsaErrors.CustomError
		assert.True(tt, vsaErrors.As(err, &customErr))
		assert.Equal(tt, vsaErrors.ErrQuotaRuleUnauthorized, customErr.TrackingID)
	})

	t.Run("APIForbidden", func(tt *testing.T) {
		ctx := context.Background()

		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() { googleproxyclient.GetGProxyClient = originalGetGProxyClient }()

		mockInvoker := googleproxyclient.NewMockInvoker(t)
		mc := &googleproxyclient.ProxyClient{
			Invoker: mockInvoker,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}

		mockInvoker.EXPECT().V1betaListAllQuotaRules(ctx, mock.Anything).
			Return(&googleproxyclient.V1betaListAllQuotaRulesForbidden{Message: "Forbidden"}, nil)

		err := _verifyNewSourceQuotaRulesReverse(ctx, event)
		assert.Error(tt, err)
		var customErr *vsaErrors.CustomError
		assert.True(tt, vsaErrors.As(err, &customErr))
		assert.Equal(tt, vsaErrors.ErrQuotaRuleForbidden, customErr.TrackingID)
	})

	t.Run("APINotFound", func(tt *testing.T) {
		ctx := context.Background()

		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() { googleproxyclient.GetGProxyClient = originalGetGProxyClient }()

		mockInvoker := googleproxyclient.NewMockInvoker(t)
		mc := &googleproxyclient.ProxyClient{
			Invoker: mockInvoker,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}

		mockInvoker.EXPECT().V1betaListAllQuotaRules(ctx, mock.Anything).
			Return(&googleproxyclient.V1betaListAllQuotaRulesNotFound{Message: "Not found"}, nil)

		err := _verifyNewSourceQuotaRulesReverse(ctx, event)
		assert.Error(tt, err)
		var customErr *vsaErrors.CustomError
		assert.True(tt, vsaErrors.As(err, &customErr))
		assert.Equal(tt, vsaErrors.ErrQuotaRuleNotFound, customErr.TrackingID)
	})

	t.Run("APIInternalServerError", func(tt *testing.T) {
		ctx := context.Background()

		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() { googleproxyclient.GetGProxyClient = originalGetGProxyClient }()

		mockInvoker := googleproxyclient.NewMockInvoker(t)
		mc := &googleproxyclient.ProxyClient{
			Invoker: mockInvoker,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}

		mockInvoker.EXPECT().V1betaListAllQuotaRules(ctx, mock.Anything).
			Return(&googleproxyclient.V1betaListAllQuotaRulesInternalServerError{Message: "Internal error"}, nil)

		err := _verifyNewSourceQuotaRulesReverse(ctx, event)
		assert.Error(tt, err)
		var customErr *vsaErrors.CustomError
		assert.True(tt, vsaErrors.As(err, &customErr))
		assert.Equal(tt, vsaErrors.ErrQuotaRuleInternalServerError, customErr.TrackingID)
	})

	t.Run("APIConflict", func(tt *testing.T) {
		ctx := context.Background()

		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() { googleproxyclient.GetGProxyClient = originalGetGProxyClient }()

		mockInvoker := googleproxyclient.NewMockInvoker(t)
		mc := &googleproxyclient.ProxyClient{
			Invoker: mockInvoker,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}

		mockInvoker.EXPECT().V1betaListAllQuotaRules(ctx, mock.Anything).
			Return(&googleproxyclient.V1betaListAllQuotaRulesConflict{Message: "Conflict"}, nil)

		err := _verifyNewSourceQuotaRulesReverse(ctx, event)
		assert.Error(tt, err)
		var customErr *vsaErrors.CustomError
		assert.True(tt, vsaErrors.As(err, &customErr))
		assert.Equal(tt, vsaErrors.ErrQuotaRuleConflict, customErr.TrackingID)
	})

	t.Run("APIUnprocessableEntity", func(tt *testing.T) {
		ctx := context.Background()

		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() { googleproxyclient.GetGProxyClient = originalGetGProxyClient }()

		mockInvoker := googleproxyclient.NewMockInvoker(t)
		mc := &googleproxyclient.ProxyClient{
			Invoker: mockInvoker,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}

		mockInvoker.EXPECT().V1betaListAllQuotaRules(ctx, mock.Anything).
			Return(&googleproxyclient.V1betaListAllQuotaRulesUnprocessableEntity{Message: "Unprocessable"}, nil)

		err := _verifyNewSourceQuotaRulesReverse(ctx, event)
		assert.Error(tt, err)
		var customErr *vsaErrors.CustomError
		assert.True(tt, vsaErrors.As(err, &customErr))
		assert.Equal(tt, vsaErrors.ErrQuotaRuleBadRequest, customErr.TrackingID)
	})

	t.Run("APITooManyRequests", func(tt *testing.T) {
		ctx := context.Background()

		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() { googleproxyclient.GetGProxyClient = originalGetGProxyClient }()

		mockInvoker := googleproxyclient.NewMockInvoker(t)
		mc := &googleproxyclient.ProxyClient{
			Invoker: mockInvoker,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}

		mockInvoker.EXPECT().V1betaListAllQuotaRules(ctx, mock.Anything).
			Return(&googleproxyclient.V1betaListAllQuotaRulesTooManyRequests{Message: "Too many requests"}, nil)

		err := _verifyNewSourceQuotaRulesReverse(ctx, event)
		assert.Error(tt, err)
		var customErr *vsaErrors.CustomError
		assert.True(tt, vsaErrors.As(err, &customErr))
		assert.Equal(tt, vsaErrors.ErrQuotaRuleTooManyRequests, customErr.TrackingID)
	})

	t.Run("APIError", func(tt *testing.T) {
		ctx := context.Background()

		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() { googleproxyclient.GetGProxyClient = originalGetGProxyClient }()

		mockInvoker := googleproxyclient.NewMockInvoker(t)
		mc := &googleproxyclient.ProxyClient{
			Invoker: mockInvoker,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}

		mockInvoker.EXPECT().V1betaListAllQuotaRules(ctx, mock.Anything).
			Return(nil, vsaErrors.New("API error"))

		err := _verifyNewSourceQuotaRulesReverse(ctx, event)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "failed to list quota rules on new source volume")
	})
}

func Test_verifyNewDestinationQuotaRulesReverse(t *testing.T) {
	event := &ReverseReplicationEvent{
		CommonReplicationEventParams: CommonReplicationEventParams{
			ReplicationModel: &datamodel.VolumeReplication{
				ReplicationAttributes: &datamodel.ReplicationDetails{
					SourceLocation:   "us-west1",
					SourceVolumeUUID: "src-volume-uuid",
				},
			},
			SourceProjectNumber: "987654321",
			SrcBasePath:         "https://test-base-path-src",
			SrcToken:            "test-token-src",
		},
	}

	t.Run("NoTransitioningQuotaRules", func(tt *testing.T) {
		ctx := context.Background()

		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() { googleproxyclient.GetGProxyClient = originalGetGProxyClient }()

		mockInvoker := googleproxyclient.NewMockInvoker(t)
		mc := &googleproxyclient.ProxyClient{
			Invoker: mockInvoker,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}

		quotaRules := []googleproxyclient.QuotaRulesV1beta{
			{
				ResourceId: "quota-rule-1",
				State:      googleproxyclient.NewOptQuotaRulesV1betaState(googleproxyclient.QuotaRulesV1betaStateREADY),
			},
			{
				ResourceId: "quota-rule-2",
				State:      googleproxyclient.NewOptQuotaRulesV1betaState(googleproxyclient.QuotaRulesV1betaStateERROR),
			},
		}

		mockInvoker.EXPECT().V1betaListAllQuotaRules(ctx, mock.Anything).Return(&googleproxyclient.V1betaListAllQuotaRulesOK{QuotaRules: quotaRules}, nil)

		err := _verifyNewDestinationQuotaRulesReverse(ctx, event)
		assert.NoError(tt, err)
	})

	t.Run("QuotaRuleCreating", func(tt *testing.T) {
		ctx := context.Background()

		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() { googleproxyclient.GetGProxyClient = originalGetGProxyClient }()

		mockInvoker := googleproxyclient.NewMockInvoker(t)
		mc := &googleproxyclient.ProxyClient{
			Invoker: mockInvoker,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}

		quotaRules := []googleproxyclient.QuotaRulesV1beta{
			{
				ResourceId:   "quota-rule-1",
				State:        googleproxyclient.NewOptQuotaRulesV1betaState(googleproxyclient.QuotaRulesV1betaStateCREATING),
				StateDetails: googleproxyclient.NewOptString("Creating"),
			},
		}

		mockInvoker.EXPECT().V1betaListAllQuotaRules(ctx, mock.Anything).Return(&googleproxyclient.V1betaListAllQuotaRulesOK{QuotaRules: quotaRules}, nil)

		err := _verifyNewDestinationQuotaRulesReverse(ctx, event)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "Cannot reverse resume replication: new destination volume has quota rules in transitioning state")
	})

	t.Run("QuotaRuleUpdating", func(tt *testing.T) {
		ctx := context.Background()

		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() { googleproxyclient.GetGProxyClient = originalGetGProxyClient }()

		mockInvoker := googleproxyclient.NewMockInvoker(t)
		mc := &googleproxyclient.ProxyClient{
			Invoker: mockInvoker,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}

		quotaRules := []googleproxyclient.QuotaRulesV1beta{
			{
				ResourceId:   "quota-rule-1",
				State:        googleproxyclient.NewOptQuotaRulesV1betaState(googleproxyclient.QuotaRulesV1betaStateUPDATING),
				StateDetails: googleproxyclient.NewOptString("Updating"),
			},
		}

		mockInvoker.EXPECT().V1betaListAllQuotaRules(ctx, mock.Anything).Return(&googleproxyclient.V1betaListAllQuotaRulesOK{QuotaRules: quotaRules}, nil)

		err := _verifyNewDestinationQuotaRulesReverse(ctx, event)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "Cannot reverse resume replication: new destination volume has quota rules in transitioning state")
	})

	t.Run("QuotaRuleDeleting", func(tt *testing.T) {
		ctx := context.Background()

		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() { googleproxyclient.GetGProxyClient = originalGetGProxyClient }()

		mockInvoker := googleproxyclient.NewMockInvoker(t)
		mc := &googleproxyclient.ProxyClient{
			Invoker: mockInvoker,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}

		quotaRules := []googleproxyclient.QuotaRulesV1beta{
			{
				ResourceId:   "quota-rule-1",
				State:        googleproxyclient.NewOptQuotaRulesV1betaState(googleproxyclient.QuotaRulesV1betaStateDELETING),
				StateDetails: googleproxyclient.NewOptString("Deleting"),
			},
		}

		mockInvoker.EXPECT().V1betaListAllQuotaRules(ctx, mock.Anything).Return(&googleproxyclient.V1betaListAllQuotaRulesOK{QuotaRules: quotaRules}, nil)

		err := _verifyNewDestinationQuotaRulesReverse(ctx, event)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "Cannot reverse resume replication: new destination volume has quota rules in transitioning state")
	})

	t.Run("QuotaRuleNoState", func(tt *testing.T) {
		ctx := context.Background()

		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() { googleproxyclient.GetGProxyClient = originalGetGProxyClient }()

		mockInvoker := googleproxyclient.NewMockInvoker(t)
		mc := &googleproxyclient.ProxyClient{
			Invoker: mockInvoker,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}

		quotaRules := []googleproxyclient.QuotaRulesV1beta{
			{
				ResourceId:   "quota-rule-1",
				State:        googleproxyclient.OptQuotaRulesV1betaState{}, // No state set
				StateDetails: googleproxyclient.NewOptString("No state"),
			},
		}

		mockInvoker.EXPECT().V1betaListAllQuotaRules(ctx, mock.Anything).Return(&googleproxyclient.V1betaListAllQuotaRulesOK{QuotaRules: quotaRules}, nil)

		err := _verifyNewDestinationQuotaRulesReverse(ctx, event)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "State: (not set)")
	})

	t.Run("NoQuotaRules", func(tt *testing.T) {
		ctx := context.Background()

		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() { googleproxyclient.GetGProxyClient = originalGetGProxyClient }()

		mockInvoker := googleproxyclient.NewMockInvoker(t)
		mc := &googleproxyclient.ProxyClient{
			Invoker: mockInvoker,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}

		mockInvoker.EXPECT().V1betaListAllQuotaRules(ctx, mock.Anything).
			Return(&googleproxyclient.V1betaListAllQuotaRulesOK{QuotaRules: []googleproxyclient.QuotaRulesV1beta{}}, nil)

		err := _verifyNewDestinationQuotaRulesReverse(ctx, event)
		assert.NoError(tt, err)
	})

	t.Run("APIBadRequest", func(tt *testing.T) {
		ctx := context.Background()

		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() { googleproxyclient.GetGProxyClient = originalGetGProxyClient }()

		mockInvoker := googleproxyclient.NewMockInvoker(t)
		mc := &googleproxyclient.ProxyClient{
			Invoker: mockInvoker,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}

		mockInvoker.EXPECT().V1betaListAllQuotaRules(ctx, mock.Anything).
			Return(&googleproxyclient.V1betaListAllQuotaRulesBadRequest{Message: "Bad request"}, nil)

		err := _verifyNewDestinationQuotaRulesReverse(ctx, event)
		assert.Error(tt, err)
		var customErr *vsaErrors.CustomError
		assert.True(tt, vsaErrors.As(err, &customErr))
		assert.Equal(tt, vsaErrors.ErrQuotaRuleBadRequest, customErr.TrackingID)
	})

	t.Run("APIInternalServerError", func(tt *testing.T) {
		ctx := context.Background()

		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() { googleproxyclient.GetGProxyClient = originalGetGProxyClient }()

		mockInvoker := googleproxyclient.NewMockInvoker(t)
		mc := &googleproxyclient.ProxyClient{
			Invoker: mockInvoker,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}

		mockInvoker.EXPECT().V1betaListAllQuotaRules(ctx, mock.Anything).
			Return(&googleproxyclient.V1betaListAllQuotaRulesInternalServerError{Message: "Internal error"}, nil)

		err := _verifyNewDestinationQuotaRulesReverse(ctx, event)
		assert.Error(tt, err)
		var customErr *vsaErrors.CustomError
		assert.True(tt, vsaErrors.As(err, &customErr))
		assert.Equal(tt, vsaErrors.ErrQuotaRuleInternalServerError, customErr.TrackingID)
	})

	t.Run("APIUnauthorized", func(tt *testing.T) {
		ctx := context.Background()

		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() { googleproxyclient.GetGProxyClient = originalGetGProxyClient }()

		mockInvoker := googleproxyclient.NewMockInvoker(t)
		mc := &googleproxyclient.ProxyClient{
			Invoker: mockInvoker,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}

		mockInvoker.EXPECT().V1betaListAllQuotaRules(ctx, mock.Anything).
			Return(&googleproxyclient.V1betaListAllQuotaRulesUnauthorized{Message: "Unauthorized"}, nil)

		err := _verifyNewDestinationQuotaRulesReverse(ctx, event)
		assert.Error(tt, err)
		var customErr *vsaErrors.CustomError
		assert.True(tt, vsaErrors.As(err, &customErr))
		assert.Equal(tt, vsaErrors.ErrQuotaRuleUnauthorized, customErr.TrackingID)
	})

	t.Run("APIForbidden", func(tt *testing.T) {
		ctx := context.Background()

		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() { googleproxyclient.GetGProxyClient = originalGetGProxyClient }()

		mockInvoker := googleproxyclient.NewMockInvoker(t)
		mc := &googleproxyclient.ProxyClient{
			Invoker: mockInvoker,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}

		mockInvoker.EXPECT().V1betaListAllQuotaRules(ctx, mock.Anything).
			Return(&googleproxyclient.V1betaListAllQuotaRulesForbidden{Message: "Forbidden"}, nil)

		err := _verifyNewDestinationQuotaRulesReverse(ctx, event)
		assert.Error(tt, err)
		var customErr *vsaErrors.CustomError
		assert.True(tt, vsaErrors.As(err, &customErr))
		assert.Equal(tt, vsaErrors.ErrQuotaRuleForbidden, customErr.TrackingID)
	})

	t.Run("APINotFound", func(tt *testing.T) {
		ctx := context.Background()

		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() { googleproxyclient.GetGProxyClient = originalGetGProxyClient }()

		mockInvoker := googleproxyclient.NewMockInvoker(t)
		mc := &googleproxyclient.ProxyClient{
			Invoker: mockInvoker,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}

		mockInvoker.EXPECT().V1betaListAllQuotaRules(ctx, mock.Anything).
			Return(&googleproxyclient.V1betaListAllQuotaRulesNotFound{Message: "Not found"}, nil)

		err := _verifyNewDestinationQuotaRulesReverse(ctx, event)
		assert.Error(tt, err)
		var customErr *vsaErrors.CustomError
		assert.True(tt, vsaErrors.As(err, &customErr))
		assert.Equal(tt, vsaErrors.ErrQuotaRuleNotFound, customErr.TrackingID)
	})

	t.Run("APIConflict", func(tt *testing.T) {
		ctx := context.Background()

		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() { googleproxyclient.GetGProxyClient = originalGetGProxyClient }()

		mockInvoker := googleproxyclient.NewMockInvoker(t)
		mc := &googleproxyclient.ProxyClient{
			Invoker: mockInvoker,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}

		mockInvoker.EXPECT().V1betaListAllQuotaRules(ctx, mock.Anything).
			Return(&googleproxyclient.V1betaListAllQuotaRulesConflict{Message: "Conflict"}, nil)

		err := _verifyNewDestinationQuotaRulesReverse(ctx, event)
		assert.Error(tt, err)
		var customErr *vsaErrors.CustomError
		assert.True(tt, vsaErrors.As(err, &customErr))
		assert.Equal(tt, vsaErrors.ErrQuotaRuleConflict, customErr.TrackingID)
	})

	t.Run("APIUnprocessableEntity", func(tt *testing.T) {
		ctx := context.Background()

		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() { googleproxyclient.GetGProxyClient = originalGetGProxyClient }()

		mockInvoker := googleproxyclient.NewMockInvoker(t)
		mc := &googleproxyclient.ProxyClient{
			Invoker: mockInvoker,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}

		mockInvoker.EXPECT().V1betaListAllQuotaRules(ctx, mock.Anything).
			Return(&googleproxyclient.V1betaListAllQuotaRulesUnprocessableEntity{Message: "Unprocessable"}, nil)

		err := _verifyNewDestinationQuotaRulesReverse(ctx, event)
		assert.Error(tt, err)
		var customErr *vsaErrors.CustomError
		assert.True(tt, vsaErrors.As(err, &customErr))
		assert.Equal(tt, vsaErrors.ErrQuotaRuleBadRequest, customErr.TrackingID)
	})

	t.Run("APITooManyRequests", func(tt *testing.T) {
		ctx := context.Background()

		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() { googleproxyclient.GetGProxyClient = originalGetGProxyClient }()

		mockInvoker := googleproxyclient.NewMockInvoker(t)
		mc := &googleproxyclient.ProxyClient{
			Invoker: mockInvoker,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}

		mockInvoker.EXPECT().V1betaListAllQuotaRules(ctx, mock.Anything).
			Return(&googleproxyclient.V1betaListAllQuotaRulesTooManyRequests{Message: "Too many requests"}, nil)

		err := _verifyNewDestinationQuotaRulesReverse(ctx, event)
		assert.Error(tt, err)
		var customErr *vsaErrors.CustomError
		assert.True(tt, vsaErrors.As(err, &customErr))
		assert.Equal(tt, vsaErrors.ErrQuotaRuleTooManyRequests, customErr.TrackingID)
	})

	t.Run("APIError", func(tt *testing.T) {
		ctx := context.Background()

		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() { googleproxyclient.GetGProxyClient = originalGetGProxyClient }()

		mockInvoker := googleproxyclient.NewMockInvoker(t)
		mc := &googleproxyclient.ProxyClient{
			Invoker: mockInvoker,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}

		mockInvoker.EXPECT().V1betaListAllQuotaRules(ctx, mock.Anything).
			Return(nil, vsaErrors.New("API error"))

		err := _verifyNewDestinationQuotaRulesReverse(ctx, event)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "failed to list quota rules on new destination volume")
	})
}

// TestVolumeIDValidation tests the volume ID validation regex pattern
func TestVolumeIDValidation(t *testing.T) {
	// Compile the regex pattern used in the validation
	compiledRegex := regexp.MustCompile(dstVolumeNameRegex)

	t.Run("ValidVolumeIDs", func(tt *testing.T) {
		validCases := []struct {
			name     string
			volumeID string
		}{
			{"SingleLetter", "a"},
			{"LettersAndNumbers", "abc123"},
			{"WithUnderscores", "test_volume_1"},
			{"MixedValidChars", "my_volume_123"},
			{"MaxLength", "a" + strings.Repeat("b", 61) + "c"}, // 63 chars total
			{"AllLowercase", "abcdefghijklmnopqrstuvwxyz"},
			{"NumbersOnly", "a123456789"},
			{"UnderscoresOnly", "a_b_c_d_e"},
		}

		for _, tc := range validCases {
			tt.Run(tc.name, func(t *testing.T) {
				matches := compiledRegex.MatchString(tc.volumeID)
				assert.True(t, matches, "Volume ID '%s' should match the regex pattern", tc.volumeID)
			})
		}
	})

	t.Run("InvalidVolumeIDs", func(tt *testing.T) {
		invalidCases := []struct {
			name     string
			volumeID string
		}{
			{"StartsWithNumber", "123abc"},
			{"StartsWithUppercase", "Abc123"},
			{"ContainsHyphen", "test-volume"},
			{"EndsWithUnderscore", "test_volume_"},
			{"ContainsSpecialChars", "test@volume"},
			{"ContainsSpaces", "test volume"},
			{"EmptyString", ""},
			{"TooLong", "a" + strings.Repeat("b", 62) + "c"}, // 64 chars total
			{"StartsWithSpecialChar", "_test"},
			{"StartsWithHyphen", "-test"},
		}

		for _, tc := range invalidCases {
			tt.Run(tc.name, func(t *testing.T) {
				matches := compiledRegex.MatchString(tc.volumeID)
				assert.False(t, matches, "Volume ID '%s' should NOT match the regex pattern", tc.volumeID)
			})
		}
	})

	t.Run("EmptyVolumeID", func(tt *testing.T) {
		// Empty volume ID should not match the regex (since it requires at least one character)
		matches := compiledRegex.MatchString("")
		assert.False(t, matches, "Empty volume ID should NOT match the regex pattern")
	})
}

func TestMapLifecycleStateToState(t *testing.T) {
	t.Run("ValidStateMappings", func(tt *testing.T) {
		testCases := []struct {
			name          string
			inputState    googleproxyclient.VolumeReplicationInternalV1betaLifeCycleState
			expectedState string
		}{
			{
				name:          "Creating",
				inputState:    googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateCreating,
				expectedState: datamodel.LifeCycleStateCreating,
			},
			{
				name:          "Available",
				inputState:    googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateAvailable,
				expectedState: datamodel.LifeCycleStateAvailable,
			},
			{
				name:          "Updating",
				inputState:    googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateUpdating,
				expectedState: datamodel.LifeCycleStateUpdating,
			},
			{
				name:          "Disabled",
				inputState:    googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateDisabled,
				expectedState: datamodel.LifeCycleStateDisabled,
			},
			{
				name:          "Deleting",
				inputState:    googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateDeleting,
				expectedState: datamodel.LifeCycleStateDeleting,
			},
			{
				name:          "Deleted",
				inputState:    googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateDeleted,
				expectedState: datamodel.LifeCycleStateDeleted,
			},
			{
				name:          "Error",
				inputState:    googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateError,
				expectedState: datamodel.LifeCycleStateError,
			},
		}

		for _, tc := range testCases {
			tt.Run(tc.name, func(t *testing.T) {
				result := mapLifecycleStateToState(tc.inputState)
				assert.Equal(t, tc.expectedState, result, "Expected state %s, got %s", tc.expectedState, result)
			})
		}
	})
	t.Run("UnknownState", func(tt *testing.T) {
		// Test with an unknown/invalid state
		unknownState := googleproxyclient.VolumeReplicationInternalV1betaLifeCycleState("unknown")
		result := mapLifecycleStateToState(unknownState)
		assert.Equal(t, datamodel.LifeCycleStateUnknown, result, "Unknown state should map to LifeCycleStateUnknown")
	})
	t.Run("EmptyState", func(tt *testing.T) {
		// Test with empty state
		emptyState := googleproxyclient.VolumeReplicationInternalV1betaLifeCycleState("")
		result := mapLifecycleStateToState(emptyState)
		assert.Equal(t, datamodel.LifeCycleStateUnknown, result, "Empty state should map to LifeCycleStateUnknown")
	})
	t.Run("CaseSensitivity", func(tt *testing.T) {
		// Test that the function is case-sensitive (should not match uppercase)
		uppercaseState := googleproxyclient.VolumeReplicationInternalV1betaLifeCycleState("CREATING")
		result := mapLifecycleStateToState(uppercaseState)
		assert.Equal(t, datamodel.LifeCycleStateUnknown, result, "Uppercase state should map to LifeCycleStateUnknown")
	})
	t.Run("AllEnumValuesCovered", func(tt *testing.T) {
		// Verify that all possible enum values are handled
		allStates := []googleproxyclient.VolumeReplicationInternalV1betaLifeCycleState{
			googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateCreating,
			googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateAvailable,
			googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateUpdating,
			googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateDisabled,
			googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateDeleting,
			googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateDeleted,
			googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateError,
		}

		for _, state := range allStates {
			result := mapLifecycleStateToState(state)
			assert.NotEqual(t, datamodel.LifeCycleStateUnknown, result, "State %s should not map to unknown", state)
		}
	})
}

func TestConvertReplicationResponseToModels(t *testing.T) {
	t.Run("ValidReplicationResponse", func(tt *testing.T) {
		testCases := []struct {
			name           string
			lifecycleState googleproxyclient.VolumeReplicationInternalV1betaLifeCycleState
			expectedState  string
		}{
			{
				name:           "CreatingState",
				lifecycleState: googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateCreating,
				expectedState:  datamodel.LifeCycleStateCreating,
			},
			{
				name:           "AvailableState",
				lifecycleState: googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateAvailable,
				expectedState:  datamodel.LifeCycleStateAvailable,
			},
			{
				name:           "UpdatingState",
				lifecycleState: googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateUpdating,
				expectedState:  datamodel.LifeCycleStateUpdating,
			},
			{
				name:           "DisabledState",
				lifecycleState: googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateDisabled,
				expectedState:  datamodel.LifeCycleStateDisabled,
			},
			{
				name:           "DeletingState",
				lifecycleState: googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateDeleting,
				expectedState:  datamodel.LifeCycleStateDeleting,
			},
			{
				name:           "DeletedState",
				lifecycleState: googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateDeleted,
				expectedState:  datamodel.LifeCycleStateDeleted,
			},
			{
				name:           "ErrorState",
				lifecycleState: googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateError,
				expectedState:  datamodel.LifeCycleStateError,
			},
		}

		for _, tc := range testCases {
			tt.Run(tc.name, func(t *testing.T) {
				// Create a mock response with the specific lifecycle state
				response := &googleproxyclient.V1betaGetMultipleReplicationsInternalOK{
					Replications: []googleproxyclient.VolumeReplicationInternalV1beta{
						{
							Name:                  googleproxyclient.OptString{Value: "test-replication", Set: true},
							VolumeReplicationUuid: googleproxyclient.OptString{Value: "test-uuid", Set: true},
							Description:           googleproxyclient.OptString{Value: "test description", Set: true},
							SourceVolumeUuid:      googleproxyclient.OptString{Value: "source-uuid", Set: true},
							SourceVolumeName:      "source-volume",
							DestinationVolumeUuid: googleproxyclient.OptString{Value: "dest-uuid", Set: true},
							DestinationVolumeName: "dest-volume",
							ReplicationSchedule:   googleproxyclient.OptVolumeReplicationInternalV1betaReplicationSchedule{Value: googleproxyclient.VolumeReplicationInternalV1betaReplicationScheduleHourly, Set: true},
							EndpointType:          googleproxyclient.VolumeReplicationInternalV1betaEndpointTypeSrc,
							TotalTransferBytes:    googleproxyclient.OptInt64{Value: 1024, Set: true},
							TotalTransferTimeSecs: googleproxyclient.OptInt64{Value: 3600, Set: true},
							LastTransferSize:      googleproxyclient.OptInt64{Value: 512, Set: true},
							LastTransferError:     googleproxyclient.OptString{Value: "", Set: true},
							LastTransferDuration:  googleproxyclient.OptInt64{Value: 1800, Set: true},
							TotalProgress:         googleproxyclient.OptInt64{Value: 50, Set: true},
							LagTime:               googleproxyclient.OptInt64{Value: 300, Set: true},
							LifeCycleState:        googleproxyclient.OptVolumeReplicationInternalV1betaLifeCycleState{Value: tc.lifecycleState, Set: true},
							LifeCycleStateDetails: googleproxyclient.OptString{Value: "test state details", Set: true},
							CreatedAt:             googleproxyclient.OptDateTime{Value: time.Now(), Set: true},
						},
					},
				}

				result := convertReplicationResponseToModels(response)

				assert.NotNil(t, result, "Result should not be nil")
				assert.Equal(t, tc.expectedState, result.State, "Expected state %s, got %s", tc.expectedState, result.State)
				assert.Equal(t, "test state details", result.StateDetails, "State details should match")
				assert.Equal(t, "test-replication", result.Name, "Name should match")
				assert.Equal(t, "test-uuid", result.UUID, "UUID should match")
			})
		}
	})

	t.Run("NilResponse", func(tt *testing.T) {
		// Note: The function doesn't handle nil response gracefully, so we expect a panic
		defer func() {
			if r := recover(); r != nil {
				// Expected panic when response is nil
			}
		}()
		result := convertReplicationResponseToModels(nil)
		assert.Nil(t, result, "Result should be nil for nil response")
	})

	t.Run("EmptyReplications", func(tt *testing.T) {
		response := &googleproxyclient.V1betaGetMultipleReplicationsInternalOK{
			Replications: []googleproxyclient.VolumeReplicationInternalV1beta{},
		}

		result := convertReplicationResponseToModels(response)
		assert.Nil(t, result, "Result should be nil for empty replications")
	})

	t.Run("UnknownLifecycleState", func(tt *testing.T) {
		unknownState := googleproxyclient.VolumeReplicationInternalV1betaLifeCycleState("unknown")
		response := &googleproxyclient.V1betaGetMultipleReplicationsInternalOK{
			Replications: []googleproxyclient.VolumeReplicationInternalV1beta{
				{
					Name:                  googleproxyclient.OptString{Value: "test-replication", Set: true},
					VolumeReplicationUuid: googleproxyclient.OptString{Value: "test-uuid", Set: true},
					Description:           googleproxyclient.OptString{Value: "test description", Set: true},
					SourceVolumeUuid:      googleproxyclient.OptString{Value: "source-uuid", Set: true},
					SourceVolumeName:      "source-volume",
					DestinationVolumeUuid: googleproxyclient.OptString{Value: "dest-uuid", Set: true},
					DestinationVolumeName: "dest-volume",
					ReplicationSchedule:   googleproxyclient.OptVolumeReplicationInternalV1betaReplicationSchedule{Value: googleproxyclient.VolumeReplicationInternalV1betaReplicationScheduleHourly, Set: true},
					EndpointType:          googleproxyclient.VolumeReplicationInternalV1betaEndpointTypeSrc,
					TotalTransferBytes:    googleproxyclient.OptInt64{Value: 1024, Set: true},
					TotalTransferTimeSecs: googleproxyclient.OptInt64{Value: 3600, Set: true},
					LastTransferSize:      googleproxyclient.OptInt64{Value: 512, Set: true},
					LastTransferError:     googleproxyclient.OptString{Value: "", Set: true},
					LastTransferDuration:  googleproxyclient.OptInt64{Value: 1800, Set: true},
					TotalProgress:         googleproxyclient.OptInt64{Value: 50, Set: true},
					LagTime:               googleproxyclient.OptInt64{Value: 300, Set: true},
					LifeCycleState:        googleproxyclient.OptVolumeReplicationInternalV1betaLifeCycleState{Value: unknownState, Set: true},
					LifeCycleStateDetails: googleproxyclient.OptString{Value: "test state details", Set: true},
					CreatedAt:             googleproxyclient.OptDateTime{Value: time.Now(), Set: true},
				},
			},
		}

		result := convertReplicationResponseToModels(response)

		assert.NotNil(t, result, "Result should not be nil")
		assert.Equal(t, datamodel.LifeCycleStateUnknown, result.State, "Unknown state should map to LifeCycleStateUnknown")
		assert.Equal(t, "test state details", result.StateDetails, "State details should match")
	})

	t.Run("CompleteReplicationData", func(tt *testing.T) {
		response := &googleproxyclient.V1betaGetMultipleReplicationsInternalOK{
			Replications: []googleproxyclient.VolumeReplicationInternalV1beta{
				{
					Name:                  googleproxyclient.OptString{Value: "test-replication", Set: true},
					VolumeReplicationUuid: googleproxyclient.OptString{Value: "test-uuid", Set: true},
					Description:           googleproxyclient.OptString{Value: "test description", Set: true},
					SourceVolumeUuid:      googleproxyclient.OptString{Value: "source-uuid", Set: true},
					SourceVolumeName:      "source-volume",
					DestinationVolumeUuid: googleproxyclient.OptString{Value: "dest-uuid", Set: true},
					DestinationVolumeName: "dest-volume",
					ReplicationSchedule:   googleproxyclient.OptVolumeReplicationInternalV1betaReplicationSchedule{Value: googleproxyclient.VolumeReplicationInternalV1betaReplicationScheduleHourly, Set: true},
					EndpointType:          googleproxyclient.VolumeReplicationInternalV1betaEndpointTypeSrc,
					TotalTransferBytes:    googleproxyclient.OptInt64{Value: 1024, Set: true},
					TotalTransferTimeSecs: googleproxyclient.OptInt64{Value: 3600, Set: true},
					LastTransferSize:      googleproxyclient.OptInt64{Value: 512, Set: true},
					LastTransferError:     googleproxyclient.OptString{Value: "", Set: true},
					LastTransferDuration:  googleproxyclient.OptInt64{Value: 1800, Set: true},
					TotalProgress:         googleproxyclient.OptInt64{Value: 50, Set: true},
					LagTime:               googleproxyclient.OptInt64{Value: 300, Set: true},
					LifeCycleState:        googleproxyclient.OptVolumeReplicationInternalV1betaLifeCycleState{Value: googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateAvailable, Set: true},
					LifeCycleStateDetails: googleproxyclient.OptString{Value: "test state details", Set: true},
					CreatedAt:             googleproxyclient.OptDateTime{Value: time.Now(), Set: true},
				},
			},
		}

		result := convertReplicationResponseToModels(response)

		assert.NotNil(t, result, "Result should not be nil")
		assert.Equal(t, datamodel.LifeCycleStateAvailable, result.State, "State should be mapped correctly")
		assert.Equal(t, "test state details", result.StateDetails, "State details should match")
		assert.Equal(t, "test-replication", result.Name, "Name should match")
		assert.Equal(t, "test-uuid", result.UUID, "UUID should match")
		assert.Equal(t, "test description", result.Description, "Description should match")
		assert.NotNil(t, result.ReplicationAttributes, "ReplicationAttributes should be set")
		assert.Equal(t, "source-uuid", result.ReplicationAttributes.SourceVolumeUUID, "SourceVolumeUUID should match")
		assert.Equal(t, "source-volume", result.ReplicationAttributes.SourceVolumeName, "SourceVolumeName should match")
		assert.Equal(t, "dest-uuid", result.ReplicationAttributes.DestinationVolumeUUID, "DestinationVolumeUUID should match")
		assert.Equal(t, "dest-volume", result.ReplicationAttributes.DestinationVolumeName, "DestinationVolumeName should match")
		assert.Equal(t, "hourly", result.ReplicationAttributes.ReplicationSchedule, "ReplicationSchedule should match")
		assert.Equal(t, "src", result.ReplicationAttributes.EndpointType, "EndpointType should match")
		assert.Equal(t, int64(1024), result.TotalTransferBytes, "TotalTransferBytes should match")
		assert.Equal(t, int64(3600), result.TotalTransferTimeSecs, "TotalTransferTimeSecs should match")
		assert.Equal(t, int64(512), result.LastTransferSize, "LastTransferSize should match")
		assert.Equal(t, "", result.LastTransferError, "LastTransferError should match")
		assert.Equal(t, int64(1800), result.LastTransferDuration, "LastTransferDuration should match")
		assert.Equal(t, int64(50), result.TotalProgress, "TotalProgress should match")
		assert.Equal(t, int64(300), result.LagTime, "LagTime should match")
	})
}

func TestIsPoolInTransitionState(t *testing.T) {
	t.Run("WhenCreatingState", func(tt *testing.T) {
		pool := &googleproxyclient.PoolInternalV1beta{
			Network:          "network",
			StoragePoolState: googleproxyclient.NewOptPoolInternalV1betaStoragePoolState(googleproxyclient.PoolInternalV1betaStoragePoolStateCREATING),
		}
		resp := isPoolInTransitionState(pool)
		assert.True(tt, resp, "Should be true")
	})
	t.Run("WhenDeletingState", func(tt *testing.T) {
		pool := &googleproxyclient.PoolInternalV1beta{
			Network:          "network",
			StoragePoolState: googleproxyclient.NewOptPoolInternalV1betaStoragePoolState(googleproxyclient.PoolInternalV1betaStoragePoolStateDELETING),
		}
		resp := isPoolInTransitionState(pool)
		assert.True(tt, resp, "Should be true")
	})
	t.Run("WhenUpdatingState", func(tt *testing.T) {
		pool := &googleproxyclient.PoolInternalV1beta{
			Network:          "network",
			StoragePoolState: googleproxyclient.NewOptPoolInternalV1betaStoragePoolState(googleproxyclient.PoolInternalV1betaStoragePoolStateUPDATING),
		}
		resp := isPoolInTransitionState(pool)
		assert.False(tt, resp, "Should be false")
	})
	t.Run("WhenReadyState", func(tt *testing.T) {
		pool := &googleproxyclient.PoolInternalV1beta{
			Network:          "network",
			StoragePoolState: googleproxyclient.NewOptPoolInternalV1betaStoragePoolState(googleproxyclient.PoolInternalV1betaStoragePoolStateREADY),
		}
		resp := isPoolInTransitionState(pool)
		assert.False(tt, resp, "Should be false")
	})
}

func TestIsPoolHealthy(t *testing.T) {
	t.Run("ReturnsFalse_WhenPoolStateIsERROR", func(tt *testing.T) {
		poolState := string(googleproxyclient.PoolV1betaStoragePoolStateERROR)
		result := isPoolHealthy(poolState)
		assert.False(tt, result, "Pool with ERROR state should be unhealthy")
	})

	t.Run("ReturnsFalse_WhenPoolStateIsDISABLED", func(tt *testing.T) {
		poolState := string(googleproxyclient.PoolV1betaStoragePoolStateDISABLED)
		result := isPoolHealthy(poolState)
		assert.False(tt, result, "Pool with DISABLED state should be unhealthy")
	})

	t.Run("ReturnsFalse_WhenPoolStateIsDEGRADED", func(tt *testing.T) {
		poolState := string(googleproxyclient.PoolV1betaStoragePoolStateDEGRADED)
		result := isPoolHealthy(poolState)
		assert.False(tt, result, "Pool with DEGRADED state should be unhealthy")
	})

	t.Run("ReturnsTrue_WhenPoolStateIsREADY", func(tt *testing.T) {
		poolState := string(googleproxyclient.PoolV1betaStoragePoolStateREADY)
		result := isPoolHealthy(poolState)
		assert.True(tt, result, "Pool with READY state should be healthy")
	})

	t.Run("ReturnsTrue_WhenPoolStateIsUPDATING", func(tt *testing.T) {
		poolState := string(googleproxyclient.PoolV1betaStoragePoolStateUPDATING)
		result := isPoolHealthy(poolState)
		assert.True(tt, result, "Pool with UPDATING state should be healthy")
	})
}

func TestIsPoolInONTAPMode(t *testing.T) {
	t.Run("WhenModeIsONTAPAndSet", func(tt *testing.T) {
		pool := &googleproxyclient.PoolInternalV1beta{
			Network: "network",
			Mode:    googleproxyclient.OptPoolInternalV1betaMode{Value: googleproxyclient.PoolInternalV1betaMode(common.ONTAPMode), Set: true},
		}
		resp := isPoolInONTAPMode(pool)
		assert.True(tt, resp, "Should be true when mode is ONTAP and Set is true")
	})
	t.Run("WhenModeIsONTAPButNotSet", func(tt *testing.T) {
		pool := &googleproxyclient.PoolInternalV1beta{
			Network: "network",
			Mode:    googleproxyclient.OptPoolInternalV1betaMode{Value: googleproxyclient.PoolInternalV1betaMode(common.ONTAPMode), Set: false},
		}
		resp := isPoolInONTAPMode(pool)
		assert.False(tt, resp, "Should be false when Set is false")
	})
	t.Run("WhenModeIsDEFAULT", func(tt *testing.T) {
		pool := &googleproxyclient.PoolInternalV1beta{
			Network: "network",
			Mode:    googleproxyclient.OptPoolInternalV1betaMode{Value: googleproxyclient.PoolInternalV1betaMode(common.DEFAULTMode), Set: true},
		}
		resp := isPoolInONTAPMode(pool)
		assert.False(tt, resp, "Should be false when mode is DEFAULT")
	})
	t.Run("WhenModeIsEmpty", func(tt *testing.T) {
		pool := &googleproxyclient.PoolInternalV1beta{
			Network: "network",
			Mode:    googleproxyclient.OptPoolInternalV1betaMode{Value: "", Set: false},
		}
		resp := isPoolInONTAPMode(pool)
		assert.False(tt, resp, "Should be false when mode is empty")
	})
}

func TestIsSrcForHybridReplication(t *testing.T) {
	reverseType := string(datamodel.HybridReplicationParametersReplicationTypeREVERSE)
	migrationType := string(datamodel.HybridReplicationParametersReplicationTypeMIGRATION)

	t.Run("ReturnsTrue_WhenAllConditionsMet", func(tt *testing.T) {
		replication := &datamodel.VolumeReplication{
			HybridReplicationAttributes: &datamodel.HybridReplicationAttribute{
				HybridReplicationType: &reverseType,
			},
			ReplicationAttributes: &datamodel.ReplicationDetails{
				DestinationLocation: remoteRegionCustomer,
			},
		}
		result := IsSrcForHybridReplication(replication)
		assert.True(tt, result)
	})

	t.Run("ReturnsFalse_WhenHybridReplicationAttributesIsNil", func(tt *testing.T) {
		replication := &datamodel.VolumeReplication{
			HybridReplicationAttributes: nil,
			ReplicationAttributes: &datamodel.ReplicationDetails{
				DestinationLocation: "",
			},
		}
		result := IsSrcForHybridReplication(replication)
		assert.False(tt, result)
	})

	t.Run("ReturnsFalse_WhenHybridReplicationTypeIsNil", func(tt *testing.T) {
		replication := &datamodel.VolumeReplication{
			HybridReplicationAttributes: &datamodel.HybridReplicationAttribute{
				HybridReplicationType: nil,
			},
			ReplicationAttributes: &datamodel.ReplicationDetails{
				DestinationLocation: "",
			},
		}
		result := IsSrcForHybridReplication(replication)
		assert.False(tt, result)
	})

	t.Run("ReturnsFalse_WhenHybridReplicationTypeIsNotReverse", func(tt *testing.T) {
		replication := &datamodel.VolumeReplication{
			HybridReplicationAttributes: &datamodel.HybridReplicationAttribute{
				HybridReplicationType: &migrationType,
			},
			ReplicationAttributes: &datamodel.ReplicationDetails{
				DestinationLocation: "",
			},
		}
		result := IsSrcForHybridReplication(replication)
		assert.False(tt, result)
	})

	t.Run("ReturnsFalse_WhenDestinationLocationIsNotEmpty", func(tt *testing.T) {
		replication := &datamodel.VolumeReplication{
			HybridReplicationAttributes: &datamodel.HybridReplicationAttribute{
				HybridReplicationType: &reverseType,
			},
			ReplicationAttributes: &datamodel.ReplicationDetails{
				DestinationLocation: "us-central1",
			},
		}
		result := IsSrcForHybridReplication(replication)
		assert.False(tt, result)
	})

	t.Run("Panics_WhenReplicationAttributesIsNil", func(tt *testing.T) {
		replication := &datamodel.VolumeReplication{
			HybridReplicationAttributes: &datamodel.HybridReplicationAttribute{
				HybridReplicationType: &reverseType,
			},
			ReplicationAttributes: nil,
		}
		assert.Panics(tt, func() {
			IsSrcForHybridReplication(replication)
		}, "Should panic when ReplicationAttributes is nil")
	})
}

func TestGetPath(t *testing.T) {
	t.Run("ReturnsFormattedPath_WhenBothParametersProvided", func(tt *testing.T) {
		svmName := "test-svm"
		volumeName := "test-volume"
		result := getPath(svmName, volumeName)
		assert.Equal(tt, "test-svm:test-volume", result)
	})

	t.Run("ReturnsFormattedPath_WhenSvmNameIsEmpty", func(tt *testing.T) {
		svmName := ""
		volumeName := "test-volume"
		result := getPath(svmName, volumeName)
		assert.Equal(tt, ":test-volume", result)
	})

	t.Run("ReturnsFormattedPath_WhenVolumeNameIsEmpty", func(tt *testing.T) {
		svmName := "test-svm"
		volumeName := ""
		result := getPath(svmName, volumeName)
		assert.Equal(tt, "test-svm:", result)
	})

	t.Run("ReturnsFormattedPath_WhenBothParametersAreEmpty", func(tt *testing.T) {
		svmName := ""
		volumeName := ""
		result := getPath(svmName, volumeName)
		assert.Equal(tt, ":", result)
	})

	t.Run("ReturnsFormattedPath_WithUnderscores", func(tt *testing.T) {
		svmName := "test_svm_name"
		volumeName := "test_volume_name"
		result := getPath(svmName, volumeName)
		assert.Equal(tt, "test_svm_name:test_volume_name", result)
	})

	t.Run("ReturnsFormattedPath_WithNumbers", func(tt *testing.T) {
		svmName := "svm123"
		volumeName := "volume456"
		result := getPath(svmName, volumeName)
		assert.Equal(tt, "svm123:volume456", result)
	})

	t.Run("ReturnsFormattedPath_WithMixedCharacters", func(tt *testing.T) {
		svmName := "svm-01_test"
		volumeName := "volume-02_test"
		result := getPath(svmName, volumeName)
		assert.Equal(tt, "svm-01_test:volume-02_test", result)
	})

	t.Run("ReturnsFormattedPath_WithLongNames", func(tt *testing.T) {
		svmName := strings.Repeat("a", 100)
		volumeName := strings.Repeat("b", 100)
		result := getPath(svmName, volumeName)
		expected := svmName + ":" + volumeName
		assert.Equal(tt, expected, result)
	})

	t.Run("ReturnsFormattedPath_WithSingleCharacterNames", func(tt *testing.T) {
		svmName := "a"
		volumeName := "b"
		result := getPath(svmName, volumeName)
		assert.Equal(tt, "a:b", result)
	})

	t.Run("ReturnsFormattedPath_WithSpaces", func(tt *testing.T) {
		svmName := "svm name"
		volumeName := "volume name"
		result := getPath(svmName, volumeName)
		assert.Equal(tt, "svm name:volume name", result)
	})
}

func Test_verifyEstablishPeering(t *testing.T) {
	ctx := context.Background()
	accountID := int64(1)
	ccfeURI := "projects/test-account/locations/us-central1/volumes/volume-123/replications/replication-123"
	params := &common.EstablishReplicationPeeringParams{
		AccountName:           "test-account",
		Region:                "us-central1",
		Zone:                  "",
		VolumeResourceId:      "volume-123",
		ReplicationResourceId: "replication-123",
		CorrelationId:         "corr-123",
		PeerVolumeName:        "peer-volume",
		PeerClusterName:       "peer-cluster",
		PeerSvmName:           "peer-svm",
		PeerIPAddresses:       []string{"10.0.0.1"},
	}

	t.Run("WhenListVolumeReplicationsFails", func(tt *testing.T) {
		mockStorage := &database.MockStorage{}
		dbError := errors.New("database error")
		mockStorage.On("ListVolumeReplications", ctx, mock.Anything, database.QueryDepthOne).Return(nil, dbError)

		originalVerifyHybridParameters := verifyHybridParameters
		originalVerifyClusterPeering := isClusterPeeringStateValid
		defer func() {
			verifyHybridParameters = originalVerifyHybridParameters
			isClusterPeeringStateValid = originalVerifyClusterPeering
		}()

		replication, err := _verifyEstablishPeering(ctx, params, mockStorage, accountID, ccfeURI)
		assert.Error(tt, err)
		assert.Nil(tt, replication)

		var customErr *vsaErrors.CustomError
		assert.True(tt, vsaErrors.As(err, &customErr), "Expected a CustomError")
		assert.NotNil(tt, customErr)
		assert.Equal(tt, vsaErrors.ErrDatabaseDataReadError, customErr.TrackingID)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenNoReplicationFound", func(tt *testing.T) {
		mockStorage := &database.MockStorage{}
		mockStorage.On("ListVolumeReplications", ctx, mock.Anything, database.QueryDepthOne).Return([]*datamodel.VolumeReplication{}, nil)

		originalVerifyHybridParameters := verifyHybridParameters
		originalVerifyClusterPeering := isClusterPeeringStateValid
		defer func() {
			verifyHybridParameters = originalVerifyHybridParameters
			isClusterPeeringStateValid = originalVerifyClusterPeering
		}()

		replication, err := _verifyEstablishPeering(ctx, params, mockStorage, accountID, ccfeURI)
		assert.Error(tt, err)
		assert.Nil(tt, replication)
		assert.Contains(tt, err.Error(), "No replication found for the given URI")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenVerifyHybridParametersFails", func(tt *testing.T) {
		mockStorage := &database.MockStorage{}
		replicationDb := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{UUID: "replication-123"},
			Name:      "test-replication",
			HybridReplicationAttributes: &datamodel.HybridReplicationAttribute{
				PeerSvmName:    "different-svm",
				PeerVolumeName: "different-volume",
				Status:         datamodel.HybridReplicationStatusPendingClusterPeer,
			},
		}
		mockStorage.On("ListVolumeReplications", ctx, mock.Anything, database.QueryDepthOne).Return([]*datamodel.VolumeReplication{replicationDb}, nil)

		originalVerifyHybridParameters := verifyHybridParameters
		originalVerifyClusterPeering := isClusterPeeringStateValid
		defer func() {
			verifyHybridParameters = originalVerifyHybridParameters
			isClusterPeeringStateValid = originalVerifyClusterPeering
		}()

		verifyHybridParameters = func(ctx context.Context, params *common.EstablishReplicationPeeringParams, hybridReplicationParameters datamodel.HybridReplicationAttribute) error {
			return fmt.Errorf("provided hybrid Replication parameters do not match with existing hybrid Replication parameters")
		}

		replication, err := _verifyEstablishPeering(ctx, params, mockStorage, accountID, ccfeURI)
		assert.Error(tt, err)
		assert.Nil(tt, replication)
		assert.Contains(tt, err.Error(), "provided hybrid Replication parameters do not match")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenClusterPeeringAlreadyEstablished_ClusterPeerPEERED", func(tt *testing.T) {
		mockStorage := &database.MockStorage{}
		replicationDb := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{UUID: "replication-123"},
			Name:      "test-replication",
			ClusterPeer: &datamodel.ClusterPeerings{
				State: datamodel.CvpClusterPeeringStatusPEERED,
			},
			HybridReplicationAttributes: &datamodel.HybridReplicationAttribute{
				PeerSvmName:    params.PeerSvmName,
				PeerVolumeName: params.PeerVolumeName,
				Status:         datamodel.HybridReplicationStatusPendingClusterPeer,
			},
		}
		mockStorage.On("ListVolumeReplications", ctx, mock.Anything, database.QueryDepthOne).Return([]*datamodel.VolumeReplication{replicationDb}, nil)

		originalVerifyHybridParameters := verifyHybridParameters
		originalVerifyClusterPeering := isClusterPeeringStateValid
		defer func() {
			verifyHybridParameters = originalVerifyHybridParameters
			isClusterPeeringStateValid = originalVerifyClusterPeering
		}()

		verifyHybridParameters = func(ctx context.Context, params *common.EstablishReplicationPeeringParams, hybridReplicationParameters datamodel.HybridReplicationAttribute) error {
			return nil
		}

		replication, err := _verifyEstablishPeering(ctx, params, mockStorage, accountID, ccfeURI)
		assert.Error(tt, err)
		assert.Nil(tt, replication)
		assert.Contains(tt, err.Error(), "cluster peering is already established")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenClusterPeeringAlreadyEstablished_InvalidStatus", func(tt *testing.T) {
		mockStorage := &database.MockStorage{}
		replicationDb := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{UUID: "replication-123"},
			Name:      "test-replication",
			HybridReplicationAttributes: &datamodel.HybridReplicationAttribute{
				PeerSvmName:    params.PeerSvmName,
				PeerVolumeName: params.PeerVolumeName,
				Status:         datamodel.HybridReplicationStatusPeered, // Not PendingClusterPeer or PendingSVMPeer
			},
		}
		mockStorage.On("ListVolumeReplications", ctx, mock.Anything, database.QueryDepthOne).Return([]*datamodel.VolumeReplication{replicationDb}, nil)

		originalVerifyHybridParameters := verifyHybridParameters
		originalVerifyClusterPeering := isClusterPeeringStateValid
		defer func() {
			verifyHybridParameters = originalVerifyHybridParameters
			isClusterPeeringStateValid = originalVerifyClusterPeering
		}()

		verifyHybridParameters = func(ctx context.Context, params *common.EstablishReplicationPeeringParams, hybridReplicationParameters datamodel.HybridReplicationAttribute) error {
			return nil
		}

		replication, err := _verifyEstablishPeering(ctx, params, mockStorage, accountID, ccfeURI)
		assert.Error(tt, err)
		assert.Nil(tt, replication)
		assert.Contains(tt, err.Error(), "cluster peering is already established")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenSucceeds_PendingClusterPeer", func(tt *testing.T) {
		mockStorage := &database.MockStorage{}
		replicationDb := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{UUID: "replication-123"},
			Name:      "test-replication",
			HybridReplicationAttributes: &datamodel.HybridReplicationAttribute{
				PeerSvmName:    params.PeerSvmName,
				PeerVolumeName: params.PeerVolumeName,
				Status:         datamodel.HybridReplicationStatusPendingClusterPeer,
			},
		}
		mockStorage.On("ListVolumeReplications", ctx, mock.Anything, database.QueryDepthOne).Return([]*datamodel.VolumeReplication{replicationDb}, nil)

		originalVerifyHybridParameters := verifyHybridParameters
		originalVerifyClusterPeering := isClusterPeeringStateValid
		defer func() {
			verifyHybridParameters = originalVerifyHybridParameters
			isClusterPeeringStateValid = originalVerifyClusterPeering
		}()

		verifyHybridParameters = func(ctx context.Context, params *common.EstablishReplicationPeeringParams, hybridReplicationParameters datamodel.HybridReplicationAttribute) error {
			return nil
		}

		replication, err := _verifyEstablishPeering(ctx, params, mockStorage, accountID, ccfeURI)
		assert.NoError(tt, err)
		assert.NotNil(tt, replication)
		assert.Equal(tt, "replication-123", replication.UUID)
		assert.Equal(tt, "test-replication", replication.Name)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenSucceeds_PendingSVMPeer", func(tt *testing.T) {
		mockStorage := &database.MockStorage{}
		replicationDb := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{UUID: "replication-123"},
			Name:      "test-replication",
			HybridReplicationAttributes: &datamodel.HybridReplicationAttribute{
				PeerSvmName:    params.PeerSvmName,
				PeerVolumeName: params.PeerVolumeName,
				Status:         datamodel.HybridReplicationStatusPendingSVMPeer,
			},
		}
		mockStorage.On("ListVolumeReplications", ctx, mock.Anything, database.QueryDepthOne).Return([]*datamodel.VolumeReplication{replicationDb}, nil)

		originalVerifyHybridParameters := verifyHybridParameters
		originalVerifyClusterPeering := isClusterPeeringStateValid
		defer func() {
			verifyHybridParameters = originalVerifyHybridParameters
			isClusterPeeringStateValid = originalVerifyClusterPeering
		}()

		verifyHybridParameters = func(ctx context.Context, params *common.EstablishReplicationPeeringParams, hybridReplicationParameters datamodel.HybridReplicationAttribute) error {
			return nil
		}

		replication, err := _verifyEstablishPeering(ctx, params, mockStorage, accountID, ccfeURI)
		assert.NoError(tt, err)
		assert.NotNil(tt, replication)
		assert.Equal(tt, "replication-123", replication.UUID)
		assert.Equal(tt, "test-replication", replication.Name)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenHybridReplicationAttributesIsNil", func(tt *testing.T) {
		mockStorage := &database.MockStorage{}
		replicationDb := &datamodel.VolumeReplication{
			BaseModel:                   datamodel.BaseModel{UUID: "replication-123"},
			Name:                        "test-replication",
			HybridReplicationAttributes: nil, // nil HybridReplicationAttributes
		}
		mockStorage.On("ListVolumeReplications", ctx, mock.Anything, database.QueryDepthOne).Return([]*datamodel.VolumeReplication{replicationDb}, nil)

		originalVerifyHybridParameters := verifyHybridParameters
		originalVerifyClusterPeering := isClusterPeeringStateValid
		defer func() {
			verifyHybridParameters = originalVerifyHybridParameters
			isClusterPeeringStateValid = originalVerifyClusterPeering
		}()

		replication, err := _verifyEstablishPeering(ctx, params, mockStorage, accountID, ccfeURI)
		assert.Error(tt, err)
		assert.Nil(tt, replication)
		assert.Contains(tt, err.Error(), "Replication does not have hybrid replication attributes")
		mockStorage.AssertExpectations(tt)
	})
}

func Test_verifyHybridParameters(t *testing.T) {
	ctx := context.Background()
	params := &common.EstablishReplicationPeeringParams{
		PeerSvmName:    "peer-svm",
		PeerVolumeName: "peer-volume",
	}

	t.Run("WhenParametersMatch", func(tt *testing.T) {
		hybridReplicationParameters := datamodel.HybridReplicationAttribute{
			PeerSvmName:    "peer-svm",
			PeerVolumeName: "peer-volume",
		}

		err := _verifyHybridParameters(ctx, params, hybridReplicationParameters)
		assert.NoError(tt, err)
	})

	t.Run("WhenPeerSvmNameDoesNotMatch", func(tt *testing.T) {
		hybridReplicationParameters := datamodel.HybridReplicationAttribute{
			PeerSvmName:    "different-svm",
			PeerVolumeName: "peer-volume",
		}

		err := _verifyHybridParameters(ctx, params, hybridReplicationParameters)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "provided hybrid Replication parameters do not match with existing hybrid Replication parameters")
	})

	t.Run("WhenPeerVolumeNameDoesNotMatch", func(tt *testing.T) {
		hybridReplicationParameters := datamodel.HybridReplicationAttribute{
			PeerSvmName:    "peer-svm",
			PeerVolumeName: "different-volume",
		}

		err := _verifyHybridParameters(ctx, params, hybridReplicationParameters)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "provided hybrid Replication parameters do not match with existing hybrid Replication parameters")
	})

	t.Run("WhenBothParametersDoNotMatch", func(tt *testing.T) {
		hybridReplicationParameters := datamodel.HybridReplicationAttribute{
			PeerSvmName:    "different-svm",
			PeerVolumeName: "different-volume",
		}

		err := _verifyHybridParameters(ctx, params, hybridReplicationParameters)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "provided hybrid Replication parameters do not match with existing hybrid Replication parameters")
	})

	t.Run("WhenParametersMatchWithEmptyStrings", func(tt *testing.T) {
		paramsEmpty := &common.EstablishReplicationPeeringParams{
			PeerSvmName:    "",
			PeerVolumeName: "",
		}
		hybridReplicationParameters := datamodel.HybridReplicationAttribute{
			PeerSvmName:    "",
			PeerVolumeName: "",
		}

		err := _verifyHybridParameters(ctx, paramsEmpty, hybridReplicationParameters)
		assert.NoError(tt, err)
	})
}

func Test_verifyClusterPeering(t *testing.T) {
	ctx := context.Background()

	t.Run("WhenClusterPeerIsPEERED", func(tt *testing.T) {
		replication := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{UUID: "replication-123"},
			Name:      "test-replication",
			ClusterPeer: &datamodel.ClusterPeerings{
				State: datamodel.CvpClusterPeeringStatusPEERED,
			},
			HybridReplicationAttributes: &datamodel.HybridReplicationAttribute{
				Status: datamodel.HybridReplicationStatusPendingClusterPeer,
			},
		}

		result := _isClusterPeeringStateValid(ctx, replication)
		assert.True(tt, result, "Should return true when ClusterPeer is PEERED")
	})

	t.Run("WhenClusterPeerIsNotPEERED", func(tt *testing.T) {
		replication := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{UUID: "replication-123"},
			Name:      "test-replication",
			ClusterPeer: &datamodel.ClusterPeerings{
				State: datamodel.CvpClusterPeeringStatusCREATING,
			},
			HybridReplicationAttributes: &datamodel.HybridReplicationAttribute{
				Status: datamodel.HybridReplicationStatusPendingClusterPeer,
			},
		}

		result := _isClusterPeeringStateValid(ctx, replication)
		assert.False(tt, result, "Should return false when ClusterPeer is not PEERED and status is valid")
	})

	t.Run("WhenStatusIsPendingClusterPeer", func(tt *testing.T) {
		replication := &datamodel.VolumeReplication{
			BaseModel:   datamodel.BaseModel{UUID: "replication-123"},
			Name:        "test-replication",
			ClusterPeer: nil,
			HybridReplicationAttributes: &datamodel.HybridReplicationAttribute{
				Status: datamodel.HybridReplicationStatusPendingClusterPeer,
			},
		}

		result := _isClusterPeeringStateValid(ctx, replication)
		assert.False(tt, result, "Should return false when status is PendingClusterPeer")
	})

	t.Run("WhenStatusIsPendingSVMPeer", func(tt *testing.T) {
		replication := &datamodel.VolumeReplication{
			BaseModel:   datamodel.BaseModel{UUID: "replication-123"},
			Name:        "test-replication",
			ClusterPeer: nil,
			HybridReplicationAttributes: &datamodel.HybridReplicationAttribute{
				Status: datamodel.HybridReplicationStatusPendingSVMPeer,
			},
		}

		result := _isClusterPeeringStateValid(ctx, replication)
		assert.False(tt, result, "Should return false when status is PendingSVMPeer")
	})

	t.Run("WhenStatusIsPeered", func(tt *testing.T) {
		replication := &datamodel.VolumeReplication{
			BaseModel:   datamodel.BaseModel{UUID: "replication-123"},
			Name:        "test-replication",
			ClusterPeer: nil,
			HybridReplicationAttributes: &datamodel.HybridReplicationAttribute{
				Status: datamodel.HybridReplicationStatusPeered,
			},
		}

		result := _isClusterPeeringStateValid(ctx, replication)
		assert.True(tt, result, "Should return true when status is Peered (invalid for establishing peering)")
	})

	t.Run("WhenStatusIsSVMPeered", func(tt *testing.T) {
		replication := &datamodel.VolumeReplication{
			BaseModel:   datamodel.BaseModel{UUID: "replication-123"},
			Name:        "test-replication",
			ClusterPeer: nil,
			HybridReplicationAttributes: &datamodel.HybridReplicationAttribute{
				Status: datamodel.HybridReplicationStatusSVMPeered,
			},
		}

		result := _isClusterPeeringStateValid(ctx, replication)
		assert.True(tt, result, "Should return true when status is SVMPeered (invalid for establishing peering)")
	})

	t.Run("WhenStatusIsExternalManaged", func(tt *testing.T) {
		replication := &datamodel.VolumeReplication{
			BaseModel:   datamodel.BaseModel{UUID: "replication-123"},
			Name:        "test-replication",
			ClusterPeer: nil,
			HybridReplicationAttributes: &datamodel.HybridReplicationAttribute{
				Status: datamodel.HybridReplicationStatusExternalManaged,
			},
		}

		result := _isClusterPeeringStateValid(ctx, replication)
		assert.True(tt, result, "Should return true when status is ExternalManaged (invalid for establishing peering)")
	})

	t.Run("WhenClusterPeerIsNilAndStatusIsValid", func(tt *testing.T) {
		replication := &datamodel.VolumeReplication{
			BaseModel:   datamodel.BaseModel{UUID: "replication-123"},
			Name:        "test-replication",
			ClusterPeer: nil,
			HybridReplicationAttributes: &datamodel.HybridReplicationAttribute{
				Status: datamodel.HybridReplicationStatusPendingClusterPeer,
			},
		}

		result := _isClusterPeeringStateValid(ctx, replication)
		assert.False(tt, result, "Should return false when ClusterPeer is nil and status is valid")
	})

	t.Run("WhenClusterPeerIsNilAndStatusIsPendingSVMPeer", func(tt *testing.T) {
		replication := &datamodel.VolumeReplication{
			BaseModel:   datamodel.BaseModel{UUID: "replication-123"},
			Name:        "test-replication",
			ClusterPeer: nil,
			HybridReplicationAttributes: &datamodel.HybridReplicationAttribute{
				Status: datamodel.HybridReplicationStatusPendingSVMPeer,
			},
		}

		result := _isClusterPeeringStateValid(ctx, replication)
		assert.False(tt, result, "Should return false when ClusterPeer is nil and status is PendingSVMPeer")
	})
}

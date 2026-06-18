//go:build mocks

package replication

import (
	"context"

	googleproxyclient "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/google-proxy-client"
	common "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/mqos"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
)

type monkeyMethods interface {
	InternalUtilGetCallbackToken() (string, error)
	InternalUtilGetSignedToken(projectNumber string) (string, error)
	InternalUtilGetPairedRegionURI(region string) (string, error)
	InternalParseRegionAndZone(location string) (string, string, error)
	validateReplicationResourceId(ctx context.Context, projectNumber string, paramReplicationResourceId string, paramsVolumeResourceId string, se database.Storage) error
	validateStoragePoolUri(uri string) error
	getDestinationPool(ctx context.Context, destBasePath string, dstToken string, remoteLocationID string, projectNumber string, xCorrelationID *string, name string) (*googleproxyclient.PoolInternalV1beta, error)
	validateVolumeQosParamsForReplication(pool mqos.PoolQosInput, throughputMibps *int64, iops *int64, vpgID *string) (*int64, error)
	replicationJobInProcess(ctx context.Context, srcProjectNumber string, destProjectNumber string, srcBasePath string, destBasePath string, srcLocationID string, destLocationId, srcToken string, destToken string, ccfeUri string, remoteCcfeUri string, srcPoolId, dstPoolId string, correlationId *string) error
	getQuotaLimit(ctx context.Context, logger log.Logger, region string, projectId string, token string, resourceType common.ResourceType) (int, error)
	internalGetReplicationCount(ctx context.Context, basePath string, projectNumber string, locationID string, poolID string, jwt string, storageClass, serviceLevel string) (int, error)
	internalCountInRegionReplications(ctx context.Context, se database.Storage, projectNumber string) (int, error)
	internalGetVolumeCount(ctx context.Context, basePath string, projectNumber string, locationID string, poolID string, jwt string, storageClass, serviceLevel string) (int, error)
	getVolume(ctx context.Context, basePath string, token string, locationID string, projectNumber string, xCorrelationID *string, volumeResourceId string) (googleproxyclient.VolumeV1beta, error)
	createReplicationObjects(event *CreateReplicationEvent, remotelocation, region, remoteRegion string) (*datamodel.VolumeReplication, error)
}

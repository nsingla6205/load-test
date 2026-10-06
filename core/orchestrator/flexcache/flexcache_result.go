package flexcache

import (
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
)

type CreateFlexCacheResult struct {
	Event             *CreateFlexCacheEvent
	DBVolume          *datamodel.Volume
	Node              *models.Node
	ClusterPeer       *vsa.ClusterPeer
	SVMPeer           *vsa.SvmPeer
	VolumeResponse    *vsa.VolumeResponse
	JobInput          *JobActivityInput
	ActiveJobType     datamodel.JobType
	ErrorTrackingID   int
	ErrorMessage      string
	ClusterPeerAction Action
	SVMPeerAction     Action
	ClusterPeeringRow *datamodel.ClusterPeerings
}

type JobActivityInput struct {
	ResourceName  string
	ResourceUUID  string
	AccountID     int64
	CorrelationID string
	RequestID     string
	WorkflowID    string
	Metadata      map[string]interface{}
}

type DeleteFlexCacheResult struct {
	DBVolume                               *datamodel.Volume
	Node                                   *models.Node
	DisableWritebackJobResponse            *vsa.OntapAsyncResponse
	UnmountJobResponse                     *vsa.OntapAsyncResponse
	DeleteJobResponse                      *vsa.OntapAsyncResponse
	ClusterPeeringRow                      *datamodel.ClusterPeerings
	VolumeReplicationCountOnClusterPeering int64
	FlexCacheVolumeCountOnClusterPeering   int64
}

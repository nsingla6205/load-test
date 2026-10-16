package opsinventory

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp/readstore"
	oasgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcp-core/servergen"
)

var replicationEnd = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)

var replicationRow = datamodel.VolumeReplication{
	BaseModel: datamodel.BaseModel{ID: 41, UUID: "rep-1"},
	Name:      "r1", State: "READY", AccountID: 1, Healthy: false, UnhealthyReason: sentinel, LastTransferError: sentinel,
	MirrorState: strPtr("snapmirrored"), LagTime: 30, LastTransferEndTime: &replicationEnd,
	Account: &datamodel.Account{Name: "1111"},
	Volume:  &datamodel.Volume{BaseModel: datamodel.BaseModel{UUID: "vol-1"}, Name: "v1"},
	ReplicationAttributes: &datamodel.ReplicationDetails{
		EndpointType: "dst", SourceVolumeUUID: "src-vol", SourceLocation: "us-east4", SourceSvmName: "svm-src",
		DestinationVolumeUUID: "vol-1", SourceHostName: sentinel, DestinationHostName: sentinel,
	},
	ClusterPeer:                 &datamodel.ClusterPeerings{},
	HybridReplicationAttributes: &datamodel.HybridReplicationAttribute{SvmPeerCommand: strPtr(sentinel)},
}

func TestNewReplicationReader_Spec(t *testing.T) {
	spec := NewReplicationReader(nil).Spec()
	assert.Equal(t, ResourceTypeReplication, spec.Type)
	assert.Equal(t, []ParentType{oasgenserver.OpsParentTypeV1Volume}, spec.NameParents)
	assert.Equal(t, ParentSpec{Column: readstore.ColReplicationSourceVolumeUUID}, spec.ListParents[oasgenserver.OpsParentTypeV1SourceVolume], "remote side has no lookup")
	assert.Equal(t, ParentSpec{Column: readstore.ColReplicationDestinationVolumeUUID}, spec.ListParents[oasgenserver.OpsParentTypeV1DestinationVolume])
	assert.Contains(t, spec.States, string(datamodel.HybridReplicationStatusPeered))
	assert.Contains(t, spec.States, string(datamodel.CvpClusterPeeringStatusPENDINGCLUSTERPEERING))
}

func TestReplicationReaderGet(t *testing.T) {
	ctx := context.Background()

	t.Run("WhenByUUID", func(t *testing.T) {
		store := readstore.NewMockReadStore(t)
		store.EXPECT().GetReplicationByUUID(mock.Anything, "rep-1").Return(&replicationRow, nil)
		res, err := NewReplicationReader(store).Get(ctx, GetRequest{UUID: "rep-1"})
		require.NoError(t, err)
		assert.Equal(t, int64(41), res.ID)
	})

	t.Run("WhenByName", func(t *testing.T) {
		store := readstore.NewMockReadStore(t)
		scope := []readstore.Eq{{Column: readstore.ColVolumeID, Value: int64(4)}}
		store.EXPECT().GetReplicationByName(mock.Anything, readstore.NameQuery{Name: "r1", Eq: scope}).Return(&replicationRow, nil)
		_, err := NewReplicationReader(store).Get(ctx, GetRequest{Name: "r1", NameScope: scope})
		require.NoError(t, err)
	})

	t.Run("WhenStoreFails", func(t *testing.T) {
		store := readstore.NewMockReadStore(t)
		store.EXPECT().GetReplicationByUUID(mock.Anything, "x").Return(nil, readstore.ErrNotFound)
		_, err := NewReplicationReader(store).Get(ctx, GetRequest{UUID: "x"})
		assert.ErrorIs(t, err, readstore.ErrNotFound)
	})
}

func TestReplicationReaderList(t *testing.T) {
	ctx := context.Background()
	opts := readstore.ListOptions{Limit: 2}

	t.Run("WhenRowsAreReturned", func(t *testing.T) {
		store := readstore.NewMockReadStore(t)
		store.EXPECT().ListReplications(mock.Anything, opts).Return([]datamodel.VolumeReplication{replicationRow}, nil)
		rows, err := NewReplicationReader(store).List(ctx, ListRequest{Options: opts})
		require.NoError(t, err)
		assert.Len(t, rows, 1)
	})

	t.Run("WhenStoreFails", func(t *testing.T) {
		store := readstore.NewMockReadStore(t)
		store.EXPECT().ListReplications(mock.Anything, opts).Return(nil, readstore.ErrInvalidQuery)
		_, err := NewReplicationReader(store).List(ctx, ListRequest{Options: opts})
		assert.ErrorIs(t, err, readstore.ErrInvalidQuery)
	})
}

func TestReplicationResource(t *testing.T) {
	t.Run("WhenRawErrorsExist", func(t *testing.T) {
		v := replicationResource(&replicationRow).Value
		got := encoded(t, v)
		assert.Equal(t, "replication", got["resourceType"])
		assert.Equal(t, "vol-1", got["volumeUuid"])
		assert.Equal(t, "snapmirrored", got["mirrorState"])
		assert.Equal(t, true, got["unhealthyReasonPresent"])
		assert.Equal(t, true, got["lastTransferErrorPresent"])
		assert.Equal(t, false, got["healthy"])
		assert.Equal(t, true, got["isHybrid"])
		assert.Equal(t, "src-vol", got["sourceVolumeUuid"])
		assert.Equal(t, "svm-src", got["sourceSvmName"])
		assert.Equal(t, "2026-10-01T00:00:00Z", got["lastTransferEndTime"])
		assert.NotContains(t, got, "sourceHostName")
		assertNoSecrets(t, v)
	})

	t.Run("WhenNoErrorsAndNoAttributes", func(t *testing.T) {
		got := encoded(t, replicationResource(&datamodel.VolumeReplication{Healthy: true}).Value)
		assert.Equal(t, false, got["unhealthyReasonPresent"])
		assert.Equal(t, false, got["lastTransferErrorPresent"])
		assert.Equal(t, false, got["isHybrid"])
		assert.NotContains(t, got, "mirrorState")
		assert.NotContains(t, got, "lastTransferEndTime")
	})
}

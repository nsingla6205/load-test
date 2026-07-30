package vsa

import (
	"context"
	"fmt"
	"strings"

	ontaprest "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/ontap-rest"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
)

const hostNodeAggregatePostfix = "nfsaas_aggr"

// DiscoverHostSystemInfo fetches cluster metadata from ONTAP and updates the host record.
func DiscoverHostSystemInfo(ctx context.Context, dbHost *datamodel.Host, client ontaprest.RESTClient) error {
	if dbHost.Attributes == nil {
		dbHost.Attributes = &datamodel.HostAttributes{}
	}
	clusterInfo, err := client.Cluster().ClusterGet(ctx, &ontaprest.ClusterGetParams{
		BaseParams: ontaprest.BaseParams{Fields: []string{"uuid", "name", "version"}},
	})
	if err != nil {
		return err
	}

	if clusterInfo.UUID != nil {
		dbHost.ExternalUUID = clusterInfo.UUID.String()
	}
	if clusterInfo.Name != nil {
		dbHost.ExternalName = strings.TrimSpace(*clusterInfo.Name)
	}
	if clusterInfo.Version != nil && clusterInfo.Version.Full != nil {
		dbHost.Attributes.Version = strings.TrimSpace(*clusterInfo.Version.Full)
	}
	if clusterInfo.Version != nil {
		dbHost.Attributes.VersionGeneration = int(nillable.FromPointer(clusterInfo.Version.Generation))
		dbHost.Attributes.VersionMajor = int(nillable.FromPointer(clusterInfo.Version.Major))
		dbHost.Attributes.VersionMinor = int(nillable.FromPointer(clusterInfo.Version.Minor))
	}
	dbHost.Attributes.Capabilities = ""
	return nil
}

// ValidateHostAggregates verifies nfsaas aggregates exist for each node, sets host capacity,
// and returns the discovered nodes so the caller can persist them without a second ONTAP round-trip.
func ValidateHostAggregates(provider Provider, dbHost *datamodel.Host, client ontaprest.RESTClient) ([]*Node, error) {
	nodes, err := provider.GetNodesWithClient(client)
	if err != nil {
		return nil, err
	}
	aggregateByName, err := hostAggregatesByNameWithClient(client)
	if err != nil {
		return nil, err
	}

	var totalAggregateSize int64
	for _, node := range nodes {
		if node == nil || strings.TrimSpace(node.Name) == "" {
			continue
		}
		nodeAggregateName := hostNodeAggregateName(strings.TrimSpace(node.Name))
		aggregate, ok := aggregateByName[nodeAggregateName]
		if !ok {
			return nil, fmt.Errorf("aggregate for node %q not found", strings.TrimSpace(node.Name))
		}
		totalAggregateSize += hostAggregateBlockStorageSize(aggregate)
	}
	dbHost.Capacity = totalAggregateSize
	return nodes, nil
}

func hostAggregateBlockStorageSize(aggregate *ontaprest.Aggregate) int64 {
	if aggregate == nil || aggregate.Space == nil || aggregate.Space.BlockStorage == nil {
		return 0
	}
	return nillable.FromPointer(aggregate.Space.BlockStorage.Size)
}

func hostAggregatesByNameWithClient(client ontaprest.RESTClient) (map[string]*ontaprest.Aggregate, error) {
	aggregateByName := make(map[string]*ontaprest.Aggregate)
	err := client.Storage().AggregateCollectionGet(&ontaprest.AggregateCollectionGetParams{
		BaseParams: ontaprest.BaseParams{Fields: []string{"name", "space"}},
	}, func(aggregates []*ontaprest.Aggregate) error {
		for _, aggregate := range aggregates {
			if aggregate == nil || aggregate.Name == nil {
				continue
			}
			name := strings.TrimSpace(*aggregate.Name)
			if name == "" {
				continue
			}
			aggregateByName[name] = aggregate
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return aggregateByName, nil
}

func hostNodeAggregateName(nodeName string) string {
	return fmt.Sprintf("%s_%s", strings.Replace(nodeName, "-", "_", -1), hostNodeAggregatePostfix)
}

package gcp

import (
	"context"
	"fmt"
	"strings"

	ontaprest "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/ontap-rest"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
)

const nodeAggregatePostfix = "nfsaas_aggr"

var getOntapProviderForHost = _getOntapProviderForHost

func _getOntapProviderForHost(ctx context.Context, details vsa.ProviderDetails) vsa.Provider {
	return vsa.NewProvider(ctx, details)
}

// CreateOntapHost persists a new host record for v1_createHost.
func (o *GCPOrchestrator) CreateOntapHost(ctx context.Context, params *commonparams.CreateHostParams) (*datamodel.Host, error) {
	if params == nil {
		return nil, customerrors.NewBadRequestErr("request body is required")
	}
	if strings.TrimSpace(params.Name) == "" ||
		strings.TrimSpace(params.Username) == "" ||
		strings.TrimSpace(string(params.Password)) == "" {
		return nil, customerrors.NewBadRequestErr("name, username, and password are required")
	}

	encryptedPassword, err := utils.EncryptPassword(params.Password)
	if err != nil {
		return nil, fmt.Errorf("failed to encrypt admin password for host %q: %w", params.Name, err)
	}

	protocolInput := ""
	if params.Protocol != nil {
		protocolInput = *params.Protocol
	}
	portInput := 0
	if params.Port != nil {
		portInput = *params.Port
	}
	protocol, port, normErr := datamodel.NormalizeExternalClusterProtocolAndPort(protocolInput, portInput)
	if normErr != nil {
		return nil, customerrors.NewBadRequestErr(normErr.Error())
	}

	ipAddress := ""
	if params.IPAddress != nil {
		ipAddress = strings.TrimSpace(*params.IPAddress)
	}
	if ipAddress == "" {
		ipAddress = strings.TrimSpace(params.Name)
	}

	count, err := o.storage.CountHostsWithIPAddress(ctx, ipAddress, false)
	if err != nil {
		return nil, fmt.Errorf("failed checking host ip uniqueness: %w", err)
	}
	if count > 0 {
		return nil, customerrors.NewConflictErr("IP address already assigned to host")
	}

	provider := providerForHost(ctx, params, ipAddress, protocol)
	client, err := provider.CreateRESTClient()
	if err != nil {
		return nil, customerrors.NewBadRequestErr(fmt.Sprintf("unable to connect to host: %v", err))
	}

	dbHost := &datamodel.Host{
		Name:         strings.TrimSpace(params.Name),
		VendorID:     utils.TrimOrNil(params.VendorID),
		Port:         &port,
		Protocol:     protocol,
		Username:     strings.TrimSpace(params.Username),
		Password:     *encryptedPassword,
		State:        utils.DefaultString(strings.TrimSpace(params.LifeCycleState), datamodel.LifeCycleStateCreated),
		StateDetails: utils.DefaultString(strings.TrimSpace(params.StateDetails), datamodel.LifeCycleStateCreatedDetails),
		Label:        utils.TrimOrEmpty(params.Label),
		IPAddress:    ipAddress,
		Attributes: &datamodel.HostAttributes{
			MaintenanceMode: utils.BoolOrFalse(params.MaintenanceMode),
			PublicAccess:    utils.BoolOrFalse(params.PublicAccess),
			Version:         strings.TrimSpace(params.OntapVersion),
		},
	}

	if err := updateHostWithSystemInfo(ctx, dbHost, client); err != nil {
		return nil, customerrors.NewBadRequestErr(fmt.Sprintf("unable to fetch cluster details: %v", err))
	}
	if err := validateAggregatesForNewHost(provider, dbHost, client); err != nil {
		return nil, customerrors.NewBadRequestErr(fmt.Sprintf("unable to validate host nodes: %v", err))
	}

	if _, err := o.storage.CreateHost(ctx, dbHost); err != nil {
		if customerrors.IsConflictErr(err) {
			return nil, err
		}
		return nil, fmt.Errorf("failed to create host: %w", err)
	}
	return dbHost, nil
}

func updateHostWithSystemInfo(ctx context.Context, dbHost *datamodel.Host, client ontaprest.RESTClient) error {
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

func validateAggregatesForNewHost(provider vsa.Provider, dbHost *datamodel.Host, client ontaprest.RESTClient) error {
	nodes, err := provider.GetNodesWithClient(client)
	if err != nil {
		return err
	}
	aggregateByName, err := aggregatesByNameWithClient(client)
	if err != nil {
		return err
	}

	var totalAggregateSize int64
	for _, node := range nodes {
		if node == nil || strings.TrimSpace(node.Name) == "" {
			continue
		}
		nodeAggregateName := getNodeAggregateName(strings.TrimSpace(node.Name))
		aggregate, ok := aggregateByName[nodeAggregateName]
		if !ok {
			return fmt.Errorf("aggregate for node %q not found", strings.TrimSpace(node.Name))
		}
		totalAggregateSize += aggregateBlockStorageSize(aggregate)
	}
	dbHost.Capacity = totalAggregateSize
	return nil
}

func aggregateBlockStorageSize(aggregate *ontaprest.Aggregate) int64 {
	if aggregate == nil || aggregate.Space == nil || aggregate.Space.BlockStorage == nil {
		return 0
	}
	return nillable.FromPointer(aggregate.Space.BlockStorage.Size)
}

func aggregatesByNameWithClient(client ontaprest.RESTClient) (map[string]*ontaprest.Aggregate, error) {
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

func getNodeAggregateName(nodeName string) string {
	return fmt.Sprintf("%s_%s", strings.Replace(nodeName, "-", "_", -1), nodeAggregatePostfix)
}

var providerForHost = _providerForHost

func _providerForHost(ctx context.Context, params *commonparams.CreateHostParams, ipAddress, protocol string) vsa.Provider {
	return getOntapProviderForHost(ctx, vsa.ProviderDetails{
		IPAddress: ipAddress,
		Hosts: map[string]string{
			ipAddress: ipAddress,
		},
		Password:           string(params.Password),
		InsecureSkipVerify: protocol == datamodel.ExternalClusterProtocolInsecureHTTPS,
	})
}

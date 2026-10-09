package vsa

import (
	"context"
	"errors"
	"fmt"
	"strings"

	ontapRest "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/ontap-rest"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
)

const (
	stateUp = "up"
)

func (rc *OntapRestProvider) AreAllNodeUpAndRunning() (bool, error) {
	// Retrieve nodes
	nodes, err := rc.GetNodes()
	if err != nil {
		rc.Logger.Errorf("Failed to retrieve nodes: %v\n", err)
		return false, err
	}

	// Check if the expected number of nodes is present
	if len(nodes) < expectedNodeCount {
		return false, fmt.Errorf("expected %d nodes, got %d", expectedNodeCount, len(nodes))
	}

	// Verify all nodes are in the "up" state
	for _, node := range nodes {
		if node.State != stateUp {
			return false, fmt.Errorf("node %s is not up", node.Name)
		}
	}

	return true, nil
}

func (rc *OntapRestProvider) GetNodes() ([]*Node, error) {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return nil, err
	}
	return rc.GetNodesWithClient(client)
}

// GetNodesWithClient gets nodes using a provided REST client to avoid creating a new connection
func (rc *OntapRestProvider) GetNodesWithClient(client ontapRest.RESTClient) ([]*Node, error) {
	var resultNodes []*Node
	// Call the NodesGet method with proper parameters using the provided client
	err := client.Cluster().NodesGet(&ontapRest.NodesGetParams{
		BaseParams: ontapRest.BaseParams{
			Fields: []string{"name", "uuid", "state"},
		},
	}, func(apiNodes []*ontapRest.Node) error {
		for _, apiNode := range apiNodes {
			// Append converted nodes to the result slice
			resultNodes = append(resultNodes, &Node{
				Name:         nillable.FromPointer(apiNode.Name),
				ExternalUUID: apiNode.UUID.String(),
				State:        nillable.FromPointer(apiNode.State),
			})
		}
		return nil
	})
	// Handle errors from the NodesGet call
	if err != nil {
		return nil, err
	}
	return resultNodes, nil
}

func (rc *OntapRestProvider) GetNodeByName(name string) (*Node, error) {
	var resultNode *Node
	// Call the NodesGet method with proper parameters
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return nil, err
	}
	err = client.Cluster().NodesGet(&ontapRest.NodesGetParams{
		BaseParams: ontapRest.BaseParams{
			Fields: []string{"name", "uuid"},
		},
	}, func(apiNodes []*ontapRest.Node) error {
		for _, apiNode := range apiNodes {
			// Check if the node name matches the provided name
			if nillable.FromPointer(apiNode.Name) == name {
				resultNode = &Node{
					Name:         nillable.FromPointer(apiNode.Name),
					ExternalUUID: apiNode.UUID.String(),
				}
				return nil
			}
		}
		return nil
	})

	// Handle errors from the NodesGet call
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrCouldNotFetchVSAClusterDetails, err)
	}

	if resultNode == nil {
		// If no node was found, return an error
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrVSAClusterNodeNotFound, fmt.Errorf("node with name %s not found", name))
	}

	return resultNode, nil
}

func (rc *OntapRestProvider) GetONTAPVersion() (*string, error) {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return nil, err
	}
	version, err := client.Cluster().GetONTAPVersion()
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrONTAPVersionFetchError, err)
	}
	return version, nil
}

// GetClusterName returns the ONTAP cluster name, which is also the name of the
// cluster's admin SVM.
func (rc *OntapRestProvider) GetClusterName(ctx context.Context) (string, error) {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return "", err
	}
	cluster, err := client.Cluster().ClusterGet(ctx, &ontapRest.ClusterGetParams{
		BaseParams: ontapRest.BaseParams{Fields: []string{"name"}},
	})
	if err != nil {
		return "", vsaerrors.NewVCPError(vsaerrors.ErrCouldNotFetchVSAClusterDetails, err)
	}
	if cluster == nil || cluster.Name == nil || strings.TrimSpace(*cluster.Name) == "" {
		return "", vsaerrors.NewVCPError(vsaerrors.ErrCouldNotFetchVSAClusterDetails,
			errors.New("ONTAP returned an empty cluster name"))
	}
	return strings.TrimSpace(*cluster.Name), nil
}

func (rc *OntapRestProvider) PostClusterLicenseAccessToken(ctx context.Context, clientSecret string) (*string, error) {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return nil, err
	}
	version, err := client.Cluster().PostClusterLicenseAccessToken(ctx, clientSecret)
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrONTAPVersionFetchError, err)
	}
	if version.Payload == nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrONTAPVersionFetchError, fmt.Errorf("payload is nil"))
	}
	return &version.Payload.AccessToken, nil
}

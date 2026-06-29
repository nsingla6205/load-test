package ontap_rest

import (
	"context"
	"time"

	"github.com/go-openapi/strfmt"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/client/cluster"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/models"
	securitypriv "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/priv/client/operations"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

var (
	deleteClusterPeerTimeout = 10 * time.Second
)

const (
	smcGrantType = "client_credentials"
	clientId     = "global-scheduler"
)

// ClusterClient describes a cluster client
type ClusterClient interface { // generate:mock
	NodesGet(params *NodesGetParams, ucbf UserCallbackFunc[[]*Node]) error
	ClusterGet(ctx context.Context, params *ClusterGetParams) (*Cluster, error)
	GetONTAPVersion() (*string, error)
	ClusterPeersList() ([]*ClusterPeerResponse, error)
	ClusterPeerCreate(params ClusterPeerCreateParams) (*ClusterPeerCreateResponse, error)
	ClusterPeerAccept(params ClusterPeerCreateParams) (*ClusterPeerCreateResponse, error)
	ClusterPeerDelete(clusterPeerID string) error
	ClusterPeerGet(clusterPeerID string) (*ClusterPeerResponse, error)
	ScheduleCreate(params *ScheduleCreateParams) error
	ScheduleCollectionGet(sfp *ScheduleCollectionGetParams, ucbf UserCallbackFunc[[]*Schedule]) error
	GetJob(UUID string) (*cluster.JobGetOK, error)
	JobGet(ctx context.Context, params *JobGetParams) (*cluster.JobGetOK, error)
	PostClusterLicenseAccessToken(ctx context.Context, clientSecret string) (*cluster.PostClusterAccessTokenOK, error)
	ModifyNode(ctx context.Context, params *NodeModifyParams) (*cluster.NodeModifyOK, error)
}

type clusterClient struct {
	api     cluster.ClientService
	apiPriv *securitypriv.ClientService
}

// NodeModifyParams represents parameters for modifying a node
type NodeModifyParams struct {
	UUID   string
	Action string
	Body   *NodeModifyBody
}

// NodeModifyBody represents the body for node modify requests
type NodeModifyBody struct {
	NVLog *NVLogModify `json:"nvlog,omitempty"`
}

// NVLogModify represents nvlog modification parameters
type NVLogModify struct {
	BackingType string `json:"backing_type,omitempty"`
}

var paginateNodesGet = _paginate[[]*Node]

// NodesGet invokes pkg/ontap-rest/client/cluster/Client.NodesGet
func (cc clusterClient) NodesGet(params *NodesGetParams, ucbf UserCallbackFunc[[]*Node]) error {
	otParams := nodesGetParamsToONTAP(params)
	return paginateNodesGet(func(next string) ([]*Node, string, error) {
		otParams.SetContext(setNext(otParams.Context, next))

		rsp, err := cc.api.NodesGet(otParams, nil)
		if err != nil {
			return nil, "", err
		}

		nodes := make([]*Node, nillable.FromPointer(rsp.Payload.NumRecords))
		for i, res := range rsp.Payload.NodeResponseInlineRecords {
			nodes[i] = &Node{NodeResponseInlineRecordsInlineArrayItem: *res}
		}

		if rsp.Payload.Links != nil && rsp.Payload.Links.Next != nil {
			return nodes, nillable.FromPointer(rsp.Payload.Links.Next.Href), nil
		}
		return nodes, "", nil
	}, ucbf)
}

// ClusterGet invokes pkg/ontap-rest/client/cluster/Client.ClusterGet.
func (cc clusterClient) ClusterGet(ctx context.Context, params *ClusterGetParams) (*Cluster, error) {
	otParams := cluster.NewClusterGetParams().WithContext(ctx)
	if params != nil && params.Fields != nil {
		otParams.SetFields(params.Fields)
	}

	response, err := cc.api.ClusterGet(otParams, nil)
	if err != nil {
		return nil, err
	}
	if response == nil || response.Payload == nil {
		return nil, errors.New("empty cluster response")
	}

	return &Cluster{Cluster: *response.Payload}, nil
}

// GetONTAPVersion returns the ONTAP version
func (cc clusterClient) GetONTAPVersion() (*string, error) {
	cluster, err := cc.api.ClusterGet(cluster.NewClusterGetParams().WithFields([]string{"version"}), nil)
	if err != nil {
		return nil, err
	}
	return cluster.Payload.Version.Full, nil
}

// ClusterPeersList returns all cluster peers for the specific host
func (cc *clusterClient) ClusterPeersList() ([]*ClusterPeerResponse, error) {
	resp, err := cc.api.ClusterPeerCollectionGet(getListClusterPeerParams(), nil)
	if err != nil {
		return nil, err
	}
	clusterPeers := convertListClusterPeerFromREST(resp)
	return clusterPeers, nil
}

// ClusterPeerCreate creates a cluster peer for the specific host
func (cc *clusterClient) ClusterPeerCreate(params ClusterPeerCreateParams) (*ClusterPeerCreateResponse, error) {
	resp, err := (*cc.apiPriv).ClusterPeerCreate(clusterPeerToONTAPCreate(params))
	if err != nil {
		return nil, err
	}
	return convertClusterPeerCreateFromREST(resp), nil
}

func (cc *clusterClient) ClusterPeerAccept(params ClusterPeerCreateParams) (*ClusterPeerCreateResponse, error) {
	resp, err := (*cc.apiPriv).ClusterPeerCreate(clusterPeerToONTAPAccept(params))
	if err != nil {
		return nil, err
	}
	return convertClusterPeerCreateFromREST(resp), nil
}

// ClusterPeerDelete deletes a cluster peer for the specific host
func (cc *clusterClient) ClusterPeerDelete(clusterPeerID string) error {
	_, err := cc.api.ClusterPeerDelete(clusterPeerIDToONTAPDelete(clusterPeerID, deleteClusterPeerTimeout), nil)
	if err != nil {
		return err
	}
	return nil
}

// ClusterPeerGet gets a single cluster peer by clusterPeerID
func (cc *clusterClient) ClusterPeerGet(clusterPeerID string) (*ClusterPeerResponse, error) {
	response, err := cc.api.ClusterPeerGet(clusterPeerIDToONTAPGet(clusterPeerID), nil)
	if err != nil {
		return nil, err
	}
	return convertClusterPeerFromREST(response), nil
}

func getListClusterPeerParams() *cluster.ClusterPeerCollectionGetParams {
	return cluster.NewClusterPeerCollectionGetParams().
		WithFields([]string{"authentication", "name", "remote", "status", "uuid"})
}

// ScheduleCreate invokes pkg/ontap-rest/client/cluster/Client.ScheduleCreate
func (cc *clusterClient) ScheduleCreate(params *ScheduleCreateParams) error {
	_, err := cc.api.ScheduleCreate(scheduleCreateParamsToONTAP(params), nil)
	if err != nil {
		return err
	}
	return nil
}

var paginateScheduleCollectionGet = _paginate[[]*Schedule]

// ScheduleCollectionGet invokes pkg/ontap-rest/client/cluster/Client.ScheduleCollectionGet
func (cc *clusterClient) ScheduleCollectionGet(sfp *ScheduleCollectionGetParams, ucbf UserCallbackFunc[[]*Schedule]) error {
	if sfp == nil || sfp.Name == "" {
		return errors.New("no name filter provided for ScheduleCollectionGet")
	}
	otParams := scheduleCollectionGetParamsToONTAP(sfp)

	return paginateScheduleCollectionGet(func(next string) ([]*Schedule, string, error) {
		otParams.SetContext(setNext(otParams.Context, next))

		rsp, err := cc.api.ScheduleCollectionGet(otParams, nil)
		if err != nil {
			return nil, "", err
		}

		resp := make([]*Schedule, nillable.FromPointer(rsp.Payload.NumRecords))
		for i, s := range rsp.Payload.ScheduleResponseInlineRecords {
			resp[i] = &Schedule{Schedule: *s}
		}
		if rsp.Payload.Links != nil && rsp.Payload.Links.Next != nil {
			return resp, nillable.FromPointer(rsp.Payload.Links.Next.Href), nil
		}

		return resp, "", nil
	}, ucbf)
}

// GetJob returns the ONTAP Job
func (cc *clusterClient) GetJob(UUID string) (*cluster.JobGetOK, error) {
	params := cluster.NewJobGetParams().WithUUID(UUID).WithFields([]string{"*", "node.name"})
	job, err := cc.api.JobGet(params, nil)
	if err != nil {
		return nil, err
	}
	return job, nil
}

// JobGet retrieves job details from ONTAP using JobGet API.
// This method matches the spec's JobGet interface (create-quota-cvs-job-function.md, Section 7).
// It calls the underlying api.JobGet and returns the response for manual conversion.
func (cc *clusterClient) JobGet(ctx context.Context, params *JobGetParams) (*cluster.JobGetOK, error) {
	// Convert our JobGetParams to the client JobGetParams using JobGetParamsToONTAP
	clientParams := JobGetParamsToONTAP(params).WithContext(ctx)

	// Call the underlying API JobGet method
	// This returns *cluster.JobGetOK which will be converted in fetchDetailsFromJob per spec
	job, err := cc.api.JobGet(clientParams, nil)
	if err != nil {
		return nil, err
	}
	return job, nil
}

func (cc clusterClient) PostClusterLicenseAccessToken(ctx context.Context, clientSecret string) (*cluster.PostClusterAccessTokenOK, error) {
	logger := util.GetLogger(ctx)
	logger.Info("In OntapProvider.PostClusterLincenseAccessToken(...)")

	info := models.AccessTokenBody{
		GrantType:    smcGrantType,
		ClientID:     clientId,
		ClientSecret: clientSecret,
	}
	param := cluster.NewPostClusterAccessTokenParams()
	param.WithInfo(&info)

	res, err := cc.api.PostClusterAccessToken(param, nil)
	parsedErr := errors.ParseOntapError(ctx, err)
	if err != nil {
		logger.Errorf("ONTAP error: PostClusterLicenseAccessToken() on ontap failed with error: %+v", parsedErr)
		return nil, err
	}
	return res, nil
}

// ModifyNode modifies a node with the given parameters
func (cc clusterClient) ModifyNode(ctx context.Context, params *NodeModifyParams) (*cluster.NodeModifyOK, error) {
	logger := util.GetLogger(ctx)

	// Create the ONTAP REST client parameters
	ontapParams := cluster.NewNodeModifyParams()
	ontapParams.UUID = strfmt.UUID(string(params.UUID))

	if params.Action != "" {
		ontapParams.Action = &params.Action
	}

	nodeInfo := &models.Node{}

	// Set nvlog backing type if provided
	if params.Body != nil && params.Body.NVLog != nil {
		nodeInfo.Nvlog = &models.NodeInlineNvlog{
			BackingType: &params.Body.NVLog.BackingType,
		}
	}

	ontapParams.Info = nodeInfo

	// Call the ONTAP REST API
	response, accepted, err := cc.api.NodeModify(ontapParams, nil)
	if err != nil {
		parsedErr := errors.ParseOntapError(ctx, err)
		logger.Errorf("ONTAP error: ModifyNode() failed with error: %+v", parsedErr)
		return nil, err
	}

	// Handle accepted response (async operation)
	if accepted != nil {
		logger.Info("Node modification accepted as async operation")
		// Convert NodeModifyAccepted to NodeModifyOK for consistency
		return &cluster.NodeModifyOK{
			Payload: &models.NodeJobLinkResponse{
				Job: accepted.Payload.Job,
			},
		}, nil
	}

	return response, nil
}

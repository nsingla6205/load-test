package activities

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"strconv"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/vlm"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	utilerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/worker/metrics"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
	"gorm.io/gorm"
)

const (
	leasePrefix = "harvest-"
)

var (
	createKubernetesLease         = utils.CreateKubernetesLease
	leaseExists                   = utils.LeaseExists
	vcpLeaseNameSpace             = env.GetString("LEASE_NAMESPACE", "vcp")
	smHarvestAuthEnabled          = env.GetBool("HARVEST_SECRET_MANAGER_AUTH_ENABLED", false)
	enableMultiHaPairRegistration = env.GetBool("ENABLE_MULTI_HA_PAIR_REGISTRATION", true)
)

// RegisterNodeToHarvestFarmInput holds input parameters for the activity
type RegisterNodeToHarvestFarmInput struct {
	PoolID            int64
	MaxNodesPerGroup  int
	CustomerProjectID string
	TenantProjectID   string
	DeploymentName    string
	PoolName          string
	IsRegionalHA      bool
}

// RegisterNodeToHarvestFarmOutput holds the output for the next activity
// Contains the rendered template and upload URL
// You can add more fields as needed
type RegisterNodeToHarvestFarmOutput struct {
	RenderedTemplate string
	UploadURL        string
}

// RegisterNodeToHarvestFarmActivity struct for dependency injection
// SE is the Storage Engine (database.Storage)
type RegisterNodeToHarvestFarmActivity struct {
	SE database.Storage
	// Injected function for loading the harvest template (for testability)
	LoadHarvestTemplateFunc func() (string, error)
}

// RegisterNodeToHarvestFarm registers all nodes to the Harvest farm and assigns them to node groups in the database.
// Nodes are processed in pairs (HA pairs) - for Large Volumes pools with multiple HA pairs, all pairs are registered.
// Now returns output for the upload activity
// Note: dbHbCtx is the context with DB heartbeat timeout configured from the workflow
func (a *RegisterNodeToHarvestFarmActivity) RegisterNodeToHarvestFarm(dbHbCtx context.Context, input RegisterNodeToHarvestFarmInput) ([]*datamodel.NodeNodeGroupMap, error) {
	logger := util.GetLogger(dbHbCtx)
	activity.RecordHeartbeat(dbHbCtx, "RegisterNodeToHarvestFarmActivity started")
	logger.Infof("Registering nodes to Harvest farm for pool ID: %d", input.PoolID)

	nodes, err := a.SE.GetNodesByPoolID(dbHbCtx, input.PoolID)
	if err != nil {
		logger.Errorf("Failed to fetch nodes for pool ID %d: %v", input.PoolID, err)
		return nil, vsaerrors.ExtractCustomError(fmt.Errorf("error fetching nodes for pool ID %d: %w", input.PoolID, err))
	}

	if len(nodes) < 2 {
		logger.Errorf("Not enough nodes found for pool ID %d: got %d", input.PoolID, len(nodes))
		return nil, vsaerrors.ExtractCustomError(fmt.Errorf("not enough nodes found for pool"))
	}

	// Warn if odd number of nodes (shouldn't happen for HA pairs)
	if len(nodes)%2 != 0 {
		logger.Warnf("Pool ID %d has odd number of nodes (%d), last node will be skipped", input.PoolID, len(nodes))
	}

	// Determine how many nodes to process based on feature flag
	maxNodesToProcess := len(nodes)
	if !enableMultiHaPairRegistration && maxNodesToProcess > 2 {
		logger.Infof("Multi-HA pair registration is disabled, registering only first HA pair (2 nodes) for pool ID %d", input.PoolID)
		maxNodesToProcess = 2
	}

	logger.Infof("Processing %d nodes (%d HA pairs) for pool ID %d", maxNodesToProcess, maxNodesToProcess/2, input.PoolID)

	var allMappings []*datamodel.NodeNodeGroupMap

	// Process nodes in pairs (HA pairs)
	for i := 0; i < maxNodesToProcess; i += 2 {
		if i+1 >= maxNodesToProcess {
			// Skip if we don't have a complete pair
			break
		}

		node1 := nodes[i]
		node2 := nodes[i+1]

		logger.Debugf("Assigning HA pair %d: node1=%d (%s), node2=%d (%s)",
			(i/2)+1, node1.ID, node1.Name, node2.ID, node2.Name)

		activity.RecordHeartbeat(dbHbCtx, "RegisterNodeToHarvestFarmActivity processing HA pair")
		nodeMappings, err := a.SE.AssignTwoNodesToTwoGroups(dbHbCtx, datamodel.NodeGroupAssignmentParams{
			Node1:            node1,
			Node2:            node2,
			MaxNodesPerGroup: input.MaxNodesPerGroup,
			CustomerProject:  input.CustomerProjectID,
			TenantProject:    input.TenantProjectID,
			DeploymentName:   input.DeploymentName,
			PoolName:         input.PoolName,
			IsRegionalHA:     input.IsRegionalHA,
		})
		if err != nil {
			logger.Errorf("Failed to assign nodes %d and %d to groups for pool ID %d: %v",
				node1.ID, node2.ID, input.PoolID, err)
			// Rollback: delete all previously created mappings to maintain atomicity
			a.rollbackNodeMappings(dbHbCtx, allMappings, logger)
			return nil, vsaerrors.ExtractCustomError(fmt.Errorf("error assigning nodes to groups for pool ID %d: %w", input.PoolID, err))
		}

		if len(nodeMappings) < 2 {
			logger.Errorf("Node group assignment returned insufficient mappings for nodes %d and %d in pool ID %d",
				node1.ID, node2.ID, input.PoolID)
			// Rollback: delete all previously created mappings to maintain atomicity
			a.rollbackNodeMappings(dbHbCtx, allMappings, logger)
			return nil, vsaerrors.ExtractCustomError(fmt.Errorf("node group assignment returned insufficient mappings for pool"))
		}

		allMappings = append(allMappings, nodeMappings...)
		logger.Infof("Successfully registered HA pair %d: nodes %d and %d", (i/2)+1, node1.ID, node2.ID)
	}

	logger.Infof("Successfully registered all %d nodes (%d HA pairs) for pool ID %d",
		len(allMappings), len(allMappings)/2, input.PoolID)
	return allMappings, nil
}

// rollbackNodeMappings deletes all previously created node-group mappings to maintain atomicity.
// This is called when registration fails for any HA pair after previous pairs have been registered.
func (a *RegisterNodeToHarvestFarmActivity) rollbackNodeMappings(dbHbCtx context.Context, mappings []*datamodel.NodeNodeGroupMap, logger log.Logger) {
	if len(mappings) == 0 {
		return
	}
	logger.Infof("Rolling back %d node mappings due to registration failure", len(mappings))
	for _, mapping := range mappings {
		activity.RecordHeartbeat(dbHbCtx, "RegisterNodeToHarvestFarmActivity rolling back node mapping")
		if err := a.SE.DeleteNodeNodeGroupMap(dbHbCtx, mapping.ID); err != nil {
			logger.Warnf("Failed to rollback node mapping ID %d during cleanup: %v", mapping.ID, err)
		} else {
			logger.Debugf("Successfully rolled back node mapping ID %d", mapping.ID)
		}
	}
	logger.Infof("Completed rollback of %d node mappings", len(mappings))
}

// ValidateAndCreateKubernetesLease validates node group mappings and creates Kubernetes leases if needed.
// Note: dbHbCtx is the context with DB heartbeat timeout configured from the workflow
func (a *RegisterNodeToHarvestFarmActivity) ValidateAndCreateKubernetesLease(dbHbCtx context.Context, nodeGroupsMap []*datamodel.NodeNodeGroupMap) ([]*datamodel.NodeNodeGroupMap, error) {
	logger := util.GetLogger(dbHbCtx)
	activity.RecordHeartbeat(dbHbCtx, "ValidateAndCreateKubernetesLease started")

	for i, nodeGroupMap := range nodeGroupsMap {
		activity.RecordHeartbeat(dbHbCtx, fmt.Sprintf("Processing node group map %d/%d", i+1, len(nodeGroupsMap)))
		if err := a.validateNodeGroupMap(nodeGroupMap, logger); err != nil {
			return nil, err
		}

		nodeGroup := nodeGroupMap.NodeGroup
		wasNewLease := nodeGroup.LeaseName == ""

		if err := a.ensureLeaseExists(dbHbCtx, nodeGroup, vcpLeaseNameSpace, logger); err != nil {
			return nil, err
		}

		// Ensure HarvestConfig has the lease name set
		nodeGroupMap.HarvestConfig.LEASE_NAME = nodeGroup.LeaseName

		// Update database records only if lease was newly created
		if wasNewLease {
			if err := a.updateDatabaseRecords(dbHbCtx, nodeGroup, nodeGroupMap, logger); err != nil {
				return nil, err
			}
		}
	}
	return nodeGroupsMap, nil
}

// validateNodeGroupMap validates the node group mapping structure
func (a *RegisterNodeToHarvestFarmActivity) validateNodeGroupMap(nodeGroupMap *datamodel.NodeNodeGroupMap, logger log.Logger) error {
	if nodeGroupMap.NodeGroup == nil {
		logger.Errorf("Failed to fetch node group details of node groupID:%s", nodeGroupMap.NodeGroupID)
		return errors.New("failed to fetch node group details from nodeGroup Map table")
	}
	return nil
}

// ensureLeaseExists ensures the Kubernetes lease exists, creating it if necessary
func (a *RegisterNodeToHarvestFarmActivity) ensureLeaseExists(dbHbCtx context.Context, nodeGroup *datamodel.NodeGroup, vcpLeaseNameSpace string, logger log.Logger) error {
	if nodeGroup.LeaseName == "" {
		return a.createNewLease(dbHbCtx, nodeGroup, vcpLeaseNameSpace, logger)
	}
	return a.validateExistingLease(dbHbCtx, nodeGroup, vcpLeaseNameSpace, logger)
}

// createNewLease creates a new lease for a node group that doesn't have one
func (a *RegisterNodeToHarvestFarmActivity) createNewLease(dbHbCtx context.Context, nodeGroup *datamodel.NodeGroup, vcpLeaseNameSpace string, logger log.Logger) error {
	logger.Infof("Creating new k8's lease for node group: %d", nodeGroup.ID)

	leaseName := leasePrefix + nodeGroup.UUID
	if err := createKubernetesLease(dbHbCtx, vcpLeaseNameSpace, leaseName); err != nil {
		logger.Errorf("Failed to create k8s lease for node group %d: %v", nodeGroup.ID, err)
		return err
	}

	nodeGroup.LeaseName = leaseName
	return nil
}

// validateExistingLease validates that an existing lease exists in Kubernetes, creating it if missing
func (a *RegisterNodeToHarvestFarmActivity) validateExistingLease(dbHbCtx context.Context, nodeGroup *datamodel.NodeGroup, vcpLeaseNameSpace string, logger log.Logger) error {
	logger.Infof("Checking if k8's lease %s exists for node group: %d", nodeGroup.LeaseName, nodeGroup.ID)

	exists, err := leaseExists(dbHbCtx, vcpLeaseNameSpace, nodeGroup.LeaseName)
	if err != nil {
		logger.Errorf("Failed to check k8s lease existence for node group %d: %v", nodeGroup.ID, err)
		return err
	}

	if !exists {
		logger.Infof("k8's lease %s doesn't exist in Kubernetes, creating it for node group: %d", nodeGroup.LeaseName, nodeGroup.ID)
		if err := createKubernetesLease(dbHbCtx, vcpLeaseNameSpace, nodeGroup.LeaseName); err != nil {
			logger.Errorf("Failed to create k8s lease %s for node group %d: %v", nodeGroup.LeaseName, nodeGroup.ID, err)
			return err
		}
		logger.Infof("Successfully created k8's lease %s for node group: %d", nodeGroup.LeaseName, nodeGroup.ID)
	} else {
		logger.Infof("k8's lease %s already exists for node group: %d", nodeGroup.LeaseName, nodeGroup.ID)
	}

	return nil
}

// updateDatabaseRecords updates the database records for node group and mapping
func (a *RegisterNodeToHarvestFarmActivity) updateDatabaseRecords(dbHbCtx context.Context, nodeGroup *datamodel.NodeGroup, nodeGroupMap *datamodel.NodeNodeGroupMap, logger log.Logger) error {
	activity.RecordHeartbeat(dbHbCtx, fmt.Sprintf("Updating DB records for node group %d and node id %d", nodeGroup.ID, nodeGroupMap.NodeID))
	if _, err := a.SE.UpdateNodeGroup(dbHbCtx, nodeGroup); err != nil {
		logger.Errorf("Failed to update k8s lease info in DB for node group %d: %v", nodeGroup.ID, err)
		return err
	}

	activity.RecordHeartbeat(dbHbCtx, fmt.Sprintf("Updating DB records for node group map %d", nodeGroup.ID))
	if _, err := a.SE.UpdateNodeNodeGroupMap(dbHbCtx, nodeGroupMap); err != nil {
		logger.Errorf("Failed to update k8s lease info in DB for node id %d: %v", nodeGroupMap.NodeID, err)
		return err
	}
	return nil
}

// AlertHarvestRegisterFailure logs error details to metrics for monitoring purposes
func (a *RegisterNodeToHarvestFarmActivity) AlertHarvestRegisterFailure(ctx context.Context, errorDetails string) error {
	metrics.IncJobStatusCounter(ctx, errorDetails, "failed to register nodes to harvest farm")
	return nil
}

// LoadActiveHarvestMappingsForPool returns active node_node_group_map rows for all nodes in the pool.
// Used by the register workflow defer when RegisterNodeToHarvestFarm committed mappings but the
// workflow never received the activity result (e.g. cancellation raced with completion).
func (a *RegisterNodeToHarvestFarmActivity) LoadActiveHarvestMappingsForPool(ctx context.Context, poolID int64) ([]*datamodel.NodeNodeGroupMap, error) {
	nodes, err := a.SE.GetNodesByPoolID(ctx, poolID)
	if err != nil {
		return nil, err
	}
	var mappings []*datamodel.NodeNodeGroupMap
	for _, node := range nodes {
		if node == nil {
			continue
		}
		mapping, mapErr := a.SE.GetActiveNodeNodeGroupMapByNodeID(ctx, node.ID, nil)
		if mapErr != nil {
			if utilerrors.IsNotFoundErr(mapErr) {
				continue
			}
			return nil, mapErr
		}
		if mapping != nil {
			mappings = append(mappings, mapping)
		}
	}
	return mappings, nil
}

// MarkCreateRetryStateInput drives harvest_retry_state.create persistence for the register workflow.
type MarkCreateRetryStateInput struct {
	Mappings        []*datamodel.NodeNodeGroupMap
	Success         bool
	Message         string
	UploadedNodeIDs map[int64]struct{}
}

// MarkCreateRetryState records whether the register-to-harvest pipeline succeeded or failed for
// each mapping, so the drift reconcile create pass can pick up and retry nodes left mid-pipeline.
// On workflow failure, nodes whose upload activity already completed are marked success so a
// partial upload (e.g. node A ok, node B failed) does not queue unnecessary retries for A.
func (a *RegisterNodeToHarvestFarmActivity) MarkCreateRetryState(ctx context.Context, input MarkCreateRetryStateInput) error {
	for _, mapping := range input.Mappings {
		success, msg := resolveHarvestCreateRetryForMapping(mapping, input.Success, input.Message, input.UploadedNodeIDs)
		if err := setHarvestNodeMapCreateRetryState(ctx, a.SE, mapping, success, msg); err != nil {
			return err
		}
	}
	return nil
}

func resolveHarvestCreateRetryForMapping(mapping *datamodel.NodeNodeGroupMap, workflowSuccess bool, workflowMsg string, uploadedNodeIDs map[int64]struct{}) (success bool, msg string) {
	if workflowSuccess {
		return true, workflowMsg
	}
	if mapping != nil && uploadedNodeIDs != nil {
		if _, exists := uploadedNodeIDs[mapping.NodeID]; exists {
			return true, "success"
		}
	}
	return false, workflowMsg
}

// UploadHarvestTemplateActivity handles uploading the rendered template as YAML via REST
// Now supports dependency injection for template functions
type UploadHarvestTemplateActivity struct {
	SE                        database.Storage
	LoadHarvestTemplateFunc   func() (string, error)
	RenderHarvestTemplateFunc func(*datamodel.HarvestConfig) (string, error)
}

// UploadHarvestTemplateInput holds the input for the upload activity
type UploadHarvestTemplateInput struct {
	NodeMappings []*datamodel.NodeNodeGroupMap
	UploadURL    string
	PoolUUID     string
	AccountID    int64
}

// UploadYAMLFileInput is the input struct for uploadYAMLFile
type UploadYAMLFileInput struct {
	URL       string
	YAML      string
	LeaseName string
	NodeID    int64
}

// uploadYAMLFile uploads the given YAML content as a file via HTTP POST multipart/form-data
func uploadYAMLFile(ctx context.Context, input UploadYAMLFileInput) (*http.Response, error) {
	var buf bytes.Buffer
	logger := util.GetLogger(ctx)
	activity.RecordHeartbeat(ctx, "Uploading YAML File for harvest node")
	writer := multipart.NewWriter(&buf)
	fileName := fmt.Sprintf("%s%d.yaml", leasePrefix, input.NodeID) // Append nodeID to the file name
	part, err := writer.CreateFormFile("file", fileName)
	if err != nil {
		return nil, errors.New("failed to create form file: " + err.Error())
	}
	_, err = part.Write([]byte(input.YAML))
	if err != nil {
		return nil, errors.New("failed to write YAML content: " + err.Error())
	}
	// Add leaseName as a form field
	if input.LeaseName != "" {
		err = writer.WriteField("leaseName", input.LeaseName)
		if err != nil {
			return nil, errors.New("failed to write leaseName field: " + err.Error())
		}
	}
	if err := writer.Close(); err != nil {
		return nil, errors.New("failed to close multipart writer: " + err.Error())
	}

	req, err := http.NewRequestWithContext(ctx, "POST", input.URL, &buf)
	if err != nil {
		logger.Errorf("Failed to create HTTP request for uploading YAML file: %v", err)
		return nil, errors.New("failed to create HTTP request: " + err.Error())
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp, err := doHarvestHTTP(ctx, req, nil)
	if err != nil {
		return nil, errors.New("failed to execute HTTP request: " + err.Error())
	}
	return resp, nil
}

// uploadHarvestPollerYAML uploads one poller YAML via uploadRebalanceTarget (shared bucket/pod helper).
func uploadHarvestPollerYAML(ctx context.Context, uploadURL, leaseName string, nodeID int64, yaml string) error {
	return uploadRebalanceTarget(ctx, uploadURL, leaseName, nodeID, yaml)
}

// uploadHarvestNodeMapping updates HarvestConfig credentials from pool, optionally persists the mapping, renders YAML,
// and POSTs multipart YAML to the harvest config upload endpoint (same path as register-node-to-harvest-farm).
// When persist is false, the DB is not updated (used for poller rebalance: upload before commit).
func uploadHarvestNodeMapping(ctx context.Context, se database.Storage, mapping *datamodel.NodeNodeGroupMap, uploadURL string, pool *datamodel.Pool, credentials *vlm.OntapCredentials, renderFunc func(*datamodel.HarvestConfig) (string, error), persist bool) error {
	logger := util.GetLogger(ctx)
	if mapping == nil {
		return errors.New("invalid node mapping: nil mapping")
	}
	if mapping.NodeGroup == nil || mapping.NodeGroup.LeaseName == "" {
		return errors.New("invalid node mapping: NodeGroup is nil or LeaseName is empty; cannot upload YAML")
	}
	if mapping.HarvestConfig == nil {
		return errors.New("invalid node mapping: nil HarvestConfig")
	}
	if renderFunc == nil {
		renderFunc = utils.RenderHarvestTemplate
	}

	if !smHarvestAuthEnabled && credentials != nil {
		mapping.HarvestConfig.PASSWORD = strconv.Quote(credentials.AdminPassword)
	} else {
		mapping.HarvestConfig.PASSWORD = ""
		mapping.HarvestConfig.AUTH_TYPE = pool.PoolCredentials.AuthType
		mapping.HarvestConfig.SECRET_ID = pool.PoolCredentials.SecretID
		mapping.HarvestConfig.SECRET_PROJECT = env.SecretManagerProjectID
	}

	if persist {
		if _, err := se.UpdateNodeNodeGroupMap(ctx, mapping); err != nil {
			logger.Errorf("Failed to update harvest config info in DB for node id %d: %v", mapping.NodeID, err)
			return err
		}
	}

	tmplStr, err := renderFunc(mapping.HarvestConfig)
	if err != nil {
		logger.Errorf("Failed to render template for node id %d: %v", mapping.NodeID, err)
		return errors.New("template render failed for node mapping: " + err.Error())
	}

	leaseName := mapping.NodeGroup.LeaseName
	if err := uploadHarvestPollerYAML(ctx, uploadURL, leaseName, mapping.NodeID, tmplStr); err != nil {
		logger.Errorf("Failed to upload YAML template for node id %d: %v", mapping.NodeID, err)
		return fmt.Errorf("upload failed for node id %d: %w", mapping.NodeID, err)
	}
	logger.Infof("Successfully uploaded rendered template for node id %d as YAML file", mapping.NodeID)
	return nil
}

// UploadHarvestTemplate uploads the rendered template as a YAML file via REST call for each node mapping
func (a *UploadHarvestTemplateActivity) UploadHarvestTemplate(ctx context.Context, input UploadHarvestTemplateInput) error {
	logger := util.GetLogger(ctx)
	activity.RecordHeartbeat(ctx, "UploadHarvestTemplate started")
	pool, err := a.SE.GetPoolByUUIDForAccount(ctx, input.PoolUUID, input.AccountID)
	if err != nil {
		var customErr *vsaerrors.CustomError
		if errors.Is(err, gorm.ErrRecordNotFound) ||
			utilerrors.IsNotFoundErr(err) ||
			(vsaerrors.As(err, &customErr) && customErr != nil && customErr.IsError(vsaerrors.ErrPoolNotFound)) {
			logger.Errorf("Pool not found for UUID %s and AccountID %d", input.PoolUUID, input.AccountID)
			return temporal.NewNonRetryableApplicationError(err.Error(), "Pool Record not found", err)
		}
		logger.Errorf("Failed to fetch pool for UUID %s and AccountID %d: %v", input.PoolUUID, input.AccountID, err)
		return err
	}
	var credentials *vlm.OntapCredentials
	if !smHarvestAuthEnabled {
		credentials, err = fetchOnTapCredentials(ctx, pool)
		if err != nil {
			return err
		}
		if credentials == nil {
			logger.Errorf("Failed to get credentials for pool %d", pool.ID)
			return fmt.Errorf("failed to get credentials for pool %d", pool.ID)
		}
	}
	renderFunc := a.RenderHarvestTemplateFunc
	if renderFunc == nil {
		renderFunc = utils.RenderHarvestTemplate
	}

	var errs []error
	for i, mapping := range input.NodeMappings {
		activity.RecordHeartbeat(ctx, fmt.Sprintf("Uploading template for node mapping %d/%d", i+1, len(input.NodeMappings)))
		if err := uploadHarvestNodeMapping(ctx, a.SE, mapping, input.UploadURL, pool, credentials, renderFunc, true); err != nil {
			// Accumulate the error and keep going so a single failing node does not block onboarding
			// of the rest of the pool. Persist + upload are idempotent, so a whole-activity retry
			// re-processes the failed nodes safely.
			logger.Warnf("failed to upload harvest YAML for node mapping %d: %v", i, err)
			errs = append(errs, fmt.Errorf("node mapping %d: %w", i, err))
			continue
		}
	}
	return errors.Join(errs...)
}

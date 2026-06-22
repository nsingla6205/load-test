package api

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/google/uuid"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/factory"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	ociserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/oci-proxy/api/oci-servergen"
	utilserrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/workflowquery"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/client"
)

const (
	invalidOPCRequestID                    = "Opc-Request-Id not set"
	errMsgInvalidAdminPasswordVersion      = "ociAdminPassword.version must be a valid integer"
	errMsgAdminPasswordVersionLessThanOne  = "ociAdminPassword.version must be greater than or equal to 1"
	errMsgOddDataEndpointCount             = "dataEndpointCount must be a multiple of 2"
	errMsgBothNodeCapacitiesAndDEC         = "nodeCapacities and dataEndpointCount are mutually exclusive; provide only one"
	errMsgNoUpdatableFields                = "request body must contain at least one updatable field"
	errMsgNeitherNodeCapacitiesNorDEC      = "one of nodeCapacities or dataEndpointCount must be provided"
	errMsgThroughputGBpsNotPositive        = "throughputGBps must be greater than 0"
	errMsgThroughputGBpsNotFinite          = "throughputGBps must be a finite number"
	errMsgNodeCapacitySizeNotPositive      = "nodeCapacities[%d].sizeInGiB must be greater than 0"
	errMsgNodeCapacityNameRequired         = "nodeCapacities[%d].name is required"
	errMsgNodeCapacityNodeUUIDRequired     = "nodeCapacities[%d].nodeUUID is required"
	errMsgNodeCapacityNodeUUIDDuplicate    = "nodeCapacities[%d].nodeUUID %q is duplicated; node_uuid must be unique within the request"
	errMsgInvalidPoolOCID                  = "poolOCID must be a valid OCID"
	errMsgInvalidCompartmentOCID           = "compartmentOCID must be a valid OCID"
	errMsgInvalidSubnetID                  = "subnetId must be a valid OCID"
	errMsgInvalidDataNicSubnetID           = "dataNicSubnetId must be a valid OCID"
	errMsgInvalidAdminPasswordOCID         = "ociAdminPassword.ocid must be a valid OCID"
	errMsgNonPositiveSizeInGiB             = "sizeInGiB must be greater than 0"
	errMsgNonPositiveThroughputGBps        = "throughputGBps must be greater than 0"
	errMsgInvalidTenancyOcid               = "tenancyOcid must be a valid OCID"
	errMsgInvalidConfigForNonSharedHA      = "dataEndpointCount must be set to 2 for non-shared HA"
	errMsgEmptyPoolOCID                    = "poolOCID must not be empty"
	errMsgEmptyCompartmentOCID             = "compartmentOCID must not be empty"
	errMsgEmptyDisplayName                 = "displayName must not be empty"
	errMsgEmptySubnetID                    = "subnetId must not be empty"
	errMsgEmptyDataNicSubnetID             = "dataNicSubnetId must not be empty"
	errMsgEmptyAdminPasswordOCID           = "ociAdminPassword.ocid must not be empty"
	errMsgEmptyAdminPasswordVersion        = "ociAdminPassword.version must not be empty"
	errMsgEmptyTenancyOcid                 = "tenancyOcid must not be empty"
	errMsgEmptyPrimaryAD                   = "primaryAvailabilityDomain must not be empty"
	errMsgEmptySerialNumberPrefix          = "serialNumberPrefix must not be empty"
	errMsgInvalidUnitConversionInput       = "invalid unit-conversion input on UpdatePool"
	errMsgMissingRequiredNodeCapacityField = "missing required nodeCapacity field on UpdatePool"
	errMsgDuplicateNodeUUID                = "duplicate node_uuid in nodeCapacities on UpdatePool"

	errMsgEmptyTieringSecretID   = "tieringConfig.secretId must not be empty"
	errMsgInvalidTieringSecretID = "tieringConfig.secretId must be a valid OCID"
	errMsgEmptyTieringNamespace  = "tieringConfig.namespace must not be empty"
	errMsgEmptyTieringBucketName = "tieringConfig.bucketName must not be empty"
	errMsgEmptyTieringServerName = "tieringConfig.serverName must not be empty"
	errMsgEmptyKmsKeyId          = "kmsKeyId must not be empty when provided"
	errMsgInvalidKmsKeyId        = "kmsKeyId must be a valid OCID"
	errMsgEmptyNsgID             = "nsgIds[%d] must not be empty"
	errMsgInvalidNsgID           = "nsgIds[%d] must be a valid OCID"
)

func normalizeCreatePoolRequest(req *ociserver.CreatePoolRequest, params *ociserver.CreatePoolParams) {
	if params != nil {
		params.TenancyOcid = strings.TrimSpace(params.TenancyOcid)
	}
	if req == nil {
		return
	}
	req.PoolOCID = strings.TrimSpace(req.PoolOCID)
	req.CompartmentOCID = strings.TrimSpace(req.CompartmentOCID)
	req.DisplayName = strings.TrimSpace(req.DisplayName)
	req.SubnetId = strings.TrimSpace(req.SubnetId)
	req.PrimaryAvailabilityDomain = strings.TrimSpace(req.PrimaryAvailabilityDomain)
	req.SecondaryAvailabilityDomain.Value = strings.TrimSpace(req.SecondaryAvailabilityDomain.Value)
	req.MediatorAvailabilityDomain.Value = strings.TrimSpace(req.MediatorAvailabilityDomain.Value)
	req.OciAdminPassword.Ocid = strings.TrimSpace(req.OciAdminPassword.Ocid)
	req.OciAdminPassword.Version = strings.TrimSpace(req.OciAdminPassword.Version)
	req.Description.Value = strings.TrimSpace(req.Description.Value)
	req.DataNicSubnetId = strings.TrimSpace(req.DataNicSubnetId)

	if req.TieringConfig.Set {
		req.TieringConfig.Value.SecretId = strings.TrimSpace(req.TieringConfig.Value.SecretId)
		req.TieringConfig.Value.Namespace = strings.TrimSpace(req.TieringConfig.Value.Namespace)
		req.TieringConfig.Value.BucketName = strings.TrimSpace(req.TieringConfig.Value.BucketName)
		req.TieringConfig.Value.ServerName = strings.TrimSpace(req.TieringConfig.Value.ServerName)
	}

	if req.KmsKeyId.Set {
		req.KmsKeyId.Value = strings.TrimSpace(req.KmsKeyId.Value)
	}
	for i := range req.NsgIds {
		req.NsgIds[i] = strings.TrimSpace(req.NsgIds[i])
	}
}

func normalizeDeletePoolParams(params *ociserver.DeletePoolParams) {
	if params == nil {
		return
	}
	params.PoolOCID = strings.TrimSpace(params.PoolOCID)
	params.TenancyOcid = strings.TrimSpace(params.TenancyOcid)
}

// Handler implements the OCI API Handler interface.
// Embedding UnimplementedHandler provides default 501 for any operation not overridden here.
type Handler struct {
	ociserver.UnimplementedHandler
	Orchestrator   factory.OrchestratorFactory
	ServerState    *ServerState
	TemporalClient client.Client
}

var workflowQueryFn = workflowquery.Query

func NewHandler(serverState *ServerState) *Handler {
	return &Handler{ServerState: serverState}
}

// opcRequestIDFromContext returns opc-request-id from the request headers stored in context.
// ociPrepareRequestMiddleware sets HeaderContextKey (and opc on r.Header); auth may refresh the same map.
func opcRequestIDFromContext(ctx context.Context) (string, error) {
	opcHeader := string(middleware.OPCRequestIDHeaderName)
	h, ok := ctx.Value(middleware.HeaderContextKey).(http.Header)
	if !ok || h == nil {
		return "", errors.New("request headers not in context")
	}
	if id := strings.TrimSpace(h.Get(opcHeader)); id != "" {
		return id, nil
	}
	return "", errors.New("opc-request-id header missing")
}

// GetHealth returns the server health status.
// During graceful shutdown, this returns an error to fail readiness probes,
// allowing the load balancer to stop routing traffic to this pod before it terminates.
func (h *Handler) GetHealth(ctx context.Context) (ociserver.GetHealthRes, error) {
	if h.ServerState != nil && h.ServerState.IsShuttingDown() {
		return &ociserver.StandardError500Headers{
			OpcRequestID: uuid.NewString(),
			Response: ociserver.Error{
				Code:    500,
				Message: "Server is shutting down",
			},
		}, nil
	}
	return &ociserver.Health{Status: ociserver.NewOptString("healthy")}, nil
}

func newCreatePoolBadRequest(opcRequestID, poolOCID, errMsg string) *ociserver.CreatePoolBadRequest {
	return &ociserver.CreatePoolBadRequest{
		OpcRequestID: opcRequestID,
		Response: ociserver.PoolOperationErrorResponse{
			Status:       string(workflowquery.WorkflowStatusFailed),
			PoolOCID:     poolOCID,
			ErrorMessage: errMsg,
		},
	}
}

// ocidRegex matches a structurally valid OCI OCID.
// Format: ocid1.<resource>.<realm>.[region][.future].<uniqueID>
// The region and future-use segments may be empty (e.g. tenancy and compartment
// OCIDs use "ocid1.tenancy.oc1..<uniqueID>"), so both 5- and 6-part forms are
// accepted. Resource type, realm, and unique ID are required.
var ocidRegex = regexp.MustCompile(
	`^ocid1\.[a-z0-9_-]+\.[a-z0-9_-]+\.[a-z0-9_-]*(\.[a-z0-9_-]*)?\.[a-z0-9_-]+$`,
)

func isValidOCID(s string) bool {
	return ocidRegex.MatchString(strings.TrimSpace(s))
}

func validateCreatePoolRequest(req *ociserver.CreatePoolRequest, params *ociserver.CreatePoolParams) error {
	if req == nil {
		return errors.New("request body is required")
	}
	if params == nil {
		return errors.New("request params are required")
	}
	normalizeCreatePoolRequest(req, params)

	if req.PoolOCID == "" {
		return errors.New(errMsgEmptyPoolOCID)
	}
	if !isValidOCID(req.PoolOCID) {
		return errors.New(errMsgInvalidPoolOCID)
	}
	if req.CompartmentOCID == "" {
		return errors.New(errMsgEmptyCompartmentOCID)
	}
	if !isValidOCID(req.CompartmentOCID) {
		return errors.New(errMsgInvalidCompartmentOCID)
	}
	if req.DisplayName == "" {
		return errors.New(errMsgEmptyDisplayName)
	}
	if req.SubnetId == "" {
		return errors.New(errMsgEmptySubnetID)
	}
	if !isValidOCID(req.SubnetId) {
		return errors.New(errMsgInvalidSubnetID)
	}
	if req.SizeInGiB <= 0 {
		return errors.New(errMsgNonPositiveSizeInGiB)
	}
	if req.DataNicSubnetId == "" {
		return errors.New(errMsgEmptyDataNicSubnetID)
	}
	if !isValidOCID(req.DataNicSubnetId) {
		return errors.New(errMsgInvalidDataNicSubnetID)
	}
	if req.OciAdminPassword.Ocid == "" {
		return errors.New(errMsgEmptyAdminPasswordOCID)
	}
	if !isValidOCID(req.OciAdminPassword.Ocid) {
		return errors.New(errMsgInvalidAdminPasswordOCID)
	}
	if req.OciAdminPassword.Version == "" {
		return errors.New(errMsgEmptyAdminPasswordVersion)
	}
	if req.ThroughputGBps <= 0 {
		return errors.New(errMsgNonPositiveThroughputGBps)
	}
	if req.DataEndpointCount%2 != 0 {
		return errors.New(errMsgOddDataEndpointCount)
	}
	if params.TenancyOcid == "" {
		return errors.New(errMsgEmptyTenancyOcid)
	}
	if !isValidOCID(params.TenancyOcid) {
		return errors.New(errMsgInvalidTenancyOcid)
	}
	// Must be checked before isSharedHA below: an empty primaryAvailabilityDomain
	// would make isSharedHA("", "") return true and then propagate empty AD strings
	// all the way to the orchestrator (since secondary/mediator default to primary).
	if req.PrimaryAvailabilityDomain == "" {
		return errors.New(errMsgEmptyPrimaryAD)
	}
	// For non-shared HA, dataEndpointCount must be set to 2
	if !isSharedHA(req.PrimaryAvailabilityDomain, req.SecondaryAvailabilityDomain.Value) && req.DataEndpointCount != 2 {
		return errors.New(errMsgInvalidConfigForNonSharedHA)
	}
	if req.TieringConfig.Set {
		tc := req.TieringConfig.Value
		if tc.SecretId == "" {
			return errors.New(errMsgEmptyTieringSecretID)
		}
		if !isValidOCID(tc.SecretId) {
			return errors.New(errMsgInvalidTieringSecretID)
		}
		if tc.Namespace == "" {
			return errors.New(errMsgEmptyTieringNamespace)
		}
		if tc.BucketName == "" {
			return errors.New(errMsgEmptyTieringBucketName)
		}
		if tc.ServerName == "" {
			return errors.New(errMsgEmptyTieringServerName)
		}
	}
	if req.KmsKeyId.Set {
		if req.KmsKeyId.Value == "" {
			return errors.New(errMsgEmptyKmsKeyId)
		}
		if !isValidOCID(req.KmsKeyId.Value) {
			return errors.New(errMsgInvalidKmsKeyId)
		}
	}
	for i, nsgID := range req.NsgIds {
		if nsgID == "" {
			return fmt.Errorf(errMsgEmptyNsgID, i)
		}
		if !isValidOCID(nsgID) {
			return fmt.Errorf(errMsgInvalidNsgID, i)
		}
	}
	return nil
}

func isSharedHA(primaryAD, secondaryAD string) bool {
	if secondaryAD == "" || secondaryAD == primaryAD {
		return true
	}
	return false
}

// CreatePool returns 202 Accepted with opc-request-id, opc-work-request-id, and the created Pool.
func (h *Handler) CreatePool(ctx context.Context, req *ociserver.CreatePoolRequest, params ociserver.CreatePoolParams) (ociserver.CreatePoolRes, error) {
	logger := util.GetLogger(ctx)

	if req == nil {
		return newCreatePoolBadRequest(uuid.NewString(), "", "request body is required"), nil
	}

	opcRequestID, err := opcRequestIDFromContext(ctx)
	if err != nil {
		logger.Error("missing opc-request-id in context", "error", err)
		return newCreatePoolBadRequest(uuid.NewString(), req.PoolOCID, invalidOPCRequestID), nil
	}

	if err := validateCreatePoolRequest(req, &params); err != nil {
		logger.Error("CreatePool request validation failed", "error", err)
		return newCreatePoolBadRequest(opcRequestID, req.PoolOCID, err.Error()), nil
	}

	ociAdminPasswordVersion, err := strconv.ParseInt(req.OciAdminPassword.Version, 10, 64)
	if err != nil {
		logger.Error("invalid ociAdminPassword version", "error", err)
		return newCreatePoolBadRequest(opcRequestID, req.PoolOCID, errMsgInvalidAdminPasswordVersion), nil
	}

	if ociAdminPasswordVersion < 1 {
		logger.Error("invalid ociAdminPassword version", "version", ociAdminPasswordVersion)
		return newCreatePoolBadRequest(opcRequestID, req.PoolOCID, errMsgAdminPasswordVersionLessThanOne), nil
	}

	var secondaryAD, mediatorAD string
	var isRegionalHA bool

	// shared HA means secondary and primary are the same and mediator is absent
	// secondaryAvailabilityDomain not set implies shared HA
	// when secondaryAvailabilityDomain is set and is same as primaryAvailabilityDomain, it implies shared HA
	if isSharedHA(req.PrimaryAvailabilityDomain, req.SecondaryAvailabilityDomain.Value) {
		secondaryAD = req.PrimaryAvailabilityDomain
		mediatorAD = req.MediatorAvailabilityDomain.Value
		if mediatorAD == "" {
			mediatorAD = req.PrimaryAvailabilityDomain
		}
		isRegionalHA = false
	} else {
		// non shared HA
		secondaryAD = req.SecondaryAvailabilityDomain.Value
		mediatorAD = req.MediatorAvailabilityDomain.Value
		if mediatorAD == "" {
			mediatorAD = req.PrimaryAvailabilityDomain
		}
		isRegionalHA = true
	}

	// Map the provided config to CreatePoolParams
	createPoolParams := &commonparams.CreatePoolParams{
		AccountName:    params.TenancyOcid,                         // compartment_id from ociconfig
		PrimaryZone:    req.PrimaryAvailabilityDomain,              // availability_domain1
		SecondaryZone:  secondaryAD,                                // availability_domain2
		MediatorZone:   mediatorAD,                                 // mediator_availability_domain
		IsRegionalHA:   isRegionalHA,                               // derived from primary vs secondary AD
		Name:           req.DisplayName,                            // deployment_id
		Description:    req.Description.Value,                      // Will be generated by orchestrator
		VendorSubNetID: req.SubnetId,                               // subnet_id from ociconfig
		SizeInBytes:    uint64(req.SizeInGiB) * 1024 * 1024 * 1024, // convert GiB to bytes
		PoolOCID:       req.PoolOCID,                               // OCI pool OCID - used to generate deployment name
		CustomPerformanceParams: &commonparams.CustomPerformanceParams{
			Enabled:         true,
			ThroughputMibps: int64(req.ThroughputGBps * workflowquery.MiBpsPerGBps), // convert GBps from API contract to MiBps for orchestrator
		},
		LargeCapacity:   req.DataEndpointCount/2 >= 2,
		CompartmentOCID: req.CompartmentOCID,
		OciAdminPassword: &commonparams.OciAdminPassword{
			Ocid:    req.OciAdminPassword.Ocid,
			Version: ociAdminPasswordVersion,
		},
		DataNICSubnetID: req.DataNicSubnetId,
		HAPairs:         uint64(req.DataEndpointCount / 2),
		KmsKeyId:        req.KmsKeyId.Value,
		// Defensive copy: the orchestrator may retain this slice across goroutine
		// boundaries (Temporal activity inputs), so aliasing req.NsgIds would
		// risk a downstream read racing with the HTTP request lifecycle. Matches
		// the same convention used by UpdatePool below.
		NsgIds: append([]string(nil), req.NsgIds...),
	}

	if req.TieringConfig.Set {
		createPoolParams.FabricPoolConfig = &commonparams.FabricPoolConfig{
			BucketName: req.TieringConfig.Value.BucketName,
			SecretOcid: req.TieringConfig.Value.SecretId,
			Namespace:  req.TieringConfig.Value.Namespace,
			ServerURL:  req.TieringConfig.Value.ServerName,
		}
	}

	if req.SecurityAttributes.Set {
		createPoolParams.SecurityAttributes = toCustomerSecurityAttributes(req.SecurityAttributes.Value)
	}

	existing, lookupErr := h.lookupExistingWorkflow(ctx, opcRequestID)
	if lookupErr != nil {
		logger.Error("CreatePool idempotency lookup failed; failing closed", "workflowID", opcRequestID, "error", lookupErr)
		return &ociserver.CreatePoolInternalServerError{
			OpcRequestID: opcRequestID,
			Response: ociserver.PoolOperationErrorResponse{
				Status:       string(workflowquery.WorkflowStatusFailed),
				PoolOCID:     req.PoolOCID,
				ErrorMessage: "Internal server error",
			},
		}, nil
	}
	if existing.Found {
		if isTerminalFailure(existing.Status) {
			logger.Info("CreatePool idempotent replay of terminally-failed workflow; surfacing failure", "workflowID", opcRequestID, "status", existing.Status)
			return &ociserver.CreatePoolConflict{
				OpcRequestID: opcRequestID,
				Response: ociserver.PoolOperationErrorResponse{
					Status:       string(existing.Status),
					PoolOCID:     req.PoolOCID,
					ErrorMessage: existing.failureMessage(),
				},
			}, nil
		}
		logger.Info("CreatePool idempotent replay for existing workflow", "workflowID", opcRequestID, "status", existing.Status)
		return newCreatePoolAccepted(opcRequestID, req.PoolOCID, opcRequestID, existing.Status), nil
	}

	createPoolParams.WorkflowID = opcRequestID

	_, workflowID, err := h.Orchestrator.CreatePool(ctx, createPoolParams)
	if err != nil {
		if utilserrors.IsUserInputValidationErr(err) {
			return newCreatePoolBadRequest(opcRequestID, req.PoolOCID, err.Error()), nil
		}
		if utilserrors.IsConflictErr(err) {
			msg := err.Error()
			if unwrapped := errors.Unwrap(err); unwrapped != nil {
				msg = unwrapped.Error()
			}
			return &ociserver.CreatePoolConflict{
				OpcRequestID: opcRequestID,
				Response: ociserver.PoolOperationErrorResponse{
					Status:       string(workflowquery.WorkflowStatusFailed),
					PoolOCID:     req.PoolOCID,
					ErrorMessage: msg,
				},
			}, nil
		}
		logger.Error("CreatePool orchestrator error", "error", err)
		return &ociserver.CreatePoolInternalServerError{
			OpcRequestID: opcRequestID,
			Response: ociserver.PoolOperationErrorResponse{
				Status:       string(workflowquery.WorkflowStatusFailed),
				PoolOCID:     req.PoolOCID,
				ErrorMessage: "Internal server error",
			},
		}, nil
	}
	return newCreatePoolAccepted(opcRequestID, req.PoolOCID, workflowID, workflowquery.WorkflowStatusInProgress), nil
}

func newCreatePoolAccepted(opcRequestID, poolOCID, workflowID string, status workflowquery.WorkflowStatus) *ociserver.CreatePoolAcceptedResponseHeaders {
	return &ociserver.CreatePoolAcceptedResponseHeaders{
		OpcRequestID: opcRequestID,
		Response: ociserver.CreatePoolAcceptedResponse{
			Status:     string(status),
			WorkflowId: workflowID,
			PoolOCID:   poolOCID,
		},
	}
}

func (h *Handler) GetWorkflow(ctx context.Context, params ociserver.GetWorkflowParams) (ociserver.GetWorkflowRes, error) {
	logger := util.GetLogger(ctx)

	opcRequestID, err := opcRequestIDFromContext(ctx)
	if err != nil {
		logger.Error("missing opc-request-id", "error", err)
		return &ociserver.GetWorkflowBadRequest{
			OpcRequestID: uuid.NewString(),
			Response: ociserver.GetWorkflowErrorResponse{
				ErrorMessage: invalidOPCRequestID,
			},
		}, nil
	}

	res, err := workflowQueryFn(ctx, h.TemporalClient, params.WorkRequestId, "")
	if err != nil {
		return mapGetWorkflowQueryError(opcRequestID, err), nil
	}
	resp := ociserver.GetWorkflowStatusResponse{
		WorkflowType: res.WorkflowType,
		Status:       string(res.Status),
	}
	if res.Error != nil {
		workflowErr := ociserver.WorkflowStatusError{
			Message: ociserver.NewOptString(res.Error.Message),
		}
		if res.Error.Cause != "" {
			workflowErr.Cause = ociserver.NewOptString(res.Error.Cause)
		}
		resp.Error = ociserver.NewOptWorkflowStatusError(workflowErr)
	}
	if res.PoolMetadata != nil {
		nodeUUIDsByName, err := h.nodeUUIDsByName(ctx, res.PoolMetadata.PoolUUID)
		if err != nil {
			return mapGetWorkflowQueryError(opcRequestID, err), nil
		}
		vms := make([]ociserver.OCICreatePoolWorkflowVM, 0, len(res.PoolMetadata.Vms))
		for _, vm := range res.PoolMetadata.Vms {
			nodeUUIDs := nodeUUIDsByName[vm.Name]
			vms = append(vms, ociserver.OCICreatePoolWorkflowVM{
				Name:            vm.Name,
				InstanceId:      vm.InstanceID,
				SerialNumber:    vm.SerialNumber,
				VsaManagementIP: vm.VSAManagementIP,
				InterclusterIP:  vm.InterclusterIP,
				NodeUUID:        nodeUUIDs.vcp,
				OntapNodeUUID:   nodeUUIDs.ontap,
				HaPair:          vm.HAPair,
				SizeInGiB:       vm.SizeInGiB,
			})
		}
		poolMeta := ociserver.OCICreatePoolWorkflowMetadata{
			PoolOCID:       res.PoolMetadata.PoolOCID,
			Vms:            vms,
			Iops:           res.PoolMetadata.IOPS,
			ThroughputGBps: roundTo2Decimals(res.PoolMetadata.ThroughputGBps),
			Credentials:    buildWorkflowCredentialsResponse(res.PoolMetadata.Credentials),
		}
		if res.PoolMetadata.ClusterIP != "" {
			poolMeta.ClusterIP = ociserver.NewOptString(res.PoolMetadata.ClusterIP)
		}
		if res.PoolMetadata.Mediator != nil {
			poolMeta.Mediator = ociserver.NewOptOCICreatePoolWorkflowMediator(ociserver.OCICreatePoolWorkflowMediator{
				Name:       res.PoolMetadata.Mediator.Name,
				IP:         res.PoolMetadata.Mediator.IP,
				HaPair:     res.PoolMetadata.Mediator.HAPair,
				InstanceId: res.PoolMetadata.Mediator.InstanceID,
			})
		}
		resp.PoolMetadata = ociserver.NewOptOCICreatePoolWorkflowMetadata(poolMeta)
	}
	if res.SvmMetadata != nil {
		lifs := make([]ociserver.SvmLif, 0, len(res.SvmMetadata.Lifs))
		for _, l := range res.SvmMetadata.Lifs {
			protocols := make([]ociserver.SvmLifProtocolsItem, 0, len(l.Protocols))
			for _, p := range l.Protocols {
				protocols = append(protocols, ociserver.SvmLifProtocolsItem(p))
			}
			lif := ociserver.SvmLif{
				Name:      ociserver.NewOptString(l.Name),
				IpAddress: ociserver.NewOptString(l.IP),
				Node:      ociserver.NewOptString(l.Node),
				Protocols: protocols,
			}
			if l.NodeUUID != "" {
				lif.NodeUUID = ociserver.NewOptString(l.NodeUUID)
			}
			if l.HaPair != nil {
				lif.HaPair = ociserver.NewOptString(*l.HaPair)
			}
			lifs = append(lifs, lif)
		}
		resp.SvmMetadata = ociserver.NewOptOCICreateSVMWorkflowMetadata(
			ociserver.OCICreateSVMWorkflowMetadata{
				Name:    ociserver.NewOptString(res.SvmMetadata.Name),
				SvmOCID: ociserver.NewOptString(res.SvmMetadata.SvmOCID),
				Lifs:    lifs,
			},
		)
	}
	return &ociserver.GetWorkflowStatusResponseHeaders{
		OpcRequestID: opcRequestID,
		Response:     resp,
	}, nil
}

// buildWorkflowCredentialsResponse maps the workflowquery credentials metadata
// into the API contract. Both `secret` and `certificate` are required by the
// schema, so we always emit them as objects; missing references collapse to
// empty {ocid:"", version:""} placeholders.
func buildWorkflowCredentialsResponse(creds *workflowquery.OCICreatePoolCredentialsMetadata) ociserver.OCICreatePoolWorkflowCredentials {
	out := ociserver.OCICreatePoolWorkflowCredentials{}
	if creds == nil {
		return out
	}
	if creds.Secret != nil {
		out.Secret = ociserver.OCIOCIDVersionRef{
			Ocid:    creds.Secret.Ocid,
			Version: creds.Secret.Version,
		}
	}
	if creds.Certificate != nil {
		out.Certificate = ociserver.OCIOCIDVersionRef{
			Ocid:    creds.Certificate.Ocid,
			Version: creds.Certificate.Version,
		}
	}
	return out
}

type nodeUUIDs struct {
	vcp   string
	ontap string
}

func (h *Handler) nodeUUIDsByName(ctx context.Context, poolUUID string) (map[string]nodeUUIDs, error) {
	if poolUUID == "" {
		return nil, nil
	}
	nodes, err := h.Orchestrator.GetNodesByPoolUUID(ctx, poolUUID)
	if err != nil {
		return nil, fmt.Errorf("node UUID enrichment failed for pool %q: %w", poolUUID, err)
	}
	uuidsByName := make(map[string]nodeUUIDs, len(nodes))
	for _, n := range nodes {
		if n != nil && n.Name != "" {
			uuids := nodeUUIDs{vcp: n.UUID}
			if n.NodeAttributes != nil {
				uuids.ontap = n.NodeAttributes.ExternalUUID
			}
			uuidsByName[n.Name] = uuids
		}
	}
	return uuidsByName, nil
}

func mapGetWorkflowQueryError(opcRequestID string, err error) ociserver.GetWorkflowRes {
	code := statusFromError(err)
	if code == http.StatusNotFound {
		return &ociserver.GetWorkflowNotFound{
			OpcRequestID: opcRequestID,
			Response: ociserver.GetWorkflowErrorResponse{
				ErrorMessage: err.Error(),
			},
		}
	}
	return &ociserver.GetWorkflowInternalServerError{
		OpcRequestID: opcRequestID,
		Response: ociserver.GetWorkflowErrorResponse{
			ErrorMessage: err.Error(),
		},
	}
}

func validateDeletePoolRequest(params *ociserver.DeletePoolParams) error {
	if params == nil {
		return errors.New("request params are required")
	}
	normalizeDeletePoolParams(params)

	if params.PoolOCID == "" {
		return errors.New(errMsgEmptyPoolOCID)
	}
	if !isValidOCID(params.PoolOCID) {
		return errors.New(errMsgInvalidPoolOCID)
	}
	if params.TenancyOcid == "" {
		return errors.New(errMsgEmptyTenancyOcid)
	}
	if !isValidOCID(params.TenancyOcid) {
		return errors.New(errMsgInvalidTenancyOcid)
	}
	return nil
}

func (h *Handler) DeletePool(ctx context.Context, params ociserver.DeletePoolParams) (ociserver.DeletePoolRes, error) {
	logger := util.GetLogger(ctx)

	opcRequestID, err := opcRequestIDFromContext(ctx)
	if err != nil {
		return &ociserver.DeletePoolBadRequest{
			OpcRequestID: uuid.NewString(),
			Response: ociserver.PoolOperationErrorResponse{
				Status:       string(workflowquery.WorkflowStatusFailed),
				PoolOCID:     params.PoolOCID,
				ErrorMessage: invalidOPCRequestID,
			},
		}, nil
	}

	if err := validateDeletePoolRequest(&params); err != nil {
		logger.Error("DeletePool request validation failed", "error", err)
		return &ociserver.DeletePoolBadRequest{
			OpcRequestID: opcRequestID,
			Response: ociserver.PoolOperationErrorResponse{
				Status:       string(workflowquery.WorkflowStatusFailed),
				PoolOCID:     params.PoolOCID,
				ErrorMessage: err.Error(),
			},
		}, nil
	}

	poolOCID := params.PoolOCID

	existing, lookupErr := h.lookupExistingWorkflow(ctx, opcRequestID)
	if lookupErr != nil {
		logger.Error("DeletePool idempotency lookup failed; failing closed", "workflowID", opcRequestID, "error", lookupErr)
		return &ociserver.DeletePoolInternalServerError{
			OpcRequestID: opcRequestID,
			Response: ociserver.PoolOperationErrorResponse{
				Status:       string(workflowquery.WorkflowStatusFailed),
				PoolOCID:     poolOCID,
				ErrorMessage: "Internal server error",
			},
		}, nil
	}
	if existing.Found {
		if isTerminalFailure(existing.Status) {
			logger.Info("DeletePool idempotent replay of terminally-failed workflow; surfacing failure", "workflowID", opcRequestID, "status", existing.Status)
			return &ociserver.DeletePoolConflict{
				OpcRequestID: opcRequestID,
				Response: ociserver.PoolOperationErrorResponse{
					Status:       string(existing.Status),
					PoolOCID:     poolOCID,
					ErrorMessage: existing.failureMessage(),
				},
			}, nil
		}
		logger.Info("DeletePool idempotent replay for existing workflow", "workflowID", opcRequestID, "status", existing.Status)
		return &ociserver.DeletePoolAcceptedResponseHeaders{
			OpcRequestID: opcRequestID,
			Response: ociserver.DeletePoolAcceptedResponse{
				Status:     string(existing.Status),
				WorkflowId: opcRequestID,
				PoolOCID:   poolOCID,
			},
		}, nil
	}

	// Map to DeletePoolParams - for OCI, AccountName is compartment OCID and PoolName is deployment name
	deletePoolParams := &commonparams.DeletePoolParams{
		AccountName: params.TenancyOcid, // account name (compartment OCID)
		PoolOCID:    poolOCID,           // path parameter
		WorkflowID:  opcRequestID,       // OCI: workflow id == opc-request-id for idempotency
	}

	// Delete the pool
	deleted, workflowID, err := h.Orchestrator.DeletePool(ctx, deletePoolParams)
	if err != nil {
		logger.Error("Failed to delete pool", "error", err.Error())
		if utilserrors.IsNotFoundErr(err) {
			logger.Info("Pool not found", "poolOCID", deletePoolParams.PoolOCID)
			return &ociserver.DeletePoolNotFound{
				OpcRequestID: opcRequestID,
				Response: ociserver.PoolOperationErrorResponse{
					Status:       string(workflowquery.WorkflowStatusFailed),
					PoolOCID:     poolOCID,
					ErrorMessage: err.Error(),
				},
			}, nil
		}
		if utilserrors.IsBadRequestErr(err) {
			return &ociserver.DeletePoolBadRequest{
				OpcRequestID: opcRequestID,
				Response: ociserver.PoolOperationErrorResponse{
					Status:       string(workflowquery.WorkflowStatusFailed),
					PoolOCID:     poolOCID,
					ErrorMessage: err.Error(),
				},
			}, nil
		}
		if utilserrors.IsConflictErr(err) {
			return &ociserver.DeletePoolConflict{
				OpcRequestID: opcRequestID,
				Response: ociserver.PoolOperationErrorResponse{
					Status:       string(workflowquery.WorkflowStatusFailed),
					PoolOCID:     poolOCID,
					ErrorMessage: fmt.Errorf("Error deleting pool : %w", err).Error(),
				},
			}, nil
		}
		logger.Error("Failed to delete pool", "error", err.Error())
		return &ociserver.DeletePoolInternalServerError{
			OpcRequestID: opcRequestID,
			Response: ociserver.PoolOperationErrorResponse{
				Status:       string(workflowquery.WorkflowStatusFailed),
				PoolOCID:     poolOCID,
				ErrorMessage: "Internal server error",
			},
		}, nil
	}
	if deleted.State == datamodel.LifeCycleStateDeleting || deleted.State == datamodel.LifeCycleStateCreating {
		return &ociserver.DeletePoolAcceptedResponseHeaders{
			OpcRequestID: opcRequestID,
			Response: ociserver.DeletePoolAcceptedResponse{
				Status:     string(workflowquery.WorkflowStatusInProgress),
				WorkflowId: workflowID,
				PoolOCID:   poolOCID,
			},
		}, nil
	}

	logger.Info("Pool deleted successfully", "poolOCID", deletePoolParams.PoolOCID)
	return &ociserver.DeletePoolNoContent{OpcRequestID: opcRequestID}, nil
}

func newUpdatePoolBadRequest(opcRequestID, poolExternalIdentifier, errMsg string) *ociserver.UpdatePoolBadRequest {
	return (*ociserver.UpdatePoolBadRequest)(&ociserver.PoolOperationErrorResponseHeaders{
		OpcRequestID: opcRequestID,
		Response: ociserver.PoolOperationErrorResponse{
			Status:       string(workflowquery.WorkflowStatusFailed),
			PoolOCID:     poolExternalIdentifier,
			ErrorMessage: errMsg,
		},
	})
}

// validateUpdatePoolUnitConversionInputs guards values that feed into the GBps→MiBps unit
// conversion and the per-node GiB→bytes math downstream. Non-positive or non-finite inputs
// would either wrap (uint64(neg)) or produce a negative MiBps that bypasses downstream shrink
// checks, so reject them before mapping to params. The pool-level `sizeInGiB` has been removed
// from the request (capacity is expressed only via `nodeCapacities[].sizeInGiB`), so it is no
// longer validated here.
// Returns an empty string when the request is valid, otherwise a single error message suitable
// for the bad-request response body.
func validateUpdatePoolUnitConversionInputs(req *ociserver.UpdatePoolRequest) string {
	if req.ThroughputGBps.Set {
		v := req.ThroughputGBps.Value
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return errMsgThroughputGBpsNotFinite
		}
		if v <= 0 {
			return errMsgThroughputGBpsNotPositive
		}
	}
	for i, nc := range req.NodeCapacities {
		if nc.SizeInGiB <= 0 {
			return fmt.Sprintf(errMsgNodeCapacitySizeNotPositive, i)
		}
	}
	return ""
}

func validateUpdatePoolNodeCapacityRequiredFields(req *ociserver.UpdatePoolRequest) string {
	for i, nc := range req.NodeCapacities {
		if strings.TrimSpace(nc.Name) == "" {
			return fmt.Sprintf(errMsgNodeCapacityNameRequired, i)
		}
		if strings.TrimSpace(nc.NodeUUID) == "" {
			return fmt.Sprintf(errMsgNodeCapacityNodeUUIDRequired, i)
		}
	}
	return ""
}

func updatePoolHasUpdatableFields(req *ociserver.UpdatePoolRequest) bool {
	return req.ThroughputGBps.Set ||
		req.DataEndpointCount.Set ||
		len(req.NodeCapacities) > 0 ||
		req.OciAdminPassword.Set ||
		req.KmsKeyId.Set ||
		req.NsgIds != nil ||
		req.SecurityAttributes.Set
}

func validateUpdatePoolRequest(req *ociserver.UpdatePoolRequest, hasNodeCapacities, hasDataEndpointCount bool) (errMsg string) {
	switch {
	case !updatePoolHasUpdatableFields(req):
		return errMsgNoUpdatableFields
	case hasNodeCapacities && hasDataEndpointCount:
		return errMsgBothNodeCapacitiesAndDEC
	case validateUpdatePoolUnitConversionInputs(req) != "":
		return errMsgInvalidUnitConversionInput
	case validateUpdatePoolNodeCapacityRequiredFields(req) != "":
		return errMsgMissingRequiredNodeCapacityField
	case validateUpdatePoolNodeCapacityUniqueness(req) != "":
		return errMsgDuplicateNodeUUID
	default:
		return ""
	}
}

func validateUpdatePoolNodeCapacityUniqueness(req *ociserver.UpdatePoolRequest) string {
	if len(req.NodeCapacities) < 2 {
		return ""
	}
	seen := make(map[string]struct{}, len(req.NodeCapacities))
	for i, nc := range req.NodeCapacities {
		u := strings.TrimSpace(nc.NodeUUID)
		if u == "" {
			continue
		}
		if _, ok := seen[u]; ok {
			return fmt.Sprintf(errMsgNodeCapacityNodeUUIDDuplicate, i, u)
		}
		seen[u] = struct{}{}
	}
	return ""
}

func (h *Handler) UpdatePool(ctx context.Context, req *ociserver.UpdatePoolRequest, params ociserver.UpdatePoolParams) (ociserver.UpdatePoolRes, error) {
	logger := util.GetLogger(ctx)
	poolExternalIdentifier := params.PoolOCID

	if poolExternalIdentifier == "" {
		return newUpdatePoolBadRequest(uuid.NewString(), poolExternalIdentifier, errMsgEmptyPoolOCID), nil
	}

	opcRequestID, err := opcRequestIDFromContext(ctx)
	if err != nil {
		logger.Error("missing opc-request-id in context", "error", err)
		return newUpdatePoolBadRequest(uuid.NewString(), poolExternalIdentifier, invalidOPCRequestID), nil
	}

	hasNodeCapacities := len(req.NodeCapacities) > 0
	hasDataEndpointCount := req.DataEndpointCount.Set

	if errMsg := validateUpdatePoolRequest(req, hasNodeCapacities, hasDataEndpointCount); errMsg != "" {
		logger.Error("UpdatePool request validation failed",
			"poolOCID", poolExternalIdentifier,
			"error", errMsg,
		)
		return newUpdatePoolBadRequest(opcRequestID, poolExternalIdentifier, errMsg), nil
	}

	updateParams := &commonparams.UpdatePoolParams{
		AccountName:            params.TenancyOcid,
		PoolExternalIdentifier: poolExternalIdentifier,
	}

	if req.ThroughputGBps.Set {
		updateParams.TotalThroughputMibps = int64(req.ThroughputGBps.Value * workflowquery.MiBpsPerGBps)
		updateParams.CustomPerformanceEnabled = true
	}
	if hasNodeCapacities {
		updateParams.NodeCapacities = make([]commonparams.NodeCapacity, 0, len(req.NodeCapacities))
		for _, nc := range req.NodeCapacities {
			updateParams.NodeCapacities = append(updateParams.NodeCapacities, commonparams.NodeCapacity{
				Name:      nc.Name,
				NodeUUID:  nc.NodeUUID,
				SizeInGiB: nc.SizeInGiB,
			})
		}
	}
	if hasDataEndpointCount {
		updateParams.HAPairs = uint64(req.DataEndpointCount.Value / 2)
	}
	if req.OciAdminPassword.Set {
		version, parseErr := strconv.ParseInt(req.OciAdminPassword.Value.Version, 10, 64)
		if parseErr != nil {
			logger.Error("invalid ociAdminPassword version", "error", parseErr)
			return newUpdatePoolBadRequest(opcRequestID, poolExternalIdentifier, errMsgInvalidAdminPasswordVersion), nil
		}
		if version < 1 {
			logger.Error("invalid ociAdminPassword version", "version", version)
			return newUpdatePoolBadRequest(opcRequestID, poolExternalIdentifier, errMsgAdminPasswordVersionLessThanOne), nil
		}
		updateParams.OciAdminPassword = &commonparams.OciAdminPassword{
			Ocid:    req.OciAdminPassword.Value.Ocid,
			Version: version,
		}
	}
	if req.KmsKeyId.Set {
		kmsKeyId := strings.TrimSpace(req.KmsKeyId.Value)
		if kmsKeyId == "" {
			return newUpdatePoolBadRequest(opcRequestID, poolExternalIdentifier, errMsgEmptyKmsKeyId), nil
		}
		if !isValidOCID(kmsKeyId) {
			return newUpdatePoolBadRequest(opcRequestID, poolExternalIdentifier, errMsgInvalidKmsKeyId), nil
		}
		updateParams.KmsKeyId = kmsKeyId
	}
	if req.NsgIds != nil {
		nsgIds := make([]string, len(req.NsgIds))
		for i, nsgID := range req.NsgIds {
			nsgID = strings.TrimSpace(nsgID)
			if nsgID == "" {
				return newUpdatePoolBadRequest(opcRequestID, poolExternalIdentifier, fmt.Sprintf(errMsgEmptyNsgID, i)), nil
			}
			if !isValidOCID(nsgID) {
				return newUpdatePoolBadRequest(opcRequestID, poolExternalIdentifier, fmt.Sprintf(errMsgInvalidNsgID, i)), nil
			}
			nsgIds[i] = nsgID
		}
		updateParams.NsgIds = nsgIds
	}
	if req.SecurityAttributes.Set {
		updateParams.SecurityAttributes = toUpdateCustomerSecurityAttributes(req.SecurityAttributes.Value)
	}

	existing, lookupErr := h.lookupExistingWorkflow(ctx, opcRequestID)
	if lookupErr != nil {
		logger.Error("UpdatePool idempotency lookup failed; failing closed", "workflowID", opcRequestID, "error", lookupErr)
		return (*ociserver.UpdatePoolInternalServerError)(&ociserver.PoolOperationErrorResponseHeaders{
			OpcRequestID: opcRequestID,
			Response: ociserver.PoolOperationErrorResponse{
				Status:       string(workflowquery.WorkflowStatusFailed),
				PoolOCID:     poolExternalIdentifier,
				ErrorMessage: "Internal server error",
			},
		}), nil
	}
	if existing.Found {
		if isTerminalFailure(existing.Status) {
			logger.Info("UpdatePool idempotent replay of terminally-failed workflow; surfacing failure", "workflowID", opcRequestID, "status", existing.Status)
			return (*ociserver.UpdatePoolConflict)(&ociserver.PoolOperationErrorResponseHeaders{
				OpcRequestID: opcRequestID,
				Response: ociserver.PoolOperationErrorResponse{
					Status:       string(existing.Status),
					PoolOCID:     poolExternalIdentifier,
					ErrorMessage: existing.failureMessage(),
				},
			}), nil
		}
		logger.Info("UpdatePool idempotent replay for existing workflow", "workflowID", opcRequestID, "status", existing.Status)
		return newUpdatePoolAccepted(opcRequestID, poolExternalIdentifier, opcRequestID, existing.Status), nil
	}

	updateParams.WorkflowID = opcRequestID

	_, workflowID, err := h.Orchestrator.UpdatePool(ctx, updateParams)
	if err != nil {
		if utilserrors.IsUserInputValidationErr(err) || utilserrors.IsBadRequestErr(err) {
			return newUpdatePoolBadRequest(opcRequestID, poolExternalIdentifier, err.Error()), nil
		}
		if utilserrors.IsNotFoundErr(err) {
			return (*ociserver.UpdatePoolNotFound)(&ociserver.PoolOperationErrorResponseHeaders{
				OpcRequestID: opcRequestID,
				Response: ociserver.PoolOperationErrorResponse{
					Status:       string(workflowquery.WorkflowStatusFailed),
					PoolOCID:     poolExternalIdentifier,
					ErrorMessage: err.Error(),
				},
			}), nil
		}
		if utilserrors.IsConflictErr(err) {
			return (*ociserver.UpdatePoolConflict)(&ociserver.PoolOperationErrorResponseHeaders{
				OpcRequestID: opcRequestID,
				Response: ociserver.PoolOperationErrorResponse{
					Status:       string(workflowquery.WorkflowStatusFailed),
					PoolOCID:     poolExternalIdentifier,
					ErrorMessage: "Error updating pool - Pool is already transitioning between states",
				},
			}), nil
		}
		logger.Error("UpdatePool orchestrator error", "error", err)
		return (*ociserver.UpdatePoolInternalServerError)(&ociserver.PoolOperationErrorResponseHeaders{
			OpcRequestID: opcRequestID,
			Response: ociserver.PoolOperationErrorResponse{
				Status:       string(workflowquery.WorkflowStatusFailed),
				PoolOCID:     poolExternalIdentifier,
				ErrorMessage: "Internal server error",
			},
		}), nil
	}

	return newUpdatePoolAccepted(opcRequestID, poolExternalIdentifier, workflowID, workflowquery.WorkflowStatusInProgress), nil
}

func newUpdatePoolAccepted(opcRequestID, poolOCID, workflowID string, status workflowquery.WorkflowStatus) *ociserver.UpdatePoolAcceptedResponseHeaders {
	return &ociserver.UpdatePoolAcceptedResponseHeaders{
		OpcRequestID: opcRequestID,
		Response: ociserver.UpdatePoolAcceptedResponse{
			Status:     string(status),
			WorkflowId: workflowID,
			PoolOCID:   poolOCID,
		},
	}
}

// NewError maps handler errors to HTTP status and returns *ErrorStatusCode. Defaults to 500 when status cannot be determined.
func (*Handler) NewError(ctx context.Context, err error) *ociserver.ErrorStatusCode {
	if err == nil {
		return &ociserver.ErrorStatusCode{StatusCode: http.StatusInternalServerError, Response: ociserver.Error{Code: 500, Message: "internal error"}}
	}
	code := statusFromError(err)
	msg := err.Error()
	if msg == "" {
		msg = http.StatusText(code)
	}
	return &ociserver.ErrorStatusCode{
		StatusCode: code,
		Response:   ociserver.Error{Code: float64(code), Message: msg},
	}
}

// statusFromError maps known error types to HTTP status codes; returns 500 for unknown errors.
func statusFromError(err error) int {
	switch {
	case errors.Is(err, context.Canceled):
		return 499 // Client Closed Request (non-standard but common)
	case errors.Is(err, context.DeadlineExceeded):
		return http.StatusGatewayTimeout // 504
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "not found"):
		return http.StatusNotFound // 404
	case strings.Contains(msg, "unauthorized") || strings.Contains(msg, "invalid token"):
		return http.StatusUnauthorized // 401
	case strings.Contains(msg, "forbidden") || strings.Contains(msg, "access denied"):
		return http.StatusForbidden // 403
	case strings.Contains(msg, "conflict") || strings.Contains(msg, "already exists"):
		return http.StatusConflict // 409
	case strings.Contains(msg, "validation") || strings.Contains(msg, "bad request") || strings.Contains(msg, "invalid"):
		return http.StatusBadRequest // 400
	case strings.Contains(msg, "too many requests") || strings.Contains(msg, "rate limit"):
		return http.StatusTooManyRequests // 429
	case strings.Contains(msg, "unprocessable"):
		return http.StatusUnprocessableEntity // 422
	default:
		return http.StatusInternalServerError // 500
	}
}

func toCustomerSecurityAttributes(in ociserver.SecurityAttributes) map[string]map[string]interface{} {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]map[string]interface{}, len(in))
	for ns, attrs := range in {
		if len(attrs) == 0 {
			continue
		}
		inner := make(map[string]interface{}, len(attrs))
		for name, v := range attrs {
			attr := map[string]string{
				"value": v.Value,
				"mode":  string(v.Mode),
			}
			inner[name] = attr
		}
		out[ns] = inner
	}
	return out
}

func toUpdateCustomerSecurityAttributes(in ociserver.SecurityAttributes) map[string]map[string]interface{} {
	if len(in) == 0 {
		return map[string]map[string]interface{}{}
	}
	attrs := toCustomerSecurityAttributes(in)
	if attrs == nil {
		return map[string]map[string]interface{}{}
	}
	return attrs
}

func roundTo2Decimals(v float64) float64 {
	return math.Round(v*100) / 100
}

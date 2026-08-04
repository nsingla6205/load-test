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
	"unicode/utf8"

	"github.com/google/uuid"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/factory"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	ociserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/oci-proxy/api/oci-servergen"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	utilserrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/workflowquery"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/client"
)

const (
	invalidOPCRequestID                    = "opc-request-id or opc-work-request-id not set"
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

	errMsgEmptyTieringSecretID    = "tieringConfig.secretId must not be empty"
	errMsgInvalidTieringSecretID  = "tieringConfig.secretId must be a valid OCID"
	errMsgInvalidTieringSecretVer = "tieringConfig.secretVersion must be a positive integer"
	errMsgEmptyTieringNamespace   = "tieringConfig.namespace must not be empty"
	errMsgEmptyTieringBucketName  = "tieringConfig.bucketName must not be empty"
	errMsgEmptyTieringServerName  = "tieringConfig.serverName must not be empty"
	errMsgEmptyKmsKeyId           = "kmsKeyId must not be empty when provided"
	errMsgInvalidKmsKeyId         = "kmsKeyId must be a valid OCID"
	errMsgEmptyNsgID              = "nsgIds[%d] must not be empty"
	errMsgInvalidNsgID            = "nsgIds[%d] must be a valid OCID"

	// ociAdminTempPassword complexity policy. Messages intentionally never echo
	// the password value, which is plain text.
	errMsgTempPasswordLength           = "ociAdminTempPassword must be between %d and %d characters"
	errMsgTempPasswordContainsUsername = "ociAdminTempPassword must not contain the admin username"
	errMsgTempPasswordConsecutive      = "ociAdminTempPassword must not contain %d or more consecutive identical characters"
	// errMsgAdminTempPasswordDisabled is returned when the reset-password
	// operation is invoked while the ENABLE_ADMIN_TEMP_PASSWORD feature flag is
	// off. The message never echoes any secret.
	errMsgAdminTempPasswordDisabled = "admin temporary password feature is not enabled"
)

// ociAdminTempPassword complexity limits (fixed policy).
const (
	ociAdminTempPasswordMinLen = 8
	ociAdminTempPasswordMaxLen = 64
	// ociAdminTempPasswordMaxRepeat is the run length of identical characters that
	// is rejected (e.g. "aaa" with a value of 3).
	ociAdminTempPasswordMaxRepeat = 3
)

// ociExpertModeUsername is the OCI expert-mode admin username the temporary
// password must not contain. Read once at startup, mirroring the workflow's
// OCI_EXPERT_MODE_USERNAME default.
var ociExpertModeUsername = env.GetString("OCI_EXPERT_MODE_USERNAME", "ociadmin")

// enableAdminTempPassword gates the OCI admin temporary-password feature (the
// reset-password operation). Read once at startup, mirroring the worker's
// ENABLE_ADMIN_TEMP_PASSWORD default so the proxy and worker agree on whether
// the feature is active.
var enableAdminTempPassword = env.GetBool("ENABLE_ADMIN_TEMP_PASSWORD", false)

func normalizeCreatePoolRequest(req *ociserver.CreatePoolRequest, params *ociserver.CreatePoolParams) {
	if params != nil {
		params.TenancyOcid = strings.TrimSpace(params.TenancyOcid)
	}
	if req == nil {
		return
	}
	req.PoolOCID = strings.TrimSpace(req.PoolOCID)
	req.CompartmentOCID = strings.TrimSpace(req.CompartmentOCID)
	req.ResourceName.Value = strings.TrimSpace(req.ResourceName.Value)
	req.SubnetId = strings.TrimSpace(req.SubnetId)
	req.PrimaryAvailabilityDomain = strings.TrimSpace(req.PrimaryAvailabilityDomain)
	req.SecondaryAvailabilityDomain.Value = strings.TrimSpace(req.SecondaryAvailabilityDomain.Value)
	req.MediatorAvailabilityDomain.Value = strings.TrimSpace(req.MediatorAvailabilityDomain.Value)
	if req.OciAdminPassword.Set {
		req.OciAdminPassword.Value.Ocid = strings.TrimSpace(req.OciAdminPassword.Value.Ocid)
		req.OciAdminPassword.Value.Version = strings.TrimSpace(req.OciAdminPassword.Value.Version)
	}
	if req.OciAdminTempPassword.Set {
		req.OciAdminTempPassword.Value = strings.TrimSpace(req.OciAdminTempPassword.Value)
	}
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

func opcWorkRequestIDFromContext(ctx context.Context) (string, error) {
	h, ok := ctx.Value(middleware.HeaderContextKey).(http.Header)
	if !ok || h == nil {
		return "", errors.New("request headers not in context")
	}
	if strings.TrimSpace(h.Get(string(middleware.OPCRequestIDHeaderName))) == "" {
		return "", errors.New("opc-request-id header missing")
	}
	opcWorkRequestID := strings.TrimSpace(h.Get(string(middleware.OPCWorkRequestIDHeaderName)))
	if opcWorkRequestID == "" {
		return "", errors.New("opc-work-request-id header missing")
	}
	return opcWorkRequestID, nil
}

// GetHealth returns the server health status.
// During graceful shutdown, this returns an error to fail readiness probes,
// allowing the load balancer to stop routing traffic to this pod before it terminates.
func (h *Handler) GetHealth(ctx context.Context) (ociserver.GetHealthRes, error) {
	if h.ServerState != nil && h.ServerState.IsShuttingDown() {
		return &ociserver.StandardError500Headers{
			OpcWorkRequestID: uuid.NewString(),
			Response: ociserver.Error{
				Code:    500,
				Message: "Server is shutting down",
			},
		}, nil
	}
	return &ociserver.Health{Status: ociserver.NewOptString("healthy")}, nil
}

func newCreatePoolBadRequest(opcWorkRequestID, poolOCID, errMsg string) *ociserver.CreatePoolBadRequest {
	return &ociserver.CreatePoolBadRequest{
		OpcWorkRequestID: opcWorkRequestID,
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

// hasConsecutiveIdenticalChars reports whether s contains a run of limit or more
// identical characters (compared per rune, e.g. "aaa" for limit 3).
func hasConsecutiveIdenticalChars(s string, limit int) bool {
	if limit <= 0 {
		return false
	}
	var prev rune
	run := 0
	first := true
	for _, r := range s {
		if !first && r == prev {
			run++
		} else {
			run = 1
		}
		if run >= limit {
			return true
		}
		prev = r
		first = false
	}
	return false
}

// validateOciAdminTempPassword enforces the fixed complexity policy for the
// optional plain-text temporary admin password: length bounds, it must not
// contain the admin username (case-insensitive), and it must not contain a run
// of identical characters. Errors never include the password value.
func validateOciAdminTempPassword(password, username string) error {
	length := utf8.RuneCountInString(password)
	if length < ociAdminTempPasswordMinLen || length > ociAdminTempPasswordMaxLen {
		return fmt.Errorf(errMsgTempPasswordLength, ociAdminTempPasswordMinLen, ociAdminTempPasswordMaxLen)
	}
	if username != "" && strings.Contains(strings.ToLower(password), strings.ToLower(username)) {
		return errors.New(errMsgTempPasswordContainsUsername)
	}
	if hasConsecutiveIdenticalChars(password, ociAdminTempPasswordMaxRepeat) {
		return fmt.Errorf(errMsgTempPasswordConsecutive, ociAdminTempPasswordMaxRepeat)
	}
	return nil
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
	// ociAdminPassword is optional: validate the secret reference only when the
	// caller supplies it. When omitted, the expert-mode password is resolved
	// downstream (env var, Vault, or a generated temporary password).
	if req.OciAdminPassword.Set {
		if req.OciAdminPassword.Value.Ocid == "" {
			return errors.New(errMsgEmptyAdminPasswordOCID)
		}
		if !isValidOCID(req.OciAdminPassword.Value.Ocid) {
			return errors.New(errMsgInvalidAdminPasswordOCID)
		}
		if req.OciAdminPassword.Value.Version == "" {
			return errors.New(errMsgEmptyAdminPasswordVersion)
		}
	}
	// Optional plain-text temporary admin password: validate complexity only when supplied.
	if req.OciAdminTempPassword.Set && req.OciAdminTempPassword.Value != "" {
		if err := validateOciAdminTempPassword(req.OciAdminTempPassword.Value, ociExpertModeUsername); err != nil {
			return err
		}
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
		if tc.SecretVersion.Set && tc.SecretVersion.Value < 1 {
			return errors.New(errMsgInvalidTieringSecretVer)
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

	opcWorkRequestID, err := opcWorkRequestIDFromContext(ctx)
	if err != nil {
		logger.Error("missing opc-request-id or opc-work-request-id in context", "error", err)
		return newCreatePoolBadRequest(uuid.NewString(), req.PoolOCID, invalidOPCRequestID), nil
	}

	if err := validateCreatePoolRequest(req, &params); err != nil {
		logger.Error("CreatePool request validation failed", "error", err)
		return newCreatePoolBadRequest(opcWorkRequestID, req.PoolOCID, err.Error()), nil
	}

	// Build the optional admin-password secret reference only when supplied. When
	// omitted it stays nil, and the create-pool workflow resolves the expert-mode
	// password from the env var / Vault / temporary-password path instead.
	var ociAdminPwd *commonparams.OciAdminPassword
	if req.OciAdminPassword.Set {
		ociAdminPasswordVersion, parseErr := strconv.ParseInt(req.OciAdminPassword.Value.Version, 10, 64)
		if parseErr != nil {
			logger.Error("invalid ociAdminPassword version", "error", parseErr)
			return newCreatePoolBadRequest(opcWorkRequestID, req.PoolOCID, errMsgInvalidAdminPasswordVersion), nil
		}
		if ociAdminPasswordVersion < 1 {
			logger.Error("invalid ociAdminPassword version", "version", ociAdminPasswordVersion)
			return newCreatePoolBadRequest(opcWorkRequestID, req.PoolOCID, errMsgAdminPasswordVersionLessThanOne), nil
		}
		ociAdminPwd = &commonparams.OciAdminPassword{
			Ocid:    req.OciAdminPassword.Value.Ocid,
			Version: ociAdminPasswordVersion,
		}
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
		Name:           req.ResourceName.Value,                     // defaults to DeploymentName when empty
		Description:    req.Description.Value,                      // Will be generated by orchestrator
		VendorSubNetID: req.SubnetId,                               // subnet_id from ociconfig
		SizeInBytes:    uint64(req.SizeInGiB) * 1024 * 1024 * 1024, // convert GiB to bytes
		PoolOCID:       req.PoolOCID,                               // OCI pool OCID - used to generate deployment name
		CustomPerformanceParams: &commonparams.CustomPerformanceParams{
			Enabled:         true,
			ThroughputMibps: workflowquery.GBpsToMBps(req.ThroughputGBps),
			Iops:            optInt64ToPtr(req.Iops), // nil → tier default (VMRS on) or VLM default (VMRS off)
		},
		LargeCapacity:        req.DataEndpointCount/2 >= 2,
		CompartmentOCID:      req.CompartmentOCID,
		OciAdminPassword:     ociAdminPwd,
		OciAdminTempPassword: req.OciAdminTempPassword.Value,
		DataNICSubnetID:      req.DataNicSubnetId,
		HAPairs:              uint64(req.DataEndpointCount / 2),
		KmsKeyId:             req.KmsKeyId.Value,
		// Optional fault domain overrides; empty values trigger auto-selection downstream.
		PrimaryFaultDomain:   strings.TrimSpace(req.PrimaryFaultDomain.Value),
		SecondaryFaultDomain: strings.TrimSpace(req.SecondaryFaultDomain.Value),
		MediatorFaultDomain:  strings.TrimSpace(req.MediatorFaultDomain.Value),
		// Defensive copy: the orchestrator may retain this slice across goroutine
		// boundaries (Temporal activity inputs), so aliasing req.NsgIds would
		// risk a downstream read racing with the HTTP request lifecycle. Matches
		// the same convention used by UpdatePool below.
		NsgIds: append([]string(nil), req.NsgIds...),
	}

	if req.TieringConfig.Set {
		var secretVersion *int64
		if req.TieringConfig.Value.SecretVersion.Set {
			v := req.TieringConfig.Value.SecretVersion.Value
			secretVersion = &v
		}
		createPoolParams.FabricPoolConfig = &commonparams.FabricPoolConfig{
			BucketName:    req.TieringConfig.Value.BucketName,
			SecretOcid:    req.TieringConfig.Value.SecretId,
			SecretVersion: secretVersion,
			Namespace:     req.TieringConfig.Value.Namespace,
			ServerURL:     req.TieringConfig.Value.ServerName,
		}
	}

	if req.SecurityAttributes.Set {
		createPoolParams.SecurityAttributes = toCustomerSecurityAttributes(req.SecurityAttributes.Value)
	}

	createPoolParams.WorkflowID = opcWorkRequestID

	_, workflowID, err := h.Orchestrator.CreatePool(ctx, createPoolParams)
	if err != nil {
		if utilserrors.IsUserInputValidationErr(err) {
			return newCreatePoolBadRequest(opcWorkRequestID, req.PoolOCID, err.Error()), nil
		}
		if utilserrors.IsConflictErr(err) {
			msg := err.Error()
			if unwrapped := errors.Unwrap(err); unwrapped != nil {
				msg = unwrapped.Error()
			}
			return &ociserver.CreatePoolConflict{
				OpcWorkRequestID: opcWorkRequestID,
				Response: ociserver.PoolOperationErrorResponse{
					Status:       string(workflowquery.WorkflowStatusFailed),
					PoolOCID:     req.PoolOCID,
					ErrorMessage: msg,
				},
			}, nil
		}
		logger.Error("CreatePool orchestrator error", "error", err)
		return &ociserver.CreatePoolInternalServerError{
			OpcWorkRequestID: opcWorkRequestID,
			Response: ociserver.PoolOperationErrorResponse{
				Status:       string(workflowquery.WorkflowStatusFailed),
				PoolOCID:     req.PoolOCID,
				ErrorMessage: "Internal server error",
			},
		}, nil
	}
	return newCreatePoolAccepted(opcWorkRequestID, req.PoolOCID, workflowID, workflowquery.WorkflowStatusInProgress), nil
}

func newCreatePoolAccepted(opcWorkRequestID, poolOCID, workflowID string, status workflowquery.WorkflowStatus) *ociserver.CreatePoolAcceptedResponseHeaders {
	return &ociserver.CreatePoolAcceptedResponseHeaders{
		OpcWorkRequestID: opcWorkRequestID,
		Response: ociserver.CreatePoolAcceptedResponse{
			Status:     string(status),
			WorkflowId: workflowID,
			PoolOCID:   poolOCID,
		},
	}
}

func (h *Handler) GetWorkflow(ctx context.Context, params ociserver.GetWorkflowParams) (ociserver.GetWorkflowRes, error) {
	opcWorkRequestID, ctxErr := opcWorkRequestIDFromContext(ctx)
	if ctxErr != nil || opcWorkRequestID == "" {
		opcWorkRequestID = strings.TrimSpace(params.WorkRequestId)
	}

	res, err := workflowQueryFn(ctx, h.TemporalClient, params.WorkRequestId, "")
	if err != nil {
		return mapGetWorkflowQueryError(opcWorkRequestID, err), nil
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
		nodeUUIDsByName, pool, err := h.poolEnrichment(ctx, res.PoolMetadata.PoolUUID)
		if err != nil {
			return mapGetWorkflowQueryError(opcWorkRequestID, err), nil
		}
		var poolCreds *datamodel.PoolCredentials
		var poolAttrs *datamodel.PoolAttributes
		if pool != nil {
			poolCreds = pool.PoolCredentials
			poolAttrs = pool.PoolAttributes
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
		var throughputGBps float64
		if poolAttrs != nil && poolAttrs.ThroughputMibps > 0 {
			throughputGBps = workflowquery.MBpsToGBps(poolAttrs.ThroughputMibps)
		}
		poolMeta := ociserver.OCICreatePoolWorkflowMetadata{
			PoolOCID:       res.PoolMetadata.PoolOCID,
			Vms:            vms,
			Iops:           res.PoolMetadata.IOPS,
			ThroughputGBps: roundTo2Decimals(throughputGBps),
			Credentials:    buildWorkflowCredentialsResponse(res.PoolMetadata.Credentials),
		}
		overlayLiveCredentialVersions(&poolMeta.Credentials, poolCreds)
		applyServiceCertificateRef(&poolMeta.Credentials, poolCreds)
		if res.PoolMetadata.ResourceName != "" {
			poolMeta.ResourceName = ociserver.NewOptString(res.PoolMetadata.ResourceName)
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
		applyPoolProviderConfig(&poolMeta, res.PoolMetadata)
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
		OpcWorkRequestID: opcWorkRequestID,
		Response:         resp,
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

func applyPoolProviderConfig(poolMeta *ociserver.OCICreatePoolWorkflowMetadata, meta *workflowquery.OCICreatePoolMetadata) {
	if poolMeta == nil || meta == nil {
		return
	}
	if meta.KmsID != "" {
		poolMeta.KmsKeyId = ociserver.NewOptString(meta.KmsID)
	}
	if len(meta.NsgIDs) > 0 {
		poolMeta.NsgIds = append([]string(nil), meta.NsgIDs...)
	}
	if len(meta.SecurityAttributes) > 0 {
		attrs := make(ociserver.SecurityAttributes, len(meta.SecurityAttributes))
		for ns, inner := range meta.SecurityAttributes {
			item := make(ociserver.SecurityAttributesItem, len(inner))
			for key, val := range inner {
				item[key] = ociserver.SecurityAttributeValue{
					Value: val.Value,
					Mode:  ociserver.SecurityAttributeValueMode(val.Mode),
				}
			}
			attrs[ns] = item
		}
		poolMeta.SecurityAttributes = ociserver.NewOptSecurityAttributes(attrs)
	}
	if fp := meta.FabricPoolConfig; fp != nil {
		tiering := ociserver.TieringConfig{
			SecretId:   fp.SecretOCID,
			Namespace:  fp.Namespace,
			BucketName: fp.BucketName,
			ServerName: fp.ServerURL,
		}
		if fp.SecretVersion != nil {
			tiering.SecretVersion = ociserver.NewOptInt64(*fp.SecretVersion)
		}
		poolMeta.TieringConfig = ociserver.NewOptTieringConfig(tiering)
	}
}

type nodeUUIDs struct {
	vcp   string
	ontap string
}

// poolEnrichment fetches the pool and its nodes (keyed by name for VM
// enrichment) in a single orchestrator/DB lookup. The returned pool carries the
// live credentials (certificate/secret versions kept current by background
// rotation, used to override the stale creation-time versions recorded in
// immutable Temporal history) and the pool attributes (e.g. the API-supplied
// throughput). A missing pool yields empty results (nil map, nil pool) so
// enrichment is best-effort and never blocks the status response.
func (h *Handler) poolEnrichment(ctx context.Context, poolUUID string) (map[string]nodeUUIDs, *datamodel.Pool, error) {
	if poolUUID == "" {
		return nil, nil, nil
	}
	pool, nodes, err := h.Orchestrator.GetPoolWithNodes(ctx, poolUUID)
	if err != nil {
		return nil, nil, fmt.Errorf("pool enrichment failed for pool %q: %w", poolUUID, err)
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
	return uuidsByName, pool, nil
}

// overlayLiveCredentialVersions replaces the credential versions in the
// workflow-history-derived block with the live versions persisted on the pool
// record. Temporal history is immutable and captures the version at pool-creation
// time, so after a certificate rotation the history version goes stale; the
// pool's PoolCredentials.External{Certificate,Secret}.Version is updated on every
// rotation and is the authoritative current version. OCIDs are stable across
// rotations and are left as recorded in history — versions are only overridden
// when history surfaced a credential reference (non-empty OCID) and the DB holds
// a concrete (>0) version.
func overlayLiveCredentialVersions(creds *ociserver.OCICreatePoolWorkflowCredentials, poolCreds *datamodel.PoolCredentials) {
	if creds == nil || poolCreds == nil {
		return
	}
	if ref := poolCreds.ExternalCertificate; ref != nil && ref.Version > 0 && creds.Certificate.Ocid != "" {
		creds.Certificate.Version = strconv.FormatInt(ref.Version, 10)
	}
	if ref := poolCreds.ExternalSecret; ref != nil && ref.Version > 0 && creds.Secret.Ocid != "" {
		creds.Secret.Version = strconv.FormatInt(ref.Version, 10)
	}
}

// applyServiceCertificateRef surfaces the dedicated read-only service
// certificate reference on the credentials response. Unlike secret/certificate,
// this reference is not captured in immutable Temporal history (it is minted by
// a separate activity), so it is sourced entirely from the live pool record.
// The optional field is emitted only when both an OCID and a concrete (>0)
// version are present so pools without a complete service-cert ref keep the
// field absent and avoid an empty version string.
func applyServiceCertificateRef(creds *ociserver.OCICreatePoolWorkflowCredentials, poolCreds *datamodel.PoolCredentials) {
	if creds == nil || poolCreds == nil {
		return
	}
	ref := poolCreds.ExternalServiceCertificate
	if ref == nil || ref.ExternalIdentifier == "" || ref.Version <= 0 {
		return
	}
	creds.ServiceCertificate = ociserver.NewOptOCIOCIDVersionRef(ociserver.OCIOCIDVersionRef{
		Ocid:    ref.ExternalIdentifier,
		Version: strconv.FormatInt(ref.Version, 10),
	})
}

func mapGetWorkflowQueryError(opcWorkRequestID string, err error) ociserver.GetWorkflowRes {
	code := statusFromError(err)
	if code == http.StatusNotFound {
		return &ociserver.GetWorkflowNotFound{
			OpcWorkRequestID: opcWorkRequestID,
			Response: ociserver.GetWorkflowErrorResponse{
				ErrorMessage: err.Error(),
			},
		}
	}
	return &ociserver.GetWorkflowInternalServerError{
		OpcWorkRequestID: opcWorkRequestID,
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

	opcWorkRequestID, err := opcWorkRequestIDFromContext(ctx)
	if err != nil {
		return &ociserver.DeletePoolBadRequest{
			OpcWorkRequestID: uuid.NewString(),
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
			OpcWorkRequestID: opcWorkRequestID,
			Response: ociserver.PoolOperationErrorResponse{
				Status:       string(workflowquery.WorkflowStatusFailed),
				PoolOCID:     params.PoolOCID,
				ErrorMessage: err.Error(),
			},
		}, nil
	}

	poolOCID := params.PoolOCID

	// Map to DeletePoolParams - for OCI, AccountName is compartment OCID and PoolName is deployment name
	deletePoolParams := &commonparams.DeletePoolParams{
		AccountName: params.TenancyOcid, // account name (compartment OCID)
		PoolOCID:    poolOCID,           // path parameter
		WorkflowID:  opcWorkRequestID,   // OCI: workflow id == opc-work-request-id for idempotency
	}

	// Delete the pool
	deleted, workflowID, err := h.Orchestrator.DeletePool(ctx, deletePoolParams)
	if err != nil {
		logger.Error("Failed to delete pool", "error", err.Error())
		if utilserrors.IsNotFoundErr(err) {
			logger.Info("Pool not found", "poolOCID", deletePoolParams.PoolOCID)
			return &ociserver.DeletePoolNotFound{
				OpcWorkRequestID: opcWorkRequestID,
				Response: ociserver.PoolOperationErrorResponse{
					Status:       string(workflowquery.WorkflowStatusFailed),
					PoolOCID:     poolOCID,
					ErrorMessage: err.Error(),
				},
			}, nil
		}
		if utilserrors.IsBadRequestErr(err) {
			return &ociserver.DeletePoolBadRequest{
				OpcWorkRequestID: opcWorkRequestID,
				Response: ociserver.PoolOperationErrorResponse{
					Status:       string(workflowquery.WorkflowStatusFailed),
					PoolOCID:     poolOCID,
					ErrorMessage: err.Error(),
				},
			}, nil
		}
		if utilserrors.IsConflictErr(err) {
			return &ociserver.DeletePoolConflict{
				OpcWorkRequestID: opcWorkRequestID,
				Response: ociserver.PoolOperationErrorResponse{
					Status:       string(workflowquery.WorkflowStatusFailed),
					PoolOCID:     poolOCID,
					ErrorMessage: fmt.Errorf("Error deleting pool : %w", err).Error(),
				},
			}, nil
		}
		logger.Error("Failed to delete pool", "error", err.Error())
		return &ociserver.DeletePoolInternalServerError{
			OpcWorkRequestID: opcWorkRequestID,
			Response: ociserver.PoolOperationErrorResponse{
				Status:       string(workflowquery.WorkflowStatusFailed),
				PoolOCID:     poolOCID,
				ErrorMessage: "Internal server error",
			},
		}, nil
	}
	if deleted.State == datamodel.LifeCycleStateDeleting || deleted.State == datamodel.LifeCycleStateCreating {
		return &ociserver.DeletePoolAcceptedResponseHeaders{
			OpcWorkRequestID: opcWorkRequestID,
			Response: ociserver.DeletePoolAcceptedResponse{
				Status:     string(workflowquery.WorkflowStatusInProgress),
				WorkflowId: workflowID,
				PoolOCID:   poolOCID,
			},
		}, nil
	}

	logger.Info("Pool deleted successfully", "poolOCID", deletePoolParams.PoolOCID)
	return &ociserver.DeletePoolNoContent{OpcWorkRequestID: opcWorkRequestID}, nil
}

func newUpdatePoolBadRequest(opcWorkRequestID, poolExternalIdentifier, errMsg string) *ociserver.UpdatePoolBadRequest {
	return (*ociserver.UpdatePoolBadRequest)(&ociserver.PoolOperationErrorResponseHeaders{
		OpcWorkRequestID: opcWorkRequestID,
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
		req.Iops.Set ||
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

	opcWorkRequestID, err := opcWorkRequestIDFromContext(ctx)
	if err != nil {
		logger.Error("missing opc-work-request-id in context", "error", err)
		return newUpdatePoolBadRequest(uuid.NewString(), poolExternalIdentifier, invalidOPCRequestID), nil
	}

	hasNodeCapacities := len(req.NodeCapacities) > 0
	hasDataEndpointCount := req.DataEndpointCount.Set

	if errMsg := validateUpdatePoolRequest(req, hasNodeCapacities, hasDataEndpointCount); errMsg != "" {
		logger.Error("UpdatePool request validation failed",
			"poolOCID", poolExternalIdentifier,
			"error", errMsg,
		)
		return newUpdatePoolBadRequest(opcWorkRequestID, poolExternalIdentifier, errMsg), nil
	}

	updateParams := &commonparams.UpdatePoolParams{
		AccountName:            params.TenancyOcid,
		PoolExternalIdentifier: poolExternalIdentifier,
	}

	if req.ThroughputGBps.Set {
		updateParams.TotalThroughputMibps = workflowquery.GBpsToMBps(req.ThroughputGBps.Value)
		updateParams.CustomPerformanceEnabled = true
	}
	if req.Iops.Set {
		iops := req.Iops.Value
		updateParams.TotalIops = &iops
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
			return newUpdatePoolBadRequest(opcWorkRequestID, poolExternalIdentifier, errMsgInvalidAdminPasswordVersion), nil
		}
		if version < 1 {
			logger.Error("invalid ociAdminPassword version", "version", version)
			return newUpdatePoolBadRequest(opcWorkRequestID, poolExternalIdentifier, errMsgAdminPasswordVersionLessThanOne), nil
		}
		updateParams.OciAdminPassword = &commonparams.OciAdminPassword{
			Ocid:    req.OciAdminPassword.Value.Ocid,
			Version: version,
		}
	}
	if req.KmsKeyId.Set {
		kmsKeyId := strings.TrimSpace(req.KmsKeyId.Value)
		if kmsKeyId == "" {
			return newUpdatePoolBadRequest(opcWorkRequestID, poolExternalIdentifier, errMsgEmptyKmsKeyId), nil
		}
		if !isValidOCID(kmsKeyId) {
			return newUpdatePoolBadRequest(opcWorkRequestID, poolExternalIdentifier, errMsgInvalidKmsKeyId), nil
		}
		updateParams.KmsKeyId = kmsKeyId
	}
	if req.NsgIds != nil {
		nsgIds := make([]string, len(req.NsgIds))
		for i, nsgID := range req.NsgIds {
			nsgID = strings.TrimSpace(nsgID)
			if nsgID == "" {
				return newUpdatePoolBadRequest(opcWorkRequestID, poolExternalIdentifier, fmt.Sprintf(errMsgEmptyNsgID, i)), nil
			}
			if !isValidOCID(nsgID) {
				return newUpdatePoolBadRequest(opcWorkRequestID, poolExternalIdentifier, fmt.Sprintf(errMsgInvalidNsgID, i)), nil
			}
			nsgIds[i] = nsgID
		}
		updateParams.NsgIds = nsgIds
	}
	if req.SecurityAttributes.Set {
		updateParams.SecurityAttributes = toUpdateCustomerSecurityAttributes(req.SecurityAttributes.Value)
	}

	updateParams.WorkflowID = opcWorkRequestID

	_, workflowID, err := h.Orchestrator.UpdatePool(ctx, updateParams)
	if err != nil {
		if utilserrors.IsUserInputValidationErr(err) || utilserrors.IsBadRequestErr(err) {
			return newUpdatePoolBadRequest(opcWorkRequestID, poolExternalIdentifier, err.Error()), nil
		}
		if utilserrors.IsNotFoundErr(err) {
			return (*ociserver.UpdatePoolNotFound)(&ociserver.PoolOperationErrorResponseHeaders{
				OpcWorkRequestID: opcWorkRequestID,
				Response: ociserver.PoolOperationErrorResponse{
					Status:       string(workflowquery.WorkflowStatusFailed),
					PoolOCID:     poolExternalIdentifier,
					ErrorMessage: err.Error(),
				},
			}), nil
		}
		if utilserrors.IsConflictErr(err) {
			return (*ociserver.UpdatePoolConflict)(&ociserver.PoolOperationErrorResponseHeaders{
				OpcWorkRequestID: opcWorkRequestID,
				Response: ociserver.PoolOperationErrorResponse{
					Status:       string(workflowquery.WorkflowStatusFailed),
					PoolOCID:     poolExternalIdentifier,
					ErrorMessage: err.Error(),
				},
			}), nil
		}
		logger.Error("UpdatePool orchestrator error", "error", err)
		return (*ociserver.UpdatePoolInternalServerError)(&ociserver.PoolOperationErrorResponseHeaders{
			OpcWorkRequestID: opcWorkRequestID,
			Response: ociserver.PoolOperationErrorResponse{
				Status:       string(workflowquery.WorkflowStatusFailed),
				PoolOCID:     poolExternalIdentifier,
				ErrorMessage: "Internal server error",
			},
		}), nil
	}

	return newUpdatePoolAccepted(opcWorkRequestID, poolExternalIdentifier, workflowID, workflowquery.WorkflowStatusInProgress), nil
}

func newUpdatePoolAccepted(opcWorkRequestID, poolOCID, workflowID string, status workflowquery.WorkflowStatus) *ociserver.UpdatePoolAcceptedResponseHeaders {
	return &ociserver.UpdatePoolAcceptedResponseHeaders{
		OpcWorkRequestID: opcWorkRequestID,
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

// optInt64ToPtr converts an OptInt64 to a *int64 pointer.
// Returns nil when the optional value is not set.
func optInt64ToPtr(v ociserver.OptInt64) *int64 {
	if !v.Set {
		return nil
	}
	return &v.Value
}

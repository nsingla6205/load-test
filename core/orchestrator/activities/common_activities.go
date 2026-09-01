package activities

import (
	"context"
	errors2 "errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	googleproxyclient "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/google-proxy-client"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	hyperscaler2 "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler"
	hyperscaler_models "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/models"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	vcputils "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/auth"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/worker/metrics"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/client"
)

const (
	VSASubnetPrefix   = "vsa-"
	VSALVSubnetPrefix = "vsa-lv-"

	// OnPremPeerRoleName is the name of the role used for external cluster peers
	// in both FlexCache and hybrid replication scenarios.
	OnPremPeerRoleName = "external-peer"
	AccessNone         = "none"
	AccessReadOnly     = "readonly"
	DefaultPath        = "DEFAULT"
)

type CommonActivities struct {
	SE database.Storage
}

type UpdateSvmActiveDirectoryParams struct {
	Svm                 *datamodel.Svm
	ActiveDirectoryUUID string
}

type CreateFirewallRuleParams struct {
	Project          string
	Network          string
	FirewallRuleName string
}

type EnsureSmbFirewallParams struct {
	Project string
	Network string
}

const (
	SmbFirewallName            = "smb-ingress"
	ILBHealthCheckFirewallName = "ilb-health-check"
)

var (
	smbFirewallSourceRanges                = splitAndTrim(DataFirewallSourceRanges)
	smbFirewallAllowedPortRules            = splitAndTrim(SmbFirewallAllowedPortRulesConfig)
	ilbHealthCheckFirewallSourceRanges     = splitAndTrim(IlbHealthCheckFirewallSourceRangesConfig)
	ilbHealthCheckFirewallAllowedPortRules = splitAndTrim(IlbHealthCheckFirewallAllowedPortRulesConfig)
)

var defaultNoneRolePrivilege = []*vsa.RolePrivilege{
	{Path: DefaultPath, Access: AccessNone},
	{Path: "debug", Access: AccessNone},
}

func splitAndTrim(csv string) []string {
	if csv == "" {
		return []string{}
	}

	parts := strings.Split(csv, ",")
	trimmed := make([]string, 0, len(parts))
	for _, part := range parts {
		value := strings.TrimSpace(part)
		if value == "" {
			continue
		}
		trimmed = append(trimmed, value)
	}
	return trimmed
}

var (
	MakeSubnetName         = _makeSubnetName
	isSubnetReusable       = _isSubnetReusable
	findEmptySubnet        = _findEmptySubnet
	getPoolsBySubnetwork   = _getPoolsBySubnetwork
	getIPsInSubnet         = _getIPsInSubnet
	getSignedJwtToken      = auth.GetSignedJwtToken
	GetCloudService        = _getCloudService
	GetPoolTenantProject   = _getPoolTenantProject
	GetBackupTenantProject = _getBackupTenantProject
)

func (ca CommonActivities) CreateJob(ctx context.Context, job *datamodel.Job) (*datamodel.Job, error) {
	logger := util.GetLogger(ctx)
	se := ca.SE
	logger.Infof("creating job: %s with status: %s", job.UUID, job.State)
	recordHeartbeat(ctx, "Creating job in database")
	return se.CreateJob(ctx, job)
}

// UpdateJobStatus updates the status of a job in the database.
func (ca CommonActivities) UpdateJobStatus(ctx context.Context, job *datamodel.Job) error {
	logger := util.GetLogger(ctx)
	se := ca.SE
	logger.Infof("updating job: %s with status: %s", job.UUID, job.State)
	// Emit Prometheus metric
	metrics.IncJobStatusCounter(ctx, job.ErrorDetails, job.State)
	recordHeartbeat(ctx, fmt.Sprintf("Updating job %s in database", job.UUID))
	return se.UpdateJob(ctx, job.UUID, job.State, job.TrackingID, job.ErrorDetails)
}

func (ca CommonActivities) GetJob(ctx context.Context, jobUUID string) (*datamodel.Job, error) {
	se := ca.SE
	recordHeartbeat(ctx, fmt.Sprintf("Fetching job %s from database", jobUUID))
	job, err := se.GetJob(ctx, jobUUID)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	if job == nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDescribingJobNotFound, fmt.Errorf("job with UUID %s not found", jobUUID))
	}
	return job, nil
}

// DescribeJob gives the status of a job in the database.
func DescribeJob(ctx context.Context, jobId, basepath, jwtToken, projectNumber, location, correlationId *string) error {
	if jobId == nil {
		return nil
	}
	recordHeartbeat(ctx, fmt.Sprintf("Describing job %s through Google proxy", *jobId))
	logger := util.GetLogger(ctx)
	googleProxyClient := googleproxyclient.GetGProxyClient(*basepath, *jwtToken, logger)

	describeOperationParams := googleproxyclient.V1betaInternalDescribeOperationParams{
		OperationId:    *jobId,
		ProjectNumber:  *projectNumber,
		LocationId:     *location,
		XCorrelationID: googleproxyclient.NewOptString(*correlationId),
	}

	res, err := googleProxyClient.Invoker.V1betaInternalDescribeOperation(ctx, describeOperationParams)
	if err != nil {
		if strings.Contains(err.Error(), "unexpected Content-Type") {
			describeOperationParams := googleproxyclient.V1betaDescribeOperationParams{
				OperationId:    *jobId,
				ProjectNumber:  *projectNumber,
				LocationId:     *location,
				XCorrelationID: googleproxyclient.NewOptString(*correlationId),
			}

			recordHeartbeat(ctx, fmt.Sprintf("Retrying job %s description through public Google proxy", *jobId))
			res, err := googleProxyClient.Invoker.V1betaDescribeOperation(ctx, describeOperationParams)
			if err != nil {
				return vsaerrors.NewVCPError(vsaerrors.ErrDescribingJobAPI, err)
			}
			operation, ok := res.(*googleproxyclient.OperationV1beta)
			if ok {
				if operation.Done.Value {
					if operation.Error.IsSet() {
						logger.Errorf("Job with operation id: %s failed", describeOperationParams.OperationId)
						return vsaerrors.WrapAsNonRetryableTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrJobFailed, errors2.New("job failed with error: "+operation.Error.Value.Message.Value)))
					}
					return nil
				}
			}
			return vsaerrors.NewVCPError(vsaerrors.ErrJobNotFinished, errors.New("job not finished"))
		} else {
			return vsaerrors.NewVCPError(vsaerrors.ErrDescribingJobAPI, err)
		}
	}

	operation, ok := res.(*googleproxyclient.InternalOperationV1beta)
	if ok {
		if operation.Done.Value {
			if operation.Error.IsSet() {
				logger.Errorf("Job with operation id: %s failed", describeOperationParams.OperationId)
				return vsaerrors.WrapAsNonRetryableTemporalApplicationError(vsaerrors.NewVCPError(operation.TrackingId.Value, errors2.New("job failed with error: "+operation.Error.Value.Message.Value)))
			}
			return nil
		}
	}
	return vsaerrors.NewVCPError(vsaerrors.ErrJobNotFinished, errors.New("job not finished"))
}

// GetSVM retrieves the SVM associated with the given pool ID.
func (ca CommonActivities) GetSVM(ctx context.Context, poolID int64) (*datamodel.Svm, error) {
	se := ca.SE
	recordHeartbeat(ctx, "Starting GetSVM activity")

	svm, err := se.GetSvmForPoolID(ctx, poolID)
	if err != nil || svm == nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	recordHeartbeat(ctx, "Finished GetSVM activity")
	return svm, nil
}

// GetPoolBySvmPoolId retrieves the Pool associated with the given pool ID from SVM.
func (ca CommonActivities) GetPoolBySvmPoolId(ctx context.Context, poolID int64) (*datamodel.Pool, error) {
	se := ca.SE

	recordHeartbeat(ctx, fmt.Sprintf("Fetching pool %d from database", poolID))
	pool, err := se.GetPoolByID(ctx, poolID)
	if err != nil || pool == nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	return pool, nil
}

func (ca CommonActivities) UpdateSvmActiveDirectory(ctx context.Context, params UpdateSvmActiveDirectoryParams) (*datamodel.Svm, error) {
	logger := util.GetLogger(ctx)

	if params.Svm == nil {
		logger.Error("SVM not provided for UpdateSvmActiveDirectory activity")
		return nil, vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("svm is nil"))
	}

	if params.Svm.ActiveDirectoryID.Valid {
		logger.Debugf("SVM %s already associated with Active Directory, skipping update", params.Svm.UUID)
		return params.Svm, nil
	}

	if params.ActiveDirectoryUUID == "" {
		logger.Error("Active Directory UUID is empty for SVM update", "svmUUID", params.Svm.UUID)
		return nil, vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("active directory uuid is empty"))
	}

	recordHeartbeat(ctx, fmt.Sprintf("Fetching active directory %s from database", params.ActiveDirectoryUUID))
	ad, err := ca.SE.GetActiveDirectoryByUUID(ctx, params.ActiveDirectoryUUID)
	if err != nil {
		logger.Error("Failed to fetch Active Directory while updating SVM", "svmUUID", params.Svm.UUID, "adUUID", params.ActiveDirectoryUUID, "error", err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	if ad == nil {
		err := fmt.Errorf("active directory %s not found", params.ActiveDirectoryUUID)
		logger.Error("Active Directory not found while updating SVM", "svmUUID", params.Svm.UUID, "adUUID", params.ActiveDirectoryUUID)
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	recordHeartbeat(ctx, fmt.Sprintf("Updating SVM %s with active directory in database", params.Svm.UUID))
	updatedSvm, err := ca.SE.UpdateSvmActiveDirectoryID(ctx, params.Svm, ad.ID)
	if err != nil {
		logger.Error("Failed to update SVM with Active Directory", "svmUUID", params.Svm.UUID, "adUUID", params.ActiveDirectoryUUID, "error", err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	updatedSvm.ActiveDirectory = ad

	return updatedSvm, nil
}

func (ca CommonActivities) UnsetSvmActiveDirectory(ctx context.Context, svm *datamodel.Svm) (*datamodel.Svm, error) {
	logger := util.GetLogger(ctx)
	recordHeartbeat(ctx, "Starting UnsetSvmActiveDirectory activity")

	if svm == nil {
		logger.Error("SVM not provided for UnsetSvmActiveDirectory activity")
		return nil, vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("svm is nil"))
	}

	updatedSvm, err := ca.SE.UnsetSvmActiveDirectoryID(ctx, svm)
	if err != nil {
		logger.Error("Failed to unset SVM Active Directory", "svmUUID", svm.UUID, "error", err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	recordHeartbeat(ctx, "Finished UnsetSvmActiveDirectory activity")
	return updatedSvm, nil
}

func (ca CommonActivities) CreateFirewallRule(ctx context.Context, params CreateFirewallRuleParams) error {
	logger := util.GetLogger(ctx)

	if params.FirewallRuleName == "" {
		err := fmt.Errorf("firewall rule name is empty")
		logger.Error("Firewall rule name has not provided", "error", err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	if params.Project == "" {
		err := fmt.Errorf("%s firewall project is empty", params.FirewallRuleName)
		logger.Error("project name has not provided", "error", err, "firewall-rule-name", params.FirewallRuleName)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	if params.Network == "" {
		err := fmt.Errorf("%s firewall network is empty", params.FirewallRuleName)
		logger.Error("firewall network not provided", "project", params.Project, "error", err, "firewall-rule-name", params.FirewallRuleName)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	recordHeartbeat(ctx, fmt.Sprintf("Resolving cloud service for firewall %s", params.FirewallRuleName))
	cloudService, err := hyperscaler2.ResolveCloudService(ctx)
	if err != nil {
		logger.Error("Failed to initialise cloud service for firewall", "project", params.Project, "network", params.Network, "firewall-rule-name", params.FirewallRuleName, "error", err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	firewallSourceRanges := make([]string, 0)
	firewallAllowedPort := make([]string, 0)
	switch params.FirewallRuleName {
	case SmbFirewallName:
		firewallSourceRanges = smbFirewallSourceRanges
		firewallAllowedPort = smbFirewallAllowedPortRules
	case ILBHealthCheckFirewallName:
		firewallSourceRanges = ilbHealthCheckFirewallSourceRanges
		firewallAllowedPort = ilbHealthCheckFirewallAllowedPortRules
	}

	recordHeartbeat(ctx, fmt.Sprintf("Creating firewall %s in project %s", params.FirewallRuleName, params.Project))
	op, err := InsertFirewall(cloudService, params.Project, params.FirewallRuleName, params.Network, FirewallPriority, IngressTrafficDirection, firewallSourceRanges, firewallAllowedPort)
	if err != nil {
		logger.Error("Failed to create firewall", "project", params.Project, "network", params.Network, "firewall-rule-name", params.FirewallRuleName, "error", err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	if op != "" {
		logger.Info("Triggered firewall create operation", "project", params.Project, "network", params.Network, "operation", op, "firewall-rule-name", params.FirewallRuleName)
	} else {
		logger.Debug("Firewall already present", "project", params.Project, "network", params.Network, "firewall-rule-name", params.FirewallRuleName)
	}

	return nil
}

func (ca CommonActivities) EnsureSmbIngressFirewall(ctx context.Context, params EnsureSmbFirewallParams) error {
	logger := util.GetLogger(ctx)

	if params.Project == "" {
		err := fmt.Errorf("smb firewall project is empty")
		logger.Error("SMB firewall project not provided", "error", err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	if params.Network == "" {
		err := fmt.Errorf("smb firewall network is empty")
		logger.Error("SMB firewall network not provided", "project", params.Project, "error", err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	recordHeartbeat(ctx, fmt.Sprintf("Resolving cloud service for SMB firewall in project %s", params.Project))
	cloudService, err := hyperscaler2.ResolveCloudService(ctx)
	if err != nil {
		logger.Error("Failed to initialise cloud service for SMB firewall", "project", params.Project, "network", params.Network, "error", err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	recordHeartbeat(ctx, fmt.Sprintf("Creating SMB ingress firewall in project %s", params.Project))
	op, err := InsertFirewall(cloudService, params.Project, SmbFirewallName, params.Network, FirewallPriority, IngressTrafficDirection, smbFirewallSourceRanges, smbFirewallAllowedPortRules)
	if err != nil {
		logger.Error("Failed to create SMB firewall", "project", params.Project, "network", params.Network, "error", err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	if op != "" {
		logger.Info("Triggered SMB firewall create operation", "project", params.Project, "network", params.Network, "operation", op)
	} else {
		logger.Debug("SMB firewall already present", "project", params.Project, "network", params.Network, "firewall", SmbFirewallName)
	}

	return nil
}

func (ca CommonActivities) ILBHealthCheckFirewall(ctx context.Context, params EnsureSmbFirewallParams) error {
	logger := util.GetLogger(ctx)

	if params.Project == "" {
		err := fmt.Errorf("ILBHealthCheck firewall project is empty")
		logger.Error("ILBHealthCheck project not provided", "error", err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	if params.Network == "" {
		err := fmt.Errorf("ILBHealthCheck firewall network is empty")
		logger.Error("ILBHealthCheck firewall network not provided", "project", params.Project, "error", err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	recordHeartbeat(ctx, fmt.Sprintf("Resolving cloud service for ILB health check firewall in project %s", params.Project))
	cloudService, err := hyperscaler2.ResolveCloudService(ctx)
	if err != nil {
		logger.Error("Failed to initialise cloud service for ILBHealthCheck firewall", "project", params.Project, "network", params.Network, "error", err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	recordHeartbeat(ctx, fmt.Sprintf("Creating ILB health check firewall in project %s", params.Project))
	op, err := InsertFirewall(cloudService, params.Project, ILBHealthCheckFirewallName, params.Network, FirewallPriority, IngressTrafficDirection, ilbHealthCheckFirewallSourceRanges, ilbHealthCheckFirewallAllowedPortRules)
	if err != nil {
		logger.Error("Failed to create ILBHealthCheck firewall", "project", params.Project, "network", params.Network, "error", err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	if op != "" {
		logger.Info("Triggered ILBHealthCheck firewall create operation", "project", params.Project, "network", params.Network, "operation", op)
	} else {
		logger.Debug("ILBHealthCheck firewall already present", "project", params.Project, "network", params.Network, "firewall", ILBHealthCheckFirewallName)
	}

	return nil
}

// GetNode retrieves the node associated with the given pool ID.
func (ca CommonActivities) GetNode(ctx context.Context, poolId int64) ([]*datamodel.Node, error) {
	se := ca.SE
	recordHeartbeat(ctx, "Fetching nodes for pool")

	nodes, err := se.GetNodesByPoolID(ctx, poolId)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	if len(nodes) == 0 {
		return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrUnexpectedNodeCountForPool, errors.New("Node not found for the pool")))
	}
	recordHeartbeat(ctx, "Nodes fetched successfully")

	return nodes, nil
}

// ResolveOntapNodeForPool returns a models.Node that can talk to ONTAP for the pool.
//
// PO / extreme ONTAP-mode pools are backed by a hardware host (pool.HostUUID) and do
// not have VSA nodes or PoolCredentials; those resolve via the hosts table.
// FLEX / VSA pools continue to use pool nodes + PoolCredentials.
func (ca CommonActivities) ResolveOntapNodeForPool(ctx context.Context, pool *datamodel.Pool) (*models.Node, error) {
	if pool == nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, errors2.New("pool is required")))
	}
	recordHeartbeat(ctx, fmt.Sprintf("Resolving ONTAP node for pool %s", pool.UUID))

	if strings.TrimSpace(pool.HostUUID) != "" && pool.IsDedicatedCapacity() {
		host, err := ca.SE.GetHost(ctx, pool.HostUUID)
		if err != nil {
			return nil, vsaerrors.WrapAsTemporalApplicationError(err)
		}
		node, err := vsa.CreateNodeForHost(host)
		if err != nil {
			return nil, vsaerrors.WrapAsTemporalApplicationError(err)
		}
		recordHeartbeat(ctx, fmt.Sprintf("Resolved ONTAP host %s for pool %s", pool.HostUUID, pool.UUID))
		return node, nil
	}

	dbNodes, err := ca.GetNode(ctx, pool.ID)
	if err != nil {
		return nil, err
	}
	if pool.PoolCredentials == nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrResourceNotFound,
				fmt.Errorf("pool credentials not found for pool %s", pool.UUID)))
	}
	return vsa.CreateNodeForProvider(vsa.NodeProviderInput{
		Nodes:            dbNodes,
		DeploymentName:   pool.DeploymentName,
		OntapCredentials: pool.PoolCredentials,
	}), nil
}

func (j CommonActivities) GetOntapJob(ctx context.Context, jobUUID string, node *models.Node) (*vsa.OntapJob, error) {
	recordHeartbeat(ctx, "Fetching ONTAP job")
	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	job, err := provider.JobGet(jobUUID)
	if err != nil {
		return nil, err
	}
	recordHeartbeat(ctx, "ONTAP job fetched successfully")
	return job, nil
}

func (j CommonActivities) GetAuthJWTToken(ctx context.Context, accountName string) (string, error) {
	logger := util.GetLogger(ctx)
	recordHeartbeat(ctx, fmt.Sprintf("Generating authentication token for account %s", accountName))
	jwtToken, err := getSignedJwtToken(accountName)
	if err != nil {
		logger.Errorf("failed to get token for account %s: %v", accountName, err)
		return "", errors.New("failed to get token")
	}
	return jwtToken, nil
}

func (j CommonActivities) ListPoolsUUID(ctx context.Context, states []string) ([]*database.PoolIdentifier, error) {
	recordHeartbeat(ctx, "Initializing pool UUID listing")
	logger := util.GetLogger(ctx)
	se := j.SE

	filter := utils.CreateFilterWithConditions(utils.NewFilterCondition("state", "IN", states))
	pools, err := se.ListPoolUUIDs(ctx, filter)
	recordHeartbeat(ctx, "Listed pool UUIDs from database")
	if err != nil {
		logger.Errorf("Failed to list pools: %v", err)
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}

	logger.Infof("Found %d pools", len(pools))
	return pools, nil
}

// _getCloudService resolves the active hyperscaler's Services implementation.
func _getCloudService(ctx context.Context) (hyperscaler2.Services, error) {
	cloudService, err := hyperscaler2.ResolveCloudService(ctx)
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrGCPClientInitializationError, errors.New("initialisation of cloud service failed"))
	}
	return cloudService, nil
}

// GenerateVSASignedURLActivity generates a signed URL for VSA image in an activity
func (ca CommonActivities) GenerateVSASignedURLActivity(ctx context.Context, vsaImagePath string) (string, error) {
	logger := util.GetLogger(ctx)

	// Get cloud service
	recordHeartbeat(ctx, "Resolving cloud service for VSA signed URL")
	cloudService, err := hyperscaler2.ResolveCloudService(ctx)
	if err != nil {
		logger.Error("Failed to initialize cloud service for signed URL generation", "error", err)
		return "", vsaerrors.NewVCPError(vsaerrors.ErrGCPClientInitializationError, err)
	}

	// Generate signed URL
	recordHeartbeat(ctx, "Generating VSA signed URL")
	signedURL, err := cloudService.GenerateVSASignedURL(ctx, vsaImagePath)
	if err != nil {
		logger.Error("Failed to generate signed URL for VSA image", "vsaImagePath", vsaImagePath, "error", err)
		return "", vsaerrors.NewVCPError(vsaerrors.ErrGCPClientInitializationError, err)
	}

	logger.Info("Successfully generated signed URL for VSA image", "vsaImagePath", vsaImagePath)
	return signedURL, nil
}

// GenerateVSAOCIPARActivity generates a PAR URL for the VSA image.
// vsaImagePath format: "/n/{namespace}/b/{bucket}/o/{objectName}"
func (ca CommonActivities) GenerateVSAOCIPARActivity(ctx context.Context, vsaImagePath string) (string, error) {
	logger := util.GetLogger(ctx)

	recordHeartbeat(ctx, "Resolving OCI service for VSA PAR")
	ociService, err := hyperscaler2.GetOCIService(ctx)
	if err != nil {
		logger.Error("Failed to initialize OCI services for PAR generation", "error", err)
		return "", vsaerrors.NewVCPError(vsaerrors.ErrOCIClientInitializationError, err)
	}

	recordHeartbeat(ctx, "Generating VSA OCI PAR")
	parURL, err := ociService.GenerateVSAPAR(ctx, vsaImagePath)
	if err != nil {
		logger.Error("Failed to generate OCI PAR for VSA image",
			"vsaImagePath", vsaImagePath, "error", err)
		errMsg := strings.ToLower(err.Error())
		if strings.Contains(errMsg, "bucketnotfound") || strings.Contains(errMsg, "bucket not found") {
			return "", vsaerrors.NewVCPError(vsaerrors.ErrOCIPARGenerationFailed, err)
		}
		return "", vsaerrors.NewVCPError(vsaerrors.ErrOCIResourceFetchError, err)
	}

	logger.Info("Successfully generated OCI PAR for VSA image", "vsaImagePath", vsaImagePath)
	return parURL, nil
}

// makeSubnetName generates a subnet name based on the project number, region and timestamp
func _makeSubnetName(projectNumber string, isLargeCapacity bool) string {
	timeNow := strconv.Itoa(int(time.Now().Unix()))
	if isLargeCapacity {
		return fmt.Sprintf("%s%s-%s", VSALVSubnetPrefix, projectNumber, timeNow)
	}
	return fmt.Sprintf("%s%s-%s", VSASubnetPrefix, projectNumber, timeNow)
}

// getSubnetToBeUsed examines existing subnets or identifies if existing subnet can be used for creation of new pool;
// also handles address range pinning when address space management is enabled.
func getSubnetToBeUsed(service hyperscaler2.Services, se database.Storage, customerProjectNumber, tenantProjectNumber, snHost, tenantProjectRegion string, isLargeCapacity bool, addressRanges []*datamodel.AddressRange) (*hyperscaler_models.Subnet, error) {
	logger := service.GetLogger()
	subnetsReceived, err := service.ListSubnetworks(snHost, tenantProjectRegion)
	if err != nil {
		logger.Errorf("Error listing subnetwork for tenant project: %s, SN host : %s, region %s. Error : %s", tenantProjectNumber, snHost, tenantProjectRegion, err.Error())
		return nil, err
	}
	if subnetsReceived == nil || len(*subnetsReceived) == 0 {
		logger.Infof("getSubnetToBeUsed: no subnets found in snHost=%s region=%s tenantProject=%s", snHost, tenantProjectRegion, tenantProjectNumber)
		return nil, nil
	}
	ctx := service.GetContext()
	recordHeartbeat(ctx, fmt.Sprintf("Fetching account %s for subnet selection", customerProjectNumber))
	account, err := se.GetAccount(ctx, customerProjectNumber)
	if err != nil {
		return nil, err
	}
	subnetPrefix := fmt.Sprintf("%s%s", VSASubnetPrefix, tenantProjectNumber)
	if isLargeCapacity {
		subnetPrefix = fmt.Sprintf("%s%s", VSALVSubnetPrefix, tenantProjectNumber)
	}
	logger.Infof("getSubnetToBeUsed: scanning %d subnet(s) in snHost=%s region=%s for prefix=%q largeCapacity=%v addressRangeCount=%d", len(*subnetsReceived), snHost, tenantProjectRegion, subnetPrefix, isLargeCapacity, len(addressRanges))
	var allPoolsInDeleting bool
	for _, subnet := range *subnetsReceived {
		if strings.HasPrefix(subnet.Name, subnetPrefix) {
			recordHeartbeat(ctx, fmt.Sprintf("Evaluating subnet %s for reuse", subnet.Name))
			// Address range pinning: skip subnets not carved from a registered range (address space mgmt only).
			if len(addressRanges) > 0 && !vcputils.SubnetCIDRInAnyRange(subnet.IpCidrRange, addressRanges) {
				logger.Infof("getSubnetToBeUsed: skipping subnet %s (CIDR %q not in any registered address range)", subnet.Name, subnet.IpCidrRange)
				continue
			}

			pools, err := getPoolsBySubnetwork(ctx, se, strconv.Itoa(int(account.ID)), subnet.Name, "")
			if err != nil {
				logger.Errorf("Error checking pools for subnet: %s, Error: %s", subnet.Name, err.Error())
				return nil, err
			}
			// if all pools are in deleting state, don't consider the subnet for reusing.
			allPoolsInDeleting = false
			// If subnet already has pools associated with it, skip this subnet for large capacity pools
			if len(pools) > 0 {
				// For large capacity pools, check if subnet already has a pool associated with it
				if isLargeCapacity {
					logger.Infof("getSubnetToBeUsed: skipping subnet %s — large capacity pool requires a dedicated subnet (has %d pool(s))", subnet.Name, len(pools))
					continue
				}
				allPoolsInDeleting = allPoolsDeleting(pools)
				if allPoolsInDeleting {
					logger.Infof("getSubnetToBeUsed: skipping subnet %s — all %d associated pool(s) are in DELETING state", subnet.Name, len(pools))
				}
			}

			// get number of free IPs in the subnet
			reuseSubnet, err := isSubnetReusable(ctx, se, subnet, strconv.Itoa(int(account.ID)), "")
			if err != nil {
				logger.Errorf("Error finding empty IP's subnet: tenant project: %s, SN host : %s, region %s. Error : %s", tenantProjectNumber, snHost, tenantProjectRegion, err.Error())
				return nil, err
			}
			if reuseSubnet && !allPoolsInDeleting {
				logger.Infof("getSubnetToBeUsed: selected subnet=%s CIDR=%s for reuse — has capacity and is within a registered address range (or range filter is off), tenantProject=%s region=%s", subnet.Name, subnet.IpCidrRange, tenantProjectNumber, tenantProjectRegion)
				return &subnet, nil
			}
			if !reuseSubnet {
				logger.Infof("getSubnetToBeUsed: skipping subnet %s (CIDR %s) — not enough free IPs for a new HA pair", subnet.Name, subnet.IpCidrRange)
			}
		}
	}
	// either no subnet was found or no subnet was found with enough free IPs sufficient to create a HA pair for pool
	logger.Infof("getSubnetToBeUsed: no reusable subnet found in snHost=%s region=%s tenantProject=%s — will create a new subnet", snHost, tenantProjectRegion, tenantProjectNumber)
	return nil, nil
}

func allPoolsDeleting(pools []*datamodel.PoolView) bool {
	for _, pool := range pools {
		if pool.State != datamodel.LifeCycleStateDeleting {
			return false
		}
	}
	return true
}

// shouldReleaseSharedDataSubnet reports whether a shared data subnet (and its carved
// intercluster ip_ranges row) may be released. Matches ReleaseDataSubnetOp gating.
func shouldReleaseSharedDataSubnet(pools []*datamodel.PoolView) bool {
	if len(pools) == 0 {
		return false
	}
	return len(pools) <= 1 || allPoolsDeleting(pools)
}

func _isSubnetReusable(ctx context.Context, se database.Storage, subnet hyperscaler_models.Subnet, accountId, poolNetwork string) (bool, error) {
	recordHeartbeat(ctx, fmt.Sprintf("Checking available addresses in subnet %s", subnet.Name))
	availableIPs, err := findEmptySubnet(ctx, se, subnet, accountId, "")
	if err != nil {
		return false, vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceFetchError, err)
	}
	if availableIPs >= totalIPPerHAPair {
		return true, nil
	}
	return false, nil
}

// _findEmptySubnet calculates the number of free IPs in the subnet
func _findEmptySubnet(ctx context.Context, se database.Storage, subnet hyperscaler_models.Subnet, accountId, poolNetwork string) (int, error) {
	// Check if the subnetwork has an IP range
	if subnet.IpCidrRange == "" {
		return 0, vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceFetchError, errors.New("subnetwork does not have an IP range"))
	}

	// Get the sum of all reserved IPs for this subnet
	recordHeartbeat(ctx, fmt.Sprintf("Fetching reserved addresses for subnet %s", subnet.Name))
	sumOfReservedIPs, err := _getSumOfReservedIPsForSubnet(ctx, se, accountId, subnet.Name, poolNetwork)
	if err != nil {
		return 0, vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceFetchError, err)
	}
	totalIPs, err := getIPsInSubnet(subnet.IpCidrRange)
	if err != nil {
		return 0, vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceFetchError, err)
	}

	// 4 IPs are reserved for the network, gateway, broadcast, and subnet address
	// Each pool reserves IPReserved from a given subnet
	freeIPs := totalIPs - 4 - int(sumOfReservedIPs)

	return freeIPs, nil
}

// _getPoolsBySubnetwork retrieves all pools that are using the specified subnetwork
func _getPoolsBySubnetwork(ctx context.Context, se database.Storage, accountID, subnetworkName, poolNetwork string) ([]*datamodel.PoolView, error) {
	filter := utils.CreateFilterWithConditions(
		utils.NewFilterCondition("account_id", "=", accountID),
		utils.NewFilterCondition("cluster_details->>'subnet_names'", "LIKE", "%"+subnetworkName+"%"),
	)
	if poolNetwork != "" {
		filter.Conditions = append(filter.Conditions, utils.NewFilterCondition("network", "=", poolNetwork))
	}
	filter.SetIncludeDeleted(false)

	recordHeartbeat(ctx, fmt.Sprintf("Fetching pools for subnet %s", subnetworkName))
	pools, err := se.ListPools(ctx, filter)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	return pools, nil
}

// _getIPsInSubnet calculates the number of IPs for a given CIDR range
func _getIPsInSubnet(ipCidrRange string) (int, error) {
	// split string by '/' to get the CIDR notation and get last part of the string which is the CIDR notation
	parts := strings.Split(ipCidrRange, "/")
	// get the number of IPs in the subnet
	cidr, err := strconv.Atoi(parts[len(parts)-1])
	if err != nil {
		return 0, err
	}
	if cidr < 0 || cidr > 32 {
		return 0, fmt.Errorf("IPCR range must be between 1 and 32. CIDR notation found : %s", ipCidrRange)
	}
	return 1 << (32 - cidr), nil
}

// _getSumOfReservedIPsForSubnet calculates the total number of IPs reserved for a specific subnet
// by summing up the IPsReserved field from cluster_details.reserved_ips_in_subnet for all pools
func _getSumOfReservedIPsForSubnet(ctx context.Context, se database.Storage, accountID, subnetworkName, poolNetwork string) (int64, error) {
	logger := util.GetLogger(ctx)

	// Get all pools using this subnetwork
	recordHeartbeat(ctx, fmt.Sprintf("Fetching pools to calculate reserved addresses for subnet %s", subnetworkName))
	pools, err := getPoolsBySubnetwork(ctx, se, accountID, subnetworkName, poolNetwork)
	if err != nil {
		return 0, err
	}

	var totalReservedIPs int64 = 0

	// Iterate through all pools and sum up reserved IPs for the specific subnet
	for _, pool := range pools {
		// We update ReservedIPsInSubnet for a given cluster during pool create which gives us the dynamic IP consumption
		// We have to handle Pools where we don't have this field set.
		if pool.ClusterDetails.ReservedIPsInSubnet != nil {
			for _, subnetToIPs := range *pool.ClusterDetails.ReservedIPsInSubnet {
				if subnetToIPs.SubnetName == subnetworkName {
					totalReservedIPs += subnetToIPs.IPsReserved
					logger.Debugf("Pool %s has %d reserved IPs in subnet %s",
						pool.Name, subnetToIPs.IPsReserved, subnetworkName)
					break
				}
			}
		} else {
			// For pools where we don't have the reservedIPsInSubnet, we will use the default value
			totalReservedIPs += ipsReservedInSubnetByPoolType(pool)
			logger.Debugf("Pool %s doesn't have reserved IPs in subnet %s, using default %d",
				pool.Name, subnetworkName, totalIPPerHAPair)
		}
	}

	logger.Infof("Total reserved IPs for subnet %s: %d", subnetworkName, totalReservedIPs)
	return totalReservedIPs, nil
}

// IPsReservedInSubnetByPoolType Based on pool type, default IPsReserved will be returned
func ipsReservedInSubnetByPoolType(pool *datamodel.PoolView) int64 {
	return int64(totalIPPerHAPair)
}

// getPoolTenantProject extracts the tenant project number from the target pool
func _getPoolTenantProject(pool *datamodel.Pool) (string, error) {
	if pool.ClusterDetails.RegionalTenantProject != "" {
		return pool.ClusterDetails.RegionalTenantProject, nil
	}
	return "", errors.NewNotFoundErr("tenant project number from pool", nil)
}

// getBackupTenantProject extracts the tenant project number from the backup
func _getBackupTenantProject(backup *datamodel.Backup) (string, error) {
	if backup.BackupVault != nil && backup.BackupVault.BucketDetails != nil {
		for _, bucketDetail := range backup.BackupVault.BucketDetails {
			if strings.EqualFold(backup.Attributes.BucketName, bucketDetail.BucketName) {
				return bucketDetail.TenantProjectNumber, nil
			}
		}
	}

	return "", errors.NewNotFoundErr("tenant project number from backup", nil)
}

type WFLastExecutionActivity struct {
	TemporalClient client.Client
}

// GetWorkflowLastExecutionTime retrieves the completion time of the last run workflow using its workflowID.
// The workflow being queried must have a query handler registered for "status", which returns the completion time of the workflow.
func (wle *WFLastExecutionActivity) GetWorkflowLastExecutionTime(ctx context.Context, workflowID string) (*time.Time, error) {
	recordHeartbeat(ctx, "Initializing workflow last execution time retrieval")
	temporalClient := wle.TemporalClient
	logger := util.GetLogger(ctx)
	var wfCompletionTime time.Time

	// Query the workflow status using the workflow ID.
	queryResult, err := temporalClient.QueryWorkflow(ctx, workflowID, "", "status")
	recordHeartbeat(ctx, "Queried workflow execution time from Temporal")
	if err != nil {
		logger.Errorf("Failed to query workflow completion time: %v", err)
		// If the workflow is not found, return default time (0) without error.
		// This will allow the caller to handle the case where the workflow has never been run.
		return &wfCompletionTime, nil
	}

	if err = queryResult.Get(&wfCompletionTime); err != nil {
		logger.Errorf("Failed to decode workflow completion time: %v", err)
		return nil, fmt.Errorf("failed to decode workflow completion time: %w", err)
	}

	// This will return the completion time of the workflow if it has completed, either Success or Failure.
	return &wfCompletionTime, nil
}

// GetOntapVersionFromPool extracts the ONTAP version from a pool, checking BuildInfo first,
// then falling back to ClusterDetails if BuildInfo is not available or doesn't have the version.
// Returns an empty string if the version cannot be found.
func GetOntapVersionFromPool(pool *datamodel.Pool) string {
	if pool == nil {
		return ""
	}
	if pool.BuildInfo != nil && pool.BuildInfo.OntapVersion != "" {
		return pool.BuildInfo.OntapVersion
	}
	if pool.ClusterDetails.OntapVersion != "" {
		return pool.ClusterDetails.OntapVersion
	}
	return ""
}

// GetExternalPeerRolePrivileges returns the privilege profile for external-peer role.
// This role has minimal privileges needed for cluster peering operations.
// Used by both FlexCache and hybrid replication.
func GetExternalPeerRolePrivileges() []*vsa.RolePrivilege {
	profile := append(
		defaultNoneRolePrivilege,
		&vsa.RolePrivilege{Path: "system capability clusterset show", Access: AccessReadOnly, Query: "-capability DATA_ONTAP.9.2.0"})
	return profile
}

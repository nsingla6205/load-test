package prepoolinfra

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/google"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/tools/vcm-prepool/names"
)

const (
	EnableServerAuthInCSR    = true     // server auth will be enabled in the CSR(Certificate Signing Request), by default client is enabled
	VCP_ADMIN_CERT_UN_SUFFIX = "_admin" // Suffix for VCP admin user certificate
	AdminUserName            = "admin"
	operationPollInterval    = 5 * time.Second
	operationPollAttempts    = 60

	// IAM needs a moment to propagate a freshly created service account before it
	// can be named as a member in a project policy, so SetIamPolicy can answer
	// with a transient 500. The pool workflow absorbs this with a dedicated
	// Temporal retry policy on CreateServiceAccountWithStorageRole; this tool has
	// no workflow engine, so it retries in-process instead.
	transientRetryAttempts       = 5
	transientRetryInitialBackoff = 2 * time.Second
)

// SetupOptions carries optional parameters for Setup.
type SetupOptions struct {
	CreateJumpHost bool
}

// Provisioner runs VCM pre-pool infrastructure operations.
type Provisioner interface {
	Setup(ctx context.Context, deploymentName, consumerNetwork, customerProjectNumber, tenantProjectRegion string, dbConfig *SpannerDBConfig, opts SetupOptions) (PrePoolInfra, error)
	Reclaim(ctx context.Context, deploymentName, consumerNetwork, customerProjectNumber, tenantProjectRegion string) error
	ReclaimAll(ctx context.Context, deploymentName, consumerNetwork, customerProjectNumber, tenantProjectRegion string) error
	DeleteVSACluster(ctx context.Context, deploymentName, consumerNetwork, customerProjectNumber, tenantProjectRegion string) error
	DeleteJumpHost(ctx context.Context, consumerNetwork, customerProjectNumber, tenantProjectRegion string) error
}

// ProvisionerFactory creates a request-scoped provisioner with fresh cloud clients.
type ProvisionerFactory func(context.Context) (Provisioner, error)

type provisioner struct {
	GCPService *google.GcpServices
}

// NewProvisioner resolves GCP clients and returns a provisioner scoped to this call.
func NewProvisioner(ctx context.Context) (Provisioner, error) {
	cloudService, err := hyperscaler.ResolveCloudService(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolve GCP service: %w", err)
	}
	gcpService, ok := cloudService.(*google.GcpServices)
	if !ok {
		return nil, fmt.Errorf("resolved cloud service is not GCP")
	}
	return &provisioner{GCPService: gcpService}, nil
}

var _ Provisioner = (*provisioner)(nil)

type PrePoolInfra struct {
	TenantProjectNumber int64            `json:"tenantProjectNumber"`
	SNHostProjectNumber int64            `json:"snHostProjectNumber"`
	DeploymentName      string           `json:"deploymentName"`
	DBConfig            *SpannerDBConfig `json:"dbConfig,omitempty"`
	ServiceAccountEmail string           `json:"serviceAccountEmail"`
	BucketName          string           `json:"bucketName"`
	MgmtVPC             string           `json:"mgmtVpc"`
	MgmtSubnet          string           `json:"mgmtSubnet"`
	IcVPC               string           `json:"icVpc"`
	IcSubnet            string           `json:"icSubnet"`
	DataNetwork         string           `json:"dataNetwork"` // network NAME in SN host project (not full path)
	DataSubnet          string           `json:"dataSubnet"`  // subnet NAME in SN host project (not full path)
	RsmVPC              string           `json:"rsmVpc"`
	RsmSubnet           string           `json:"rsmSubnet"`
}

// Steps:
// 0. Ensure local Spanner emulator instance/database when configured
// 1. FindTenantProject — Service Consumer Management API
// 2. GetTenancyInfo — Service Consumer Management API (SN host project + data network/subnet)
// 3. CreateVPCs — Compute API (mgmt, IC, RSM)
// 4. CreateSubnets — Compute API
// 5. CreateFirewalls — Compute API (internal + data-plane)
// 6. CreateServiceAccount — IAM API + attach roles/storage.objectUser
// 7. CreateAutoTierBucket — Cloud Storage API
// 8. CreateONTAPCredentials — using env password
func (p *provisioner) Setup(
	ctx context.Context,
	deploymentName,
	consumerNetwork,
	customerProjectNumber,
	tenantProjectRegion string,
	dbConfig *SpannerDBConfig,
	opts SetupOptions,
) (PrePoolInfra, error) {
	slog.Info("provisioning VCM Pre-Pool",
		"consumerNetwork", consumerNetwork, "customerProjectNumber", customerProjectNumber, "tenantProjectRegion", tenantProjectRegion)

	// Step 0: Validate and create local Spanner emulator instance/database.
	// Fail before any GCP writes when SPANNER_EMULATOR_HOST is set but
	// dbConfig is missing or bootstrap fails.
	if err := ensureSpannerEmulatorDatabase(ctx, dbConfig); err != nil {
		slog.Error("failed to create Spanner emulator database", "error", err)
		return PrePoolInfra{}, fmt.Errorf("create Spanner emulator database: %w", err)
	}

	// Step 1: Find tenant project
	tenantProjectNumber, err := p.findTenantProject(consumerNetwork, customerProjectNumber, tenantProjectRegion)
	if err != nil {
		slog.Error("failed to find tenancy project", "error", err)
		return PrePoolInfra{}, fmt.Errorf("find tenant project: %w", err)
	}
	slog.Info("tenancy project found", "tenantProjectNumber", tenantProjectNumber)

	// Step 2: GetTenancyInfo
	tenancyInfo, err := p.getTenancyInfo(ctx, consumerNetwork, tenantProjectNumber, tenantProjectRegion)
	if err != nil {
		slog.Error("failed to get tenancy info", "error", err)
		return PrePoolInfra{}, fmt.Errorf("get tenancy info: %w", err)
	}
	slog.Info("Tenancy Info:", "tenancyInfo", tenancyInfo)

	tenantProjectNum, err := parseProjectNumber(tenantProjectNumber)
	if err != nil {
		slog.Error("failed to parse tenant project number", "tenantProjectNumber", tenantProjectNumber, "error", err)
		return PrePoolInfra{}, fmt.Errorf("parse tenant project number: %w", err)
	}
	snHostProjectNumber, err := p.resolveProjectNumber(tenancyInfo.SNHostProject)
	if err != nil {
		slog.Error("failed to resolve SN host project number", "snHostProject", tenancyInfo.SNHostProject, "error", err)
		return PrePoolInfra{}, fmt.Errorf("resolve SN host project number: %w", err)
	}
	slog.Info("project numbers resolved",
		"tenantProjectNumber", tenantProjectNum, "snHostProjectNumber", snHostProjectNumber)

	// Step 3: CreateVPCs
	createdVPCs, err := p.createVPCs(ctx, tenantProjectNumber)
	if err != nil {
		slog.Error("failed to create VPCs", "error", err)
		return PrePoolInfra{}, fmt.Errorf("create VPCs: %w", err)
	}
	slog.Info("VPCs created", "createdVPCs", createdVPCs)

	// Step 4: CreateSubnets
	createdSubnets, err := p.createSubnets(ctx, tenantProjectNumber, tenantProjectRegion)
	if err != nil {
		slog.Error("failed to create subnets", "error", err)
		return PrePoolInfra{}, fmt.Errorf("create subnets: %w", err)
	}
	slog.Info("subnets created", "createdSubnets", createdSubnets)

	// Step 5: CreateFirewalls
	createdFirewalls, err := p.createFirewalls(ctx, tenantProjectNumber, tenancyInfo.DataNetworkName, tenancyInfo.SNHostProject)
	if err != nil {
		slog.Error("failed to create firewalls", "error", err)
		return PrePoolInfra{}, fmt.Errorf("create firewalls: %w", err)
	}
	slog.Info("firewalls created", "createdFirewalls", createdFirewalls)

	// Step 5b: Optionally create a development jump host on the management subnet.
	if opts.CreateJumpHost {
		if err := p.createJumpHost(ctx, tenantProjectNumber, tenantProjectRegion); err != nil {
			slog.Error("failed to create jump host", "error", err)
			return PrePoolInfra{}, fmt.Errorf("create jump host: %w", err)
		}
		slog.Info("jump host created")
	}

	// Step 6: CreateServiceAccount
	serviceAccountEmail, err := p.createServiceAccount(ctx, deploymentName, tenantProjectNumber)
	if err != nil {
		slog.Error("failed to create service account", "error", err)
		return PrePoolInfra{}, fmt.Errorf("create service account: %w", err)
	}
	slog.Info("service account created", "serviceAccountEmail", serviceAccountEmail)

	// Step 7: CreateAutoTierBucket
	bucketName, err := p.createAutoTierBucket(ctx, tenantProjectNumber, tenantProjectRegion, deploymentName)
	if err != nil {
		slog.Error("failed to create auto tier bucket", "error", err)
		return PrePoolInfra{}, fmt.Errorf("create auto tier bucket: %w", err)
	}
	slog.Info("auto tier bucket created", "bucketName", bucketName)

	// Step 8: CreateONTAPCredentials
	_, err = p.createONTAPCredentials(deploymentName)
	if err != nil {
		slog.Error("failed to create ONTAP credentials", "error", err)
		return PrePoolInfra{}, fmt.Errorf("create ONTAP credentials: %w", err)
	}
	slog.Info("ONTAP credentials created", "deploymentName", deploymentName)

	return PrePoolInfra{
		TenantProjectNumber: tenantProjectNum,
		SNHostProjectNumber: snHostProjectNumber,
		DeploymentName:      deploymentName,
		DBConfig:            dbConfig,
		ServiceAccountEmail: serviceAccountEmail,
		BucketName:          bucketName,
		MgmtVPC:             createdVPCs.MgmtVPC,
		MgmtSubnet:          createdSubnets.MgmtSubnet,
		IcVPC:               createdVPCs.IcVPC,
		IcSubnet:            createdSubnets.IcSubnet,
		DataNetwork:         tenancyInfo.DataNetworkName,
		DataSubnet:          tenancyInfo.DataSubnetName,
		RsmVPC:              createdVPCs.RsmVPC,
		RsmSubnet:           createdSubnets.RsmSubnet,
	}, nil
}

// DeleteVSACluster stops and deletes Compute instances, disks, forwarding
// rules, backend services, network endpoint groups, and IP addresses that
// match the deployment label or name prefix. It does not delete the service
// account, auto-tier bucket, or ONTAP credentials.
func (p *provisioner) DeleteVSACluster(
	ctx context.Context,
	deploymentName,
	consumerNetwork,
	customerProjectNumber,
	tenantProjectRegion string,
) error {
	tenantProjectNumber, zoneList, err := p.resolveTenantProjectAndZones(ctx, consumerNetwork, customerProjectNumber, tenantProjectRegion)
	if err != nil {
		return err
	}
	if err := p.deleteVSACluster(ctx, deploymentName, tenantProjectNumber, tenantProjectRegion, zoneList); err != nil {
		slog.Error("failed to delete VSA cluster", "error", err)
		return fmt.Errorf("delete VSA cluster: %w", err)
	}
	slog.Info("VSA cluster deleted successfully")
	return nil
}

func (p *provisioner) resolveTenantProjectAndZones(
	ctx context.Context,
	consumerNetwork,
	customerProjectNumber,
	tenantProjectRegion string,
) (string, []string, error) {
	tenantProjectNumber, err := p.findTenantProject(consumerNetwork, customerProjectNumber, tenantProjectRegion)
	if err != nil {
		slog.Error("failed to find tenancy project", "error", err)
		return "", nil, fmt.Errorf("find tenant project: %w", err)
	}
	slog.Info("tenancy project found", "tenantProjectNumber", tenantProjectNumber)

	zoneList, err := p.getZoneList(ctx, tenantProjectNumber, tenantProjectRegion)
	if err != nil {
		slog.Error("failed to get zone list", "error", err)
		return "", nil, fmt.Errorf("get zone list: %w", err)
	}
	slog.Info("zone list found", "zoneList", zoneList)
	return tenantProjectNumber, zoneList, nil
}

// Steps:
// 0. Find tenant project and zones
// 1. Delete VSA Cluster
// 2. DeleteServiceAccount
// 3. Delete Auto Tier Bucket
// 4. Delete Ontap Credentials
// Network resources (VPCs, subnets, firewalls) are left in place; use ReclaimAll.
func (p *provisioner) Reclaim(ctx context.Context, deploymentName, consumerNetwork, customerProjectNumber, tenantProjectRegion string) error {
	tenantProjectNumber, zoneList, err := p.resolveTenantProjectAndZones(ctx, consumerNetwork, customerProjectNumber, tenantProjectRegion)
	if err != nil {
		return err
	}

	var errs []error
	errs = collectCleanupError(errs, "delete VSA cluster", p.deleteVSACluster(ctx, deploymentName, tenantProjectNumber, tenantProjectRegion, zoneList))
	errs = collectCleanupError(errs, "delete service account", p.deleteServiceAccount(deploymentName, tenantProjectNumber))
	bucketName := names.GenerateBucketName(deploymentName)
	errs = collectCleanupError(errs, "delete auto tier bucket", p.deleteAutoTierBucket(ctx, bucketName))
	errs = collectCleanupError(errs, "delete ONTAP credentials", p.deleteONTAPCredentials(deploymentName))
	return errors.Join(errs...)
}

// DeleteJumpHost removes the development jump host and its SSH firewall rule.
// The VM is deleted before the firewall. Missing resources are treated as success.
func (p *provisioner) DeleteJumpHost(ctx context.Context, consumerNetwork, customerProjectNumber, tenantProjectRegion string) error {
	tenantProjectNumber, err := p.findTenantProject(consumerNetwork, customerProjectNumber, tenantProjectRegion)
	if err != nil {
		return fmt.Errorf("find tenant project: %w", err)
	}
	return p.deleteJumpHost(ctx, tenantProjectNumber, tenantProjectRegion)
}

// ReclaimAll runs Reclaim, then deletes the jump host (if any), then deletes
// tool-created firewalls, subnets, and VPCs (mgmt/IC/RSM in the tenant
// project, plus named data-plane firewalls in the SN host project). It does
// not delete the Service Networking data network or subnet. Network cleanup
// is aborted if the SN host project cannot be resolved, so data-plane rules
// are never deleted from the tenant project by mistake.
func (p *provisioner) ReclaimAll(ctx context.Context, deploymentName, consumerNetwork, customerProjectNumber, tenantProjectRegion string) error {
	var errs []error
	errs = collectCleanupError(errs, "reclaim", p.Reclaim(ctx, deploymentName, consumerNetwork, customerProjectNumber, tenantProjectRegion))

	tenantProjectNumber, err := p.findTenantProject(consumerNetwork, customerProjectNumber, tenantProjectRegion)
	if err != nil {
		return errors.Join(append(errs, fmt.Errorf("find tenant project: %w", err))...)
	}

	// Delete the jump host and its firewall before the management VPC/subnet.
	errs = collectCleanupError(errs, "delete jump host", p.deleteJumpHost(ctx, tenantProjectNumber, tenantProjectRegion))

	snHostProject, err := p.GCPService.GetSnHost(tenantProjectNumber)
	if err != nil {
		slog.Error("failed to resolve SN host project; aborting network cleanup", "error", err)
		return errors.Join(append(errs, fmt.Errorf("resolve SN host project: %w", err))...)
	}
	if strings.TrimSpace(snHostProject) == "" {
		slog.Error("SN host project is empty; aborting network cleanup", "tenantProjectNumber", tenantProjectNumber)
		return errors.Join(append(errs, fmt.Errorf("resolve SN host project: empty result for tenant %s", tenantProjectNumber))...)
	}

	errs = collectCleanupError(errs, "delete networks", p.deleteNetworks(ctx, tenantProjectNumber, tenantProjectRegion, snHostProject))
	return errors.Join(errs...)
}

func parseProjectNumber(project string) (int64, error) {
	n, err := strconv.ParseInt(strings.TrimSpace(project), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("project %q is not a numeric project number", project)
	}
	return n, nil
}

// resolveProjectNumber returns a GCP project number. A numeric project string is
// parsed directly. A project id is looked up through Cloud Resource Manager.
func (p *provisioner) resolveProjectNumber(project string) (int64, error) {
	project = strings.TrimSpace(project)
	if project == "" {
		return 0, fmt.Errorf("project is empty")
	}
	if n, err := strconv.ParseInt(project, 10, 64); err == nil {
		return n, nil
	}
	n, err := p.GCPService.ResolveProjectIDToNumber(project)
	if err != nil {
		return 0, fmt.Errorf("resolve project number for %s: %w", project, err)
	}
	return n, nil
}

func collectCleanupError(errs []error, step string, err error) []error {
	if err == nil {
		slog.Info("cleanup step completed", "step", step)
		return errs
	}
	slog.Error("cleanup step failed; continuing", "step", step, "error", err)
	return append(errs, fmt.Errorf("%s: %w", step, err))
}

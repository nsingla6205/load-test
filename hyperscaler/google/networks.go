package google

import (
	stdErrors "errors"
	"fmt"
	"strings"
	"time"

	models "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/models"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"google.golang.org/api/compute/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/servicenetworking/v1"
)

var (
	GlobalRegion                        = "global"
	serviceNetworkingConnectionErrorMsg = "Please create Service Networking connection with service"
	ipExhaustionErrorMsg                = "Couldn't find free blocks in allocated IP ranges. Please allocate new ranges for this service provider"
	consumerNetworkNotFoundErrorMsg     = "does not exist"
	serviceDisabledErrorReason          = "SERVICE_DISABLED"
	accessNotConfiguredErrorMsg         = "accessNotConfigured"

	// secondaryRangeConflictMarkers are the lowercased GCP messages that mean the requested
	// secondary range can never be attached, so the patch must not be retried.
	secondaryRangeConflictMarkers = []string{
		"already exists",
		"conflicts with",
		"ip range overlaps",
		"overlaps with",
		"ipcidrrangeoverlap",
		"alreadyexists",
	}

	waitTimeoutMinutes         = time.Minute * time.Duration(env.GetInt("GCP_LRO_TIMEOUT_MINUTES", 20))
	minimumTenantNetworkSize   = env.GetInt64("DATA_SUBNET_CIDR_BLOCK", int64(28))
	minimumLVTenantNetworkSize = env.GetInt64("DATA_SUBNET_CIDR_BLOCK_LV", int64(26))
	defaultSleepTime           = time.Duration(env.GetInt64("GCP_NETWORK_SLEEP_SECONDS", int64(28))) * time.Second

	CreateTPSubnetOp       = _createTPSubnetOp
	getProjectIDFromNumber = _getProjectIDFromNumber
	createAddress          = _createAddress
	createForwardingRule   = _createForwardingRule
)

// GetTenantProject lists registered tenancy units for the customer project
func (gcpService *GcpServices) GetTenantProject(consumerNetwork, customerProjectNumber, tenantProjectRegion string) (string, error) {
	parent := fmt.Sprintf("services/%s/projects/%s", gcpService.GetServiceConsumerManagementEndpoint(), customerProjectNumber)
	gcpService.Logger.Debugf("Inside GetTenantProject. consumerNetwork: %s, customerProjectNumber: %s, tenantProjectRegion: %s , parent : %s ", consumerNetwork, customerProjectNumber, tenantProjectRegion, parent)

	tenantProjectsResp, err := gcpService.AdminGCPService.managementService.Services.TenancyUnits.List(parent).Context(gcpService.Ctx).Do()
	if err != nil {
		gcpService.Logger.Errorf("List TenancyUnits call failed for project : %s Region : %s Error : %s ", customerProjectNumber, tenantProjectRegion, err.Error())
		return "", vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceFetchError, err)
	}

	for _, tenancy := range tenantProjectsResp.TenancyUnits {
		for _, tenantResource := range tenancy.TenantResources {
			tenantProjectNumber := strings.TrimPrefix(tenantResource.Resource, "projects/")
			if tenantResource.Tag == consumerNetwork+"-"+tenantProjectRegion && tenantProjectNumber != "" {
				gcpService.Logger.Infof("Found tenancy for 1P for Tenant project: %s, consumer network : %s", tenantProjectNumber, consumerNetwork)
				return tenantProjectNumber, nil
			}
		}
	}
	gcpService.Logger.Errorf("Tenancy not found : consumerNetwork: %s, customerProjectNumber: %s, tenantProjectRegion: %s , parent : %s ", consumerNetwork, customerProjectNumber, tenantProjectRegion, parent)
	return "", vsaerrors.WrapAsNonRetryableTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrPSAPeeringNotFoundError, fmt.Errorf("vpc peering network for TenancyUnit '%s' not found. Use the correct vpc name and ensure VPC network peering with tenant project has already been established", consumerNetwork)))
}

// getNetworkSize returns the appropriate network size based on the isLargeCapacity flag
func getNetworkSize(isLargeCapacity bool) int64 {
	if isLargeCapacity {
		return minimumLVTenantNetworkSize
	}
	return minimumTenantNetworkSize
}

// CreateTPSubnetOp returns GCP operation for creating subnetwork for a tenant project.
// The subnet is created without secondary ranges; the IC LIF secondary range is attached
// afterwards by AddSubnetSecondaryRangeOp so that GCP does not have to satisfy the primary
// allocation and the secondary block in a single request.
func (gcpService *GcpServices) CreateTPSubnetOp(tenantProjectNumber, consumerNetwork, region, subnetName string, isLargeCapacity bool, requestedRanges []string) (*string, error) {
	consumerProjectNumber, consumerPeeringNetwork, err := utils.ParseProjectId(consumerNetwork)
	if err != nil {
		return nil, err
	}
	gcpService.Logger.Infof("Calling CreateTPSubnetOp consumerProjectNumber : %s consumerPeeringNetwork : %s tenantProjectNumber : %s region: %s subnet name : %s isLargeCapacity: %t requestedRanges: %v", consumerProjectNumber, consumerPeeringNetwork, tenantProjectNumber, region, subnetName, isLargeCapacity, requestedRanges)

	// Use the calculator to determine the appropriate network size
	networkSize := getNetworkSize(isLargeCapacity)

	request := servicenetworking.AddSubnetworkRequest{
		Consumer:        "projects/" + consumerProjectNumber,
		ConsumerNetwork: consumerNetwork,
		Description:     "vsa-network",
		IpPrefixLength:  networkSize,
		Region:          region,
		Subnetwork:      subnetName,
		RequestedRanges: requestedRanges,
	}
	snProducerOperation, err := CreateTPSubnetOp(gcpService, &request, tenantProjectNumber)
	if err != nil {
		return nil, err
	}
	return &snProducerOperation.Name, nil
}

// AddSubnetSecondaryRangeOp attaches a secondary IP range to an existing subnetwork and
// returns the name of the resulting regional compute operation. Reference :
// https://cloud.google.com/compute/docs/reference/rest/v1/subnetworks/patch
//
// Patch replaces the whole secondaryIpRanges list, so the current list is read first and the
// new range appended to it. The read also supplies the fingerprint that GCP uses for optimistic
// concurrency. An empty operation name is returned when the range is already attached, which
// makes the call safe to retry.
func (gcpService *GcpServices) AddSubnetSecondaryRangeOp(projectID, region, subnetName, rangeName, ipCidrRange string) (string, error) {
	gcpService.Logger.Infof("Calling AddSubnetSecondaryRangeOp for project : %s, region : %s, subnet : %s, range name : %s, CIDR : %s", projectID, region, subnetName, rangeName, ipCidrRange)

	current, err := gcpService.AdminGCPService.computeService.Subnetworks.Get(projectID, region, subnetName).Context(gcpService.Ctx).Do()
	if err != nil {
		gcpService.Logger.Errorf("AddSubnetSecondaryRangeOp failed to read subnet %s in project %s, region %s. Error : %s", subnetName, projectID, region, err.Error())
		return "", vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceFetchError, err)
	}
	for _, existing := range current.SecondaryIpRanges {
		if existing == nil {
			continue
		}
		if existing.RangeName == rangeName && existing.IpCidrRange == ipCidrRange {
			gcpService.Logger.Infof("Secondary range %s (%s) already attached to subnet %s, skipping patch", rangeName, ipCidrRange, subnetName)
			return "", nil
		}
		if existing.RangeName == rangeName || existing.IpCidrRange == ipCidrRange {
			gcpService.Logger.Errorf("Secondary range conflict on subnet %s: requested %s (%s) collides with existing %s (%s)", subnetName, rangeName, ipCidrRange, existing.RangeName, existing.IpCidrRange)
			return "", vsaerrors.WrapAsNonRetryableTemporalApplicationError(
				vsaerrors.NewVCPError(vsaerrors.ErrResourceStateConflictError,
					fmt.Errorf("secondary range %s (%s) conflicts with existing range %s (%s) on subnet %s", rangeName, ipCidrRange, existing.RangeName, existing.IpCidrRange, subnetName)))
		}
	}

	ranges := make([]*compute.SubnetworkSecondaryRange, 0, len(current.SecondaryIpRanges)+1)
	ranges = append(ranges, current.SecondaryIpRanges...)
	ranges = append(ranges, &compute.SubnetworkSecondaryRange{
		RangeName:   rangeName,
		IpCidrRange: ipCidrRange,
	})

	patch := &compute.Subnetwork{
		Fingerprint:       current.Fingerprint,
		SecondaryIpRanges: ranges,
	}
	op, err := gcpService.AdminGCPService.computeService.Subnetworks.Patch(projectID, region, subnetName, patch).Context(gcpService.Ctx).Do()
	if err != nil {
		if isSecondaryRangeConflictError(err) {
			gcpService.Logger.Errorf("AddSubnetSecondaryRangeOp failed with an unusable range: subnet %s, range %s (%s). Error : %s", subnetName, rangeName, ipCidrRange, err.Error())
			return "", vsaerrors.WrapAsNonRetryableTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrResourceStateConflictError, err))
		}
		// A stale fingerprint (412) stays retryable so the next attempt re-reads the subnet.
		gcpService.Logger.Errorf("AddSubnetSecondaryRangeOp failed for subnet %s in project %s, region %s with retryable error : %s", subnetName, projectID, region, err.Error())
		return "", vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceProvisionError, err)
	}
	gcpService.Logger.Infof("Operation to attach secondary range %s (%s) to subnet %s created successfully", rangeName, ipCidrRange, subnetName)
	return op.Name, nil
}

// isSecondaryRangeConflictError reports whether a subnetworks.patch failure is caused by the
// requested range itself (overlap with an existing range, or a duplicate name) rather than by a
// transient condition. Such failures never succeed on retry.
func isSecondaryRangeConflictError(err error) bool {
	if err == nil {
		return false
	}
	var gerr *googleapi.Error
	if stdErrors.As(err, &gerr) && gerr.Code == 409 {
		return true
	}
	msg := strings.ToLower(err.Error())
	for _, marker := range secondaryRangeConflictMarkers {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

// ReleaseSubnetwork calls GCP releaseSubnetwork API and return a long-running operation.
func (gcpService *GcpServices) ReleaseSubnetworkOp(region, projectId, subnetwork string) (string, error) {
	op, err := gcpService.AdminGCPService.computeService.Subnetworks.Delete(projectId, region, subnetwork).Do()
	if err != nil {
		if strings.Contains(err.Error(), "notFound") {
			// If the subnetwork is not found, it means it has already been deleted or never existed.
			gcpService.Logger.Debugf("Subnet already deleted because it is not found in GCP. subnet name: %s, projectId: %s, region: %s, error : %s", subnetwork, projectId, region, err.Error())
			return "", nil
		}
		if strings.Contains(err.Error(), "resourceInUseByAnotherResource") {
			gcpService.Logger.Debugf("Failed to delete subnetwork because it is in use by another resource: %s, projectId: %s, region: %s, error : %s", subnetwork, projectId, region, err.Error())
			return "", nil
		}
		gcpService.Logger.Errorf("Failed to delete subnetwork: %s, projectId: %s, region: %s, error : %s", subnetwork, projectId, region, err.Error())
		return "", vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceDeprovisionError, err)
	}
	return op.Name, nil
}

// isServiceDisabledError inspects a GCP API error and reports whether it indicates
// that a required Google API (e.g. Compute Engine) is disabled/not enabled on the
// target project.
func isServiceDisabledError(err error) bool {
	if err == nil {
		return false
	}
	var gerr *googleapi.Error
	if stdErrors.As(err, &gerr) {
		if gerr.Code == 403 {
			for _, e := range gerr.Errors {
				if strings.EqualFold(e.Reason, serviceDisabledErrorReason) || strings.EqualFold(e.Reason, accessNotConfiguredErrorMsg) {
					return true
				}
			}
			if strings.Contains(gerr.Body, serviceDisabledErrorReason) || strings.Contains(gerr.Body, accessNotConfiguredErrorMsg) {
				return true
			}
		}
	}
	msg := err.Error()
	return strings.Contains(msg, serviceDisabledErrorReason) || strings.Contains(msg, accessNotConfiguredErrorMsg)
}

// GetSnHost returns host project peered with the given service project
func (gcpService *GcpServices) GetSnHost(project string) (string, error) {
	snProject, err := gcpService.AdminGCPService.computeService.Projects.GetXpnHost(project).Do()
	if err != nil {
		if strings.Contains(err.Error(), serviceNetworkingConnectionErrorMsg) {
			gcpService.Logger.Errorf(fmt.Sprintf("error getting SN host for project due to peering. Should not retry for project : %s, Error : %v", project, err))
			return "", vsaerrors.WrapAsNonRetryableTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrPSAPeeringNotFoundError, fmt.Errorf("SN Producer Host Project %s Error: %v", project, err)))
		}
		if isServiceDisabledError(err) {
			gcpService.Logger.Errorf(fmt.Sprintf("error getting SN host for project due to a disabled GCP API. Should not retry for project : %s, Error : %v", project, err))
			return "", vsaerrors.WrapAsNonRetryableTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrGCPServiceDisabledError, fmt.Errorf("SN Host Project %s Error: %v", project, err)))
		}
		gcpService.Logger.Errorf(fmt.Sprintf("error getting SN host for project : %s, Error : %v", project, err))
		return "", err
	}
	// for a new VPC, sn host project will be empty. we need to return empty in this case to establish datalink
	return snProject.Name, nil
}

// _createTPSubnetOp returns GCP operation for subnetwork in a producer tenant project. This method will make producer's tenant project to be a shared VPC service project as needed. Reference : https://cloud.google.com/service-infrastructure/docs/service-networking/reference/rest/v1/services/addSubnetwork
func _createTPSubnetOp(gcpService *GcpServices, request *servicenetworking.AddSubnetworkRequest, tenantProjectNumber string) (*models.ComputeOperation, error) {
	parent := fmt.Sprintf("services/%s/projects/%s", gcpService.GetServiceNetworkingEndpoint(), tenantProjectNumber)
	tu, err := gcpService.AdminGCPService.networkingService.Services.AddSubnetwork(parent, request).Context(gcpService.Ctx).Do()
	if err != nil || (tu != nil && tu.Error != nil) {
		terminalOperationError := err == nil
		providerErrorMessage := ""
		if err == nil {
			providerErrorMessage = tu.Error.Message
		} else {
			providerErrorMessage = err.Error()
		}
		if strings.Contains(providerErrorMessage, "are not successfully connected yet") || strings.Contains(providerErrorMessage, serviceNetworkingConnectionErrorMsg) {
			gcpService.Logger.Error("CreateTPSubnetOp failed with peering error")
			// Do not attach provider message: Temporal details can expose tenant resource paths.
			return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrPSAPeeringNotFoundError, nil))
		} else if strings.Contains(providerErrorMessage, ipExhaustionErrorMsg) {
			gcpService.Logger.Error("CreateTPSubnetOp failed with IP exhaustion")
			return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrGCPCustomerIPExhaustion, nil))
		}
		if terminalOperationError {
			gcpService.Logger.Errorf("CreateTPSubnetOp failed with an unclassified terminal GCP operation error for operationName=%s", tu.Name)
			return nil, newTerminalGCPOperationError()
		}
		gcpService.Logger.Errorf("CreateTPSubnetOp failed with retryable error: %s", vsaerrors.RedactProviderError(providerErrorMessage))
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceProvisionError, err)
	}
	gcpService.Logger.Debugf("CreateTPSubnetOp for tenant project : %s successful", tenantProjectNumber)
	return convertServiceNetOpToComputeOp(tu), nil
}

// GetSubnetwork retrieves a subnetwork for a given project name, region and subnetwork name using compute API. Reference : https://cloud.google.com/compute/docs/reference/rest/v1/subnetworks/get
func (gcpService *GcpServices) GetSubnetwork(projectName, region, subnetName string) (*models.Subnet, error) {
	gcpService.Logger.Debugf("Calling GetSubnetwork for project name : %s, region : %s, subnet name : %s", projectName, region, subnetName)

	subnetwork, err := gcpService.AdminGCPService.computeService.Subnetworks.Get(projectName, region, subnetName).Context(gcpService.Ctx).Do()
	if err != nil {
		gcpService.Logger.Errorf("GetSubnetwork failed for project name : %s, region : %s, subnet name : %s with error : %v", projectName, region, subnetName, err.Error())
		return nil, err
	}
	gcpService.Logger.Debugf("GetSubnetwork success with response :  %s", subnetwork.Name)
	return convertGoogleSubnetToSubnet(subnetwork), nil
}

// ListSubnetworks retrieves a subnetwork for a given project name, region and subnetwork name using compute API. Reference : https://cloud.google.com/compute/docs/reference/rest/v1/subnetworks/get
func (gcpService *GcpServices) ListSubnetworks(projectName, region string) (*[]models.Subnet, error) {
	subnetworks, err := gcpService.AdminGCPService.computeService.Subnetworks.List(projectName, region).Context(gcpService.Ctx).Do()
	if err != nil {
		gcpService.Logger.Debugf("ListSubnetworks failed for project name : %s, region : %s", projectName, region)
		return nil, err
	}
	gcpService.Logger.Debugf("ListSubnetworks success with number of subnets = %d", len(subnetworks.Items))
	return convertGoogleSubnetsToSubnets(subnetworks), nil
}

// GetVPCNetwork retrieves a VPC network for given project name and VPC network name
func (gcpService *GcpServices) GetVPCNetwork(projectName, vpcNetworkName string) (*models.VPCNetwork, error) {
	gcpService.Logger.Debugf("calling GetVPCNetwork for project name : %s, VPC network name : %s", projectName, vpcNetworkName)

	vpcNetwork, err := gcpService.AdminGCPService.computeService.Networks.Get(projectName, vpcNetworkName).Context(gcpService.Ctx).Do()
	if err != nil {
		gcpService.Logger.Errorf("GetVPCNetwork failed projectName : %s, VPC network name : %s with error : %v", projectName, vpcNetworkName, err.Error())
		return nil, err
	}
	gcpService.Logger.Debugf("GetVPCNetwork success with response :  %s", vpcNetwork.Name)
	return convertGoogleVPCToVPC(vpcNetwork), nil
}

// CreateVPC creates a VPC network in a project using compute API. This function also waits until the operation concludes. Reference : https://cloud.google.com/compute/docs/reference/rest/v1/networks/insert
func (gcpService *GcpServices) CreateVPC(vpcNetwork *models.VPCNetwork) (string, error) {
	projectName := vpcNetwork.ProjectName
	vpcNetworkName := vpcNetwork.Name
	op, err := gcpService.AdminGCPService.computeService.Networks.Insert(projectName, convertVPCToGoogleVPC(vpcNetwork)).Context(gcpService.Ctx).Do()
	if err != nil {
		gcpService.Logger.Errorf("Failed to create VPC for project name : %s, VPC network name : %s with error : %v", projectName, vpcNetworkName, err.Error())
		return "", vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceProvisionError, err)
	}
	gcpService.Logger.Debugf("Operation for Create VPC for project name : %s, VPC network name : %s created successfully", projectName, vpcNetworkName)
	return op.Name, nil
}

// CreateSubnetwork allocates a subnetwork in a single region in VPC for a project using compute API. Reference : https://cloud.google.com/compute/docs/reference/rest/v1/subnetworks/insert
func (gcpService *GcpServices) CreateSubnetwork(request *models.Subnet) (string, error) {
	projectName := request.ProjectName
	gcpService.Logger.Debugf("Creating Subnet: %s for Project name: %s and VPC : %s ", request.Name, projectName, request.Network)

	op, err := gcpService.AdminGCPService.computeService.Subnetworks.Insert(projectName, *request.Region, convertSubnetToGoogleSubnet(request)).Context(gcpService.Ctx).Do()
	if err != nil {
		gcpService.Logger.Errorf("Failed to create subnetwork for project name : %s, VPC network name : %s, subnetwork name : %s with error : %v", projectName, request.Network, request.Name, err.Error())
		return "", vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceProvisionError, err)
	}
	gcpService.Logger.Debugf("Operation to create subnetwork successful for project name : %s, VPC network name : %s, subnetwork name : %s", projectName, request.Network, request.Name)
	return op.Name, nil
}

// GetFirewall retrieves a firewall rule for given project name and firewall name using compute API. Reference : https://cloud.google.com/compute/docs/reference/rest/v1/firewalls/get
func (gcpService *GcpServices) GetFirewall(projectName string, firewallName string) (*models.Firewall, error) {
	gcpService.Logger.Debugf("calling get firewall for project name : %s, firewall name : %s", projectName, firewallName)

	firewall, err := gcpService.AdminGCPService.computeService.Firewalls.Get(projectName, firewallName).Context(gcpService.Ctx).Do()
	if err != nil {
		gcpService.Logger.Errorf("Failed to get firewall for project name : %s, firewall name : %s with error : %v", projectName, firewallName, err.Error())
		return nil, err
	}
	gcpService.Logger.Debugf("Get firewall successful with response :  %s", firewall.Name)
	return convertGCPFirewallToFirewall(firewall), nil
}

// getAddress retrieves an address for given project name, region and address name using compute API. Reference : https://cloud.google.com/compute/docs/reference/rest/v1/addresses/get
func (gcpService *GcpServices) GetAddress(projectName string, region string, address string) (*models.Address, error) {
	gcpService.Logger.Debugf("calling get address for project name : %s, address name : %s", projectName, address)

	ipAddress, err := gcpService.AdminGCPService.computeService.Addresses.Get(projectName, region, address).Context(gcpService.Ctx).Do()
	if err != nil {
		gcpService.Logger.Errorf("Failed to get address for project name : %s, address name : %s with error : %v", projectName, address, err.Error())
		return nil, err
	}

	gcpService.Logger.Debugf("Get address successful with response :  %s", ipAddress.Name)
	return convertGCPAddressToAddress(ipAddress), nil
}

func (gcpService *GcpServices) CreateAddressOperation(address *models.Address) (string, error) {
	projectName := address.ProjectId
	addressName := address.AddressName

	gcpService.Logger.Infof("Creating address %s for project %s ", addressName, projectName)

	operation, err := createAddress(gcpService, address)
	if err != nil {
		gcpService.Logger.Errorf("Failed to create address %s for project %s. Error: %+v", addressName, projectName, err)
		return "", err
	}

	return operation.Name, nil
}

func (gcpService *GcpServices) CreateForwardingRuleOperation(address *models.ForwardingRule) (string, error) {
	projectName := address.ProjectId
	addressName := address.Name
	gcpService.Logger.Infof("Creating forwarding rule %s for project %s ", addressName, projectName)

	operation, err := createForwardingRule(gcpService, address)
	if err != nil {
		return "", err
	}

	return operation.Name, nil
}

func (gcpService *GcpServices) ReleaseAddress(region, projectNumber, address string) (string, error) {
	op, err := gcpService.AdminGCPService.computeService.Addresses.Delete(projectNumber, region, address).Do()
	if err != nil {
		if strings.Contains(err.Error(), "notFound") {
			return "", vsaerrors.NewVCPError(vsaerrors.ErrResourceNotFound, errors.NewNotFoundErr("compute.Subnetwork", &address))
		}
		if strings.Contains(err.Error(), "resourceInUseByAnotherResource") {
			gcpService.Logger.Warnf("Failed to delete address because it is in use by another resource: %s, error : %s", address, err.Error())
			return "", nil
		}
		gcpService.Logger.Error("Failed to delete address...")
		return "", vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceDeprovisionError, err)
	}

	return op.Name, nil
}

// create address
func _createAddress(gcpService *GcpServices, request *models.Address) (*models.ComputeOperation, error) {
	addressName := request.AddressName
	projectName := request.ProjectId
	gcpService.Logger.Debugf("Calling create address for project name : %s, address name : %s", projectName, addressName)

	op, err := gcpService.AdminGCPService.computeService.Addresses.Insert(projectName, request.Region, convertAddressToGoogleAddress(request)).Context(gcpService.Ctx).Do()

	if err != nil {
		gcpService.Logger.Errorf("Failed to create address for project name : %s, address name : %s with error : %v", projectName, addressName, err.Error())
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceDeprovisionError, err)
	}

	gcpService.Logger.Debugf("Operation to create address successful for project name : %s,address name : %s", projectName, addressName)
	return convertComputeOpToComputeOp(op), nil
}

// getForwardingRules retrieves a forwarding rule for given project name, region and endpoint name using compute API. Reference : https://cloud.google.com/compute/docs/reference/rest/v1/forwardingRules/get
func (gcpService *GcpServices) GetForwardingRule(projectName string, region string, endpointName string) (*models.ForwardingRule, error) {
	gcpService.Logger.Debugf("calling get forwarding rules for project name : %s, forwarding rule name : %s", projectName, endpointName)

	forwardingrule, err := gcpService.AdminGCPService.computeService.ForwardingRules.Get(projectName, region, endpointName).Context(gcpService.Ctx).Do()
	if err != nil {
		gcpService.Logger.Errorf("Failed to get forwarding rules for project name : %s, endpoint name : %s with error : %v", projectName, endpointName, err.Error())
		return nil, err
	}
	gcpService.Logger.Debugf("Get forwarding rules successful with response :  %s", forwardingrule.Name)
	return convertGCPForwardingRuleToForwardingRule(forwardingrule), nil
}

func _createForwardingRule(gcpService *GcpServices, request *models.ForwardingRule) (*models.ComputeOperation, error) {
	addressName := request.Name
	projectName := request.ProjectId
	gcpService.Logger.Debugf("Calling create forwarding rule for project name : %s, forwarding rule : %s", projectName, addressName)

	op, err := gcpService.AdminGCPService.computeService.ForwardingRules.Insert(projectName, request.Region, convertForwardingRuleToGoogleForwardingRule(request)).Context(gcpService.Ctx).Do()

	if err != nil {
		gcpService.Logger.Errorf("Failed to create forwarding rule for project name : %s, forwarding rule : %s with error : %v", projectName, addressName, err.Error())
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceProvisionError, err)
	}

	gcpService.Logger.Debugf("Operation to create subnetwork successful for project name : %s,address name : %s", projectName, addressName)
	return convertComputeOpToComputeOp(op), nil
}

func (gcpService *GcpServices) DeleteForwardingRule(region, projectNumber, forwardingRule string) (string, error) {
	op, err := gcpService.AdminGCPService.computeService.ForwardingRules.Delete(projectNumber, region, forwardingRule).Do()
	if err != nil {
		if strings.Contains(err.Error(), "notFound") {
			return "", vsaerrors.NewVCPError(vsaerrors.ErrResourceNotFound, errors.NewNotFoundErr("compute.Subnetwork", &forwardingRule))
		}
		if strings.Contains(err.Error(), "resourceInUseByAnotherResource") {
			gcpService.Logger.Errorf("Failed to delete forwarding rule because it is in use by another resource: %s, error : %s", forwardingRule, err.Error())
			return "", nil
		}
		gcpService.Logger.Error("Failed to delete forwardingRule...")
		return "", vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceDeprovisionError, err)
	}

	return op.Name, nil
}

// InsertFirewall creates a firewall rule in a project using compute API. This function also waits until the operation concludes
func (gcpService *GcpServices) InsertFirewall(firewallRule *models.Firewall) (string, error) {
	projectName := firewallRule.ProjectName
	firewallName := firewallRule.Name

	gcpService.Logger.Debugf("Inserting firewall rule %s for project %s ", firewallName, projectName)
	op, err := gcpService.AdminGCPService.computeService.Firewalls.Insert(projectName, convertToGoogleFirewallRule(firewallRule)).Context(gcpService.Ctx).Do()
	if err != nil {
		gcpService.Logger.Errorf("Insert firewall failed for project name : %s, firewall name : %s with error : %v", projectName, firewallName, err.Error())
		return "", vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceProvisionError, err)
	}
	gcpService.Logger.Debugf("Operation to insert firewall created successfully for project name : %s, firewall name : %s", projectName, firewallName)
	return op.Name, nil
}

// UpdateFirewall creates a firewall rule in a project using compute API. This function also waits until the operation concludes
func (gcpService *GcpServices) UpdateFirewall(firewallRule *models.Firewall) (string, error) {
	projectName := firewallRule.ProjectName
	firewallName := firewallRule.Name
	gcpService.Logger.Debugf("Updating firewall rule %s for project %s ", firewallName, projectName)

	op, err := gcpService.AdminGCPService.computeService.Firewalls.Update(projectName, firewallName, convertToGoogleFirewallRule(firewallRule)).Context(gcpService.Ctx).Do()
	if err != nil {
		gcpService.Logger.Debugf("failed to update firewall for project name : %s, firewall name : %s", projectName, firewallName)
		return "", vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceProvisionError, err)
	}
	gcpService.Logger.Debugf("Operation to update firewall created successfully for project name : %s, firewall name : %s err : %v", projectName, firewallName, err)
	return op.Name, nil
}

// GetServiceNetOpStatus get the status of the operation on basis of the operation name
func (gcpService *GcpServices) GetServiceNetOpStatus(operationName string) (*models.ComputeOperation, error) {
	op, err := gcpService.AdminGCPService.networkingService.Operations.Get(operationName).Do()
	if err != nil {
		gcpService.Logger.Errorf("GetServiceNetOpStatus failed: operationName=%s error=%s", operationName, vsaerrors.RedactProviderError(err.Error()))
		return nil, err
	}
	if op != nil && op.Error != nil {
		// operationName is a safe correlation handle; never log op.Error.Message.
		gcpService.Logger.Errorf("GetServiceNetOpStatus operation failed: operationName=%s", operationName)
		providerErrorMessage := op.Error.Message
		if strings.Contains(providerErrorMessage, serviceNetworkingConnectionErrorMsg) {
			return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrPSAPeeringNotFoundError, nil))
		} else if strings.Contains(providerErrorMessage, ipExhaustionErrorMsg) {
			gcpService.Logger.Error("GetServiceNetOpStatus failed with IP exhaustion")
			return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrGCPCustomerIPExhaustion, nil))
		} else if strings.Contains(providerErrorMessage, consumerNetworkNotFoundErrorMsg) {
			gcpService.Logger.Error("GetServiceNetOpStatus failed: consumer network not found")
			return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrGCPConsumerNetworkNotFound, nil))
		}
		return nil, newTerminalGCPOperationError()
	}
	gcpService.Logger.Debug(fmt.Sprintf("Fetching GetServiceNetOpStatus successful : %s", op.Name))
	return convertServiceNetOpToComputeOp(op), nil
}

// newTerminalGCPOperationError reports a GCP long-running operation that finished in a
// failed state.
func newTerminalGCPOperationError() error {
	// Temporal NonRetryable: polling the same completed LRO again cannot succeed.
	// Catalog retriable=true (3012): the customer may submit a new request.
	// Do not attach the provider error: OriginalErr is serialized into Temporal
	// details and GCP messages can contain tenant resource identifiers.
	return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
		vsaerrors.NewVCPError(vsaerrors.ErrGCPTerminalOperationError, nil),
	)
}

// firstComputeOperationErrorMessage returns the first nested operation error
// message when present. Always bounds-checks Errors so an empty or nil slice
// cannot panic (GCP may return {"error": {}}).
func firstComputeOperationErrorMessage(op *compute.Operation) string {
	if op == nil || op.Error == nil || len(op.Error.Errors) == 0 {
		return ""
	}
	if op.Error.Errors[0] == nil {
		return ""
	}
	return op.Error.Errors[0].Message
}

// GetComputeGlobalOpStatus gets ComputeOperation object for the given tenantProject, operationName
func (gcpService *GcpServices) GetComputeGlobalOpStatus(tenantProject, operationName string) (*models.ComputeOperation, error) {
	op, err := gcpService.AdminGCPService.computeService.GlobalOperations.Get(tenantProject, operationName).Do()
	if err != nil {
		gcpService.Logger.Errorf("Failed to get compute global operation status: project=%s operationName=%s error=%s",
			tenantProject, operationName, vsaerrors.RedactProviderError(err.Error()))
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceFetchError, err)
	}

	if op != nil && op.Error != nil {
		// Safe correlation fields only. Never index Errors without a length check
		// and never return provider messages (tenant resource paths).
		if msg := firstComputeOperationErrorMessage(op); msg != "" {
			gcpService.Logger.Debugf("GetComputeGlobalOpStatus nested provider error (redacted): operationName=%s detail=%s",
				operationName, vsaerrors.RedactProviderError(msg))
		}
		gcpService.Logger.Errorf("GetComputeGlobalOpStatus operation failed: project=%s operationName=%s", tenantProject, operationName)
		return nil, newTerminalGCPOperationError()
	}
	gcpService.Logger.Debug(fmt.Sprintf("Fetching getComputeGlobalOpStatus successful : %s", op.Name))
	return convertComputeOpToComputeOp(op), nil
}

// GetComputeRegionalOpStatus gets ComputeOperation object for the given projectNumber, region, operationName
func (gcpService *GcpServices) GetComputeRegionalOpStatus(projectNumber, region, operationName string) (*models.ComputeOperation, error) {
	op, err := gcpService.AdminGCPService.computeService.RegionOperations.Get(projectNumber, region, operationName).Do()
	if err != nil {
		gcpService.Logger.Errorf("Failed to get compute regional operation status: project=%s region=%s operationName=%s error=%s",
			projectNumber, region, operationName, vsaerrors.RedactProviderError(err.Error()))
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceFetchError, err)
	}
	if op != nil && op.Error != nil {
		if msg := firstComputeOperationErrorMessage(op); msg != "" {
			gcpService.Logger.Debugf("GetComputeRegionalOpStatus nested provider error (redacted): operationName=%s detail=%s",
				operationName, vsaerrors.RedactProviderError(msg))
		}
		gcpService.Logger.Errorf("GetComputeRegionalOpStatus operation failed: project=%s region=%s operationName=%s", projectNumber, region, operationName)
		return nil, newTerminalGCPOperationError()
	}
	gcpService.Logger.Debug(fmt.Sprintf("Fetching GetComputeRegionalOpStatus successful : %s", operationName))
	return convertComputeOpToComputeOp(op), nil
}

// GetZones retrieves a list of zones for a given project name using compute API. Reference : https://cloud.google.com/compute/docs/reference/rest/v1/zones/list
func (gcpService *GcpServices) GetZones(projectNumber, region string) ([]string, error) {
	projectName, err := getProjectIDFromNumber(gcpService, projectNumber)
	if err != nil {
		gcpService.Logger.Errorf("GetZones failed for project number : %s with error : %v", projectNumber, err)
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceFetchError, err)
	}

	gcpService.Logger.Debugf("Calling GetZones for project name : %s", projectName)

	regionUrl := "https://www.googleapis.com/compute/v1/projects/" + projectName + "/regions/" + region
	zoneList, err := gcpService.AdminGCPService.computeService.Zones.List(projectName).Filter("region= \"" + regionUrl + "\"").Context(gcpService.Ctx).Do()
	if err != nil {
		gcpService.Logger.Errorf("GetZones failed for project name : %s with error : %v", projectName, err.Error())
		return nil, err
	}
	var zones []string
	for _, zone := range zoneList.Items {
		zones = append(zones, zone.Name)
	}
	gcpService.Logger.Debugf("GetZones success with number of zones = %d", len(zones))
	return zones, nil
}

// IsMachineTypeAvailable checks if a specific machine type is available in a given project and zone using compute API. Reference : https://cloud.google.com/compute/docs/reference/rest/v1/machineTypes/get
func (gcpService *GcpServices) IsMachineTypeAvailable(projectNumber, zone, machineType string) (bool, error) {
	projectName, err := getProjectIDFromNumber(gcpService, projectNumber)
	if err != nil {
		gcpService.Logger.Errorf("IsMachineTypeAvailable failed for project number : %s with error : %v", projectNumber, err)
		return false, vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceFetchError, err)
	}
	gcpService.Logger.Debugf("Checking if machine type %s is available in project name : %s, zone : %s", machineType, projectName, zone)

	_, err = gcpService.AdminGCPService.computeService.MachineTypes.Get(projectName, zone, machineType).Context(gcpService.Ctx).Do()
	if err != nil {
		gcpService.Logger.Debugf("Machine type %s is not available in zone %s", machineType, zone)
		return false, nil
	}
	gcpService.Logger.Debugf("Machine type %s is available in zone %s", machineType, zone)
	return true, nil
}

// _getProjectIDFromNumber retrieves the project ID from the project number using compute API. Reference : https://cloud.google.com/compute/docs/reference/rest/v1/projects/get
func _getProjectIDFromNumber(gcpService *GcpServices, projectNumber string) (string, error) {
	project, err := gcpService.AdminGCPService.computeService.Projects.Get(projectNumber).Context(gcpService.Ctx).Do()
	if err != nil {
		return "", err
	}
	return project.Name, nil
}

// ResolveProjectNumberToName resolves a GCP project number to its project name/ID
// This is useful when dealing with service account emails that may contain project numbers instead of names
func (gcpService *GcpServices) ResolveProjectNumberToName(projectNumber string) (string, error) {
	return getProjectIDFromNumber(gcpService, projectNumber)
}

// ListAddressesWithFilter retrieves a list of addresses with optional filtering
// Parameters:
//   - projectName: GCP project name
//   - region: GCP region (use "global" for global addresses)
//   - subnetName: Optional subnet name to filter by (partial match)
//   - deploymentID: Optional deployment_id label to filter by
//   - additionalLabels: Optional additional labels to filter by (map of key-value pairs)
func (gcpService *GcpServices) ListAddressesWithFilter(projectName, region, subnetName, deploymentID string, additionalLabels map[string]string) (*[]models.Address, error) {
	gcpService.Logger.Debugf("Calling ListAddressesWithFilter for project: %s, region: %s, subnet: %s, deployment: %s",
		projectName, region, subnetName, deploymentID)

	// Build filter string
	var filterParts []string

	if projectName == "" {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceFetchError, errors.New("project name is required"))
	}

	// Add subnet filter if provided
	if subnetName != "" {
		subnetFilter := fmt.Sprintf("subnetwork=\"%s\"", subnetName)
		filterParts = append(filterParts, subnetFilter)
	}

	// Add deployment_id filter if provided
	if deploymentID != "" {
		deploymentFilter := fmt.Sprintf("labels.deployment_id=\"%s\"", deploymentID)
		filterParts = append(filterParts, deploymentFilter)
	}

	// Add additional label filters if provided
	if additionalLabels != nil {
		for key, value := range additionalLabels {
			labelFilter := fmt.Sprintf("labels.%s=\"%s\"", key, value)
			filterParts = append(filterParts, labelFilter)
		}
	}

	// Combine all filters with AND
	filter := strings.Join(filterParts, " AND ")

	gcpService.Logger.Debugf("Using filter: %s", filter)

	// Handle global addresses (region = "global")
	var addresses *compute.AddressList
	var err error

	if region == GlobalRegion {
		call := gcpService.AdminGCPService.computeService.GlobalAddresses.List(projectName).Context(gcpService.Ctx)
		if filter != "" {
			call = call.Filter(filter)
		}
		addresses, err = call.Do()
	} else {
		call := gcpService.AdminGCPService.computeService.Addresses.List(projectName, region).Context(gcpService.Ctx)
		if filter != "" {
			call = call.Filter(filter)
		}
		addresses, err = call.Do()
	}

	if err != nil {
		gcpService.Logger.Errorf("ListAddressesWithFilter failed for project: %s, region: %s, filter: %s with error: %v",
			projectName, region, filter, err.Error())
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceFetchError, err)
	}

	gcpService.Logger.Debugf("ListAddressesWithFilter success with number of addresses: %d", len(addresses.Items))
	return convertGoogleAddressesToAddresses(addresses), nil
}

// ListAddressesByDeployment retrieves addresses filtered by subnet and deployment_id
func (gcpService *GcpServices) ListAddressesByDeployment(projectName, region, deploymentID string) (*[]models.Address, error) {
	return gcpService.ListAddressesWithFilter(projectName, region, "", deploymentID, nil)
}

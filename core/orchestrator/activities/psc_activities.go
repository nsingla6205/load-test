package activities

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	dbutils "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	hyperscaler2 "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler"
	hyperscaler_models "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/models"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/activity"
)

var (
	CreateAddress              = _createAddress
	CreateForwardingRule       = _createForwardingRule
	GetClusterLogForwarding    = _getClusterLogForwarding
	GetSecurityAudit           = _getSecurityAudit
	DeleteForwardingRule       = _deleteForwardingRule
	DeleteAddress              = _deleteAddress
	GetPSCAddressName          = _getPSCAddressName
	GetAddressURI              = _getAddressURI
	GetForwardingRuleIPAddress = _getForwardingRuleIPAddress
)

type PSCActivity struct {
	SE database.Storage
}

const (
	GinLoggingSubnetName    = "sn-sre-01"
	InternalAddressType     = "INTERNAL"
	NotFoundString          = "not found"
	LogForwardingUserString = "user"
)

var (
	serviceAttachment         = env.GetString("GIN_SERVICE_ATTACHMENT", "")
	ginLoggingMetricsProtocol = env.GetString("GIN_METRICS_PROTOCOL", "tcp-unencrypted")
	ginLoggingMetricsPort     = env.GetInt64("GIN_METRICS_PORT", 5140)
	infraSubnetIpRange        = env.GetString("INFRA_SUBNET_IP_RANGE", "198.18.240.0/28")
)

func (j *PSCActivity) CreateInternalInfraSubnet(ctx context.Context, project string) (*[]commonparams.Operations, error) {
	serviceStruct, err := hyperscaler2.ResolveCloudService(ctx)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	service := hyperscaler2.Services(serviceStruct)

	// Record heartbeat to indicate progress to temporal server
	activity.RecordHeartbeat(ctx, "Setting up SRE Subnet for VSA pool")
	operations := make([]commonparams.Operations, 0)
	op := ""
	op, err = InsertSubnet(service, project, &Region, GinLoggingSubnetName, MgmtVpcName, infraSubnetIpRange)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	if op != "" {
		operations = append(operations, commonparams.Operations{
			OperationName:      op,
			OperationType:      "subnet",
			IsDone:             false,
			IsRegionalResource: true,
			Project:            project,
		})
	}

	return &operations, nil
}

func (j *PSCActivity) UpdateSecurityAudit(ctx context.Context, node *models.Node) error {
	activity.RecordHeartbeat(ctx, "Updating security audit settings")
	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	securityAudit, err := GetSecurityAudit(ctx, node)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	if securityAudit != nil {
		if securityAudit.Cli && securityAudit.Ontapi && securityAudit.HTTP {
			return nil
		}
		params := vsa.UpdateSecurityAuditParams{
			Cli:    true,
			Ontapi: true,
			HTTP:   true,
		}
		securityAudit, err = provider.UpdateSecurityAudit(params)
		if err != nil {
			return vsaerrors.WrapAsTemporalApplicationError(err)
		}
	}
	return nil
}

func _getSecurityAudit(ctx context.Context, node *models.Node) (*vsa.SecurityAudit, error) {
	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	resp, err := provider.GetSecurityAudit()
	if err != nil {
		return nil, err
	}

	if resp == nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(errors.New("Unable to retrieve security audit settings."))
	}
	return resp, nil
}

func (j *PSCActivity) CreateClusterLogForwarding(ctx context.Context, node *models.Node, address string) error {
	activity.RecordHeartbeat(ctx, "Creating log forwarding configuration")
	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	err = GetClusterLogForwarding(ctx, node, address, ginLoggingMetricsPort)
	if err != nil {
		if strings.Contains(err.Error(), NotFoundString) {
			user := LogForwardingUserString
			verifyServer := false
			// Create the forwarding request parameters
			securityLogForwardingParams := vsa.CreateSecurityLogForwardingParams{
				Address:      &address,
				Port:         &ginLoggingMetricsPort,
				Protocol:     &ginLoggingMetricsProtocol,
				Facility:     &user,
				VerifyServer: &verifyServer,
			}

			_, err := provider.CreateSecurityLogForwarding(securityLogForwardingParams)
			if err != nil {
				return vsaerrors.WrapAsTemporalApplicationError(err)
			}
		} else {
			return vsaerrors.WrapAsTemporalApplicationError(err)
		}
	}

	return nil
}

func (j *PSCActivity) CreateEMSEventForwarding(ctx context.Context, node *models.Node, address string) error {
	activity.RecordHeartbeat(ctx, "Creating EMS event forwarding configuration")
	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	// Create EMS event forwarding parameters
	emsEventForwardingParams := vsa.CreateEMSEventForwardingParams{
		DestinationName: "syslog-ems",
		DestinationIP:   address,
		DestinationPort: ginLoggingMetricsPort,
		Transport:       ginLoggingMetricsProtocol,
		TimestampFormat: "rfc-3164",
		MessageFormat:   "legacy-netapp",
		FilterName:      "syslog-ems",
		Severities:      []string{"INFORMATIONAL", "EMERGENCY", "ERROR", "ALERT", "NOTICE"},
	}

	err = provider.CreateEMSEventForwarding(emsEventForwardingParams)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	return nil
}

func (j *PSCActivity) DeleteEMSEventForwarding(ctx context.Context, node *models.Node) error {
	activity.RecordHeartbeat(ctx, "Deleting EMS event forwarding configuration")
	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	// Delete EMS event forwarding using the same names as creation
	destinationName := "syslog-ems"
	filterName := "syslog-ems"

	err = provider.DeleteEMSEventForwarding(destinationName, filterName)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	return nil
}

func _getClusterLogForwarding(ctx context.Context, node *models.Node, address string, port int64) error {
	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	// Create the forwarding request parameters
	securityLogForwardingParams := vsa.GetSecurityLogForwardingParams{
		Address: address,
		Port:    port,
	}

	err = provider.GetSecurityLogForwarding(securityLogForwardingParams)
	if err != nil {
		return err
	}

	return nil
}

func (j *PSCActivity) CreateForwardingRuleForPSCEndpoint(ctx context.Context, projectName string, region string, privateAddressName string, addressURI string) (*[]commonparams.Operations, error) {
	activity.RecordHeartbeat(ctx, fmt.Sprintf("Creating forwarding rule: %s", privateAddressName))
	var service hyperscaler2.Services
	service, err := hyperscaler2.ResolveCloudService(ctx)
	if err != nil {
		return nil, err
	}
	operations := make([]commonparams.Operations, 0)
	op := ""
	op, err = CreateForwardingRule(service, projectName, region, privateAddressName, MgmtVpcName, addressURI)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	if op != "" {
		operations = append(operations, commonparams.Operations{
			OperationName:      op,
			OperationType:      "forwardingrule",
			IsDone:             false,
			IsRegionalResource: true,
			Project:            projectName,
		})
	}
	return &operations, nil
}

func _createForwardingRule(gService hyperscaler2.Services, projectName string, region string, privateAddressName string, vpcName string, addressURI string) (string, error) {
	logger := gService.GetLogger()

	// first validate it does not exist already.
	forwardingRule, err := gService.GetForwardingRule(projectName, region, privateAddressName)
	if err != nil {
		resourceNotFound, errReceived := resourceNotFoundCheck(err.Error(), projectName, vpcName, "", privateAddressName, "")
		if !resourceNotFound {
			return "", errReceived
		}
	}
	if forwardingRule != nil {
		logger.Infof("Forwarding rule exists. Skipping creation. project name : %s , vpc name : %s, address name: %s", projectName, vpcName, privateAddressName)
		return "", nil
	}

	vpcNetwork, err := gService.GetVPCNetwork(projectName, vpcName)
	if err != nil {
		resourceNotFound, errReceived := resourceNotFoundCheck(err.Error(), projectName, vpcName, "", "", "")
		if !resourceNotFound {
			return "", errReceived
		}
		logger.Errorf("Failed to GetNetwork %v in region %s for project %s. Error : %v ", vpcName, region, projectName, err)
		return "", err
	}
	if vpcNetwork == nil || vpcNetwork.SelfLink == "" {
		errorMessage := fmt.Sprintf("Failed to GetNetwork %v in region %s for project %s", vpcName, region, projectName)
		logger.Error(errorMessage)
		return "", vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceFetchError, errors.New(errorMessage))
	}
	logger.Warnf("Creating forwarding rule %s in region %s for project %s using network: %+v, attachment: %+v and address: %+v", privateAddressName, region, projectName, vpcNetwork.SelfLink, serviceAttachment, addressURI)
	forwardingRuleRequest := &hyperscaler_models.ForwardingRule{
		Network:   vpcNetwork.SelfLink,
		Target:    serviceAttachment,
		IPAddress: addressURI,
		Region:    region,
		ProjectId: projectName,
		Name:      privateAddressName,
	}
	logger.Infof("forwardingRuleRequest : %+v ", forwardingRuleRequest)
	operationName := ""
	operationName, err = gService.CreateForwardingRuleOperation(forwardingRuleRequest)
	if err != nil {
		logger.Errorf("Failed to create forwarding rule %v for project %s", privateAddressName, projectName)
		return "", err
	}

	return operationName, err
}

func (j *PSCActivity) CreateAddressForPSCEndpoint(ctx context.Context, projectName string, region string, privateAddressName string) (*[]commonparams.Operations, error) {
	activity.RecordHeartbeat(ctx, fmt.Sprintf("Creating address: %s", privateAddressName))
	var service hyperscaler2.Services
	service, err := hyperscaler2.ResolveCloudService(ctx)
	if err != nil {
		return nil, err
	}
	operations := make([]commonparams.Operations, 0)
	op := ""
	op, err = CreateAddress(service, projectName, region, GinLoggingSubnetName, privateAddressName)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	if op != "" {
		operations = append(operations, commonparams.Operations{
			OperationName:      op,
			OperationType:      "ipaddress",
			IsDone:             false,
			IsRegionalResource: true,
			Project:            projectName,
		})
	}
	return &operations, nil
}

func (j *PSCActivity) DeleteForwardingRule(ctx context.Context, pool *datamodel.Pool) (*[]commonparams.Operations, error) {
	logger := util.GetLogger(ctx)
	se := j.SE
	conds := []*dbutils.FilterCondition{
		{Field: "account_id", Op: "=", Value: pool.AccountID},
		{Field: "network", Op: "=", Value: pool.Network},
		{Field: "state", Op: "<>", Value: datamodel.LifeCycleStateDeleted},
	}
	filter := &dbutils.Filter{Conditions: conds}
	pools, err := se.ListPools(ctx, filter)

	if err != nil {
		logger.Errorf("Failed to get pools for account: %d, network: %s, error: %s", pool.AccountID, pool.Network, err.Error())
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	if len(pools) > 1 {
		logger.Infof("Skipping release forwarding rule as there are other pools in the same region for the account. Account: %s, Network: %s", pool.Account.Name, pool.Network)
		return nil, nil
	}

	privateAddressName := GetPSCAddressName()
	activity.RecordHeartbeat(ctx, fmt.Sprintf("Deleting forwarding rule: %s", privateAddressName))
	var service hyperscaler2.Services
	service, err = hyperscaler2.ResolveCloudService(ctx)
	if err != nil {
		return nil, err
	}

	operations := make([]commonparams.Operations, 0)
	op := ""
	op, err = DeleteForwardingRule(service, MgmtVpcName, pool.ClusterDetails.RegionalTenantProject, privateAddressName, pool.ClusterDetails)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	logger.Infof("Deleted Forwarding Rule %s", privateAddressName)
	if op != "" {
		operations = append(operations, commonparams.Operations{
			OperationName:      op,
			OperationType:      "forwardingrule",
			IsDone:             false,
			IsRegionalResource: true,
			Project:            pool.ClusterDetails.RegionalTenantProject,
		})
	}
	return &operations, nil
}

func (j *PSCActivity) DeleteAddress(ctx context.Context, pool *datamodel.Pool) (*[]commonparams.Operations, error) {
	logger := util.GetLogger(ctx)
	se := j.SE
	conds := []*dbutils.FilterCondition{
		{Field: "account_id", Op: "=", Value: pool.AccountID},
		{Field: "network", Op: "=", Value: pool.Network},
		{Field: "state", Op: "<>", Value: datamodel.LifeCycleStateDeleted},
	}
	filter := &dbutils.Filter{Conditions: conds}
	pools, err := se.ListPools(ctx, filter)

	if err != nil {
		logger.Errorf("Failed to get pools for account: %d, network: %s, error: %s", pool.AccountID, pool.Network, err.Error())
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	if len(pools) > 1 {
		logger.Infof("Skipping release address as there are other pools in the same region for the account. Account: %s, Network: %s", pool.Account.Name, pool.Network)
		return nil, nil
	}

	privateAddressName := GetPSCAddressName()
	activity.RecordHeartbeat(ctx, fmt.Sprintf("Deleting address: %s", privateAddressName))
	var service hyperscaler2.Services
	service, err = hyperscaler2.ResolveCloudService(ctx)
	if err != nil {
		return nil, err
	}

	operations := make([]commonparams.Operations, 0)
	op := ""
	op, err = DeleteAddress(service, MgmtVpcName, pool.ClusterDetails.RegionalTenantProject, privateAddressName, pool.ClusterDetails)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	logger.Infof("Deleted Address %s", privateAddressName)
	if op != "" {
		operations = append(operations, commonparams.Operations{
			OperationName:      op,
			OperationType:      "ipaddress",
			IsDone:             false,
			IsRegionalResource: true,
			Project:            pool.ClusterDetails.RegionalTenantProject,
		})
	}
	return &operations, nil
}

func _createAddress(gService hyperscaler2.Services, projectName, region string, subNetwork, privateAddressName string) (string, error) {
	var subnetURI string
	logger := gService.GetLogger()

	// first validate it does not exist already.
	address, err := gService.GetAddress(projectName, region, privateAddressName)
	if err != nil {
		resourceNotFound, errReceived := resourceNotFoundCheck(err.Error(), projectName, "", subNetwork, privateAddressName, "")
		if !resourceNotFound {
			return "", errReceived
		}
	}
	if address != nil {
		logger.Infof("Address exists. Skipping creation. project name : %s , address name : %s", projectName, privateAddressName)
		return "", nil
	}

	// Get subnet from which private ip will be carved out
	subNet, err := gService.GetSubnetwork(projectName, region, subNetwork)
	if err != nil {
		resourceNotFound, errReceived := resourceNotFoundCheck(err.Error(), projectName, "", subNetwork, "", "")
		if !resourceNotFound {
			return "", errReceived
		}
		logger.Errorf("Error getting subnetwork for project : %s and subnetwork : %s. Error : %v ", projectName, subNetwork, err)
		return "", err
	}
	if subNet == nil || subNet.SelfLink == "" {
		errorMessage := fmt.Sprintf("Error getting subnetwork for project : %s and subnetwork : %s. ", projectName, subNetwork)
		logger.Error(errorMessage)
		return "", vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceFetchError, errors.New(errorMessage))
	}

	subnetURI = subNet.SelfLink
	addressRequest := &hyperscaler_models.Address{
		ProjectId:   projectName,
		Region:      region,
		Type:        InternalAddressType,
		SubnetURI:   subnetURI,
		AddressName: privateAddressName,
	}
	operationName := ""
	operationName, err = gService.CreateAddressOperation(addressRequest)
	if err != nil {
		logger.Errorf("Error creating address for project : %s and address name : %s. Error : %v ", projectName, privateAddressName, err)
		return "", err
	}

	return operationName, err
}

func (j *PSCActivity) GetAddressURI(ctx context.Context, projectName string, region string, privateAddressName string) (*string, error) {
	activity.RecordHeartbeat(ctx, "Retrieving address URI")
	service, err := hyperscaler2.ResolveCloudService(ctx)
	returnString := ""
	if err != nil {
		return &returnString, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	return GetAddressURI(service, projectName, region, privateAddressName)
}

func _getAddressURI(gService hyperscaler2.Services, projectName string, region string, privateAddressName string) (*string, error) {
	address, err := gService.GetAddress(projectName, region, privateAddressName)
	returnString := ""
	if err != nil {
		resourceNotFound, errReceived := resourceNotFoundCheck(err.Error(), projectName, "", "", privateAddressName, "")
		if !resourceNotFound {
			return &returnString, errReceived
		}
	}
	if address == nil || address.SelfLink == "" {
		return &returnString, nil
	}
	return &address.SelfLink, nil
}

func (j *PSCActivity) GetForwardingRuleIPAddress(ctx context.Context, projectName string, region string, privateAddressName string) (*string, error) {
	activity.RecordHeartbeat(ctx, "Retrieving forwarding rule IP address")
	service, err := hyperscaler2.ResolveCloudService(ctx)
	returnString := ""
	if err != nil {
		return &returnString, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	return _getForwardingRuleIPAddress(service, projectName, region, privateAddressName)
}

func _getForwardingRuleIPAddress(gService hyperscaler2.Services, projectName string, region string, endpointName string) (*string, error) {
	forwardingRule, err := gService.GetForwardingRule(projectName, region, endpointName)
	returnString := ""
	if err != nil {
		resourceNotFound, errReceived := resourceNotFoundCheck(err.Error(), projectName, "", "", endpointName, "")
		if !resourceNotFound {
			return &returnString, errReceived
		}
	}
	if forwardingRule == nil || forwardingRule.IPAddress == "" {
		return &returnString, nil
	}
	return &forwardingRule.IPAddress, nil
}

func _deleteForwardingRule(service hyperscaler2.Services, consumerVpc, accountName, addressName string, clusterDetails datamodel.ClusterDetails) (string, error) {
	logger := service.GetLogger()
	tenantProjectNumber := ""
	var err error
	if clusterDetails.RegionalTenantProject != "" {
		tenantProjectNumber = clusterDetails.RegionalTenantProject
	} else {
		tenantProjectNumber, err = service.GetTenantProject(consumerVpc, accountName, Region)
		if err != nil {
			logger.Errorf("Error finding tenancy unit: %v", err)
			return "", err
		}
	}

	// Check if the forwarding rule exists
	_, err = service.GetForwardingRule(tenantProjectNumber, Region, addressName)
	operationName := ""
	if err == nil {
		operationName, err = service.DeleteForwardingRule(Region, tenantProjectNumber, addressName)
		if err != nil {
			logger.Errorf("Error Releasing forwarding rule: %v", err)
			// To avoid returning an error here, in the case of activity restart, we log it and continue.
		}
	} else {
		logger.Errorf("Error getting forwarding rule %s project %s, skipping release", addressName, accountName)
	}

	return operationName, nil
}

func _deleteAddress(service hyperscaler2.Services, consumerVpc, accountName, addressName string, clusterDetails datamodel.ClusterDetails) (string, error) {
	logger := service.GetLogger()
	tenantProjectNumber := ""
	var err error
	if clusterDetails.RegionalTenantProject != "" {
		tenantProjectNumber = clusterDetails.RegionalTenantProject
	} else {
		tenantProjectNumber, err = service.GetTenantProject(consumerVpc, accountName, Region)
		if err != nil {
			logger.Errorf("Error finding tenancy unit: %v", err)
			return "", err
		}
	}

	operationName := ""
	// Check if the address exists
	_, err = service.GetAddress(tenantProjectNumber, Region, addressName)
	if err == nil {
		operationName, err = service.ReleaseAddress(Region, tenantProjectNumber, addressName)
		if err != nil {
			logger.Errorf("Error Releasing address: %v", err)
			// To avoid returning an error here, in the case of activity restart, we log it and continue.
		}
	} else {
		logger.Errorf("Error getting address %s project %s, skipping release", addressName, accountName)
	}

	return operationName, nil
}

func _getPSCAddressName() string {
	return Region + "-rg-fluent-bit-psc"
}

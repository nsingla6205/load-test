package google

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/common"
	models "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/models"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"google.golang.org/api/compute/v1"
	"google.golang.org/api/dns/v1"
	"google.golang.org/api/privateca/v1"
	"google.golang.org/api/secretmanager/v1"
	"google.golang.org/api/servicenetworking/v1"
)

var (
	ValidateAndConvertPrivateCACertificateToCustomCertificate = _validateAndConvertPrivateCACertificateToCustomCertificate
	ConvertSecretToCustomSecret                               = _convertSecretToCustomSecret
	ValidateAndConvertToCustomCloudDNSRecord                  = _validateAndConvertToCustomCloudDNSRecord
)

func convertComputeOpToComputeOp(op *compute.Operation) *models.ComputeOperation {
	if op == nil {
		return nil
	}
	return &models.ComputeOperation{
		Name:     op.Name,
		Status:   op.Status,
		Progress: op.Progress,
		Done:     op.Status == "DONE",
	}
}

func convertServiceNetOpToComputeOp(op *servicenetworking.Operation) *models.ComputeOperation {
	if op == nil {
		return nil
	}
	operation := &models.ComputeOperation{
		Name:     op.Name,
		Done:     op.Done,
		Response: op.Response,
	}
	if op.Error != nil {
		operation.ErrorResponse = op.Error.Message
	}
	return operation
}

func convertGoogleSubnetToSubnet(subnet *compute.Subnetwork) *models.Subnet {
	if subnet == nil {
		return nil
	}
	secondaries := make([]models.SecondaryIpRange, 0, len(subnet.SecondaryIpRanges))
	for _, sr := range subnet.SecondaryIpRanges {
		if sr == nil {
			continue
		}
		secondaries = append(secondaries, models.SecondaryIpRange{
			RangeName:   sr.RangeName,
			IpCidrRange: sr.IpCidrRange,
		})
	}
	return &models.Subnet{
		Name:              subnet.Name,
		Network:           subnet.Network,
		IpCidrRange:       subnet.IpCidrRange,
		GatewayAddress:    subnet.GatewayAddress,
		SelfLink:          subnet.SelfLink,
		SecondaryIpRanges: secondaries,
	}
}

func convertGoogleSubnetsToSubnets(subnets *compute.SubnetworkList) *[]models.Subnet {
	if subnets == nil {
		return nil
	}
	returnSubnets := make([]models.Subnet, len(subnets.Items))
	for i, subnet := range subnets.Items {
		returnSubnets[i] = *convertGoogleSubnetToSubnet(subnet)
	}
	return &returnSubnets
}

func convertSubnetToGoogleSubnet(subnet *models.Subnet) *compute.Subnetwork {
	if subnet == nil {
		return nil
	}
	return &compute.Subnetwork{
		Name:                  subnet.Name,
		Network:               subnet.Network,
		IpCidrRange:           subnet.IpCidrRange,
		PrivateIpGoogleAccess: true,
	}
}

func convertGoogleVPCToVPC(vpc *compute.Network) *models.VPCNetwork {
	if vpc == nil {
		return nil
	}
	return &models.VPCNetwork{
		Name:        vpc.Name,
		ProjectName: vpc.SelfLink,
		SelfLink:    vpc.SelfLink,
	}
}

func convertVPCToGoogleVPC(vpc *models.VPCNetwork) *compute.Network {
	if vpc == nil {
		return nil
	}
	return &compute.Network{
		Name:                  vpc.Name,
		AutoCreateSubnetworks: false,
		Mtu:                   8896,
		// make sure AutoCreateSubnetworks field is included in request
		ForceSendFields: []string{"AutoCreateSubnetworks"},
	}
}

func convertToGoogleFirewallRule(firewallRequest *models.Firewall) *compute.Firewall {
	if firewallRequest == nil {
		return nil
	}
	return &compute.Firewall{
		Name:         firewallRequest.Name,
		Description:  "Allow traffic on specific ports for " + firewallRequest.Name,
		Network:      fmt.Sprintf("projects/%s/global/networks/%s", firewallRequest.ProjectName, firewallRequest.VPCNetworkName),
		Allowed:      getFirewallAllowedRulesGCP(firewallRequest.AllowedPortRules),
		SourceRanges: firewallRequest.SourceRanges,
		Direction:    firewallRequest.Direction,
		Priority:     firewallRequest.Priority,
	}
}

func convertGCPFirewallToFirewall(firewall *compute.Firewall) *models.Firewall {
	if firewall == nil {
		return nil
	}
	return &models.Firewall{
		Name:           firewall.Name,
		Description:    firewall.Description,
		VPCNetworkName: firewall.Network,
		SourceRanges:   firewall.SourceRanges,
	}
}

func convertGCPAddressToAddress(address *compute.Address) *models.Address {
	if address == nil {
		return nil
	}
	return &models.Address{
		AddressName: address.Name,
		Region:      address.Region,
		SelfLink:    address.SelfLink,
		SubnetURI:   address.Subnetwork,
	}
}

func convertAddressToGoogleAddress(address *models.Address) *compute.Address {
	if address == nil {
		return nil
	}
	return &compute.Address{
		Name:        address.AddressName,
		AddressType: address.Type,
		Region:      address.Region,
		Subnetwork:  address.SubnetURI,
		SelfLink:    address.SelfLink,
	}
}

func convertForwardingRuleToGoogleForwardingRule(forwardingRule *models.ForwardingRule) *compute.ForwardingRule {
	if forwardingRule == nil {
		return nil
	}
	return &compute.ForwardingRule{
		Name:      forwardingRule.Name,
		IPAddress: forwardingRule.IPAddress,
		Network:   forwardingRule.Network,
		Region:    forwardingRule.Region,
		Target:    forwardingRule.Target,
	}
}
func convertGCPForwardingRuleToForwardingRule(forwardingRule *compute.ForwardingRule) *models.ForwardingRule {
	if forwardingRule == nil {
		return nil
	}
	return &models.ForwardingRule{
		IPAddress: forwardingRule.IPAddress,
		Network:   forwardingRule.Network,
		SelfLink:  forwardingRule.SelfLink,
		Region:    forwardingRule.Region,
		Target:    forwardingRule.Target,
		Name:      forwardingRule.Name,
	}
}
func getFirewallAllowedRulesGCP(allowedPortRules []string) []*compute.FirewallAllowed {
	firewallAllowedPortRules := []*compute.FirewallAllowed{}
	for _, rule := range allowedPortRules {
		// Check if it's a single port number
		if _, err := strconv.Atoi(rule); err == nil {
			if len(firewallAllowedPortRules) > 0 {
				lastRule := firewallAllowedPortRules[len(firewallAllowedPortRules)-1]
				lastRule.Ports = append(lastRule.Ports, rule)
			}
		} else if isPortRange(rule) {
			// Handle port ranges like "63001-65000"
			if len(firewallAllowedPortRules) > 0 {
				lastRule := firewallAllowedPortRules[len(firewallAllowedPortRules)-1]
				lastRule.Ports = append(lastRule.Ports, rule)
			}
		} else {
			// Treat as protocol
			firewallAllowedPortRules = append(firewallAllowedPortRules, &compute.FirewallAllowed{IPProtocol: rule})
		}
	}
	return firewallAllowedPortRules
}

// isPortRange checks if a string represents a valid port range (e.g., "63001-65000")
func isPortRange(rule string) bool {
	parts := strings.Split(rule, "-")
	if len(parts) != 2 {
		return false
	}
	// Check if both parts are valid port numbers
	startPort, err1 := strconv.Atoi(parts[0])
	endPort, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return false
	}
	// Validate port range (1-65535)
	return startPort >= 1 && startPort <= 65535 && endPort >= 1 && endPort <= 65535 && startPort <= endPort
}

func _validateAndConvertPrivateCACertificateToCustomCertificate(certificateId string, cert *privateca.Certificate) (*models.CustomCertificate, error) {
	if cert == nil {
		return nil, vsaerrors.ExtractCustomError(fmt.Errorf("input certificate is nil"))
	}
	customCert, err := convertPrivateCACertificateToCustomCertificate(certificateId, cert)
	if err != nil {
		return nil, err
	}
	if cert.CertificateDescription != nil && cert.CertificateDescription.SubjectDescription != nil {
		if cert.CertificateDescription.SubjectDescription.Subject != nil {
			if cert.CertificateDescription.SubjectDescription.Subject.CommonName != "" {
				customCert.SubjectCommonName = cert.CertificateDescription.SubjectDescription.Subject.CommonName
			}
			if cert.CertificateDescription.SubjectDescription.Subject.Organization != "" {
				customCert.SubjectOrganization = cert.CertificateDescription.SubjectDescription.Subject.Organization
			}
			if cert.CertificateDescription.SubjectDescription.SubjectAltName.DnsNames != nil {
				customCert.SubjectAltName = cert.CertificateDescription.SubjectDescription.SubjectAltName.DnsNames
			}
		}
	}
	return customCert, nil
}

func _convertSecretToCustomSecret(secret *secretmanager.Secret, secretVersion *models.CustomSecretVersion) (*models.CustomSecret, error) {
	if secret == nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrResourceEmptyError, fmt.Errorf("input secret is nil"))
	}
	if secretVersion == nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrResourceEmptyError, fmt.Errorf("input secret version is nil"))
	}
	createTime, err := common.ParseTimestamps(secret.CreateTime)
	if err != nil {
		return nil, err
	}
	lifeTime, err := common.ParseTimestamps(secret.ExpireTime)
	if err != nil {
		return nil, err
	}

	version, err := common.ConvertToCustomSecretVersion(secretVersion.Name, secretVersion.Value)
	if err != nil {
		return nil, err
	}
	customCert := &models.CustomSecret{
		Name:          secret.Name,
		CreateTime:    createTime,
		LifeTime:      lifeTime,
		SecretVersion: version,
	}

	return customCert, nil
}

// extractCANameFromAuthority extracts the CA name from the certificate authority URI
// Expected format: projects/{project}/locations/{location}/caPools/{pool}/certificateAuthorities/{ca}
func extractCANameFromAuthority(authority string) (string, error) {
	if authority == "" {
		return "", nil
	}

	// Expected format: projects/.../certificateAuthorities/{ca}
	const prefix = "certificateAuthorities/"
	idx := strings.LastIndex(authority, prefix)
	if idx == -1 {
		return "", fmt.Errorf("invalid authority format: %s", authority)
	}

	caName := authority[idx+len(prefix):]
	if caName == "" {
		return "", fmt.Errorf("empty CA name in authority: %s", authority)
	}

	return caName, nil
}

func convertPrivateCACertificateToCustomCertificate(certificateId string, cert *privateca.Certificate) (*models.CustomCertificate, error) {
	if cert == nil {
		return nil, vsaerrors.ExtractCustomError(fmt.Errorf("input certificate is nil"))
	}
	createTime, err := common.ParseTimestamps(cert.CreateTime)
	if err != nil {
		return nil, err
	}

	// Extract serial number from certificate description
	var serialNumber string
	if cert.CertificateDescription != nil && cert.CertificateDescription.SubjectDescription != nil {
		serialNumber = cert.CertificateDescription.SubjectDescription.HexSerialNumber
	}

	// Extract CA name from IssuerCertificateAuthority with validation
	// Format: projects/{project}/locations/{location}/caPools/{pool}/certificateAuthorities/{ca}
	caName, err := extractCANameFromAuthority(cert.IssuerCertificateAuthority)
	if err != nil {
		return nil, vsaerrors.ExtractCustomError(fmt.Errorf("failed to extract CA name: %w", err))
	}

	customCert := &models.CustomCertificate{
		CertificateID:              certificateId,
		Name:                       cert.Name,
		PemCertificate:             cert.PemCertificate,
		CreateTime:                 createTime,
		LifeTime:                   cert.Lifetime,
		PemCertificateChain:        cert.PemCertificateChain,
		IssuerCertificateAuthority: cert.IssuerCertificateAuthority,
		SerialNumber:               serialNumber, // Set the serial number
		CaName:                     caName,       // Set the CA name
	}
	return customCert, nil
}

func _validateAndConvertToCustomCloudDNSRecord(recordSet *dns.ResourceRecordSet, managedZone string) (*models.CustomCloudDNSRecord, error) {
	if recordSet == nil {
		return nil, vsaerrors.ExtractCustomError(fmt.Errorf("resource record set is nil"))
	}
	if recordSet.Name == "" || recordSet.Type == "" || recordSet.Ttl == 0 || recordSet.Rrdatas == nil || len(recordSet.Rrdatas) == 0 {
		return nil, vsaerrors.ExtractCustomError(fmt.Errorf("resource record set is invalid"))
	}
	return &models.CustomCloudDNSRecord{
		RecordName:  recordSet.Name,
		Type:        recordSet.Type,
		TTL:         recordSet.Ttl,
		Data:        recordSet.Rrdatas[0],
		ManagedZone: managedZone,
	}, nil
}

func convertGoogleAddressesToAddresses(addresses *compute.AddressList) *[]models.Address {
	if addresses == nil || len(addresses.Items) == 0 {
		return &[]models.Address{}
	}

	var result []models.Address
	for _, address := range addresses.Items {
		convertedAddress := convertGCPAddressToAddress(address)
		if convertedAddress != nil {
			result = append(result, *convertedAddress)
		}
	}

	return &result
}

package google

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	models "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/models"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"google.golang.org/api/compute/v1"
	"google.golang.org/api/dns/v1"
	"google.golang.org/api/privateca/v1"
	"google.golang.org/api/secretmanager/v1"
	"google.golang.org/api/servicenetworking/v1"
)

func Test_convertComputeOpToComputeOp(t *testing.T) {
	t.Run("WhenOperationIsNil", func(tt *testing.T) {
		result := convertComputeOpToComputeOp(nil)
		assert.Nil(tt, result, "Expected result to be nil when input operation is nil")
	})

	t.Run("WhenOperationIsValid", func(tt *testing.T) {
		input := &compute.Operation{
			Name:     "test-operation",
			Status:   "DONE",
			Progress: 100,
		}

		expected := &models.ComputeOperation{
			Name:     "test-operation",
			Status:   "DONE",
			Progress: 100,
			Done:     true,
		}

		result := convertComputeOpToComputeOp(input)
		assert.Equal(tt, expected, result, "Expected result to match the converted operation")
	})
}

func Test_convertServiceNetOpToComputeOp(t *testing.T) {
	t.Run("WhenOperationIsNil", func(tt *testing.T) {
		result := convertServiceNetOpToComputeOp(nil)
		assert.Nil(tt, result, "Expected result to be nil when input operation is nil")
	})

	t.Run("WhenErrorResponseShouldBeEmpty", func(tt *testing.T) {
		input := &servicenetworking.Operation{
			Name: "test-operation",
			Done: true,
		}

		expected := &models.ComputeOperation{
			Name:          "test-operation",
			Done:          true,
			ErrorResponse: "",
		}

		result := convertServiceNetOpToComputeOp(input)
		assert.Equal(tt, expected.ErrorResponse, result.ErrorResponse, "")
		assert.Equal(tt, expected, result, "Expected result to match the converted operation")
	})
	t.Run("WhenOperationIsValid", func(tt *testing.T) {
		input := &servicenetworking.Operation{
			Name:  "test-operation",
			Done:  true,
			Error: &servicenetworking.Status{Code: 400, Message: "test-error"},
		}

		expected := &models.ComputeOperation{
			Name:          "test-operation",
			Done:          true,
			ErrorResponse: "test-error",
		}

		result := convertServiceNetOpToComputeOp(input)
		assert.Equal(tt, expected, result, "Expected result to match the converted operation")
	})
}

func Test_convertGoogleSubnetToSubnet(t *testing.T) {
	t.Run("WhenSubnetIsNil", func(tt *testing.T) {
		result := convertGoogleSubnetToSubnet(nil)
		assert.Nil(tt, result, "Expected result to be nil when input subnet is nil")
	})

	t.Run("WhenSubnetIsValid", func(tt *testing.T) {
		input := &compute.Subnetwork{
			Name:        "test-subnet",
			Network:     "test-network",
			IpCidrRange: "10.0.0.0/24",
			SelfLink:    "projects/test-project/regions/us-central1/subnetworks/test-subnet",
			SecondaryIpRanges: []*compute.SubnetworkSecondaryRange{
				{RangeName: "ic-lif-10-1-0-0-29-abc", IpCidrRange: "10.1.0.0/29"},
			},
		}

		expected := &models.Subnet{
			Name:        "test-subnet",
			Network:     "test-network",
			IpCidrRange: "10.0.0.0/24",
			SelfLink:    "projects/test-project/regions/us-central1/subnetworks/test-subnet",
			SecondaryIpRanges: []models.SecondaryIpRange{
				{RangeName: "ic-lif-10-1-0-0-29-abc", IpCidrRange: "10.1.0.0/29"},
			},
		}

		result := convertGoogleSubnetToSubnet(input)
		assert.Equal(tt, expected, result, "Expected result to match the converted subnet")
	})

	t.Run("WhenSecondaryRangeEntryIsNil", func(tt *testing.T) {
		input := &compute.Subnetwork{
			Name:        "test-subnet",
			Network:     "test-network",
			IpCidrRange: "10.0.0.0/24",
			SecondaryIpRanges: []*compute.SubnetworkSecondaryRange{
				nil,
				{RangeName: "ic-lif-10-1-0-0-29-abc", IpCidrRange: "10.1.0.0/29"},
			},
		}

		result := convertGoogleSubnetToSubnet(input)
		require.NotNil(tt, result)
		require.Len(tt, result.SecondaryIpRanges, 1)
		assert.Equal(tt, "10.1.0.0/29", result.SecondaryIpRanges[0].IpCidrRange)
	})
}

func Test_convertSubnetToGoogleSubnet(t *testing.T) {
	t.Run("WhenSubnetIsNil", func(tt *testing.T) {
		result := convertSubnetToGoogleSubnet(nil)
		assert.Nil(tt, result, "Expected result to be nil when input subnet is nil")
	})

	t.Run("WhenSubnetIsValid", func(tt *testing.T) {
		input := &models.Subnet{
			Name:        "test-subnet",
			Network:     "test-network",
			IpCidrRange: "10.0.0.0/24",
		}

		expected := &compute.Subnetwork{
			Name:                  "test-subnet",
			Network:               "test-network",
			IpCidrRange:           "10.0.0.0/24",
			PrivateIpGoogleAccess: true,
		}

		result := convertSubnetToGoogleSubnet(input)
		assert.Equal(tt, expected, result, "Expected result to match the converted subnet")
	})
}

func Test_convertGoogleVPCToVPC(t *testing.T) {
	t.Run("WhenVPCIsNil", func(tt *testing.T) {
		result := convertGoogleVPCToVPC(nil)
		assert.Nil(tt, result, "Expected result to be nil when input VPC is nil")
	})

	t.Run("WhenVPCIsValid", func(tt *testing.T) {
		input := &compute.Network{
			Name:     "test-vpc",
			SelfLink: "projects/test-project/global/networks/test-vpc",
		}

		expected := &models.VPCNetwork{
			Name:        "test-vpc",
			ProjectName: "projects/test-project/global/networks/test-vpc",
			SelfLink:    "projects/test-project/global/networks/test-vpc",
		}

		result := convertGoogleVPCToVPC(input)
		assert.Equal(tt, expected, result, "Expected result to match the converted VPC")
	})
}

func Test_convertVPCToGoogleVPC(t *testing.T) {
	t.Run("WhenVPCIsNil", func(tt *testing.T) {
		result := convertVPCToGoogleVPC(nil)
		assert.Nil(tt, result, "Expected result to be nil when input VPC is nil")
	})

	t.Run("WhenVPCIsValid", func(tt *testing.T) {
		input := &models.VPCNetwork{
			Name: "test-vpc",
		}

		expected := &compute.Network{
			Name:                  "test-vpc",
			AutoCreateSubnetworks: false,
			Mtu:                   8896,
			ForceSendFields:       []string{"AutoCreateSubnetworks"},
		}

		result := convertVPCToGoogleVPC(input)
		assert.Equal(tt, expected, result, "Expected result to match the converted VPC")
	})
}

func Test_convertToGoogleFirewallRule(t *testing.T) {
	t.Run("WhenFirewallRequestIsNil", func(tt *testing.T) {
		result := convertToGoogleFirewallRule(nil)
		assert.Nil(tt, result, "Expected result to be nil when input firewall request is nil")
	})

	t.Run("WhenFirewallRequestIsValid", func(tt *testing.T) {
		input := &models.Firewall{
			Name:             "test-firewall",
			ProjectName:      "test-project",
			VPCNetworkName:   "test-vpc",
			AllowedPortRules: []string{"tcp", "udp"},
			SourceRanges:     []string{"0.0.0.0/0"},
			Direction:        "INGRESS",
			Priority:         1000,
		}

		expected := &compute.Firewall{
			Name:         "test-firewall",
			Description:  "Allow traffic on specific ports for test-firewall",
			Network:      "projects/test-project/global/networks/test-vpc",
			Allowed:      []*compute.FirewallAllowed{{IPProtocol: "tcp"}, {IPProtocol: "udp"}},
			SourceRanges: []string{"0.0.0.0/0"},
			Direction:    "INGRESS",
			Priority:     1000,
		}

		result := convertToGoogleFirewallRule(input)
		assert.Equal(tt, expected, result, "Expected result to match the converted firewall rule")
	})
}

func Test_convertGCPFirewallToFirewall(t *testing.T) {
	t.Run("WhenFirewallIsNil", func(tt *testing.T) {
		result := convertGCPFirewallToFirewall(nil)
		assert.Nil(tt, result, "Expected result to be nil when input firewall is nil")
	})

	t.Run("WhenFirewallIsValid", func(tt *testing.T) {
		input := &compute.Firewall{
			Name:         "test-firewall",
			Description:  "Allow incoming traffic",
			Network:      "test-vpc",
			SourceRanges: []string{"0.0.0.0/0"},
		}

		expected := &models.Firewall{
			Name:           "test-firewall",
			Description:    "Allow incoming traffic",
			VPCNetworkName: "test-vpc",
			SourceRanges:   []string{"0.0.0.0/0"},
		}

		result := convertGCPFirewallToFirewall(input)
		assert.Equal(tt, expected, result, "Expected result to match the converted firewall")
	})
}

func Test_convertGCPAddressToAddress(t *testing.T) {
	t.Run("WhenAddressIsNil", func(tt *testing.T) {
		result := convertGCPAddressToAddress(nil)
		assert.Nil(tt, result, "Expected result to be nil when input address is nil")
	})

	t.Run("WhenAddressIsValid", func(tt *testing.T) {
		input := &compute.Address{
			Name:     "test-address",
			Region:   "test-region",
			SelfLink: "projects/test-project/regions/us-central1/addresses/test-address",
		}

		expected := &models.Address{
			AddressName: "test-address",
			Region:      "test-region",
			SelfLink:    "projects/test-project/regions/us-central1/addresses/test-address",
		}

		result := convertGCPAddressToAddress(input)
		assert.Equal(tt, expected, result, "Expected result to match the converted address")
	})
}

func Test_convertAddressToGoogleAddress(t *testing.T) {
	t.Run("WhenAddressIsNil", func(tt *testing.T) {
		result := convertAddressToGoogleAddress(nil)
		assert.Nil(tt, result, "Expected result to be nil when input address is nil")
	})

	t.Run("WhenAddressIsValid", func(tt *testing.T) {
		input := &models.Address{
			AddressName: "test-address",
			Region:      "test-region",
			SelfLink:    "projects/test-project/regions/us-central1/addresses/test-address",
		}

		expected := &compute.Address{
			Name:     "test-address",
			Region:   "test-region",
			SelfLink: "projects/test-project/regions/us-central1/addresses/test-address",
		}

		result := convertAddressToGoogleAddress(input)
		assert.Equal(tt, expected, result, "Expected result to match the converted address")
	})
}

func Test_convertForwardingRuleToGoogleForwardingRule(t *testing.T) {
	t.Run("WhenForwardingRuleIsNil", func(tt *testing.T) {
		result := convertForwardingRuleToGoogleForwardingRule(nil)
		assert.Nil(tt, result, "Expected result to be nil when input forwarding rule is nil")
	})

	t.Run("WhenForwardingRuleIsValid", func(tt *testing.T) {
		input := &models.ForwardingRule{
			Name:      "test-address",
			IPAddress: "test-ip",
			Network:   "test-region",
			Region:    "test-region",
			Target:    "test-target",
		}

		expected := &compute.ForwardingRule{
			Name:      "test-address",
			IPAddress: "test-ip",
			Network:   "test-region",
			Region:    "test-region",
			Target:    "test-target",
		}

		result := convertForwardingRuleToGoogleForwardingRule(input)
		assert.Equal(tt, expected, result, "Expected result to match the forwarding rule")
	})
}

func Test_convertGCPForwardingRuleToForwardingRule(t *testing.T) {
	t.Run("WhenForwardingRuleIsNil", func(tt *testing.T) {
		result := convertGCPForwardingRuleToForwardingRule(nil)
		assert.Nil(tt, result, "Expected result to be nil when input forwarding rule is nil")
	})

	t.Run("WhenForwardingRuleIsValid", func(tt *testing.T) {
		input := &compute.ForwardingRule{
			Name:      "test-address",
			IPAddress: "test-ip",
			Network:   "test-region",
			Region:    "test-region",
			Target:    "test-target",
		}

		expected := &models.ForwardingRule{
			Name:      "test-address",
			IPAddress: "test-ip",
			Network:   "test-region",
			Region:    "test-region",
			Target:    "test-target",
		}

		result := convertGCPForwardingRuleToForwardingRule(input)
		assert.Equal(tt, expected, result, "Expected result to match the converted forwarding rule")
	})
}

func Test_getFirewallAllowedRulesGCP(t *testing.T) {
	t.Run("WhenAllowedPortRulesIsEmpty", func(tt *testing.T) {
		allowedPortRules := []string{}
		result := getFirewallAllowedRulesGCP(allowedPortRules)
		assert.Empty(tt, result, "Expected result to be empty when allowedPortRules is empty")
	})

	t.Run("WhenAllowedPortRulesHasProtocols", func(tt *testing.T) {
		allowedPortRules := []string{"tcp", "udp", "icmp"}
		result := getFirewallAllowedRulesGCP(allowedPortRules)

		expected := []*compute.FirewallAllowed{
			{IPProtocol: "tcp"},
			{IPProtocol: "udp"},
			{IPProtocol: "icmp"},
		}

		assert.Equal(tt, expected, result, "Expected result to match the allowedPortRules")
	})

	t.Run("WhenPortRulesHasNFSPorts", func(tt *testing.T) {
		allowedPortRules := []string{"tcp", "111", "635", "2049", "4045", "udp", "111", "4046", "4049"}
		result := getFirewallAllowedRulesGCP(allowedPortRules)

		expected := []*compute.FirewallAllowed{
			{
				IPProtocol: "tcp",
				Ports:      []string{"111", "635", "2049", "4045"},
			},
			{
				IPProtocol: "udp",
				Ports:      []string{"111", "4046", "4049"},
			},
		}

		assert.Equal(tt, expected, result, "Expected result to match the allowedPortRules")
	})

	t.Run("WhenAllowedPortRulesHasPorts", func(tt *testing.T) {
		allowedPortRules := []string{"tcp", "3290"}
		result := getFirewallAllowedRulesGCP(allowedPortRules)

		expected := []*compute.FirewallAllowed{
			{IPProtocol: "tcp", Ports: []string{"3290"}},
		}

		assert.Equal(tt, expected, result, "Expected result to match the allowedPortRules")
	})
	t.Run("WhenAllowedPortRulesHasPorts1", func(tt *testing.T) {
		allowedPortRules := []string{"tcp", "3290", "90"}
		result := getFirewallAllowedRulesGCP(allowedPortRules)

		expected := []*compute.FirewallAllowed{
			{IPProtocol: "tcp", Ports: []string{"3290", "90"}},
		}

		assert.Equal(tt, expected, result, "Expected result to match the allowedPortRules")
	})
	t.Run("WhenAllowedPortRulesHasPortsAndProtocols", func(tt *testing.T) {
		allowedPortRules := []string{"tcp", "3290", "90", "udp", "icmp"}
		result := getFirewallAllowedRulesGCP(allowedPortRules)

		expected := []*compute.FirewallAllowed{
			{IPProtocol: "tcp", Ports: []string{"3290", "90"}},
			{IPProtocol: "udp"},
			{IPProtocol: "icmp"},
		}

		assert.Equal(tt, expected, result, "Expected result to match the allowedPortRules")
	})
	t.Run("WhenMultiplePortsMultipleProtocols", func(tt *testing.T) {
		allowedPortRules := []string{"tcp", "3290", "90", "udp", "223", "icmp", "443"}
		result := getFirewallAllowedRulesGCP(allowedPortRules)

		expected := []*compute.FirewallAllowed{
			{IPProtocol: "tcp", Ports: []string{"3290", "90"}},
			{IPProtocol: "udp", Ports: []string{"223"}},
			{IPProtocol: "icmp", Ports: []string{"443"}},
		}

		assert.Equal(tt, expected, result, "Expected result to match the allowedPortRules")
	})

	t.Run("WhenAllowedPortRulesHasPortRanges", func(tt *testing.T) {
		allowedPortRules := []string{"tcp", "111", "635", "2049", "4045", "udp", "111", "4046", "4049", "63001-65000"}
		result := getFirewallAllowedRulesGCP(allowedPortRules)

		expected := []*compute.FirewallAllowed{
			{
				IPProtocol: "tcp",
				Ports:      []string{"111", "635", "2049", "4045"},
			},
			{
				IPProtocol: "udp",
				Ports:      []string{"111", "4046", "4049", "63001-65000"},
			},
		}

		assert.Equal(tt, expected, result, "Expected result to match the allowedPortRules with port ranges")
	})
}
func TestValidateEnvironmentVariables(t *testing.T) {
	originalAuthtype := env.AuthType
	originalSecretmanagerProjectID := env.SecretManagerProjectID
	originalCaDeployedProjectID := env.CaPoolDeployedProjectID
	originalCaPoolName := env.CaPoolName
	originalCaName := env.CaName
	originalVsaDeployedDnsName := env.VsaDeployedDnsName
	originalVsaManagedZone := env.VsaManagedZone
	originalCertificateLifetime := env.CertificateLifetime
	originalRegion := env.Region
	originalCloudDNSCacheTTL := env.CloudDNSCacheTTL
	orignalNodePassword := env.NodePassword
	originalMgmtRegionalNatIP := env.MgmtRegionalNatIP
	originalMgmtFirewallSourceRanges := env.MgmtFirewallSourceRanges
	originalRsmFirewallSourceRanges := env.RsmFirewallSourceRanges
	originalIcFirewallSourceRanges := env.IcFirewallSourceRanges
	originalDataFirewallSourceRanges := env.DataFirewallSourceRanges
	originalMgmtNetworkIpRange := env.MgmtNetworkIpRange
	originalRsmNetworkIpRange := env.RsmNetworkIpRange
	originalIcNetworkIpRange := env.IcNetworkIpRange

	env.AuthType = env.USER_CERTIFICATE // Set AuthType to USER_CERTIFICATE for this test
	env.SecretManagerProjectID = ""
	env.CaPoolDeployedProjectID = "" // Reset CaPoolDeployedProjectID for this test
	env.CaName = ""                  // Reset CaName for this test
	env.CaPoolName = ""              // Reset CaPoolName for this test
	env.VsaDeployedDnsName = ""      // Reset VsaDeployedDnsName for this test
	env.VsaManagedZone = ""          // Reset VsaManagedZone for this test
	env.CertificateLifetime = ""     // Reset CertificateLifetime for this test
	env.Region = ""
	env.CloudDNSCacheTTL = 0
	env.NodePassword = ""
	env.MgmtRegionalNatIP = ""
	env.MgmtFirewallSourceRanges = ""
	env.RsmFirewallSourceRanges = ""
	env.IcFirewallSourceRanges = ""
	env.DataFirewallSourceRanges = ""
	env.MgmtNetworkIpRange = ""
	env.RsmNetworkIpRange = ""
	env.IcNetworkIpRange = ""

	defer func() {
		env.AuthType = originalAuthtype                             // Restore original AuthType after test
		env.CaPoolDeployedProjectID = originalCaDeployedProjectID   // Restore original CaPoolDeployedProjectID
		env.SecretManagerProjectID = originalSecretmanagerProjectID // Restore original SecretManagerProjectID
		env.CaPoolName = originalCaPoolName
		env.VsaDeployedDnsName = originalVsaDeployedDnsName
		env.CaName = originalCaName // Restore original CaName
		env.VsaManagedZone = originalVsaManagedZone
		env.CertificateLifetime = originalCertificateLifetime
		env.Region = originalRegion
		env.CloudDNSCacheTTL = originalCloudDNSCacheTTL // Restore original CloudDNSCacheTTL
		env.NodePassword = orignalNodePassword
		env.MgmtRegionalNatIP = originalMgmtRegionalNatIP
		env.MgmtFirewallSourceRanges = originalMgmtFirewallSourceRanges
		env.RsmFirewallSourceRanges = originalRsmFirewallSourceRanges
		env.IcFirewallSourceRanges = originalIcFirewallSourceRanges
		env.DataFirewallSourceRanges = originalDataFirewallSourceRanges
		env.MgmtNetworkIpRange = originalMgmtNetworkIpRange
		env.RsmNetworkIpRange = originalRsmNetworkIpRange
		env.IcNetworkIpRange = originalIcNetworkIpRange
	}()

	err := env.ValidateEnvironmentVariables()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "LOCAL_REGION must be set for authentication")

	env.Region = "us-central1" // Reset Region for this test

	err = env.ValidateEnvironmentVariables()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "CA_NAME must be set for authentication")

	env.CaName = "ca-name"
	err = env.ValidateEnvironmentVariables()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "CA_POOL_NAME must be set for authentication")

	env.CaPoolName = "ca-pool-name"
	err = env.ValidateEnvironmentVariables()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "CA_POOL_DEPLOYED_PROJECT_ID must be set for authentication")

	env.CaPoolDeployedProjectID = "ca-pool-deployed-project-id"
	err = env.ValidateEnvironmentVariables()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "SECRET_MANAGER_PROJECT_ID must be set for authentication")

	env.SecretManagerProjectID = "secret-manager-project-id"
	err = env.ValidateEnvironmentVariables()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "VSA_DEPLOYED_DNS_NAME must be set for authentication")

	env.VsaDeployedDnsName = "vsa-deployed-dns-name"
	err = env.ValidateEnvironmentVariables()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "VSA_MANAGED_ZONE must be set for authentication")

	env.VsaManagedZone = "vsa-managed-zone"
	err = env.ValidateEnvironmentVariables()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "CERTIFICATE_LIFETIME must be set for authentication")

	env.CertificateLifetime = "30000s"
	err = env.ValidateEnvironmentVariables()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "CLOUD_DNS_CACHE_TTL must be set for authentication")

	env.CloudDNSCacheTTL = 300
	err = env.ValidateEnvironmentVariables()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "VSA_NODE_PASSWORD must be set for authentication")

	env.NodePassword = "node-password"
	err = env.ValidateEnvironmentVariables()
	assert.Error(t, err)
	// The error could be for any of the network environment variables since map iteration order is not deterministic
	assert.Contains(t, err.Error(), "must be set for")

	// Set all required network environment variables
	env.MgmtFirewallSourceRanges = "10.0.0.0/8"
	env.RsmFirewallSourceRanges = "10.0.0.0/8"
	env.IcFirewallSourceRanges = "10.0.0.0/8"
	env.DataFirewallSourceRanges = "10.0.0.0/8"
	env.MgmtRegionalNatIP = "10.0.0.1/32"
	env.MgmtNetworkIpRange = "192.168.1.0/24"
	env.RsmNetworkIpRange = "192.168.2.0/24"
	env.IcNetworkIpRange = "192.168.3.0/24"

	// Update maps with current values only if they haven't been explicitly cleared for testing
	// If maps are empty, assume they were intentionally cleared for testing
	if len(env.NetworkSourceRanges) > 0 {
		env.NetworkSourceRanges["MGMT_FIREWALL_SOURCE_RANGES"] = env.MgmtFirewallSourceRanges
		env.NetworkSourceRanges["RSM_FIREWALL_SOURCE_RANGES"] = env.RsmFirewallSourceRanges
		env.NetworkSourceRanges["IC_FIREWALL_SOURCE_RANGES"] = env.IcFirewallSourceRanges
		env.NetworkSourceRanges["DATA_FIREWALL_SOURCE_RANGES"] = env.DataFirewallSourceRanges
		env.NetworkSourceRanges["MGMT_REGIONAL_NAT_IP"] = env.MgmtRegionalNatIP
	}

	if len(env.NetworkIpRanges) > 0 {
		env.NetworkIpRanges["MGMT_NETWORK_IP_RANGE"] = env.MgmtNetworkIpRange
		env.NetworkIpRanges["RSM_NETWORK_IP_RANGE"] = env.RsmNetworkIpRange
		env.NetworkIpRanges["IC_NETWORK_IP_RANGE"] = env.IcNetworkIpRange
	}

	err = env.ValidateEnvironmentVariables()
	assert.NoError(t, err)
}

func Test_convertPrivateCACertificateToCustomCertificate(t *testing.T) {
	t.Run("NilCertificate", func(tt *testing.T) {
		result, err := _validateAndConvertPrivateCACertificateToCustomCertificate("cert-id", nil)
		assert.Nil(tt, result)
		assert.EqualError(tt, fmt.Errorf("input certificate is nil"), err.(*vsaerrors.CustomError).OriginalErr.Error())
	})

	t.Run("InvalidCreateTime", func(tt *testing.T) {
		input := &privateca.Certificate{
			CreateTime: "invalid",
			Lifetime:   time.Now().Format(time.RFC3339),
		}
		result, err := _validateAndConvertPrivateCACertificateToCustomCertificate("cert-id", input)
		assert.Nil(tt, result)
		assert.Contains(tt, err.(*vsaerrors.CustomError).OriginalErr.Error(), "failed to parse time")
	})

	t.Run("ValidCertificate", func(tt *testing.T) {
		createTime := time.Now().Format(time.RFC3339)
		lifetime := "30000s"
		input := &privateca.Certificate{
			Name:                       "test-cert",
			PemCertificate:             "pem-data",
			CreateTime:                 createTime,
			Lifetime:                   lifetime,
			PemCertificateChain:        []string{"chain1", "chain2"},
			IssuerCertificateAuthority: "projects/test-project/locations/us-central1/caPools/test-pool/certificateAuthorities/issuer",
			CertificateDescription: &privateca.CertificateDescription{
				SubjectDescription: &privateca.SubjectDescription{
					Subject: &privateca.Subject{
						CommonName:   "common",
						Organization: "org",
					},
					SubjectAltName: &privateca.SubjectAltNames{
						DnsNames: []string{"dns1", "dns2"},
					},
				},
			},
		}
		expectedCreateTime, _ := time.Parse(time.RFC3339, createTime)
		expected := &models.CustomCertificate{
			CertificateID:              "cert-id",
			Name:                       "test-cert",
			PemCertificate:             "pem-data",
			CreateTime:                 &expectedCreateTime,
			LifeTime:                   lifetime,
			PemCertificateChain:        []string{"chain1", "chain2"},
			IssuerCertificateAuthority: "projects/test-project/locations/us-central1/caPools/test-pool/certificateAuthorities/issuer",
			SubjectCommonName:          "common",
			SubjectOrganization:        "org",
			SubjectAltName:             []string{"dns1", "dns2"},
			CaName:                     "issuer", // CaName should be extracted from IssuerCertificateAuthority
		}
		result, err := _validateAndConvertPrivateCACertificateToCustomCertificate("cert-id", input)
		assert.NoError(tt, err)
		assert.Equal(tt, expected, result)
	})
}

func Test_convertSecretToCustomSecret(t *testing.T) {
	t.Run("WhenSecretIsNil", func(tt *testing.T) {
		result, err := _convertSecretToCustomSecret(nil, nil)
		assert.Nil(tt, result, "Expected result to be nil when input secret is nil")
		assert.EqualError(tt, err.(*vsaerrors.CustomError).OriginalErr, "input secret is nil", "Expected error message to match")
	})

	t.Run("WhenCreateTimeIsInvalid", func(tt *testing.T) {
		input := &secretmanager.Secret{
			CreateTime: "invalid-time",
		}
		result, err := _convertSecretToCustomSecret(input, &models.CustomSecretVersion{
			Name:  "secret-name",
			Value: "secret-value",
		})
		assert.Nil(tt, result, "Expected result to be nil when CreateTime is invalid")
		assert.Contains(tt, err.(*vsaerrors.CustomError).OriginalErr.Error(), "failed to parse time", "Expected error message to contain 'failed to parse CreateTime'")
	})

	t.Run("WhenExpireTimeIsInvalid", func(tt *testing.T) {
		input := &secretmanager.Secret{
			CreateTime: time.Now().Format(time.RFC3339),
			ExpireTime: "invalid-time",
		}
		result, err := _convertSecretToCustomSecret(input, &models.CustomSecretVersion{
			Name:  "secret-name",
			Value: "secret-value",
		})
		assert.Nil(tt, result)
		assert.Contains(tt, err.(*vsaerrors.CustomError).OriginalErr.Error(), "failed to parse time")
	})

	t.Run("WhenSecretIsValid", func(tt *testing.T) {
		createTime := time.Now().Format(time.RFC3339)
		expireTime := time.Now().Add(24 * time.Hour).Format(time.RFC3339)
		input := &secretmanager.Secret{
			Name:       "test-secret",
			CreateTime: createTime,
			ExpireTime: expireTime,
		}
		secretVersion := &models.CustomSecretVersion{
			Name:  "test-version",
			Value: "test-value",
		}

		expectedCreateTime, _ := time.Parse(time.RFC3339, createTime)
		expectedExpireTime, _ := time.Parse(time.RFC3339, expireTime)

		expected := &models.CustomSecret{
			Name:          "test-secret",
			CreateTime:    &expectedCreateTime,
			LifeTime:      &expectedExpireTime,
			SecretVersion: secretVersion,
		}

		result, err := _convertSecretToCustomSecret(input, secretVersion)
		assert.NoError(tt, err, "Expected no error when secret is valid")
		assert.Equal(tt, expected, result, "Expected result to match the converted secret")
	})
}

func Test_validateAndConvertToCustomCloudDNSRecord(t *testing.T) {
	t.Run("NilRecordSet", func(tt *testing.T) {
		result, err := _validateAndConvertToCustomCloudDNSRecord(nil, "zone")
		assert.Nil(tt, result)
		assert.Equal(tt, "resource record set is nil", err.(*vsaerrors.CustomError).OriginalErr.Error())
	})

	t.Run("InvalidRecordSet_EmptyFields", func(tt *testing.T) {
		recordSet := &dns.ResourceRecordSet{}
		result, err := _validateAndConvertToCustomCloudDNSRecord(recordSet, "zone")
		assert.Nil(tt, result)
		assert.Equal(tt, "resource record set is invalid", err.(*vsaerrors.CustomError).OriginalErr.Error())
	})

	t.Run("InvalidRecordSet_EmptyRrdatas", func(tt *testing.T) {
		recordSet := &dns.ResourceRecordSet{
			Name: "test.com.",
			Type: "A",
			Ttl:  300,
		}
		result, err := _validateAndConvertToCustomCloudDNSRecord(recordSet, "zone")
		assert.Nil(tt, result)
		assert.Equal(tt, "resource record set is invalid", err.(*vsaerrors.CustomError).OriginalErr.Error())
	})

	t.Run("ValidRecordSet", func(tt *testing.T) {
		recordSet := &dns.ResourceRecordSet{
			Name:    "test.com.",
			Type:    "A",
			Ttl:     300,
			Rrdatas: []string{"1.2.3.4"},
		}
		managedZone := "zone"
		expected := &models.CustomCloudDNSRecord{
			RecordName:  "test.com.",
			Type:        "A",
			TTL:         300,
			Data:        "1.2.3.4",
			ManagedZone: managedZone,
		}
		result, err := _validateAndConvertToCustomCloudDNSRecord(recordSet, managedZone)
		assert.NoError(tt, err)
		assert.Equal(tt, expected, result)
	})
}

func Test_extractCANameFromAuthority(t *testing.T) {
	t.Run("WhenAuthorityIsEmpty", func(tt *testing.T) {
		result, err := extractCANameFromAuthority("")
		assert.NoError(tt, err, "Expected no error when authority is empty")
		assert.Equal(tt, "", result, "Expected empty string when authority is empty")
	})

	t.Run("WhenAuthorityIsValidFullPath", func(tt *testing.T) {
		authority := "projects/my-project/locations/us-central1/caPools/my-pool/certificateAuthorities/my-ca"
		result, err := extractCANameFromAuthority(authority)
		assert.NoError(tt, err, "Expected no error for valid authority")
		assert.Equal(tt, "my-ca", result, "Expected CA name to be extracted correctly")
	})

	t.Run("WhenAuthorityHasMultipleCertificateAuthoritiesInPath", func(tt *testing.T) {
		// Edge case: if the path contains 'certificateAuthorities' multiple times,
		// it should extract the last one
		authority := "projects/certificateAuthorities/locations/us-central1/certificateAuthorities/my-ca-name"
		result, err := extractCANameFromAuthority(authority)
		assert.NoError(tt, err, "Expected no error for valid authority")
		assert.Equal(tt, "my-ca-name", result, "Expected CA name to be extracted from last occurrence")
	})

	t.Run("WhenAuthorityIsMissingCertificateAuthoritiesPrefix", func(tt *testing.T) {
		authority := "projects/my-project/locations/us-central1/caPools/my-pool/my-ca"
		result, err := extractCANameFromAuthority(authority)
		assert.Error(tt, err, "Expected error when authority format is invalid")
		assert.Equal(tt, "", result, "Expected empty string when error occurs")
		assert.Contains(tt, err.Error(), "invalid authority format", "Expected error message to mention invalid format")
	})

	t.Run("WhenAuthorityHasEmptyCAName", func(tt *testing.T) {
		authority := "projects/my-project/locations/us-central1/caPools/my-pool/certificateAuthorities/"
		result, err := extractCANameFromAuthority(authority)
		assert.Error(tt, err, "Expected error when CA name is empty")
		assert.Equal(tt, "", result, "Expected empty string when error occurs")
		assert.Contains(tt, err.Error(), "empty CA name", "Expected error message to mention empty CA name")
	})

	t.Run("WhenAuthorityIsMinimalValid", func(tt *testing.T) {
		authority := "certificateAuthorities/simple-ca"
		result, err := extractCANameFromAuthority(authority)
		assert.NoError(tt, err, "Expected no error for minimal valid authority")
		assert.Equal(tt, "simple-ca", result, "Expected CA name to be extracted correctly")
	})

	t.Run("WhenAuthorityHasCANameWithHyphens", func(tt *testing.T) {
		authority := "projects/my-project/locations/us-central1/caPools/my-pool/certificateAuthorities/my-ca-with-hyphens-123"
		result, err := extractCANameFromAuthority(authority)
		assert.NoError(tt, err, "Expected no error for valid authority with hyphens")
		assert.Equal(tt, "my-ca-with-hyphens-123", result, "Expected CA name with hyphens to be extracted correctly")
	})

	t.Run("WhenAuthorityHasTrailingContent", func(tt *testing.T) {
		// This tests if there's any trailing content after the CA name
		authority := "projects/my-project/locations/us-central1/caPools/my-pool/certificateAuthorities/my-ca/extra/path"
		result, err := extractCANameFromAuthority(authority)
		assert.NoError(tt, err, "Expected no error for authority with trailing content")
		assert.Equal(tt, "my-ca/extra/path", result, "Expected entire remaining path to be returned as CA name")
	})
}

// ... existing code ...

func Test_convertGoogleAddressesToAddresses(t *testing.T) {
	t.Run("WhenAddressesIsNil", func(tt *testing.T) {
		result := convertGoogleAddressesToAddresses(nil)
		assert.NotNil(tt, result)
		assert.Empty(tt, *result)
	})

	t.Run("WhenAddressesItemsIsEmpty", func(tt *testing.T) {
		addresses := &compute.AddressList{
			Items: []*compute.Address{},
		}
		result := convertGoogleAddressesToAddresses(addresses)
		assert.NotNil(tt, result)
		assert.Empty(tt, *result)
	})

	t.Run("WhenAddressesHasValidItems", func(tt *testing.T) {
		addresses := &compute.AddressList{
			Items: []*compute.Address{
				{
					Name:       "test-address-1",
					Region:     "us-central1",
					SelfLink:   "https://www.googleapis.com/compute/v1/projects/test-project/regions/us-central1/addresses/test-address-1",
					Subnetwork: "https://www.googleapis.com/compute/v1/projects/test-project/regions/us-central1/subnetworks/test-subnet",
				},
				{
					Name:       "test-address-2",
					Region:     "us-west1",
					SelfLink:   "https://www.googleapis.com/compute/v1/projects/test-project/regions/us-west1/addresses/test-address-2",
					Subnetwork: "https://www.googleapis.com/compute/v1/projects/test-project/regions/us-west1/subnetworks/test-subnet-2",
				},
			},
		}

		expected := []models.Address{
			{
				AddressName: "test-address-1",
				Region:      "us-central1",
				SelfLink:    "https://www.googleapis.com/compute/v1/projects/test-project/regions/us-central1/addresses/test-address-1",
				SubnetURI:   "https://www.googleapis.com/compute/v1/projects/test-project/regions/us-central1/subnetworks/test-subnet",
			},
			{
				AddressName: "test-address-2",
				Region:      "us-west1",
				SelfLink:    "https://www.googleapis.com/compute/v1/projects/test-project/regions/us-west1/addresses/test-address-2",
				SubnetURI:   "https://www.googleapis.com/compute/v1/projects/test-project/regions/us-west1/subnetworks/test-subnet-2",
			},
		}

		result := convertGoogleAddressesToAddresses(addresses)
		assert.NotNil(tt, result)
		assert.Equal(tt, expected, *result)
	})

	t.Run("WhenAddressesHasNilItem", func(tt *testing.T) {
		addresses := &compute.AddressList{
			Items: []*compute.Address{
				{
					Name:       "test-address-1",
					Region:     "us-central1",
					SelfLink:   "https://www.googleapis.com/compute/v1/projects/test-project/regions/us-central1/addresses/test-address-1",
					Subnetwork: "https://www.googleapis.com/compute/v1/projects/test-project/regions/us-central1/subnetworks/test-subnet",
				},
				nil, // This should be filtered out
				{
					Name:       "test-address-2",
					Region:     "us-west1",
					SelfLink:   "https://www.googleapis.com/compute/v1/projects/test-project/regions/us-west1/addresses/test-address-2",
					Subnetwork: "https://www.googleapis.com/compute/v1/projects/test-project/regions/us-west1/subnetworks/test-subnet-2",
				},
			},
		}

		expected := []models.Address{
			{
				AddressName: "test-address-1",
				Region:      "us-central1",
				SelfLink:    "https://www.googleapis.com/compute/v1/projects/test-project/regions/us-central1/addresses/test-address-1",
				SubnetURI:   "https://www.googleapis.com/compute/v1/projects/test-project/regions/us-central1/subnetworks/test-subnet",
			},
			{
				AddressName: "test-address-2",
				Region:      "us-west1",
				SelfLink:    "https://www.googleapis.com/compute/v1/projects/test-project/regions/us-west1/addresses/test-address-2",
				SubnetURI:   "https://www.googleapis.com/compute/v1/projects/test-project/regions/us-west1/subnetworks/test-subnet-2",
			},
		}

		result := convertGoogleAddressesToAddresses(addresses)
		assert.NotNil(tt, result)
		assert.Equal(tt, expected, *result)
	})
}

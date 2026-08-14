package common

import (
	"fmt"
	"strings"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
)

// HostClusterDNSZone returns the cluster DNS zone suffix used for host certificates and DNS records.
func HostClusterDNSZone(externalClusterName string) string {
	dnsZone := strings.Trim(strings.TrimSpace(env.VsaDeployedDnsName), ".")
	return fmt.Sprintf("%s.%s", strings.TrimSpace(externalClusterName), dnsZone)
}

// BuildHostCertificateWildcardSAN returns the wildcard SAN issued for the host cluster certificate.
// This matches the domain generated in _createCertificateInCASAndPrivateKeyInSM.
func BuildHostCertificateWildcardSAN(externalClusterName string) string {
	return fmt.Sprintf("*.%s", HostClusterDNSZone(externalClusterName))
}

// BuildHostManagementTLSName returns the DNS hostname used to verify the host's
// ONTAP server certificate.
func BuildHostManagementTLSName(externalClusterName string) string {
	return fmt.Sprintf("dns-1.%s", HostClusterDNSZone(externalClusterName))
}

// BuildHostCloudDNSRecordName returns the Cloud DNS A-record name for an external cluster host.
// The record is created under the same cluster DNS zone covered by BuildHostCertificateWildcardSAN.
func BuildHostCloudDNSRecordName(externalClusterName string) string {
	return fmt.Sprintf("%s.", BuildHostManagementTLSName(externalClusterName))
}

// BuildHostClientCACertificateName returns the deterministic ONTAP name for a host client-ca install.
func BuildHostClientCACertificateName(externalClusterName string) string {
	return fmt.Sprintf("%s-client-ca", strings.TrimSpace(externalClusterName))
}

// BuildHostClientCACertificateNameForChainIndex returns the ONTAP name for a host client-ca at chain index.
func BuildHostClientCACertificateNameForChainIndex(externalClusterName string, chainIndex int) string {
	return fmt.Sprintf("%s-client-ca-%d", strings.TrimSpace(externalClusterName), chainIndex)
}

// BuildHostServerCACertificateName returns the deterministic ONTAP name for a host server-ca install.
func BuildHostServerCACertificateName(externalClusterName string) string {
	return fmt.Sprintf("%s-server-ca", strings.TrimSpace(externalClusterName))
}

// BuildHostServerCACertificateNameForChainIndex returns the ONTAP name for a host server-ca at chain index.
func BuildHostServerCACertificateNameForChainIndex(externalClusterName string, chainIndex int) string {
	return fmt.Sprintf("%s-server-ca-%d", strings.TrimSpace(externalClusterName), chainIndex)
}

package common_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	orchcommon "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
)

func TestBuildHostCloudDNSRecordName(t *testing.T) {
	originalDNSName := env.VsaDeployedDnsName
	t.Cleanup(func() { env.VsaDeployedDnsName = originalDNSName })
	env.VsaDeployedDnsName = "example.com"
	externalClusterName := "external-cluster"

	assert.Equal(t, "external-cluster.example.com", orchcommon.HostClusterDNSZone(externalClusterName))
	assert.Equal(t, "*.external-cluster.example.com", orchcommon.BuildHostCertificateWildcardSAN(externalClusterName))
	assert.Equal(t, "dns-1.external-cluster.example.com", orchcommon.BuildHostManagementTLSName(externalClusterName))
	assert.Equal(t, "dns-1.external-cluster.example.com.", orchcommon.BuildHostCloudDNSRecordName(externalClusterName))
	assert.Equal(t, "external-cluster-client-ca", orchcommon.BuildHostClientCACertificateName(externalClusterName))
	assert.Equal(t, "external-cluster-server-ca", orchcommon.BuildHostServerCACertificateName(externalClusterName))
	assert.Equal(t, "external-cluster-client-ca-0", orchcommon.BuildHostClientCACertificateNameForChainIndex(externalClusterName, 0))
	assert.Equal(t, "external-cluster-client-ca-1", orchcommon.BuildHostClientCACertificateNameForChainIndex(externalClusterName, 1))
	assert.Equal(t, "external-cluster-server-ca-0", orchcommon.BuildHostServerCACertificateNameForChainIndex(externalClusterName, 0))
	assert.Equal(t, "external-cluster-server-ca-1", orchcommon.BuildHostServerCACertificateNameForChainIndex(externalClusterName, 1))
}

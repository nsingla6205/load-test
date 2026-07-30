package common

import (
	"fmt"
	"strings"
)

// BuildHostClientCACertificateNameForChainIndex returns the ONTAP name for a host client-ca at chain index.
func BuildHostClientCACertificateNameForChainIndex(externalClusterName string, chainIndex int) string {
	return fmt.Sprintf("%s-client-ca-%d", strings.TrimSpace(externalClusterName), chainIndex)
}

// BuildHostServerCACertificateNameForChainIndex returns the ONTAP name for a host server-ca at chain index.
func BuildHostServerCACertificateNameForChainIndex(externalClusterName string, chainIndex int) string {
	return fmt.Sprintf("%s-server-ca-%d", strings.TrimSpace(externalClusterName), chainIndex)
}

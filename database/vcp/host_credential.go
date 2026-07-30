package database

import (
	"strings"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
)

// HostCredential is the resolved connection identity for an account host.
// It carries the joined host row plus the ONTAP username and the Secret
// Manager secret id used to resolve the password at call time.
type HostCredential struct {
	Host        *datamodel.Host
	Username    string
	SecretId    string
	AccountUUID string
}

// ManagementIP returns the management IP address to reach the host, falling back
// to the host name when no explicit IP address is recorded.
func (hc *HostCredential) ManagementIP() string {
	if hc == nil {
		return ""
	}
	return hostManagementIP(hc.Host)
}

func hostExternalName(host *datamodel.Host) string {
	return HostExternalName(host)
}

// HostExternalName returns the external cluster name for a host row.
func HostExternalName(host *datamodel.Host) string {
	if host == nil {
		return ""
	}
	if name := strings.TrimSpace(host.ExternalName); name != "" {
		return name
	}
	return strings.TrimSpace(host.Name)
}

func hostManagementIP(host *datamodel.Host) string {
	if host == nil {
		return ""
	}
	if ip := strings.TrimSpace(host.IPAddress); ip != "" {
		return ip
	}
	return strings.TrimSpace(host.Name)
}

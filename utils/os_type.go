package utils

import "strings"

const (
	APIOSTypeUnspecified = "OS_TYPE_UNSPECIFIED"
	APIOSTypeLinux       = "LINUX"
	APIOSTypeWindows     = "WINDOWS"
	APIOSTypeESXI        = "ESXI"
)

// MapONTAPOSTypeToAPIEnum maps an ONTAP LUN/igroup os_type to the VCM/GCP
// OSType enum. ONTAP stores ESXi as "vmware"; the published API uses ESXI.
func MapONTAPOSTypeToAPIEnum(osType string) string {
	switch strings.ToLower(strings.TrimSpace(osType)) {
	case "linux":
		return APIOSTypeLinux
	case "windows":
		return APIOSTypeWindows
	case "vmware", "esxi":
		return APIOSTypeESXI
	default:
		return strings.ToUpper(osType)
	}
}

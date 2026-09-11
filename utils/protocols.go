package utils

import (
	"regexp"
	"strings"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
)

const (
	ProtocolNFS         = "NFS"
	ProtocolNFSv3       = "NFSV3"
	ProtocolNFSv4       = "NFSV4"
	ProtocolSMB         = "SMB"
	ProtocolISCSI       = "ISCSI"
	ProtocolNVMe        = "NVME"
	ProtocolUnspecified = "PROTOCOL_UNSPECIFIED"
)

// ONTAP layer protocol values
const (
	ontapNFS   = "nfs"
	ontapNFSv3 = "nfs3"
	ontapNFSv4 = "nfs4"
	ontapCIFS  = "cifs"
	ontapISCSI = "iscsi"
	ontapNVMe  = "nvme"
)

const unixPermissionsRegexPattern = "^[0-7]{4}$"

const (
	CIFSSharePropertyBrowsable            = "browsable"
	CIFSSharePropertyChangenotify         = "changenotify"
	CIFSSharePropertyEncryptData          = "encrypt_data"
	CIFSSharePropertyOplocks              = "oplocks"
	CIFSSharePropertyShowsnapshot         = "showsnapshot"
	CIFSSharePropertyShowPreviousVersions = "show_previous_versions"
	CIFSSharePropertyCA                   = "continuously_available"
	CIFSAccessBasedEnumeration            = "access_based_enumeration"
	CIFSSharePropertyNonBrowsable         = "non_browsable"
	CIFSShareSmbSettingsUnspecified       = "unspecified"
)

// ProtocolType represents the category of a protocol
type ProtocolType string

const (
	ProtocolTypeNAS ProtocolType = "nas"
	ProtocolTypeSAN ProtocolType = "san"
)

// ProtocolInfo contains information about a protocol
type ProtocolInfo struct {
	OntapValue   string       // The value expected by ONTAP layer (e.g., "nfsv3", "cifs")
	ProtocolType ProtocolType // The protocol category (NAS or SAN)
}

// ProtocolMap maps Swagger enum values to ONTAP layer information
var ProtocolMap = map[string]ProtocolInfo{
	ProtocolNFS: {
		OntapValue:   ontapNFS,
		ProtocolType: ProtocolTypeNAS,
	},
	ProtocolNFSv3: {
		OntapValue:   ontapNFSv3,
		ProtocolType: ProtocolTypeNAS,
	},
	ProtocolNFSv4: {
		OntapValue:   ontapNFSv4,
		ProtocolType: ProtocolTypeNAS,
	},
	ProtocolSMB: {
		OntapValue:   ontapCIFS,
		ProtocolType: ProtocolTypeNAS,
	},
	ProtocolISCSI: {
		OntapValue:   ontapISCSI,
		ProtocolType: ProtocolTypeSAN,
	},
	ProtocolNVMe: {
		OntapValue:   ontapNVMe,
		ProtocolType: ProtocolTypeSAN,
	},
}

// GetOntapValue returns the ONTAP layer value for a given protocol
func GetOntapValue(protocol string) string {
	return ProtocolMap[protocol].OntapValue
}

// GetProtocolType returns the protocol type (NAS or SAN) for a given protocol
func GetProtocolType(protocol string) ProtocolType {
	return ProtocolMap[protocol].ProtocolType
}

// IsNASProtocol checks if a protocol is a NAS protocol (file-based)
func IsNASProtocol(protocol string) bool {
	if info, exists := ProtocolMap[protocol]; exists {
		return info.ProtocolType == ProtocolTypeNAS
	}
	return false
}

// IsSANProtocol checks if a protocol is a SAN protocol (block-based)
func IsSANProtocol(protocol string) bool {
	if info, exists := ProtocolMap[protocol]; exists {
		return info.ProtocolType == ProtocolTypeSAN
	}
	return false
}

// IsNasProtocols checks if the provided protocols are all NAS protocols
func IsNasProtocols(protocols []string) bool {
	if len(protocols) == 0 {
		return false
	}
	for _, protocol := range protocols {
		if !IsNASProtocol(protocol) {
			return false
		}
	}
	return true
}

func IsSMBProtocols(protocols []string) bool {
	isSMB := false
	if len(protocols) == 0 {
		return false
	}
	for _, protocol := range protocols {
		if IsSMBProtocol(protocol) {
			isSMB = true
			break
		}
	}
	return isSMB
}

func IsSMBProtocol(protocol string) bool {
	return protocol == ProtocolSMB
}

// IsSMBOnlyProtocols reports SMB-only volumes (SMB present, no NFS protocol).
func IsSMBOnlyProtocols(protocols []string) bool {
	return IsSMBProtocols(protocols) && !IsNFSProtocols(protocols)
}

// IsNFSProtocols checks if the provided protocols contain any NFS protocol
func IsNFSProtocols(protocols []string) bool {
	if len(protocols) == 0 {
		return false
	}
	for _, protocol := range protocols {
		if protocol == ProtocolNFS || protocol == ProtocolNFSv3 || protocol == ProtocolNFSv4 {
			return true
		}
	}
	return false
}

// IsNFSOnlyProtocols checks if every protocol is an NFS variant (NFS, NFSV3, or NFSV4).
func IsNFSOnlyProtocols(protocols []string) bool {
	if len(protocols) == 0 {
		return false
	}
	for _, protocol := range protocols {
		if protocol != ProtocolNFS && protocol != ProtocolNFSv3 && protocol != ProtocolNFSv4 {
			return false
		}
	}
	return true
}

// IsSanProtocols checks if the provided protocols are all SAN protocols
func IsSanProtocols(protocols []string) bool {
	if len(protocols) == 0 {
		return false
	}
	for _, protocol := range protocols {
		if !IsSANProtocol(protocol) {
			return false
		}
	}
	return true
}

// ResolveEffectiveUnixSecurityStyle returns the security style to use when unix permissions are in play.
// When explicitlySet is true, securityStyle is returned unchanged. Otherwise, when securityStyle is empty
// and unixPermissions is set on an NFS-only volume, defaults to unix.
func ResolveEffectiveUnixSecurityStyle(protocols []string, securityStyle string, explicitlySet bool, unixPermissions string) string {
	if explicitlySet {
		return securityStyle
	}
	if securityStyle != "" {
		return securityStyle
	}
	if unixPermissions != "" && IsNFSOnlyProtocols(protocols) {
		return UnixSecurityStyle
	}
	return securityStyle
}

// ValidateUnixPermissions checks unix permission format and compatibility with protocols and security style.
func ValidateUnixPermissions(protocolTypes []string, unixPermissions string, securityStyle string) error {
	if unixPermissions == "" {
		return errors.NewUserInputValidationErr("UnixPermissions cannot be empty.")
	}
	if IsNFSProtocols(protocolTypes) {
		result, _ := regexp.MatchString(unixPermissionsRegexPattern, unixPermissions)
		if !result {
			return errors.NewUserInputValidationErr("Unix permissions should be 4 digit long in octal format(0 to 7),  Create/Update volume with valid unix permissions, example: 0755")
		}
		effectiveStyle := strings.ToLower(ResolveEffectiveUnixSecurityStyle(protocolTypes, securityStyle, false, unixPermissions))
		if effectiveStyle != UnixSecurityStyle {
			return errors.NewUserInputValidationErr("Unix permissions are only supported with unix security-style")
		}
	} else {
		return errors.NewUserInputValidationErr("Unix permissions is only supported with NFS protocol volumes, Create/Update SMB volume without unix permissions.")
	}
	return nil
}

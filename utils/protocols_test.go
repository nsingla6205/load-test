package utils

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetOntapValue(t *testing.T) {
	t.Run("WhenProtocolExists", func(tt *testing.T) {
		ontapValue := GetOntapValue(ProtocolNFSv3)
		assert.Equal(tt, "nfs3", ontapValue)
	})

	t.Run("WhenProtocolDoesNotExist", func(tt *testing.T) {
		ontapValue := GetOntapValue("INVALID")
		assert.Equal(tt, "", ontapValue)
	})

	t.Run("AllProtocolOntapValues", func(tt *testing.T) {
		expectedOntapValues := map[string]string{
			ProtocolNFS:   "nfs",
			ProtocolNFSv3: "nfs3",
			ProtocolNFSv4: "nfs4",
			ProtocolSMB:   "cifs",
			ProtocolISCSI: "iscsi",
		}

		for protocol, expectedValue := range expectedOntapValues {
			ontapValue := GetOntapValue(protocol)
			assert.Equal(tt, expectedValue, ontapValue)
		}
	})
}

func TestGetProtocolType(t *testing.T) {
	t.Run("WhenProtocolExists", func(tt *testing.T) {
		protocolType := GetProtocolType(ProtocolNFSv3)
		assert.Equal(tt, ProtocolTypeNAS, protocolType)
	})

	t.Run("WhenProtocolDoesNotExist", func(tt *testing.T) {
		protocolType := GetProtocolType("INVALID")
		assert.Equal(tt, ProtocolType(""), protocolType)
	})

	t.Run("NASProtocolTypes", func(tt *testing.T) {
		nasProtocols := []string{ProtocolNFS, ProtocolNFSv3, ProtocolNFSv4, ProtocolSMB}
		for _, protocol := range nasProtocols {
			protocolType := GetProtocolType(protocol)
			assert.Equal(tt, ProtocolTypeNAS, protocolType, "Protocol %s should be NAS type", protocol)
		}
	})

	t.Run("SANProtocolType", func(tt *testing.T) {
		protocolType := GetProtocolType(ProtocolISCSI)
		assert.Equal(tt, ProtocolTypeSAN, protocolType)
	})
}

func TestIsNASProtocol(t *testing.T) {
	t.Run("NFSv3IsNASProtocol", func(tt *testing.T) {
		assert.True(tt, IsNASProtocol(ProtocolNFSv3))
	})

	t.Run("NFSv4IsNASProtocol", func(tt *testing.T) {
		assert.True(tt, IsNASProtocol(ProtocolNFSv4))
	})

	t.Run("SMBIsNASProtocol", func(tt *testing.T) {
		assert.True(tt, IsNASProtocol(ProtocolSMB))
	})

	t.Run("ISCSIIsNotNASProtocol", func(tt *testing.T) {
		assert.False(tt, IsNASProtocol(ProtocolISCSI))
	})

	t.Run("InvalidProtocolIsNotNASProtocol", func(tt *testing.T) {
		assert.False(tt, IsNASProtocol("INVALID"))
	})
}

func TestIsSANProtocol(t *testing.T) {
	t.Run("ISCSIIsSANProtocol", func(tt *testing.T) {
		assert.True(tt, IsSANProtocol(ProtocolISCSI))
	})

	t.Run("NFSv3IsNotSANProtocol", func(tt *testing.T) {
		assert.False(tt, IsSANProtocol(ProtocolNFSv3))
	})

	t.Run("NFSv4IsNotSANProtocol", func(tt *testing.T) {
		assert.False(tt, IsSANProtocol(ProtocolNFSv4))
	})

	t.Run("SMBIsNotSANProtocol", func(tt *testing.T) {
		assert.False(tt, IsSANProtocol(ProtocolSMB))
	})

	t.Run("InvalidProtocolIsNotSANProtocol", func(tt *testing.T) {
		assert.False(tt, IsSANProtocol("INVALID"))
	})
}

func TestProtocolMapConsistency(t *testing.T) {
	t.Run("AllProtocolsHaveCorrectMapping", func(tt *testing.T) {
		expectedMappings := map[string]struct {
			ontapValue   string
			protocolType ProtocolType
		}{
			ProtocolNFS: {
				ontapValue:   "nfs",
				protocolType: ProtocolTypeNAS,
			},
			ProtocolNFSv3: {
				ontapValue:   "nfs3",
				protocolType: ProtocolTypeNAS,
			},
			ProtocolNFSv4: {
				ontapValue:   "nfs4",
				protocolType: ProtocolTypeNAS,
			},
			ProtocolSMB: {
				ontapValue:   "cifs",
				protocolType: ProtocolTypeNAS,
			},
			ProtocolISCSI: {
				ontapValue:   "iscsi",
				protocolType: ProtocolTypeSAN,
			},
			ProtocolNVMe: {
				ontapValue:   "nvme",
				protocolType: ProtocolTypeSAN,
			},
		}

		for protocol, expected := range expectedMappings {
			info, ok := ProtocolMap[protocol]
			assert.True(tt, ok, "Protocol %s should exist in ProtocolMap", protocol)
			assert.Equal(tt, expected.ontapValue, info.OntapValue, "OntapValue mismatch for %s", protocol)
			assert.Equal(tt, expected.protocolType, info.ProtocolType, "ProtocolType mismatch for %s", protocol)
		}
	})

	t.Run("ProtocolMapHasExpectedSize", func(tt *testing.T) {
		assert.Len(tt, ProtocolMap, 6, "ProtocolMap should contain exactly 6 protocols")
	})
}

func TestProtocolConstants(t *testing.T) {
	t.Run("ProtocolConstantsAreCorrect", func(tt *testing.T) {
		assert.Equal(tt, "NFSV3", ProtocolNFSv3)
		assert.Equal(tt, "NFSV4", ProtocolNFSv4)
		assert.Equal(tt, "SMB", ProtocolSMB)
		assert.Equal(tt, "ISCSI", ProtocolISCSI)
	})

	t.Run("OntapConstantsAreCorrect", func(tt *testing.T) {
		nfsValue := GetOntapValue(ProtocolNFS)
		assert.Equal(tt, "nfs", nfsValue)

		nfsv3Value := GetOntapValue(ProtocolNFSv3)
		assert.Equal(tt, "nfs3", nfsv3Value)

		nfsv4Value := GetOntapValue(ProtocolNFSv4)
		assert.Equal(tt, "nfs4", nfsv4Value)

		cifsValue := GetOntapValue(ProtocolSMB)
		assert.Equal(tt, "cifs", cifsValue)

		iscsiValue := GetOntapValue(ProtocolISCSI)
		assert.Equal(tt, "iscsi", iscsiValue)
	})

	t.Run("ProtocolTypesAreCorrect", func(tt *testing.T) {
		assert.Equal(tt, ProtocolType("nas"), ProtocolTypeNAS)
		assert.Equal(tt, ProtocolType("san"), ProtocolTypeSAN)
	})
}

func TestProtocolInfo_Structure(t *testing.T) {
	t.Run("ProtocolInfoHasCorrectFields", func(tt *testing.T) {
		info := ProtocolInfo{
			OntapValue:   "test",
			ProtocolType: ProtocolTypeNAS,
		}

		assert.Equal(tt, "test", info.OntapValue)
		assert.Equal(tt, ProtocolTypeNAS, info.ProtocolType)
	})
}

func TestIsNasProtocols(t *testing.T) {
	t.Run("All NAS protocols", func(tt *testing.T) {
		protocols := []string{ProtocolNFS, ProtocolNFSv3, ProtocolNFSv4, ProtocolSMB}
		assert.True(tt, IsNasProtocols(protocols))
	})

	t.Run("Mixed NAS and SAN protocols", func(tt *testing.T) {
		protocols := []string{ProtocolNFSv3, ProtocolISCSI}
		assert.False(tt, IsNasProtocols(protocols))
	})

	t.Run("All SAN protocols", func(tt *testing.T) {
		protocols := []string{ProtocolISCSI}
		assert.False(tt, IsNasProtocols(protocols))
	})

	t.Run("Empty slice", func(tt *testing.T) {
		protocols := []string{}
		assert.False(tt, IsNasProtocols(protocols))
	})

	t.Run("Invalid protocol", func(tt *testing.T) {
		protocols := []string{"INVALID"}
		assert.False(tt, IsNasProtocols(protocols))
	})
}

func TestIsSanProtocols(t *testing.T) {
	t.Run("All SAN protocols", func(tt *testing.T) {
		protocols := []string{ProtocolISCSI}
		assert.True(tt, IsSanProtocols(protocols))
	})

	t.Run("Mixed SAN and NAS protocols", func(tt *testing.T) {
		protocols := []string{ProtocolISCSI, ProtocolNFSv3}
		assert.False(tt, IsSanProtocols(protocols))
	})

	t.Run("All NAS protocols", func(tt *testing.T) {
		protocols := []string{ProtocolNFSv3, ProtocolNFSv4, ProtocolSMB}
		assert.False(tt, IsSanProtocols(protocols))
	})

	t.Run("Empty slice", func(tt *testing.T) {
		protocols := []string{}
		assert.False(tt, IsSanProtocols(protocols))
	})

	t.Run("Invalid protocol", func(tt *testing.T) {
		protocols := []string{"INVALID"}
		assert.False(tt, IsSanProtocols(protocols))
	})
}

func TestIsNFSProtocols(t *testing.T) {
	t.Run("All NFS and SMB protocols", func(tt *testing.T) {
		protocols := []string{ProtocolNFS, ProtocolNFSv3, ProtocolNFSv4, ProtocolSMB}
		assert.True(tt, IsNFSProtocols(protocols))
	})

	t.Run("All NFS protocols", func(tt *testing.T) {
		protocols := []string{ProtocolNFS, ProtocolNFSv3, ProtocolNFSv4}
		assert.True(tt, IsNFSProtocols(protocols))
	})

	t.Run("Only NFSV3 protocol", func(tt *testing.T) {
		protocols := []string{ProtocolNFSv3}
		assert.True(tt, IsNFSProtocols(protocols))
	})

	t.Run("Only SMB protocol", func(tt *testing.T) {
		protocols := []string{ProtocolSMB}
		assert.False(tt, IsNFSProtocols(protocols))
	})

	t.Run("Empty slice", func(tt *testing.T) {
		protocols := []string{}
		assert.False(tt, IsNFSProtocols(protocols))
	})

	t.Run("Invalid protocol", func(tt *testing.T) {
		protocols := []string{"INVALID"}
		assert.False(tt, IsNFSProtocols(protocols))
	})
}

func TestIsSMBOnlyProtocols(t *testing.T) {
	assert.True(t, IsSMBOnlyProtocols([]string{ProtocolSMB}))
	assert.False(t, IsSMBOnlyProtocols([]string{ProtocolNFSv3, ProtocolSMB}))
	assert.False(t, IsSMBOnlyProtocols([]string{ProtocolNFSv3}))
}

func TestIsNFSOnlyProtocols(t *testing.T) {
	t.Run("All NFS variants", func(tt *testing.T) {
		protocols := []string{ProtocolNFS, ProtocolNFSv3, ProtocolNFSv4}
		assert.True(tt, IsNFSOnlyProtocols(protocols))
	})

	t.Run("Single NFSV3", func(tt *testing.T) {
		assert.True(tt, IsNFSOnlyProtocols([]string{ProtocolNFSv3}))
	})

	t.Run("Mixed NFS and SMB", func(tt *testing.T) {
		protocols := []string{ProtocolNFSv3, ProtocolSMB}
		assert.False(tt, IsNFSOnlyProtocols(protocols))
	})

	t.Run("Only SMB", func(tt *testing.T) {
		assert.False(tt, IsNFSOnlyProtocols([]string{ProtocolSMB}))
	})

	t.Run("Empty slice", func(tt *testing.T) {
		assert.False(tt, IsNFSOnlyProtocols([]string{}))
	})
}

func TestResolveEffectiveUnixSecurityStyle(t *testing.T) {
	tests := []struct {
		name            string
		protocols       []string
		securityStyle   string
		explicitlySet   bool
		unixPermissions string
		expected        string
	}{
		{
			name:            "explicit security style",
			protocols:       []string{"NFSV3"},
			securityStyle:   "ntfs",
			explicitlySet:   true,
			unixPermissions: "0755",
			expected:        "ntfs",
		},
		{
			name:            "NFS-only unix permissions without security style",
			protocols:       []string{"NFSV3"},
			securityStyle:   "",
			explicitlySet:   false,
			unixPermissions: "0755",
			expected:        UnixSecurityStyle,
		},
		{
			name:            "dual protocol without security style",
			protocols:       []string{"NFSV3", "SMB"},
			securityStyle:   "",
			explicitlySet:   false,
			unixPermissions: "0755",
			expected:        "",
		},
		{
			name:            "mixed SAN and NFS without security style",
			protocols:       []string{"ISCSI", "NFSV3"},
			securityStyle:   "",
			explicitlySet:   false,
			unixPermissions: "0755",
			expected:        "",
		},
		{
			name:            "no unix permissions",
			protocols:       []string{"NFSV3"},
			securityStyle:   "",
			explicitlySet:   false,
			unixPermissions: "",
			expected:        "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ResolveEffectiveUnixSecurityStyle(tt.protocols, tt.securityStyle, tt.explicitlySet, tt.unixPermissions)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestValidateUnixPermissions(t *testing.T) {
	tests := []struct {
		name       string
		protocols  []string
		permission string
		style      string
		wantErr    bool
		errMsg     string
	}{
		{
			name:       "Valid NFS permissions",
			protocols:  []string{"NFSV3"},
			permission: "0755",
			style:      "unix",
			wantErr:    false,
		},
		{
			name:       "Empty permissions",
			protocols:  []string{"NFSV3"},
			permission: "",
			style:      "unix",
			wantErr:    true,
			errMsg:     "UnixPermissions cannot be empty.",
		},
		{
			name:       "Invalid permission format",
			protocols:  []string{"NFSV3"},
			permission: "08",
			style:      "unix",
			wantErr:    true,
			errMsg:     "Unix permissions should be 4 digit long in octal format",
		},
		{
			name:       "Mismatched security style",
			protocols:  []string{"NFSV3"},
			permission: "0755",
			style:      "ntfs",
			wantErr:    true,
			errMsg:     "Unix permissions are only supported with unix security-style",
		},
		{
			name:       "NFS-only without explicit security style",
			protocols:  []string{"NFSV3"},
			permission: "0755",
			style:      "",
			wantErr:    false,
		},
		{
			name:       "Dual protocol without explicit security style",
			protocols:  []string{"NFSV3", "SMB"},
			permission: "0755",
			style:      "",
			wantErr:    true,
			errMsg:     "Unix permissions are only supported with unix security-style",
		},
		{
			name:       "Unsupported protocol",
			protocols:  []string{"SMB"},
			permission: "0755",
			style:      "unix",
			wantErr:    true,
			errMsg:     "Unix permissions is only supported with NFS protocol volumes",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateUnixPermissions(tt.protocols, tt.permission, tt.style)
			if tt.wantErr {
				assert.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
)

func TestConvertToDBSnapshotPolicySchedule(t *testing.T) {
	schedule := &models.SnapshotPolicySchedule{
		Count:           5,
		SnapmirrorLabel: "label1",
		Schedule: &models.Schedule{
			DaysOfMonth: []int{1, 15},
			DaysOfWeek:  []int{2, 3},
			Hours:       []int{4},
			Minutes:     []int{30},
		},
	}
	result := ConvertToDBSnapshotPolicySchedule([]*models.SnapshotPolicySchedule{schedule})
	require.Len(t, result, 1)
	assert.Equal(t, int64(5), result[0].Count)
	assert.Equal(t, "label1", result[0].SnapmirrorLabel)
	assert.Equal(t, []int{1, 15}, result[0].DaysOfMonth)
}

func TestConvertToDBSnapshotPolicySchedule_SkipsNilSchedule(t *testing.T) {
	assert.Nil(t, ConvertToDBSnapshotPolicySchedule(nil))
	assert.Nil(t, ConvertToDBSnapshotPolicySchedule([]*models.SnapshotPolicySchedule{
		nil,
		{Schedule: nil},
	}))
}

func TestConvertExportRulesToDatamodel_SkipsNilRules(t *testing.T) {
	rules := ConvertExportRulesToDatamodel([]*models.ExportRule{
		nil,
		{AllowedClients: "0.0.0.0/0", AccessType: models.ReadWrite, NFSv3: true, Index: 1},
	})
	require.Len(t, rules, 1)
	assert.Equal(t, "0.0.0.0/0", rules[0].AllowedClients)
}

func TestBuildFilePropertiesFromParams_NilParams(t *testing.T) {
	assert.Nil(t, BuildFilePropertiesFromParams(nil, "token"))
}

func TestBuildFilePropertiesFromParams_DefaultUnixPermissions(t *testing.T) {
	params := &models.FileProperties{
		ExportPolicy: &models.ExportPolicy{
			ExportPolicyName: "share",
			ExportRules:      []*models.ExportRule{{AccessType: models.ReadWrite, NFSv3: true, Index: 1}},
		},
		SecurityStyle: utils.UnixSecurityStyle,
	}
	result := BuildFilePropertiesFromParams(params, "token")
	require.NotNil(t, result)
	assert.Equal(t, DefaultUnixPermissionsOctal, result.UnixPermissions)
}

func TestBuildFilePropertiesFromParams_ExplicitUnixPermissions(t *testing.T) {
	params := &models.FileProperties{
		ExportPolicy: &models.ExportPolicy{
			ExportPolicyName: "share",
			ExportRules:      []*models.ExportRule{{AccessType: models.ReadWrite, NFSv3: true, Index: 1}},
		},
		SecurityStyle:   utils.UnixSecurityStyle,
		UnixPermissions: "0777",
	}
	result := BuildFilePropertiesFromParams(params, "token")
	require.NotNil(t, result)
	assert.Equal(t, "0777", result.UnixPermissions)
}

func TestBuildFilePropertiesFromParams_SMBShareSettings(t *testing.T) {
	params := &models.FileProperties{
		ExportPolicy: &models.ExportPolicy{
			ExportPolicyName: "share",
			ExportRules:      []*models.ExportRule{{AccessType: models.ReadWrite, CIFS: true, Index: 1}},
		},
		SecurityStyle:    "ntfs",
		SMBShareSettings: []string{"browsable"},
	}
	result := BuildFilePropertiesFromParams(params, "my-share")
	require.NotNil(t, result)
	assert.Equal(t, "/my-share", result.JunctionPath)
	assert.Equal(t, []string{"browsable"}, result.SMBShareSettings)
}

func TestValidateAllowedClients(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"Single valid IPv4", "192.168.1.1", false},
		{"Multiple valid IPv4", "10.0.0.1,192.168.1.2", false},
		{"Valid IPv4 CIDR", "10.0.0.0/24", false},
		{"Mix of IPv4 and CIDR", "10.0.0.1,10.0.0.0/24", false},
		{"Invalid IP", "999.999.999.999", true},
		{"Invalid CIDR", "10.0.0.0/33", true},
		{"IP not matching CIDR base", "10.0.0.5/24", true},
		{"Duplicate IPs", "10.0.0.1,10.0.0.1", true},
		{"Duplicate CIDRs", "10.0.0.0/24,10.0.0.0/24", true},
		{"Empty string", "", true},
		{"Zero address with nonzero mask", "0.0.0.0/8", true},
		{"Allow all clients", models.AllowedAllClients, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateAllowedClients(tt.input)
			if tt.wantErr {
				assert.Error(t, err, "expected error for input: %q", tt.input)
			} else {
				assert.NoError(t, err, "expected no error for input: %q", tt.input)
			}
		})
	}
}

func TestValidateExportRulesAgainstProtocols_NFSv3RuleOnNFSv4Volume(t *testing.T) {
	err := ValidateExportRulesAgainstProtocols([]*models.ExportRule{
		{AllowedClients: "10.0.0.0/8", NFSv3: true},
	}, []string{utils.ProtocolNFSv4})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Cannot specify NFSv3 export policy rules for non-NFSv3 volume")
}

func TestGetMaxQuotaForVolume(t *testing.T) {
	assert.Equal(t, utils.MaxQuotaInBytesForFileVolume, GetMaxQuotaForVolume([]string{utils.ProtocolNFSv3}))
	assert.Equal(t, utils.MaxQuotaInBytesVolumeForVolume, GetMaxQuotaForVolume([]string{"ISCSI"}))
}

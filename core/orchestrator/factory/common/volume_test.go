package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	orchcommon "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
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

func TestPopulateSnapshotPolicyFromParams(t *testing.T) {
	assert.Nil(t, PopulateSnapshotPolicyFromParams(nil))

	result := PopulateSnapshotPolicyFromParams(&models.SnapshotPolicy{
		Name:      "policy-1",
		IsEnabled: true,
		Schedules: []*models.SnapshotPolicySchedule{
			{
				Count:           3,
				SnapmirrorLabel: "daily",
				Schedule:        &models.Schedule{Hours: []int{1}},
			},
		},
	})
	require.NotNil(t, result)
	assert.Equal(t, "policy-1", result.Name)
	assert.True(t, result.IsEnabled)
	require.Len(t, result.Schedules, 1)
	assert.Equal(t, int64(3), result.Schedules[0].Count)
}

func TestValidateSANSnapReserveIncrease(t *testing.T) {
	volumeSize := int64(100 * 1024 * 1024 * 1024)

	t.Run("allows decrease", func(t *testing.T) {
		newSnapReserve := int64(10)
		assert.NoError(t, ValidateSANSnapReserveIncrease(&newSnapReserve, 20, volumeSize, 0))
	})

	t.Run("rejects increase without volume resize", func(t *testing.T) {
		newSnapReserve := int64(50)
		err := ValidateSANSnapReserveIncrease(&newSnapReserve, 0, volumeSize, 0)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "Cannot increase SnapReserve to 50%")
	})

	t.Run("allows increase with sufficient volume resize", func(t *testing.T) {
		newSnapReserve := int64(20)
		assert.NoError(t, ValidateSANSnapReserveIncrease(&newSnapReserve, 10, volumeSize, int64(120*1024*1024*1024)))
	})

	t.Run("rejects increase when resized volume still has insufficient LUN space", func(t *testing.T) {
		newSnapReserve := int64(50)
		err := ValidateSANSnapReserveIncrease(&newSnapReserve, 20, volumeSize, int64(100*1024*1024*1024))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "Please increase the volume size to at least")
	})
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

func TestIsVolumeUpdateRequired(t *testing.T) {
	response := &vsa.VolumeResponse{Size: 200, SnapReserve: 10, SnapshotPolicyName: "policy1", SnapshotDirectoryAccessEnabled: true}
	existing := &datamodel.Volume{
		AutoTieringEnabled: false,
		AutoTieringPolicy:  &datamodel.AutoTieringPolicy{CoolingThresholdDays: 10, TieringPolicy: "auto"},
		VolumeAttributes: &datamodel.VolumeAttributes{
			FileProperties: &datamodel.FileProperties{UnixPermissions: "0755"},
		},
	}

	assert.True(t, IsVolumeUpdateRequired(response, &orchcommon.UpdateVolumeParams{QuotaInBytes: 300}, existing))
	assert.True(t, IsVolumeUpdateRequired(response, &orchcommon.UpdateVolumeParams{
		SnapshotPolicy: &models.SnapshotPolicy{Name: "policy2"},
	}, existing))
	assert.True(t, IsVolumeUpdateRequired(response, &orchcommon.UpdateVolumeParams{SnapReserve: int64Ptr(20)}, existing))
	assert.True(t, IsVolumeUpdateRequired(response, &orchcommon.UpdateVolumeParams{SnapshotDirectoryAccess: boolPtr(false)}, existing))
	assert.True(t, IsVolumeUpdateRequired(response, &orchcommon.UpdateVolumeParams{
		AutoTieringPolicy: &orchcommon.AutoTieringPolicy{AutoTieringEnabled: true, CoolingThresholdDays: 10, TieringPolicy: "auto"},
	}, existing))
	assert.True(t, IsVolumeUpdateRequired(response, &orchcommon.UpdateVolumeParams{
		FileProperties: &models.FileProperties{UnixPermissions: "0770"},
	}, existing))
	assert.False(t, IsVolumeUpdateRequired(nil, &orchcommon.UpdateVolumeParams{QuotaInBytes: 100}, existing))
	assert.False(t, IsVolumeUpdateRequired(response, nil, existing))
	assert.False(t, IsVolumeUpdateRequired(response, &orchcommon.UpdateVolumeParams{QuotaInBytes: 200}, existing))
}

func TestIsExportPolicyRulesUpdateRequired(t *testing.T) {
	current := &datamodel.ExportPolicy{
		ExportPolicyName: "share",
		ExportRules: []*datamodel.ExportRule{
			{AllowedClients: "0.0.0.0/0", AccessType: models.ReadWrite, NFSv3: true, Index: 1},
		},
	}
	update := &models.ExportPolicy{
		ExportPolicyName: "share",
		ExportRules: []*models.ExportRule{
			{AllowedClients: "10.0.0.0/8", AccessType: models.ReadWrite, NFSv3: true, Index: 1},
		},
	}

	assert.True(t, IsExportPolicyRulesUpdateRequired(nil, update))
	assert.True(t, IsExportPolicyRulesUpdateRequired(current, update))
	assert.False(t, IsExportPolicyRulesUpdateRequired(current, &models.ExportPolicy{ExportPolicyName: "other"}))
	assert.False(t, IsExportPolicyRulesUpdateRequired(nil, nil))
	assert.False(t, IsExportPolicyRulesUpdateRequired(&datamodel.ExportPolicy{}, nil))
	assert.True(t, IsExportPolicyRulesUpdateRequired(current, nil))
	assert.False(t, IsExportPolicyRulesUpdateRequired(&datamodel.ExportPolicy{ExportRules: []*datamodel.ExportRule{}}, nil))

	removedRule := &datamodel.ExportPolicy{
		ExportPolicyName: "share",
		ExportRules: []*datamodel.ExportRule{
			{AllowedClients: "0.0.0.0/0", AccessType: models.ReadWrite, NFSv3: true, Index: 1},
			{AllowedClients: "10.0.0.0/8", AccessType: models.ReadWrite, NFSv3: true, Index: 2},
		},
	}
	assert.True(t, IsExportPolicyRulesUpdateRequired(removedRule, update))
}

func TestIsVolumeUpdateRequired_UnixPermissionsWithoutExistingMetadata(t *testing.T) {
	response := &vsa.VolumeResponse{Size: 100}
	params := &orchcommon.UpdateVolumeParams{
		FileProperties: &models.FileProperties{UnixPermissions: "0770"},
	}
	assert.True(t, IsVolumeUpdateRequired(response, params, nil))
}

func int64Ptr(v int64) *int64 { return &v }
func boolPtr(v bool) *bool    { return &v }

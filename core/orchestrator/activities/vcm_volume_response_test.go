package activities

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	utilErrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
)

func TestBuildVolumeOperationEnrichment_NASExportPolicy(t *testing.T) {
	provider := vsa.NewMockProvider(t)
	provider.On("GetExportPolicy", "nfs-vol", "test-svm").Return(&vsa.ExportPolicy{
		ExportPolicyName: "nfs-vol",
		ExportRules: []*vsa.ExportRule{{
			AllowedClients: "10.0.0.0/8",
			AccessType:     models.ReadWrite,
			NFSv3:          true,
			NFSv4:          false,
			Superuser:      true,
		}},
	}, nil)

	enrichment, err := BuildVolumeOperationEnrichment(provider, "test-svm", &vsa.VolumeResponse{
		JunctionPath:     "/nfs-vol",
		ExportPolicyName: "nfs-vol",
	})
	require.NoError(t, err)
	require.NotNil(t, enrichment.ExportPolicy)
	rules, ok := enrichment.ExportPolicy["rules"].([]map[string]any)
	require.True(t, ok)
	require.Len(t, rules, 1)
	assert.Equal(t, "10.0.0.0/8", rules[0]["allowedClients"])
	assert.Equal(t, models.ReadWrite, rules[0]["accessType"])
	assert.Equal(t, true, rules[0]["nfsv3"])
	assert.Equal(t, false, rules[0]["nfsv4"])
	assert.Equal(t, "true", rules[0]["hasRootAccess"])
	assert.Equal(t, false, rules[0]["kerberos5ReadOnly"])
	assert.Equal(t, false, rules[0]["kerberos5ReadWrite"])
	assert.Equal(t, false, rules[0]["kerberos5iReadOnly"])
	assert.Equal(t, false, rules[0]["kerberos5iReadWrite"])
	assert.Equal(t, false, rules[0]["kerberos5pReadOnly"])
	assert.Equal(t, false, rules[0]["kerberos5pReadWrite"])
	assert.NotContains(t, rules[0], "allSquash")
	assert.NotContains(t, rules[0], "anonUid")
}

func TestExportPolicyOperationPayload_AllSquashAndKerberos(t *testing.T) {
	allSquash := true
	anonUID := int64(65534)
	payload := exportPolicyOperationPayload(&vsa.ExportPolicy{
		ExportRules: []*vsa.ExportRule{{
			AllowedClients:     "0.0.0.0/0",
			AccessType:         models.ReadWrite,
			NFSv3:              true,
			Kerberos5ReadWrite: true,
			Superuser:          false,
			AllSquash:          &allSquash,
			AnonUid:            &anonUID,
		}},
	})

	require.NotNil(t, payload)
	rules, ok := payload["rules"].([]map[string]any)
	require.True(t, ok)
	require.Len(t, rules, 1)
	assert.Equal(t, true, rules[0]["allSquash"])
	assert.Equal(t, int64(65534), rules[0]["anonUid"])
	assert.Equal(t, false, rules[0]["kerberos5ReadOnly"])
	assert.Equal(t, true, rules[0]["kerberos5ReadWrite"])
	assert.Equal(t, "false", rules[0]["hasRootAccess"])
}

func TestBuildVolumeOperationEnrichment_NASSnapshotPolicy(t *testing.T) {
	provider := vsa.NewMockProvider(t)
	provider.On("GetSnapshotPolicy", "voltest_nfs01").Return(&vsa.SnapshotPolicy{
		Name:      "voltest_nfs01",
		IsEnabled: true,
		Schedules: []*vsa.SnapshotPolicySchedule{{
			Count:           4,
			SnapmirrorLabel: "hourly",
			Schedule: &vsa.Schedule{
				Minutes: []int{0},
			},
		}},
	}, nil)

	enrichment, err := BuildVolumeOperationEnrichment(provider, "test-svm", &vsa.VolumeResponse{
		JunctionPath:       "/voltest_nfs01",
		SnapshotPolicyName: "voltest_nfs01",
	})
	require.NoError(t, err)
	require.NotNil(t, enrichment.SnapshotPolicy)
	assert.Equal(t, true, enrichment.SnapshotPolicy["enabled"])
	hourlySchedule, ok := enrichment.SnapshotPolicy["hourlySchedule"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, float64(4), hourlySchedule["snapshotsToKeep"])
	assert.Equal(t, float64(0), hourlySchedule["minute"])
}

func TestBuildVolumeOperationEnrichment_SANSnapshotPolicy(t *testing.T) {
	provider := vsa.NewMockProvider(t)
	provider.On("LunList", vsa.LunGetParams{
		SvmName:    "test-svm",
		VolumeName: "voltest_iscsi01",
	}).Return([]*vsa.LunResponse{{
		ProviderResponse: vsa.ProviderResponse{ExternalUUID: "lun-uuid"},
		OSType:           "linux",
		IgroupNames:      []string{"hgtest_linux01"},
	}}, nil)
	provider.On("IgroupNamesForLunUUID", "test-svm", "lun-uuid").Return([]string{"hgtest_linux01", "hgtest_linux03"}, nil)
	provider.On("GetSnapshotPolicy", "voltest_iscsi01").Return(&vsa.SnapshotPolicy{
		Name:      "voltest_iscsi01",
		IsEnabled: true,
		Schedules: []*vsa.SnapshotPolicySchedule{{
			Count:           4,
			SnapmirrorLabel: "hourly",
			Schedule:        &vsa.Schedule{Minutes: []int{0}},
		}},
	}, nil)

	enrichment, err := BuildVolumeOperationEnrichment(provider, "test-svm", &vsa.VolumeResponse{
		ProviderResponse:   vsa.ProviderResponse{Name: "voltest_iscsi01"},
		SnapshotPolicyName: "voltest_iscsi01",
	})
	require.NoError(t, err)
	require.Len(t, enrichment.BlockDevices, 1)
	require.NotNil(t, enrichment.SnapshotPolicy)
	assert.Equal(t, true, enrichment.SnapshotPolicy["enabled"])
}

func TestBuildVolumeOperationEnrichment_SANBlockDevices(t *testing.T) {
	provider := vsa.NewMockProvider(t)
	provider.On("LunList", vsa.LunGetParams{
		SvmName:    "test-svm",
		VolumeName: "vol-1",
	}).Return([]*vsa.LunResponse{{
		ProviderResponse: vsa.ProviderResponse{Name: "/vol/lun_vol-1", ExternalUUID: "lun-uuid"},
		OSType:           "linux",
		IgroupNames:      []string{"hg-old"},
	}}, nil)
	provider.On("IgroupNamesForLunUUID", "test-svm", "lun-uuid").Return([]string{"hg-old", "hg-new"}, nil)

	enrichment, err := BuildVolumeOperationEnrichment(provider, "test-svm", &vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{Name: "vol-1"},
	})
	require.NoError(t, err)
	require.Len(t, enrichment.BlockDevices, 1)
	assert.Equal(t, "LINUX", enrichment.BlockDevices[0]["osType"])
	assert.Equal(t, []string{"hg-old", "hg-new"}, enrichment.BlockDevices[0]["igroups"])
}

func TestBuildVolumeOperationEnrichment_SANBlockDevices_VMwareMapsToESXI(t *testing.T) {
	provider := vsa.NewMockProvider(t)
	provider.On("LunList", vsa.LunGetParams{
		SvmName:    "test-svm",
		VolumeName: "vol-esxi",
	}).Return([]*vsa.LunResponse{{
		ProviderResponse: vsa.ProviderResponse{Name: "/vol/lun_vol-esxi", ExternalUUID: "lun-uuid"},
		OSType:           "vmware",
		IgroupNames:      []string{"hg-esxi"},
	}}, nil)
	provider.On("IgroupNamesForLunUUID", "test-svm", "lun-uuid").Return([]string{"hg-esxi"}, nil)

	enrichment, err := BuildVolumeOperationEnrichment(provider, "test-svm", &vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{Name: "vol-esxi"},
	})
	require.NoError(t, err)
	require.Len(t, enrichment.BlockDevices, 1)
	assert.Equal(t, "ESXI", enrichment.BlockDevices[0]["osType"])
}

func TestBuildVolumeOperationEnrichment_SANNoLuns(t *testing.T) {
	provider := vsa.NewMockProvider(t)
	provider.On("LunList", mock.Anything).Return(nil, vsaerrors.NewVCPError(vsaerrors.ErrOntapRestAPIError, utilErrors.NewNotFoundErr("lun", nil)))

	enrichment, err := BuildVolumeOperationEnrichment(provider, "test-svm", &vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{Name: "vol-1"},
	})
	require.NoError(t, err)
	assert.Empty(t, enrichment.BlockDevices)
}

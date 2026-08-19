package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
)

func TestUpdateExternalClusterParams_HasUpdates(t *testing.T) {
	tests := []struct {
		name   string
		params *UpdateExternalClusterParams
		want   bool
	}{
		{name: "nil params", params: nil, want: false},
		{name: "empty params", params: &UpdateExternalClusterParams{}, want: false},
		{name: "description set", params: &UpdateExternalClusterParams{Description: ptrString("x")}, want: true},
		{name: "label set", params: &UpdateExternalClusterParams{Label: ptrString("x")}, want: true},
		{name: "management IP set", params: &UpdateExternalClusterParams{ManagementIP: ptrString("10.0.0.1")}, want: true},
		{name: "protocol set", params: &UpdateExternalClusterParams{Protocol: ptrString("HTTPS")}, want: true},
		{name: "port set", params: &UpdateExternalClusterParams{Port: ptrInt(443)}, want: true},
		{name: "username set", params: &UpdateExternalClusterParams{Username: ptrString("admin")}, want: true},
		{name: "password set", params: &UpdateExternalClusterParams{Password: ptrString("secret")}, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.params.HasUpdates())
		})
	}
}

func ptrString(s string) *string { return &s }

func ptrInt(n int) *int { return &n }

func TestMaintenanceOperationType_IsValid(t *testing.T) {
	tests := []struct {
		op   MaintenanceOperationType
		want bool
	}{
		{op: MaintenanceOperationRebootMigrate, want: true},
		{op: MaintenanceOperationRecreate, want: true},
		{op: MaintenanceOperationType("bogus"), want: false},
		{op: MaintenanceOperationType(""), want: false},
	}
	for _, tt := range tests {
		t.Run(string(tt.op), func(t *testing.T) {
			assert.Equal(t, tt.want, tt.op.IsValid())
		})
	}
}

func TestVolumeFetchOptionsFromFields(t *testing.T) {
	t.Run("NilFieldSet", func(tt *testing.T) {
		opts := VolumeFetchOptionsFromFields(nil)
		assert.Equal(tt, VolumeFetchOptions{}, opts)
	})

	t.Run("DerivesRequestedOptions", func(tt *testing.T) {
		opts := VolumeFetchOptionsFromFields(map[string]bool{
			"activeDirectoryConfigId": true,
			"kmsConfigResourceId":     true,
			"throughputMibps":         true,
			"mountPoints":             true,
			"inReplication":           true,
		})
		assert.True(tt, opts.NeedActiveDirectory)
		assert.True(tt, opts.NeedKmsConfig)
		assert.True(tt, opts.NeedVolumePerformanceGroup)
		assert.True(tt, opts.NeedIPAddresses)
		assert.True(tt, opts.NeedInReplication)
	})
}

func TestVCMCreatePoolParams_DeriveInputs(t *testing.T) {
	t.Run("derives region, location, tenancy (data net), mode, capacity", func(tt *testing.T) {
		p := &VCMCreatePoolParams{
			PrimaryZone:          "us-central1-a",
			SecondaryZone:        "us-central1-c",
			MediatorZone:         "us-central1-b",
			ServiceTenantProject: "tenant-proj",
			SNHostProject:        "host-proj",
			Mode:                 "ONTAP",
			SizeInBytes:          2 * vcmBytesPerGiB,
			NodeMgmt:             VCMNetConfig{VPC: "mgmt-vpc", Subnet: "mgmt-subnet"},
			Data:                 &VCMNetConfig{VPC: "data-vpc", Subnet: "data-subnet"},
		}

		d := p.DeriveInputs()

		assert.Equal(tt, "us-central1", d.Region, "region must strip the zone suffix")
		assert.True(tt, d.IsOntapMode)
		assert.False(tt, d.IsLargeCapacityPool)
		assert.Equal(tt, int64(2), d.DesiredCapacityInGiB, "capacity must convert bytes to GiB")

		assert.Equal(tt, "us-central1", d.LocationInfo.Region)
		assert.Equal(tt, "us-central1-a", d.LocationInfo.PrimaryZone)
		assert.Equal(tt, "us-central1-c", d.LocationInfo.SecondaryZone)
		assert.Equal(tt, "us-central1-b", d.LocationInfo.MediatorZone)

		assert.Equal(tt, "tenant-proj", d.TenancyInfo.RegionalTenantProject)
		assert.Equal(tt, "host-proj", d.TenancyInfo.SnHostProject)
		assert.Equal(tt, "data-vpc", d.TenancyInfo.Network, "must prefer the data net config when present")
		assert.Equal(tt, []string{"data-subnet"}, d.TenancyInfo.SubnetworkNames)
	})

	t.Run("falls back to node-mgmt net and non-ontap mode", func(tt *testing.T) {
		p := &VCMCreatePoolParams{
			PrimaryZone: "us-east4-a",
			Mode:        "DEFAULT",
			NodeMgmt:    VCMNetConfig{VPC: "mgmt-vpc", Subnet: "mgmt-subnet"},
		}

		d := p.DeriveInputs()

		assert.Equal(tt, "us-east4", d.Region)
		assert.False(tt, d.IsOntapMode)
		assert.Equal(tt, "mgmt-vpc", d.TenancyInfo.Network)
		assert.Equal(tt, []string{"mgmt-subnet"}, d.TenancyInfo.SubnetworkNames)
	})

	t.Run("derives ontap credentials auth type from material present", func(tt *testing.T) {
		cert := &VCMCreatePoolParams{
			OntapCredentials: []VCMOntapCredential{
				{Type: "admin", CertificateID: "cert-1", SecretID: "secret-1"},
			},
		}
		d := cert.DeriveInputs()
		assert.Equal(tt, "cert-1", d.OntapCredentials.CertificateID)
		assert.Equal(tt, env.USER_CERTIFICATE, d.OntapCredentials.AuthType, "certificate present -> user certificate")

		secret := &VCMCreatePoolParams{
			CaURI: "ca-uri",
			OntapCredentials: []VCMOntapCredential{
				{Type: "admin", SecretID: "secret-1"},
			},
		}
		d = secret.DeriveInputs()
		assert.Equal(tt, "secret-1", d.OntapCredentials.SecretID)
		assert.Equal(tt, "ca-uri", d.OntapCredentials.CaURI)
		assert.Equal(tt, env.USERNAME_PWD_SEC_MGR, d.OntapCredentials.AuthType, "secret only -> secret manager auth")
	})
}

func TestSelectOntapCredential(t *testing.T) {
	admin := VCMOntapCredential{Type: "admin", SecretID: "admin-secret"}
	def := VCMOntapCredential{Type: "default", SecretID: "default-secret"}
	other := VCMOntapCredential{Type: "ontap-mode", SecretID: "mode-secret"}

	tests := []struct {
		name  string
		creds []VCMOntapCredential
		want  VCMOntapCredential
	}{
		{name: "prefers admin", creds: []VCMOntapCredential{other, def, admin}, want: admin},
		{name: "falls back to default", creds: []VCMOntapCredential{other, def}, want: def},
		{name: "falls back to first", creds: []VCMOntapCredential{other}, want: other},
		{name: "empty returns zero value", creds: nil, want: VCMOntapCredential{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, SelectOntapCredential(tc.creds))
		})
	}
}

func TestNormalizeExpertModeVolumeTenancyLimits(t *testing.T) {
	tests := []struct {
		name      string
		limit     int
		normalize func(int) int
		want      int
	}{
		{
			name:      "PageLimitDefaults",
			limit:     0,
			normalize: NormalizeExpertModeVolumeTenancyPageLimit,
			want:      DefaultExpertModeVolumeTenancyPageLimit,
		},
		{
			name:      "PageLimitIsCapped",
			limit:     MaxExpertModeVolumeTenancyPageLimit + 1,
			normalize: NormalizeExpertModeVolumeTenancyPageLimit,
			want:      MaxExpertModeVolumeTenancyPageLimit,
		},
		{
			name:      "QueryLimitAllowsLookahead",
			limit:     MaxExpertModeVolumeTenancyPageLimit + 1,
			normalize: NormalizeExpertModeVolumeTenancyQueryLimit,
			want:      MaxExpertModeVolumeTenancyQueryLimit,
		},
		{
			name:      "QueryLimitIsCapped",
			limit:     MaxExpertModeVolumeTenancyQueryLimit + 1,
			normalize: NormalizeExpertModeVolumeTenancyQueryLimit,
			want:      MaxExpertModeVolumeTenancyQueryLimit,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.normalize(tt.limit))
		})
	}
}

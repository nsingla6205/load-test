package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
)

func TestSnapmirrorRelationshipEndpointUUID(t *testing.T) {
	tests := []struct {
		name         string
		relationship *SnapmirrorRelationship
		want         string
	}{
		{name: "nil relationship", relationship: nil, want: ""},
		{name: "no destination", relationship: &SnapmirrorRelationship{}, want: ""},
		{name: "destination set", relationship: &SnapmirrorRelationship{DestinationUUID: ptrString("endpoint-uuid")}, want: "endpoint-uuid"},
		{name: "destination padded", relationship: &SnapmirrorRelationship{DestinationUUID: ptrString("  endpoint-uuid  ")}, want: "endpoint-uuid"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.relationship.EndpointUUID())
		})
	}
}

func TestBackupEndpointUUID(t *testing.T) {
	tests := []struct {
		name   string
		backup *datamodel.Backup
		want   string
	}{
		{name: "nil backup", backup: nil, want: ""},
		{name: "no attributes", backup: &datamodel.Backup{}, want: ""},
		{name: "endpoint set", backup: &datamodel.Backup{Attributes: &datamodel.BackupAttributes{EndpointUUID: " endpoint-uuid "}}, want: "endpoint-uuid"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, BackupEndpointUUID(tt.backup))
		})
	}
}

func TestBackupEndpointMatches(t *testing.T) {
	tests := []struct {
		name           string
		endpoint       string
		backupEndpoint string
		want           bool
	}{
		{name: "same endpoint", endpoint: "endpoint-a", backupEndpoint: "endpoint-a", want: true},
		{name: "different endpoint", endpoint: "endpoint-a", backupEndpoint: "endpoint-b", want: false},
		{name: "case differs", endpoint: "ENDPOINT-A", backupEndpoint: "endpoint-a", want: true},
		{name: "padding differs", endpoint: " endpoint-a ", backupEndpoint: "endpoint-a", want: true},
		// An endpoint we cannot read has to count as a match, so callers stay on the safe side.
		{name: "endpoint unknown", endpoint: "", backupEndpoint: "endpoint-a", want: true},
		{name: "backup endpoint unknown", endpoint: "endpoint-a", backupEndpoint: "  ", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, BackupEndpointMatches(tt.endpoint, tt.backupEndpoint))
		})
	}
}

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

	t.Run("derives ontap credentials from default user + admin password", func(tt *testing.T) {
		// Cert path: default user drives cert material; admin entry supplies the
		// bootstrap admin password (SecretID).
		cert := &VCMCreatePoolParams{
			OntapCredentials: []VCMOntapCredential{
				{Type: "admin", SecretID: "admin-pass"},
				{Type: "default", Username: "vcm-admin", CertificateID: "cert-1", CertPrivateKeySecretID: "key-1", IsCertAuth: true},
			},
		}
		d := cert.DeriveInputs()
		assert.Equal(tt, "cert-1", d.OntapCredentials.CertificateID)
		assert.Equal(tt, "key-1", d.OntapCredentials.CertPrivateKeySecretID)
		assert.Equal(tt, "admin-pass", d.OntapCredentials.SecretID, "cert bootstrap uses adminPassSecretId")
		assert.Equal(tt, env.USER_CERTIFICATE, d.OntapCredentials.AuthType, "certificate present -> user certificate")

		// Password path: default user's password secret drives SecretID.
		secret := &VCMCreatePoolParams{
			CaURI: "ca-uri",
			OntapCredentials: []VCMOntapCredential{
				{Type: "admin", SecretID: "admin-pass"},
				{Type: "default", Username: "root", SecretID: "vcm-secret"},
			},
		}
		d = secret.DeriveInputs()
		assert.Equal(tt, "vcm-secret", d.OntapCredentials.SecretID)
		assert.Equal(tt, "ca-uri", d.OntapCredentials.CaURI)
		assert.Equal(tt, env.USERNAME_PWD_SEC_MGR, d.OntapCredentials.AuthType, "secret only -> secret manager auth")
	})
}

func TestDeriveOntapCredentials(t *testing.T) {
	t.Run("admin-only falls back to the built-in admin password secret", func(tt *testing.T) {
		got := DeriveOntapCredentials([]VCMOntapCredential{{Type: "admin", SecretID: "admin-pass"}}, "ca-uri")

		assert.Equal(tt, "admin-pass", got.SecretID)
		assert.Equal(tt, env.USERNAME_PWD_SEC_MGR, got.AuthType)
		assert.Equal(tt, "ca-uri", got.CaURI)
		assert.Empty(tt, got.Username, "no user entry means no username to authenticate as")
	})

	t.Run("no credentials at all yields plain password auth", func(tt *testing.T) {
		got := DeriveOntapCredentials(nil, "")

		assert.Empty(tt, got.SecretID)
		assert.Equal(tt, env.USERNAME_PWD, got.AuthType)
	})

	t.Run("first non-admin entry becomes primary when no default user exists", func(tt *testing.T) {
		// Without a "default" entry the first non-admin credential (here
		// PURPOSE_ONTAP_MODE) drives the identity, and its username is kept as
		// configured rather than replaced by the built-in admin.
		got := DeriveOntapCredentials([]VCMOntapCredential{
			{Type: "admin", SecretID: "admin-pass"},
			{Type: "ontap-mode", Username: "ontap-user", SecretID: "ontap-secret"},
			{Type: "ontap-mode", Username: "second-user", SecretID: "second-secret"},
		}, "")

		assert.Equal(tt, "ontap-user", got.Username)
		assert.Equal(tt, "ontap-secret", got.SecretID)
		assert.Equal(tt, env.USERNAME_PWD_SEC_MGR, got.AuthType)
	})

	t.Run("primary without a password secret borrows the admin password secret", func(tt *testing.T) {
		got := DeriveOntapCredentials([]VCMOntapCredential{
			{Type: "admin", SecretID: "admin-pass"},
			{Type: "default", Username: "vcm-admin"},
		}, "")

		assert.Equal(tt, env.Admin, got.Username, "the default-mode user is not password-authenticatable")
		assert.Equal(tt, "admin-pass", got.SecretID)
		assert.Equal(tt, env.USERNAME_PWD_SEC_MGR, got.AuthType)
	})

	t.Run("no password secret anywhere yields plain password auth", func(tt *testing.T) {
		got := DeriveOntapCredentials([]VCMOntapCredential{{Type: "default", Username: "vcm-admin"}}, "")

		assert.Empty(tt, got.SecretID)
		assert.Equal(tt, env.USERNAME_PWD, got.AuthType)
	})

	t.Run("cert-auth entry flagged without a certificate id falls through to password auth", func(tt *testing.T) {
		got := DeriveOntapCredentials([]VCMOntapCredential{
			{Type: "admin", SecretID: "admin-pass"},
			{Type: "default", Username: "vcm-admin", IsCertAuth: true},
		}, "")

		assert.Equal(tt, env.USERNAME_PWD_SEC_MGR, got.AuthType)
		assert.Empty(tt, got.CertificateID)
	})
}

func TestAdminPasswordSecret(t *testing.T) {
	creds := []VCMOntapCredential{
		{Type: "default", SecretID: "vcm-secret"},
		{Type: "admin", SecretID: "admin-pass"},
	}
	assert.Equal(t, "admin-pass", AdminPasswordSecret(creds))
	assert.Empty(t, AdminPasswordSecret([]VCMOntapCredential{{Type: "default", SecretID: "x"}}))
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

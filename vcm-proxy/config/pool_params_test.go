package config

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	vcmserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcm-proxy/api/vcm-servergen"
)

// poolSeedStore applies a seed configuration that satisfies every create-pool
// precondition, optionally mutated by the caller to exercise a gap.
func poolSeedStore(t *testing.T, mutate func(*vcmserver.SeedConfigV1)) *Store {
	t.Helper()
	store := NewStoreWithOpener(log.NewLogger(), &stubDBOpener{})
	req := testSetConfigRequest(testDBConfig(), "us-central1-a")
	mutateSeed(req, func(seed *vcmserver.SeedConfigV1) {
		seed.DeploymentId = vcmserver.NewOptString("gcnv-test")
		seed.Zone2 = vcmserver.NewOptString("us-central1-c")
		seed.MediatorZone = vcmserver.NewOptString("us-central1-b")
		seed.ConsumerProjectNum = vcmserver.NewOptInt64(1029876893740)
		seed.ServiceTenantProjectNum = vcmserver.NewOptInt64(807186998501)
		seed.SnHostProjectNum = vcmserver.NewOptInt64(303030303030)
		seed.ServiceAccountEmail = vcmserver.NewOptString("sa@example.iam.gserviceaccount.com")
		seed.CaUri = vcmserver.NewOptString("ca-uri")
		seed.OntapLicenseSecret = vcmserver.NewOptString("projects/237071556299/secrets/ontap-license-secret")
		seed.NetConfig = vcmserver.NewOptNetConfigV1(vcmserver.NetConfigV1{
			Nodemgmt: vcmserver.NewOptNetInterfaceConfigV1(vcmserver.NetInterfaceConfigV1{Vpc: "mgmt-vpc", Subnet: "mgmt-subnet"}),
			Data:     vcmserver.NewOptNetInterfaceConfigV1(vcmserver.NetInterfaceConfigV1{Vpc: "data-vpc", Subnet: "data-subnet"}),
		})
		seed.OntapCredentials = ontapModeOntapCredentials(
			passwordUser(vcmserver.UserCredentialsV1UserPurposePURPOSEONTAPMODE, "mode-user", "mode-secret"))
		if mutate != nil {
			mutate(seed)
		}
	})
	store.Apply(context.Background(), req)
	return store
}

// ontapModeOntapCredentials returns the baseline credentials plus an ONTAP-mode
// user, which an ONTAP-mode create requires.
func ontapModeOntapCredentials(mode vcmserver.UserCredentialsV1) vcmserver.OptOntapCredentialsV1 {
	creds := defaultOntapCredentials("admin-secret").Value
	creds.OntapModeUserCredentials.SetTo(mode)
	return vcmserver.NewOptOntapCredentialsV1(creds)
}

// ontapModeShape builds a shape selecting ONTAP mode. An empty fileURL and
// checksum leave the RBAC pair unset.
func ontapModeShape(fileURL, checksum string) vcmserver.ClusterShapeV1 {
	shape := vcmserver.ClusterShapeV1{
		Mode: vcmserver.NewOptClusterShapeV1Mode(vcmserver.ClusterShapeV1ModeONTAP),
	}
	if fileURL == "" && checksum == "" {
		return shape
	}
	rbac := vcmserver.OntapModeRbacV1{}
	if fileURL != "" {
		rbac.RbacFileUrl.SetTo(fileURL)
	}
	if checksum != "" {
		rbac.RbacFileCheckSum.SetTo(checksum)
	}
	shape.OntapModeRbac.SetTo(rbac)
	return shape
}

func testClusterShape() vcmserver.ClusterShapeV1 {
	shape := ontapModeShape("gs://request-bucket/ontap-mode.yaml", "request-checksum")
	shape.ProvisionedThroughputMibps = vcmserver.NewOptInt64(1024)
	shape.ProvisionedIops = vcmserver.NewOptInt64(5000)
	shape.ProvisionedSizeInBytes = vcmserver.NewOptInt64(1099511627776)
	shape.QosType = vcmserver.NewOptString("auto")
	shape.RegionIdentifier = vcmserver.NewOptString("34")
	shape.AutoTieringConfig = vcmserver.NewOptAutoTieringConfigV1(vcmserver.AutoTieringConfigV1{
		AutoTierBucketName: vcmserver.NewOptString("bucket-1"),
	})
	return shape
}

func TestStore_SeedPoolParams_MapsConfiguredInputs(t *testing.T) {
	params, err := poolSeedStore(t, nil).SeedPoolParams()

	require.NoError(t, err)
	assert.Equal(t, "gcnv-test", params.DeploymentID)
	assert.Equal(t, "us-central1-a", params.PrimaryZone)
	assert.Equal(t, "us-central1-c", params.SecondaryZone)
	assert.Equal(t, "us-central1-b", params.MediatorZone)
	assert.Equal(t, "1029876893740", params.ProjectID)
	assert.Equal(t, "807186998501", params.ServiceTenantProject)
	assert.Equal(t, "303030303030", params.SNHostProject)
	assert.Equal(t, "sa@example.iam.gserviceaccount.com", params.ServiceAccountEmail)
	assert.Equal(t, "ca-uri", params.CaURI)
	assert.Equal(t, "projects/237071556299/secrets/ontap-license-secret", params.OntapLicenseSecretURI)
	assert.Equal(t, commonparams.VCMNetConfig{VPC: "mgmt-vpc", Subnet: "mgmt-subnet"}, params.NodeMgmt)
	require.NotNil(t, params.Data)
	assert.Equal(t, commonparams.VCMNetConfig{VPC: "data-vpc", Subnet: "data-subnet"}, *params.Data)
	// admin (adminPassSecretId) + default (VCM_ADMIN) + ontap-mode entries.
	require.Len(t, params.OntapCredentials, 3)
	assert.Equal(t, "admin-pass", commonparams.AdminPasswordSecret(params.OntapCredentials))
	var def, mode commonparams.VCMOntapCredential
	for _, c := range params.OntapCredentials {
		switch c.Type {
		case "default":
			def = c
		case "ontap-mode":
			mode = c
		}
	}
	assert.Equal(t, "admin-secret", def.SecretID, "default user resolves its password secret")
	assert.Empty(t, def.CertificateID, "password auth must not resolve certificate material")
	assert.Equal(t, "mode-user", mode.Username)
	assert.Equal(t, "mode-secret", mode.SecretID)

	// Request-owned fields stay zero until the shape is overlaid.
	assert.Empty(t, params.Mode)
	assert.Zero(t, params.SizeInBytes)
	assert.Empty(t, params.OntapModeRbacFileURL, "the RBAC pair is request-owned")
}

func TestStore_SeedPoolParams_AbsentDataNetStaysNil(t *testing.T) {
	store := poolSeedStore(t, func(seed *vcmserver.SeedConfigV1) {
		seed.NetConfig = vcmserver.NewOptNetConfigV1(vcmserver.NetConfigV1{
			Nodemgmt: vcmserver.NewOptNetInterfaceConfigV1(vcmserver.NetInterfaceConfigV1{Vpc: "mgmt-vpc", Subnet: "mgmt-subnet"}),
		})
	})

	params, err := store.SeedPoolParams()

	require.NoError(t, err)
	assert.Nil(t, params.Data)
}

func TestStore_SeedPoolParams_AbsentOntapLicenseStaysEmpty(t *testing.T) {
	store := poolSeedStore(t, func(seed *vcmserver.SeedConfigV1) { seed.OntapLicenseSecret.Reset() })

	params, err := store.SeedPoolParams()

	require.NoError(t, err, "ontapLicenseSecret is optional for pool create")
	assert.Empty(t, params.OntapLicenseSecretURI)
}

func TestStore_SeedPoolParams_RejectsIncompleteConfig(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*vcmserver.SeedConfigV1)
		wantMsg string
	}{
		{
			name:    "missing deploymentId",
			mutate:  func(seed *vcmserver.SeedConfigV1) { seed.DeploymentId.Reset() },
			wantMsg: "deploymentId is not configured",
		},
		{
			name:    "missing zone1",
			mutate:  func(seed *vcmserver.SeedConfigV1) { seed.Zone1.Reset() },
			wantMsg: "zone1 is not configured",
		},
		{
			name:    "missing netConfig",
			mutate:  func(seed *vcmserver.SeedConfigV1) { seed.NetConfig.Reset() },
			wantMsg: "netConfig is not configured",
		},
		{
			name: "missing nodemgmt",
			mutate: func(seed *vcmserver.SeedConfigV1) {
				seed.NetConfig = vcmserver.NewOptNetConfigV1(vcmserver.NetConfigV1{})
			},
			wantMsg: "netConfig.nodemgmt is not configured",
		},
		{
			name:    "missing ontapCredentials",
			mutate:  func(seed *vcmserver.SeedConfigV1) { seed.OntapCredentials.Reset() },
			wantMsg: "ontapCredentials are not configured",
		},
		{
			name: "missing adminPassSecretId",
			mutate: func(seed *vcmserver.SeedConfigV1) {
				seed.OntapCredentials = ontapCredentials("",
					passwordUser(vcmserver.UserCredentialsV1UserPurposePURPOSEVCMADMIN, "root", "secret-1"))
			},
			wantMsg: "adminPassSecretId is required",
		},
		{
			// A CERT user without certId would otherwise leave only the
			// private-key secret set and be dereferenced as a password.
			name: "cert user without certId",
			mutate: func(seed *vcmserver.SeedConfigV1) {
				seed.OntapCredentials = ontapCredentials("admin-pass",
					certUser(vcmserver.UserCredentialsV1UserPurposePURPOSEVCMADMIN, "vcm-admin", "", "key-1"))
			},
			wantMsg: "certId is required when activeAuthMode is AUTHMODE_CERT",
		},
		{
			name: "cert user without certPrivateKeySecret",
			mutate: func(seed *vcmserver.SeedConfigV1) {
				seed.OntapCredentials = ontapCredentials("admin-pass",
					certUser(vcmserver.UserCredentialsV1UserPurposePURPOSEVCMADMIN, "vcm-admin", "cert-1", ""))
			},
			wantMsg: "certPrivateKeySecret is required when activeAuthMode is AUTHMODE_CERT",
		},
		{
			name: "cert user without VSA_DEPLOYED_DNS_NAME",
			mutate: func(seed *vcmserver.SeedConfigV1) {
				seed.OntapCredentials = ontapCredentials("admin-pass",
					certUser(vcmserver.UserCredentialsV1UserPurposePURPOSEVCMADMIN, "vcm-admin", "cert-1", "key-1"))
			},
			wantMsg: "VSA_DEPLOYED_DNS_NAME",
		},
		{
			name: "password user without passwordSecret",
			mutate: func(seed *vcmserver.SeedConfigV1) {
				seed.OntapCredentials = ontapCredentials("admin-pass",
					passwordUser(vcmserver.UserCredentialsV1UserPurposePURPOSEVCMADMIN, "root", ""))
			},
			wantMsg: "passwordSecret is required when activeAuthMode is AUTHMODE_PASSWORD",
		},
		{
			name: "ontap-mode user with mismatched purpose",
			mutate: func(seed *vcmserver.SeedConfigV1) {
				oc := vcmserver.OntapCredentialsV1{}
				oc.AdminPassSecretId.SetTo("admin-pass")
				oc.DefaultModeUserCredentials.SetTo(passwordUser(vcmserver.UserCredentialsV1UserPurposePURPOSEVCMADMIN, "root", "secret-1"))
				oc.OntapModeUserCredentials.SetTo(passwordUser(vcmserver.UserCredentialsV1UserPurposePURPOSEVCMADMIN, "om", "om-secret"))
				seed.OntapCredentials = vcmserver.NewOptOntapCredentialsV1(oc)
			},
			wantMsg: "ontapModeUserCredentials.userPurpose must be PURPOSE_ONTAP_MODE",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.wantMsg == "VSA_DEPLOYED_DNS_NAME" {
				restore := setVsaDeployedDnsName(t, "")
				defer restore()
			}
			_, err := poolSeedStore(t, tt.mutate).SeedPoolParams()

			require.Error(t, err)
			assert.ErrorIs(t, err, datamodel.ErrInvalidConfig)
			assert.Contains(t, err.Error(), tt.wantMsg)
		})
	}
}

func TestStore_DeploymentID(t *testing.T) {
	deploymentID, err := poolSeedStore(t, nil).DeploymentID()
	require.NoError(t, err)
	assert.Equal(t, "gcnv-test", deploymentID)

	_, err = poolSeedStore(t, func(seed *vcmserver.SeedConfigV1) { seed.DeploymentId.Reset() }).DeploymentID()
	require.Error(t, err)
	assert.ErrorIs(t, err, datamodel.ErrInvalidConfig)
}

func TestApplyClusterShape(t *testing.T) {
	params := &commonparams.VCMCreatePoolParams{}

	ApplyClusterShape(params, testClusterShape())

	assert.Equal(t, "ONTAP", params.Mode)
	assert.Equal(t, "auto", params.QosType)
	assert.Equal(t, int64(1024), params.ThroughputMibps)
	assert.Equal(t, int64(5000), params.Iops)
	assert.Equal(t, int64(1099511627776), params.SizeInBytes)
	assert.Equal(t, "34", params.RegionIdentifier)
	assert.Equal(t, "bucket-1", params.AutoTierBucketName)
	assert.Equal(t, "gs://request-bucket/ontap-mode.yaml", params.OntapModeRbacFileURL)
	assert.Equal(t, "request-checksum", params.OntapModeRbacChecksum)
}

func TestApplyClusterShape_EmptyShapeDefaultsMode(t *testing.T) {
	params := &commonparams.VCMCreatePoolParams{
		RegionIdentifier:      "keep-me",
		OntapModeRbacFileURL:  "gs://keep/rbac",
		OntapModeRbacChecksum: "keep-checksum",
	}

	ApplyClusterShape(params, vcmserver.ClusterShapeV1{})

	assert.Equal(t, "DEFAULT", params.Mode, "unset mode must default to DEFAULT")
	assert.Zero(t, params.ThroughputMibps)
	assert.Empty(t, params.AutoTierBucketName)
	assert.Equal(t, "keep-me", params.RegionIdentifier, "unset regionIdentifier must not clear the existing value")
	assert.Equal(t, "gs://keep/rbac", params.OntapModeRbacFileURL, "unset RBAC must not clear the existing pair")
	assert.Equal(t, "keep-checksum", params.OntapModeRbacChecksum)
}

func TestStore_CreatePoolParams_MergesRequestControlsWithConfig(t *testing.T) {
	deadline := time.Date(2099, 7, 29, 13, 30, 0, 0, time.UTC)

	params, err := poolSeedStore(t, nil).CreatePoolParams("req-1", deadline, testClusterShape())

	require.NoError(t, err)
	require.NotNil(t, params.VCM)
	assert.Equal(t, "req-1", params.VCM.RequestID)
	assert.Equal(t, deadline, params.VCM.Deadline)
	assert.Equal(t, "gcnv-test", params.VCM.DeploymentID)
	assert.Equal(t, "ONTAP", params.VCM.Mode)
	assert.Equal(t, "gs://request-bucket/ontap-mode.yaml", params.VCM.OntapModeRbacFileURL)
	assert.Equal(t, "request-checksum", params.VCM.OntapModeRbacChecksum)
}

func TestStore_CreatePoolParams_PropagatesConfigError(t *testing.T) {
	store := poolSeedStore(t, func(seed *vcmserver.SeedConfigV1) { seed.DeploymentId.Reset() })

	_, err := store.CreatePoolParams("req-1", time.Now().UTC().Add(time.Hour), testClusterShape())

	require.Error(t, err)
	assert.ErrorIs(t, err, datamodel.ErrInvalidConfig)
}

func TestStore_CreatePoolParams_ValidatesOntapModeInputs(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*vcmserver.SeedConfigV1)
		shape     vcmserver.ClusterShapeV1
		wantError string
	}{
		{
			name: "missing ONTAP-mode credential",
			mutate: func(seed *vcmserver.SeedConfigV1) {
				seed.OntapCredentials = defaultOntapCredentials("admin-secret")
			},
			shape:     testClusterShape(),
			wantError: "ontapModeUserCredentials",
		},
		{
			name: "password credential without username",
			mutate: func(seed *vcmserver.SeedConfigV1) {
				seed.OntapCredentials = ontapModeOntapCredentials(
					passwordUser(vcmserver.UserCredentialsV1UserPurposePURPOSEONTAPMODE, "", "mode-secret"))
			},
			shape:     testClusterShape(),
			wantError: "username",
		},
		{
			name: "DEFAULT mode does not require the ONTAP-mode credential",
			mutate: func(seed *vcmserver.SeedConfigV1) {
				seed.OntapCredentials = defaultOntapCredentials("admin-secret")
			},
			shape: vcmserver.ClusterShapeV1{
				Mode: vcmserver.NewOptClusterShapeV1Mode(vcmserver.ClusterShapeV1ModeDEFAULT),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := poolSeedStore(t, tt.mutate).
				CreatePoolParams("req-1", time.Now().UTC().Add(time.Hour), tt.shape)

			if tt.wantError == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.ErrorIs(t, err, datamodel.ErrInvalidConfig)
			assert.Contains(t, err.Error(), tt.wantError)
		})
	}
}

func TestValidateOntapModeRbac(t *testing.T) {
	tests := []struct {
		name      string
		shape     vcmserver.ClusterShapeV1
		wantError string
	}{
		{name: "complete pair", shape: ontapModeShape("gs://bucket/rbac", "checksum")},
		{name: "missing pair", shape: ontapModeShape("", ""), wantError: "rbacFileUrl"},
		{name: "checksum without file URL", shape: ontapModeShape("", "checksum"), wantError: "rbacFileUrl"},
		{name: "file URL without checksum", shape: ontapModeShape("gs://bucket/rbac", ""), wantError: "rbacFileCheckSum"},
		{name: "blank file URL", shape: ontapModeShape("  ", "checksum"), wantError: "rbacFileUrl"},
		{
			name:  "DEFAULT mode does not require the pair",
			shape: vcmserver.ClusterShapeV1{Mode: vcmserver.NewOptClusterShapeV1Mode(vcmserver.ClusterShapeV1ModeDEFAULT)},
		},
		{name: "unset mode defaults to DEFAULT", shape: vcmserver.ClusterShapeV1{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateOntapModeRbac(tt.shape)

			if tt.wantError == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantError)
		})
	}
}

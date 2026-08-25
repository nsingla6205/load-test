package hyperscaler

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	models "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/models"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
)

func TestUnsupportedServices_allUnsupportedMethods(t *testing.T) {
	svc := UnsupportedServices{}
	ctx := context.Background()

	tests := []struct {
		name string
		run  func() error
	}{
		{name: "CreateVPC", run: func() error { _, err := svc.CreateVPC(nil); return err }},
		{name: "GetVPCNetwork", run: func() error { _, err := svc.GetVPCNetwork("proj", "vpc"); return err }},
		{name: "CreateSubnetwork", run: func() error { _, err := svc.CreateSubnetwork(nil); return err }},
		{name: "GetSubnetwork", run: func() error { _, err := svc.GetSubnetwork("proj", "us-central1", "subnet"); return err }},
		{name: "ReleaseSubnetworkOp", run: func() error { _, err := svc.ReleaseSubnetworkOp("us-central1", "123", "subnet"); return err }},
		{name: "ListSubnetworks", run: func() error { _, err := svc.ListSubnetworks("proj", "us-central1"); return err }},
		{name: "InsertFirewall", run: func() error { _, err := svc.InsertFirewall(nil); return err }},
		{name: "UpdateFirewall", run: func() error { _, err := svc.UpdateFirewall(nil); return err }},
		{name: "GetFirewall", run: func() error { _, err := svc.GetFirewall("proj", "fw"); return err }},
		{name: "GetAddress", run: func() error { _, err := svc.GetAddress("proj", "us-central1", "addr"); return err }},
		{name: "CreateAddressOperation", run: func() error { _, err := svc.CreateAddressOperation(nil); return err }},
		{name: "ReleaseAddress", run: func() error { _, err := svc.ReleaseAddress("us-central1", "123", "addr"); return err }},
		{name: "ListAddressesByDeployment", run: func() error { _, err := svc.ListAddressesByDeployment("proj", "us-central1", "dep"); return err }},
		{name: "GetForwardingRule", run: func() error { _, err := svc.GetForwardingRule("proj", "us-central1", "ep"); return err }},
		{name: "CreateForwardingRuleOperation", run: func() error { _, err := svc.CreateForwardingRuleOperation(nil); return err }},
		{name: "DeleteForwardingRule", run: func() error { _, err := svc.DeleteForwardingRule("us-central1", "123", "addr"); return err }},
		{name: "GetComputeRegionalOpStatus", run: func() error { _, err := svc.GetComputeRegionalOpStatus("123", "us-central1", "op"); return err }},
		{name: "CreateBucketIfNotExists", run: func() error { return svc.CreateBucketIfNotExists(ctx, "proj", "bucket", "us-central1", nil) }},
		{name: "DeleteBucketWithLifecyclePolicy", run: func() error { _, err := svc.DeleteBucketWithLifecyclePolicy(ctx, "bucket"); return err }},
		{name: "GetBucket", run: func() error { _, err := svc.GetBucket(ctx, "bucket"); return err }},
		{name: "GetFileFromBucket", run: func() error { _, err := svc.GetFileFromBucket(ctx, "bucket", "file"); return err }},
		{name: "GetFileContentFromBucket", run: func() error { _, err := svc.GetFileContentFromBucket(ctx, "bucket", "file"); return err }},
		{name: "EmptyBucket", run: func() error { return svc.EmptyBucket(ctx, "bucket") }},
		{name: "GetServiceAccount", run: func() error { _, err := svc.GetServiceAccount("proj", "sa@proj.iam.gserviceaccount.com"); return err }},
		{name: "CreateServiceAccount", run: func() error {
			_, err := svc.CreateServiceAccount(nil, "123", "sa@proj.iam.gserviceaccount.com")
			return err
		}},
		{name: "IsServiceAccountCreated", run: func() error { _, _, err := svc.IsServiceAccountCreated("sa@proj.iam.gserviceaccount.com"); return err }},
		{name: "AttachOrUpdateRolesForServiceAccounts", run: func() error {
			return svc.AttachOrUpdateRolesForServiceAccounts([]string{"roles/viewer"}, "sa@proj.iam.gserviceaccount.com", "proj")
		}},
		{name: "RemoveRolesFromServiceAccounts", run: func() error {
			return svc.RemoveRolesFromServiceAccounts([]string{"roles/viewer"}, "sa@proj.iam.gserviceaccount.com", "proj")
		}},
		{name: "GetServiceAccountRoles", run: func() error {
			_, err := svc.GetServiceAccountRoles("sa@proj.iam.gserviceaccount.com", "proj")
			return err
		}},
		{name: "DeleteServiceAccount", run: func() error { return svc.DeleteServiceAccount("proj", "sa@proj.iam.gserviceaccount.com") }},
		{name: "DisableServiceAccount", run: func() error { return svc.DisableServiceAccount("sa@proj.iam.gserviceaccount.com") }},
		{name: "GetServiceAccountByEmail", run: func() error { _, err := svc.GetServiceAccountByEmail("sa@proj.iam.gserviceaccount.com"); return err }},
		{name: "CreateServiceAccountKey", run: func() error {
			_, err := svc.CreateServiceAccountKey(ctx, "sa@proj.iam.gserviceaccount.com")
			return err
		}},
		{name: "DeleteAllServiceAccountKeys", run: func() error { return svc.DeleteAllServiceAccountKeys(ctx, "sa@proj.iam.gserviceaccount.com") }},
		{name: "DeleteServiceAccountKeysExcludingKey", run: func() error {
			return svc.DeleteServiceAccountKeysExcludingKey(ctx, "sa@proj.iam.gserviceaccount.com", "key")
		}},
		{name: "CreateHmacKey", run: func() error { _, _, err := svc.CreateHmacKey("proj", "sa@proj.iam.gserviceaccount.com"); return err }},
		{name: "DeleteHmacKey", run: func() error { return svc.DeleteHmacKey("proj", "access-key", "sa@proj.iam.gserviceaccount.com") }},
		{name: "CreateCertificate", run: func() error { _, err := svc.CreateCertificate(nil); return err }},
		{name: "RevokeCertificate", run: func() error { _, err := svc.RevokeCertificate(nil); return err }},
		{name: "GetCertificate", run: func() error { _, err := svc.GetCertificate("proj", "us-central1", "pool", "cert"); return err }},
		{name: "CreateSecret", run: func() error { _, err := svc.CreateSecret("proj", "us-central1", "id", "value"); return err }},
		{name: "GetSecretWithLatestVersion", run: func() error { _, err := svc.GetSecretWithLatestVersion("proj", "secret"); return err }},
		{name: "DeleteSecret", run: func() error { return svc.DeleteSecret("proj", "secret") }},
		{name: "GetSecretWithCustomVersion", run: func() error { _, err := svc.GetSecretWithCustomVersion("proj", "secret", "1"); return err }},
		{name: "GetZones", run: func() error { _, err := svc.GetZones("proj", "us-central1"); return err }},
		{name: "IsMachineTypeAvailable", run: func() error {
			_, err := svc.IsMachineTypeAvailable("123", "us-central1-a", "n2-standard-4")
			return err
		}},
		{name: "CreateResourceRecordSet", run: func() error { _, err := svc.CreateResourceRecordSet("proj", "zone", "1.2.3.4", "record"); return err }},
		{name: "GetResourceRecordSet", run: func() error { _, err := svc.GetResourceRecordSet("proj", "zone", "record"); return err }},
		{name: "DeleteResourceRecordSet", run: func() error { return svc.DeleteResourceRecordSet("proj", "zone", "record") }},
		{name: "CreateCloudRunService", run: func() error { _, err := svc.CreateCloudRunService(ctx, nil); return err }},
		{name: "CheckOperationStatus", run: func() error { _, err := svc.CheckOperationStatus(ctx, "op"); return err }},
		{name: "GetCloudRunServiceURL", run: func() error { _, err := svc.GetCloudRunServiceURL(ctx, "proj", "us-central1", "svc"); return err }},
		{name: "DeleteCloudRunService", run: func() error { _, err := svc.DeleteCloudRunService(ctx, "proj", "us-central1", "svc"); return err }},
		{name: "GetIdentityToken", run: func() error { _, err := svc.GetIdentityToken(ctx, "audience"); return err }},
		{name: "GetTenantProject", run: func() error {
			_, err := svc.GetTenantProject("projects/123/global/networks/net", "123", "us-central1")
			return err
		}},
		{name: "GetSnHost", run: func() error { _, err := svc.GetSnHost("proj"); return err }},
		{name: "CreateTPSubnetOp", run: func() error {
			_, err := svc.CreateTPSubnetOp("123", "net", "us-central1", "subnet", false, nil)
			return err
		}},
		{name: "AddSubnetSecondaryRangeOp", run: func() error {
			_, err := svc.AddSubnetSecondaryRangeOp("proj", "us-central1", "subnet", "ic-lif-range", "10.50.166.0/29")
			return err
		}},
		{name: "GetServiceNetOpStatus", run: func() error { _, err := svc.GetServiceNetOpStatus("op"); return err }},
		{name: "GetComputeGlobalOpStatus", run: func() error { _, err := svc.GetComputeGlobalOpStatus("tp", "op"); return err }},
		{name: "GenerateVSASignedURL", run: func() error { _, err := svc.GenerateVSASignedURL(ctx, "vsa-image.tgz"); return err }},
		{name: "RotateBucketCmek", run: func() error { _, _, err := svc.RotateBucketCmek(ctx, "bucket", "key-version", 100, 4, 3); return err }},
		{name: "GrantServiceAccountRole", run: func() error {
			return svc.GrantServiceAccountRole(ctx, "sa@proj.iam.gserviceaccount.com", "member@proj.iam.gserviceaccount.com", "roles/viewer")
		}},
		{name: "EnableServiceAccount", run: func() error { return svc.EnableServiceAccount("sa@proj.iam.gserviceaccount.com") }},
		{name: "IsServiceAccountKeyPresent", run: func() error {
			_, err := svc.IsServiceAccountKeyPresent(ctx, "sa@proj.iam.gserviceaccount.com", "key-id")
			return err
		}},
		{name: "ResolveProjectNumberToName", run: func() error { _, err := svc.ResolveProjectNumberToName("123456789"); return err }},
		{name: "GetImageLabels", run: func() error { _, err := svc.GetImageLabels(ctx, "proj", "image"); return err }},
		{name: "AddSecretVersion", run: func() error {
			_, err := svc.AddSecretVersion("proj", "secret", "value")
			return err
		}},
		{name: "DeleteServiceAccountKey", run: func() error {
			return svc.DeleteServiceAccountKey(ctx, "projects/-/serviceAccounts/sa@proj.iam.gserviceaccount.com/keys/key-id")
		}},
		{name: "GetKmsCryptoKey", run: func() error {
			_, err := svc.GetKmsCryptoKey(ctx, &models.KmsAccess{}, "projects/p/locations/l/keyRings/r/cryptoKeys/k")
			return err
		}},
		{name: "EncryptWithKmsKey", run: func() error {
			return svc.EncryptWithKmsKey(ctx, &models.KmsAccess{}, "projects/p/locations/l/keyRings/r/cryptoKeys/k", "test")
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertUnsupportedHyperscalerError(t, tt.run())
		})
	}
}

func TestUnsupportedServices_implementsServicesUnion(t *testing.T) {
	var svc Services = UnsupportedServices{}
	require.NotNil(t, svc)
}

func TestUnsupportedServices_infraMethods(t *testing.T) {
	svc := UnsupportedServices{}

	assert.False(t, svc.IsAdminClientInitialized())
	assert.NotNil(t, svc.GetLogger())
	assert.Equal(t, context.Background(), svc.GetContext())

	err := svc.InitializeClients()
	assertUnsupportedHyperscalerError(t, err)
}

func TestUnsupported_returnsInternalServerError(t *testing.T) {
	err := unsupported()
	require.Error(t, err)

	var customErr *vsaerrors.CustomError
	require.True(t, errors.As(err, &customErr))
	assert.Equal(t, vsaerrors.ErrInternalServerError, customErr.TrackingID)
}

func assertUnsupportedHyperscalerError(t *testing.T, err error) {
	t.Helper()
	require.Error(t, err)

	var customErr *vsaerrors.CustomError
	require.True(t, errors.As(err, &customErr))
	assert.Equal(t, vsaerrors.ErrInternalServerError, customErr.TrackingID)
}

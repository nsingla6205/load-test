package hyperscaler

import (
	"context"
	"errors"

	models "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/models"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
)

var errUnsupported = errors.New("operation not supported by the active hyperscaler")

// unsupported logs the specific cause server-side and returns a generic internal
// server error. The multi-cloud abstraction must never be surfaced to customers,
// so the caller receives ErrInternalServerError with no provider-specific detail.
func unsupported() error {
	log.NewLogger().Error(errUnsupported.Error())
	return vsaerrors.NewVCPError(vsaerrors.ErrInternalServerError, nil)
}

// UnsupportedServices implements Services, returning a generic internal server error for every method.
type UnsupportedServices struct{}

func (UnsupportedServices) InitializeClients() error    { return unsupported() }
func (UnsupportedServices) GetLogger() log.Logger       { return log.NewLogger() }
func (UnsupportedServices) GetContext() context.Context { return context.Background() }

func (UnsupportedServices) CreateVPC(vpcNetwork *models.VPCNetwork) (string, error) {
	return "", unsupported()
}
func (UnsupportedServices) GetVPCNetwork(projectName, vpcNetworkName string) (*models.VPCNetwork, error) {
	return nil, unsupported()
}
func (UnsupportedServices) CreateSubnetwork(request *models.Subnet) (string, error) {
	return "", unsupported()
}
func (UnsupportedServices) GetSubnetwork(projectName, region, subnetName string) (*models.Subnet, error) {
	return nil, unsupported()
}
func (UnsupportedServices) ReleaseSubnetworkOp(region, projectId, subnetwork string) (string, error) {
	return "", unsupported()
}
func (UnsupportedServices) ListSubnetworks(projectName, region string) (*[]models.Subnet, error) {
	return nil, unsupported()
}
func (UnsupportedServices) InsertFirewall(firewallRule *models.Firewall) (string, error) {
	return "", unsupported()
}
func (UnsupportedServices) UpdateFirewall(firewallRule *models.Firewall) (string, error) {
	return "", unsupported()
}
func (UnsupportedServices) GetFirewall(projectName string, firewallName string) (*models.Firewall, error) {
	return nil, unsupported()
}
func (UnsupportedServices) GetAddress(projectName string, region string, address string) (*models.Address, error) {
	return nil, unsupported()
}
func (UnsupportedServices) CreateAddressOperation(address *models.Address) (string, error) {
	return "", unsupported()
}
func (UnsupportedServices) ReleaseAddress(region, projectNumber, addressName string) (string, error) {
	return "", unsupported()
}
func (UnsupportedServices) ListAddressesByDeployment(projectName, region, deploymentID string) (*[]models.Address, error) {
	return nil, unsupported()
}
func (UnsupportedServices) GetForwardingRule(projectName string, region string, endpointName string) (*models.ForwardingRule, error) {
	return nil, unsupported()
}
func (UnsupportedServices) CreateForwardingRuleOperation(forwardingRule *models.ForwardingRule) (string, error) {
	return "", unsupported()
}
func (UnsupportedServices) DeleteForwardingRule(region, projectNumber, addressName string) (string, error) {
	return "", unsupported()
}
func (UnsupportedServices) GetComputeRegionalOpStatus(projectNumber, region, operationName string) (*models.ComputeOperation, error) {
	return nil, unsupported()
}
func (UnsupportedServices) CreateBucketIfNotExists(ctx context.Context, projectID, bucketName, region string, kmsGrant *string) error {
	return unsupported()
}
func (UnsupportedServices) DeleteBucketWithLifecyclePolicy(ctx context.Context, bucketName string) (bool, error) {
	return false, unsupported()
}
func (UnsupportedServices) GetBucket(ctx context.Context, bucketName string) (*models.BucketDetails, error) {
	return nil, unsupported()
}
func (UnsupportedServices) GetFileFromBucket(ctx context.Context, bucketName, fileName string) (*models.BucketFileDetails, error) {
	return nil, unsupported()
}
func (UnsupportedServices) GetFileContentFromBucket(ctx context.Context, bucketName, fileName string) (*models.BucketFileDetails, error) {
	return nil, unsupported()
}
func (UnsupportedServices) EmptyBucket(ctx context.Context, bucketName string) error {
	return unsupported()
}
func (UnsupportedServices) GetServiceAccount(projectID, email string) (*models.ServiceAccount, error) {
	return nil, unsupported()
}
func (UnsupportedServices) CreateServiceAccount(createRequest *models.CreateServiceAccountRequest, projectNumber, email string) (*models.ServiceAccount, error) {
	return nil, unsupported()
}
func (UnsupportedServices) IsServiceAccountCreated(email string) (*models.ServiceAccount, bool, error) {
	return nil, false, unsupported()
}
func (UnsupportedServices) AttachOrUpdateRolesForServiceAccounts(roles []string, serviceAccountEmail, projectID string) error {
	return unsupported()
}
func (UnsupportedServices) RemoveRolesFromServiceAccounts(roles []string, serviceAccountEmail, projectID string) error {
	return unsupported()
}
func (UnsupportedServices) GetServiceAccountRoles(serviceAccountEmail, projectID string) ([]string, error) {
	return nil, unsupported()
}
func (UnsupportedServices) DeleteServiceAccount(project string, email string) error {
	return unsupported()
}
func (UnsupportedServices) DisableServiceAccount(email string) error { return unsupported() }
func (UnsupportedServices) GetServiceAccountByEmail(email string) (*models.ServiceAccount, error) {
	return nil, unsupported()
}
func (UnsupportedServices) CreateServiceAccountKey(ctx context.Context, email string) (*models.ServiceAccountKey, error) {
	return nil, unsupported()
}
func (UnsupportedServices) DeleteAllServiceAccountKeys(ctx context.Context, email string) error {
	return unsupported()
}
func (UnsupportedServices) DeleteServiceAccountKeysExcludingKey(ctx context.Context, email, keyToExclude string) error {
	return unsupported()
}
func (UnsupportedServices) CreateHmacKey(projectID string, serviceAccount string) (*string, *string, error) {
	return nil, nil, unsupported()
}
func (UnsupportedServices) DeleteHmacKey(projectID string, accessKey string, ServiceAccount string) error {
	return unsupported()
}
func (UnsupportedServices) CreateCertificate(cert *models.CustomCertificate) (*models.CustomCertificate, error) {
	return nil, unsupported()
}
func (UnsupportedServices) RevokeCertificate(cert *models.CustomCertificate) (string, error) {
	return "", unsupported()
}
func (UnsupportedServices) GetCertificate(projectID, region, poolName, certificateID string) (*models.CustomCertificate, error) {
	return nil, unsupported()
}
func (UnsupportedServices) CreateSecret(projectID, region, secretID, secretValue string) (*models.CustomSecret, error) {
	return nil, unsupported()
}
func (UnsupportedServices) GetSecretWithLatestVersion(projectID, secretID string) (*models.CustomSecret, error) {
	return nil, unsupported()
}
func (UnsupportedServices) DeleteSecret(projectID, secretID string) error { return unsupported() }
func (UnsupportedServices) GetSecretWithCustomVersion(projectID, secretID string, versionID string) (*models.CustomSecret, error) {
	return nil, unsupported()
}
func (UnsupportedServices) GetZones(projectName, region string) ([]string, error) {
	return nil, unsupported()
}
func (UnsupportedServices) IsMachineTypeAvailable(projectNumber, zone, machineType string) (bool, error) {
	return false, unsupported()
}
func (UnsupportedServices) CreateResourceRecordSet(projectID, managedZone, ipAddress, recordName string) (*models.CustomCloudDNSRecord, error) {
	return nil, unsupported()
}
func (UnsupportedServices) GetResourceRecordSet(projectID, managedZone, recordName string) (*models.CustomCloudDNSRecord, error) {
	return nil, unsupported()
}
func (UnsupportedServices) DeleteResourceRecordSet(projectID, managedZone, recordName string) error {
	return unsupported()
}
func (UnsupportedServices) CreateCloudRunService(ctx context.Context, config *models.CloudRunServiceConfig) (*models.CloudRunOperationResponse, error) {
	return nil, unsupported()
}
func (UnsupportedServices) CheckOperationStatus(ctx context.Context, operationName string) (bool, error) {
	return false, unsupported()
}
func (UnsupportedServices) GetCloudRunServiceURL(ctx context.Context, projectID, locationID, serviceName string) (string, error) {
	return "", unsupported()
}
func (UnsupportedServices) DeleteCloudRunService(ctx context.Context, projectID, locationID, serviceName string) (*models.CloudRunOperationResponse, error) {
	return nil, unsupported()
}
func (UnsupportedServices) GetIdentityToken(ctx context.Context, audience string) (string, error) {
	return "", unsupported()
}
func (UnsupportedServices) IsAdminClientInitialized() bool { return false }
func (UnsupportedServices) GetTenantProject(consumerNetwork, customerProjectNumber, tenantProjectRegion string) (string, error) {
	return "", unsupported()
}
func (UnsupportedServices) GetSnHost(project string) (string, error) {
	return "", unsupported()
}
func (UnsupportedServices) CreateTPSubnetOp(tenantProjectNumber, consumerNetwork, region, subnetName string, isLargeCapacity bool, requestedRanges []string) (*string, error) {
	return nil, unsupported()
}
func (UnsupportedServices) GetServiceNetOpStatus(operationName string) (*models.ComputeOperation, error) {
	return nil, unsupported()
}
func (UnsupportedServices) GetComputeGlobalOpStatus(tenantProject, operationName string) (*models.ComputeOperation, error) {
	return nil, unsupported()
}
func (UnsupportedServices) GenerateVSASignedURL(ctx context.Context, vsaImageName string) (string, error) {
	return "", unsupported()
}
func (UnsupportedServices) RotateBucketCmek(ctx context.Context, bucketName, primaryKeyVersion string, pageSize, maxWorkers, maxPasses int) (int64, int64, error) {
	return 0, 0, unsupported()
}

func (UnsupportedServices) GrantServiceAccountRole(ctx context.Context, email, memberEmail, role string) error {
	return unsupported()
}
func (UnsupportedServices) EnableServiceAccount(saEmail string) error {
	return unsupported()
}
func (UnsupportedServices) IsServiceAccountKeyPresent(ctx context.Context, email, keyID string) (bool, error) {
	return false, unsupported()
}
func (UnsupportedServices) ResolveProjectNumberToName(projectNumber string) (string, error) {
	return "", unsupported()
}
func (UnsupportedServices) GetImageLabels(ctx context.Context, project, image string) (map[string]string, error) {
	return nil, unsupported()
}
func (UnsupportedServices) AddSecretVersion(projectID, secretID, secretValue string) (*models.CustomSecretVersion, error) {
	return nil, unsupported()
}
func (UnsupportedServices) DeleteServiceAccountKey(ctx context.Context, keyName string) error {
	return unsupported()
}
func (UnsupportedServices) GetKmsCryptoKey(ctx context.Context, access *models.KmsAccess, keyPath string) (*models.CryptoKey, error) {
	return nil, unsupported()
}
func (UnsupportedServices) EncryptWithKmsKey(ctx context.Context, access *models.KmsAccess, keyPath, plaintext string) error {
	return unsupported()
}

// Compile-time guarantee that the ErrUnsupported base satisfies the full union.
var _ Services = UnsupportedServices{}

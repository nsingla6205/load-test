package google

import (
	"context"
	"fmt"
	"strings"
	"time"

	models "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	retry2 "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/retry"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"google.golang.org/api/iam/v1"
)

var (
	createServiceAccountKey          = _createServiceAccountKey
	getServiceAccountIamPolicy       = _getServiceAccountIamPolicy
	setServiceAccountIamPolicy       = _setServiceAccountIamPolicy
	retryTimeout                     = 1 * time.Minute
	retryInterval                    = 5 * time.Second
	listServiceAccountsKeysWithRetry = ListServiceAccountsKeysWithRetry
	deleteServiceAccountKeyWithRetry = DeleteServiceAccountKeyWithRetry
	deleteAllServiceAccountKeys      = _deleteAllServiceAccountKeys
	retryDo                          = retry2.RetryDoWithTimeout
	keyResourceUrlPrefix             = "projects/-/serviceAccounts/"
	isMemberInPolicy                 = _isMemberInPolicy
)

const (
	RetryTimeOutForGetIamPolicy  = 1 * time.Minute
	RetryIntervalForGetIamPolicy = 5 * time.Second
)

func (gcpService *GcpServices) CreateServiceAccountKey(ctx context.Context, email string) (*models.ServiceAccountKey, error) {
	return createServiceAccountKey(gcpService, ctx, email)
}

func convertServiceAccountKeyToModel(key *iam.ServiceAccountKey) *models.ServiceAccountKey {
	return &models.ServiceAccountKey{
		Name:            key.Name,
		KeyAlgorithm:    key.KeyAlgorithm,
		KeyOrigin:       key.KeyOrigin,
		PrivateKeyType:  key.PrivateKeyType,
		PrivateKeyData:  key.PrivateKeyData,
		PublicKeyData:   key.PublicKeyData,
		ValidAfterTime:  key.ValidAfterTime,
		ValidBeforeTime: key.ValidBeforeTime,
	}
}

func _createServiceAccountKey(gcpService *GcpServices, ctx context.Context, serviceAccountEmail string) (*models.ServiceAccountKey, error) {
	request := &iam.CreateServiceAccountKeyRequest{}
	key, err := gcpService.AdminGCPService.iamService.Projects.ServiceAccounts.Keys.Create(fmt.Sprintf("projects/-/serviceAccounts/%s", serviceAccountEmail), request).Context(ctx).Do()
	if err != nil {
		util.GetLogger(ctx).Errorf("Failed to create service account key for %s: %v", serviceAccountEmail, err)
		return nil, fmt.Errorf("failed to create service account key for %s: %v", serviceAccountEmail, err)
	}
	if key == nil {
		util.GetLogger(ctx).Errorf("Received nil key when creating service account key for %s", serviceAccountEmail)
		return nil, fmt.Errorf("received nil key when creating service account key for %s", serviceAccountEmail)
	}
	return convertServiceAccountKeyToModel(key), nil
}

func _getServiceAccountIamPolicy(ctx context.Context, gcpService *GcpServices, resource string) (*iam.Policy, error) {
	return gcpService.AdminGCPService.iamService.Projects.ServiceAccounts.GetIamPolicy(resource).Context(ctx).Do()
}

func _setServiceAccountIamPolicy(ctx context.Context, gcpService *GcpServices, resource string, policy *iam.Policy) (*iam.Policy, error) {
	return gcpService.AdminGCPService.iamService.Projects.ServiceAccounts.SetIamPolicy(resource, &iam.SetIamPolicyRequest{
		Policy: policy,
	}).Context(ctx).Do()
}

func _isMemberInPolicy(log log.Logger, policy *iam.Policy, member, role string) bool {
	if policy != nil {
		for _, binding := range policy.Bindings {
			if binding.Role == role {
				for _, m := range binding.Members {
					if m == member {
						log.Infof("Role %s is present with member %s", role, member)
						return true
					}
				}
			}
		}
	}
	return false
}

func (gcpService *GcpServices) GrantServiceAccountRole(ctx context.Context, email string, memberEmail string, role string) error {
	logger := util.GetLogger(ctx)
	resource := fmt.Sprintf("projects/-/serviceAccounts/%s", email)
	member := fmt.Sprintf("serviceAccount:%s", memberEmail)

	// Get the current IAM policy
	currentPolicy, err := getServiceAccountIamPolicy(ctx, gcpService, resource)
	if err != nil {
		logger.Errorf("Failed to get current IAM policy: %v", err)
		return fmt.Errorf("failed to get current IAM policy: %w", err)
	}

	// Check if the member already has the role
	if isMemberInPolicy(logger, currentPolicy, member, role) {
		logger.Infof("Member %s already has role %s, no action needed", member, role)
		return nil
	}

	// Create new binding for the role
	policy := &iam.Policy{}
	policy.Bindings = append(currentPolicy.Bindings, &iam.Binding{
		Role:    role,
		Members: []string{member},
	})

	// Set the updated IAM policy
	_, err = setServiceAccountIamPolicy(ctx, gcpService, resource, policy)
	if err != nil {
		logger.Errorf("Failed to set IAM policy: %v", err)
		return err
	}

	// Verify that the role has been granted
	err = retryDo(ctx, RetryTimeOutForGetIamPolicy, RetryIntervalForGetIamPolicy, "getServiceAccountIamPolicy", func(attempt int) (bool, error) {
		updatedPolicy, err := getServiceAccountIamPolicy(ctx, gcpService, resource)
		if err != nil {
			return true, err
		}
		if !isMemberInPolicy(logger, updatedPolicy, member, role) {
			logger.Warnf("Member %s not found in IAM policy after attempt %d", member, attempt)
			return true, retry2.NewRetriableErr(fmt.Sprintf("Member %s not found in IAM policy", member))
		}
		return false, nil
	})
	if err != nil {
		logger.Errorf("Failed to get updated IAM policy after retries: %v", err)
		return err
	}

	logger.Infof("Successfully granted role %s to %s", role, member)
	return nil
}

func (gcpService *GcpServices) DeleteAllServiceAccountKeys(ctx context.Context, email string) error {
	return deleteAllServiceAccountKeys(ctx, gcpService, email)
}

// _deleteAllServiceAccountKeys deletes keys for a service account using email
func _deleteAllServiceAccountKeys(ctx context.Context, c *GcpServices, email string) error {
	keyList, err := listServiceAccountsKeysWithRetry(ctx, c, keyResourceUrlPrefix+email)
	if err != nil {
		return fmt.Errorf("Projects.ServiceAccounts.Keys.List: %v", err)
	}
	if keyList.Keys != nil {
		for _, key := range keyList.Keys {
			if key.KeyType == "USER_MANAGED" {
				if err := deleteServiceAccountKeyWithRetry(ctx, c, key.Name); err != nil {
					return fmt.Errorf("Projects.ServiceAccounts.Keys.Delete: %v", err)
				}
			}
		}
	}
	return nil
}

// ListServiceAccountsKeysWithRetry list keys for a particular service account using email
func ListServiceAccountsKeysWithRetry(ctx context.Context, c *GcpServices, email string) (keyResponse *iam.ListServiceAccountKeysResponse, err error) {
	err = retryDo(ctx, retryTimeout, retryInterval, "ListServiceAccountsKeysWithRetry", func(attempt int) (bool, error) {
		keyResponse, err = c.AdminGCPService.iamService.Projects.ServiceAccounts.Keys.List(email).Do()
		if err != nil {
			return true, err
		}
		return false, nil
	})
	if err != nil {
		return nil, err
	}
	return keyResponse, nil
}

func (c *GcpServices) DeleteServiceAccountKey(ctx context.Context, keyName string) error {
	return DeleteServiceAccountKeyWithRetry(ctx, c, keyName)
}

// DeleteServiceAccountKeyWithRetry deletes keys using keyName
func DeleteServiceAccountKeyWithRetry(ctx context.Context, c *GcpServices, keyName string) (err error) {
	err = retryDo(ctx, retryTimeout, retryInterval, "DeleteServiceAccountKeyWithRetry", func(attempt int) (bool, error) {
		_, err = c.AdminGCPService.iamService.Projects.ServiceAccounts.Keys.Delete(keyName).Do()
		if err != nil {
			return true, err
		}
		return false, nil
	})
	if err != nil {
		return err
	}
	return nil
}

// IsServiceAccountKeyPresent checks whether a specific USER_MANAGED key (identified by keyID)
// exists in GCP for the given service account email. The keyID is matched against the suffix
// of the key resource name (e.g. "projects/-/serviceAccounts/{email}/keys/{keyID}").
func (c *GcpServices) IsServiceAccountKeyPresent(ctx context.Context, email, keyID string) (bool, error) {
	keyList, err := listServiceAccountsKeysWithRetry(ctx, c, keyResourceUrlPrefix+email)
	if err != nil {
		return false, fmt.Errorf("failed to list service account keys for %s: %v", email, err)
	}
	if keyList.Keys != nil {
		for _, key := range keyList.Keys {
			if key.KeyType == "USER_MANAGED" && strings.HasSuffix(key.Name, "/"+keyID) {
				return true, nil
			}
		}
	}
	return false, nil
}

func (c *GcpServices) DeleteServiceAccountKeysExcludingKey(ctx context.Context, email, keyToExclude string) error {
	keyList, err := listServiceAccountsKeysWithRetry(ctx, c, keyResourceUrlPrefix+email)
	if err != nil {
		return fmt.Errorf("Projects.ServiceAccounts.Keys.List: %v", err)
	}
	for _, key := range keyList.Keys {
		if key.Name != keyToExclude && key.KeyType == "USER_MANAGED" {
			util.GetLogger(ctx).Infof("Deleting service account key %s for service account %s", key.Name, email)
			err := deleteServiceAccountKeyWithRetry(ctx, c, key.Name)
			if err != nil {
				return fmt.Errorf("Projects.ServiceAccounts.Keys.Delete: %v", err)
			}
		}
	}
	return nil
}

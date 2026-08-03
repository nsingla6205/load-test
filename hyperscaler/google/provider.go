package google

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"cloud.google.com/go/storage"
	hyperscalermodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/models"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	logger "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
	retryutils "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/retry"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/cloudkms/v1"
	projectsManagement "google.golang.org/api/cloudresourcemanager/v1"
	"google.golang.org/api/compute/v1"
	"google.golang.org/api/dns/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/iam/v1"
	"google.golang.org/api/impersonate"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
	"google.golang.org/api/privateca/v1"
	cloudrun "google.golang.org/api/run/v2"
	"google.golang.org/api/secretmanager/v1"
	"google.golang.org/api/serviceconsumermanagement/v1"
	"google.golang.org/api/servicenetworking/v1"
	storagev1 "google.golang.org/api/storage/v1"
	scopesHttp "google.golang.org/api/transport/http"
)

const INACTIVE = "INACTIVE"

const (
	iamPolicyConflictMaxAttempts = 6
	iamPolicyConflictBaseBackoff = 1 * time.Second
	iamPolicyConflictMaxBackoff  = 5 * time.Second
	iamPolicyConflictMaxJitter   = 500
)

var iamPolicyConflictBackoff = _iamPolicyConflictBackoff

// rotateObjectRetryBaseDelay is the per-attempt base backoff for GCS object CMEK
// rotation retries. Var (not const) so unit tests can shorten it without changing
// the production default.
var rotateObjectRetryBaseDelay = 1 * time.Second

func _iamPolicyConflictBackoff(attempt int) time.Duration {
	return retryutils.ExponentialBackoffWithJitter(attempt, iamPolicyConflictBaseBackoff, iamPolicyConflictMaxBackoff, iamPolicyConflictMaxJitter)
}

func unwrapErr(err error) error {
	var customErr *vsaerrors.CustomError
	if errors.As(err, &customErr) && customErr.OriginalErr != nil {
		return customErr.OriginalErr
	}
	return err
}

func (gcpService *GcpServices) sleepWithContext(d time.Duration, projectID string) error {
	ctx := gcpService.GetContext()
	timer := time.NewTimer(d)
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		if !timer.Stop() {
			<-timer.C
		}
		gcpService.GetLogger().Warnf("Context cancelled while waiting to retry SetIamPolicy for project %s: %v",
			projectID, ctx.Err())
		return ctx.Err()
	}
}

func init() {
	// Override initialization functions if mock path is set
	if VSAMockPath != "" {
		initializeManagementService = _initializeMockManagementService
		initializeNetworkingService = _initializeMockNetworkingService
		initializeComputeService = _initializeMockComputeService
		initializeIamService = _initializeMockIamService
		initializeCloudProjectsService = _initializeMockCloudProjectsService
	}
}

var (
	// serviceNetworkingEndpoint is the endpoint for the Service Networking API.
	serviceNetworkingEndpoint = env.GetString("GCP_SERVICE_NETWORKING_ENDPOINT_URL", "")
	// serviceConsumerManagementEndpoint is the endpoint for the Service Consumer Management API.
	serviceConsumerManagementEndpoint = env.GetString("GCP_CONSUMER_MGMT_ENDPOINT_URL", "")
	// MockMetaDataHost is the endpoint for the metadata server.
	MockMetaDataHost = env.GetString("GCE_METADATA_HOST", "")
	VSAMockPath      = env.GetString("GOOGLE_EMULATOR_PATH", "")

	newClient       = _newClient
	newClientScopes = scopesHttp.NewClient

	newGoogleClient                 = _newGoogleClient
	initializeManagementService     = _initializeManagementService
	initializeNetworkingService     = _initializeNetworkingService
	initializeComputeService        = _initializeComputeService
	initializeStorageService        = _initializeStorageService
	initializeIamService            = _initializeIamService
	initializeCloudProjectsService  = _initializeCloudProjectsService
	initializePrivateCaService      = _initializePrivateCaService
	initializeSecretManagerService  = _initializeSecretManagerService
	initializeCloudDnsService       = _initializeCloudDnsService
	initializeStorageV1Service      = _initializeStorageV1Service
	initializeCloudRunService       = _initializeCloudRunService
	initializeMockManagementService = _initializeMockManagementService
	initializeMockNetworkingService = _initializeMockNetworkingService
	initializeMockComputeService    = _initializeMockComputeService
	isBucketDeletePolicySet         = _isBucketDeletePolicySet
	// test seams for CMEK rotation helpers
	newBucketIterator = func(bucket *storage.BucketHandle, ctx context.Context) objectIterator {
		return bucket.Objects(ctx, nil)
	}
	newObjectCopier = func(bucket *storage.BucketHandle, name string) objectCopier {
		return bucket.Object(name).CopierFrom(bucket.Object(name))
	}
)

// objectIterator abstracts the storage object iterator for testing.
type objectIterator interface {
	Next() (*storage.ObjectAttrs, error)
	PageInfo() *iterator.PageInfo
}

// objectCopier abstracts the object copier for testing.
type objectCopier interface {
	Run(ctx context.Context) (*storage.ObjectAttrs, error)
}

type GcpServices struct {
	Ctx    context.Context
	Logger logger.Logger
	Retry  RetryStrategy

	serviceConsumerManagementEndpoint string
	serviceNetworkingEndpoint         string

	AdminGCPService *AdminGCPService
}

type AdminGCPService struct {
	managementService    *serviceconsumermanagement.APIService
	networkingService    *servicenetworking.APIService
	computeService       *compute.Service
	storageService       *storage.Client
	storageV1Service     *storagev1.Service
	iamService           *iam.Service
	privateCaService     *privateca.Service
	secretManagerService *secretmanager.Service
	cloudProjectsService *projectsManagement.Service
	cloudDnsService      *dns.Service
	cloudRunService      *cloudrun.Service
}

// _newClient redirects to third party library HTTP NewClient for networking, while it helps to mock the function for init_test
func _newClient(ctx context.Context, opts ...option.ClientOption) (*http.Client, string, error) {
	return newClientScopes(ctx, opts...)
}

// InitializeClients Initialize the nvc clients & admin clients
func (gcpService *GcpServices) InitializeClients() error {
	var adminServiceClient *AdminGCPService
	var err error
	if !gcpService.IsAdminClientInitialized() {
		adminServiceClient, err = newGoogleClient(gcpService.Ctx)
		if err != nil {
			return err
		}
		gcpService.AdminGCPService = adminServiceClient
	}
	return nil
}

// IsAdminClientInitialized checks the admin initialisation
func (gcpService *GcpServices) IsAdminClientInitialized() bool {
	return gcpService.AdminGCPService != nil
}

// GetComputeService returns an initialized compute service, initializing clients if needed.
func (gcpService *GcpServices) GetComputeService(ctx context.Context) (*compute.Service, error) {
	if gcpService.Ctx == nil {
		gcpService.Ctx = ctx
	}
	if gcpService.AdminGCPService == nil || gcpService.AdminGCPService.computeService == nil {
		if err := gcpService.InitializeClients(); err != nil {
			return nil, err
		}
	}
	return gcpService.AdminGCPService.computeService, nil
}

// GetImageLabels fetches image labels via the compute API and returns them.
func (gcpService *GcpServices) GetImageLabels(ctx context.Context, project, image string) (map[string]string, error) {
	if gcpService == nil {
		return nil, fmt.Errorf("gcp service is nil")
	}

	computeSvc, err := gcpService.GetComputeService(ctx)
	if err != nil {
		return nil, err
	}

	imageResp, err := computeSvc.Images.Get(project, image).Context(ctx).Fields("labels").Do()
	if err != nil {
		return nil, err
	}

	if imageResp == nil {
		return nil, fmt.Errorf("image response is nil")
	}

	return imageResp.Labels, nil
}

// GetLogger returns the logger instance for gcpService if exists, else creates a new one
func (gcpService *GcpServices) GetLogger() logger.Logger {
	if gcpService.Logger == nil {
		gcpService.Logger = util.GetLogger(gcpService.Ctx)
	}
	return gcpService.Logger
}

// GetLogger returns the logger instance for gcpService if exists, else creates a new one
func (gcpService *GcpServices) GetContext() context.Context {
	if gcpService.Ctx == nil {
		gcpService.Ctx = context.Background()
	}
	return gcpService.Ctx
}

// _initializeAdminClient creates a new googleService object using Workload identity and Initializes the services
func _newGoogleClient(ctx context.Context) (*AdminGCPService, error) {
	log := util.GetLogger(ctx)
	managementService, err := initializeManagementService(ctx)
	if err != nil {
		log.Errorf("Error initializeManagementService : %s", err.Error())
		return nil, err
	}

	networkingService, err := initializeNetworkingService(ctx)
	if err != nil {
		log.Errorf("Error initializeNetworkingService : %s", err.Error())
		return nil, err
	}

	computeService, err := initializeComputeService(ctx)
	if err != nil {
		log.Errorf("Error initializeComputeService : %s", err.Error())
		return nil, err
	}

	storageService, err := initializeStorageService(ctx)
	if err != nil {
		log.Errorf("Error initializeStorageService :%s", err.Error())
		return nil, err
	}

	iamService, err := initializeIamService(ctx)
	if err != nil {
		log.Errorf("Error initializeIamService :%s", err.Error())
		return nil, err
	}

	cloudProjectService, err := initializeCloudProjectsService(ctx)
	if err != nil {
		log.Error("Error initializeCloudProjectsService", err)
		return nil, err
	}

	privateCaService, err := initializePrivateCaService(ctx)
	if err != nil {
		log.Errorf("Error initializePrivateCaService :%s", err.Error())
		return nil, err
	}

	secretManagerService, err := initializeSecretManagerService(ctx)
	if err != nil {
		log.Errorf("Error initializeSecretManagerService :%s", err.Error())
		return nil, err
	}

	cloudRunService, err := initializeCloudRunService(ctx)
	if err != nil {
		log.Errorf("error initializing CloudRun Service: %s", err.Error())
		return nil, err
	}

	cloudDnsService, err := initializeCloudDnsService(ctx)
	if err != nil {
		log.Errorf("Error initializeCloudDnsService :%s", err.Error())
		return nil, err
	}

	storageV1Service, err := initializeStorageV1Service(ctx)
	if err != nil {
		log.Errorf("Error initializeStorageV1Service :%s", err.Error())
		return nil, err
	}

	gServices := AdminGCPService{
		networkingService:    networkingService,
		managementService:    managementService,
		computeService:       computeService,
		storageService:       storageService,
		storageV1Service:     storageV1Service,
		iamService:           iamService,
		secretManagerService: secretManagerService,
		cloudProjectsService: cloudProjectService,
		cloudRunService:      cloudRunService,
		privateCaService:     privateCaService,
		cloudDnsService:      cloudDnsService,
	}
	return &gServices, nil
}

// _initializeManagementService initializes the service consumer management API service
func _initializeManagementService(ctx context.Context) (*serviceconsumermanagement.APIService, error) {
	logger := util.GetLogger(ctx)

	scopesOption := option.WithScopes(serviceconsumermanagement.CloudPlatformScope)
	opts := []option.ClientOption{scopesOption}

	if MockMetaDataHost != "" {
		opts = append(opts, option.WithTokenSource(google.ComputeTokenSource("", serviceconsumermanagement.CloudPlatformScope)))
	}
	client, endpoint, err := newClient(ctx, opts...)
	if err != nil {
		logger.Errorf("error while creating new client for _initializeManagementService : %v", err.Error())
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrGCPClientInitializationError, err)
	}
	client.Timeout = waitTimeoutMinutes
	svc, err := serviceconsumermanagement.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		logger.Errorf("serviceconsumermanagement.NewService error : %s", err.Error())
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrGCPClientInitializationError, err)
	}
	if endpoint != "" {
		svc.BasePath = endpoint
	}
	return svc, nil
}

// _initializeNetworkingService initializes the service networking API service
func _initializeNetworkingService(ctx context.Context) (*servicenetworking.APIService, error) {
	logger := util.GetLogger(ctx)

	scopesOption := option.WithScopes(servicenetworking.CloudPlatformScope, servicenetworking.ServiceManagementScope)
	opts := []option.ClientOption{scopesOption}

	if MockMetaDataHost != "" {
		opts = append(opts, option.WithTokenSource(google.ComputeTokenSource("", servicenetworking.CloudPlatformScope, servicenetworking.ServiceManagementScope)))
	}
	client, endpoint, err := newClient(ctx, opts...)
	if err != nil {
		logger.Errorf("error while creating new client for _initializeNetworkingService : %s", err.Error())
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrGCPClientInitializationError, err)
	}
	client.Timeout = waitTimeoutMinutes
	svc, err := servicenetworking.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		logger.Errorf("servicenetworking.NewService error : %s", err.Error())
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrGCPClientInitializationError, err)
	}
	if endpoint != "" {
		svc.BasePath = endpoint
	}
	return svc, nil
}

// _initializeIamService initializes the IAM API service
func _initializeIamService(ctx context.Context) (*iam.Service, error) {
	slogger := util.GetLogger(ctx)
	scopesOption := option.WithScopes(iam.CloudPlatformScope)
	opts := []option.ClientOption{scopesOption}

	if MockMetaDataHost != "" {
		opts = append(opts, option.WithTokenSource(google.ComputeTokenSource("", iam.CloudPlatformScope)))
	}
	client, endpoint, err := newClient(ctx, opts...)
	if err != nil {
		slogger.Error("error while creating new client for _initializeIamService", err)
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrGCPClientInitializationError, err)
	}
	client.Timeout = defaultSleepTime

	svc, err := iam.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		slogger.Error("compute.NewService error", err)
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrGCPClientInitializationError, err)
	}

	if endpoint != "" {
		svc.BasePath = endpoint
	}

	return svc, nil
}

func _initializeCloudProjectsService(ctx context.Context) (*projectsManagement.Service, error) {
	scopesOption := option.WithScopes(projectsManagement.CloudPlatformScope)
	opts := []option.ClientOption{scopesOption}
	slogger := util.GetLogger(ctx)

	if MockMetaDataHost != "" {
		opts = append(opts, option.WithTokenSource(google.ComputeTokenSource("", projectsManagement.CloudPlatformScope)))
	}

	client, endpoint, err := newClient(ctx, opts...)
	if err != nil {
		slogger.Error("error while creating new client for _initializeCloudProjectsService", err)
		return nil, err
	}
	client.Timeout = waitTimeoutMinutes
	svc, err := projectsManagement.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		slogger.Error("projectsManagement.NewService error", err)
		return nil, err
	}
	if endpoint != "" {
		svc.BasePath = endpoint
	}
	return svc, nil
}

// _initializeComputeService initializes the compute API service in GCP
func _initializeComputeService(ctx context.Context) (*compute.Service, error) {
	logger := util.GetLogger(ctx)

	scopesOption := option.WithScopes(compute.ComputeReadonlyScope, compute.ComputeScope, compute.CloudPlatformScope)
	opts := []option.ClientOption{scopesOption}

	if MockMetaDataHost != "" {
		opts = append(opts, option.WithTokenSource(google.ComputeTokenSource("", compute.ComputeReadonlyScope, compute.ComputeScope, compute.CloudPlatformScope)))
	}
	client, endpoint, err := newClient(ctx, opts...)
	if err != nil {
		logger.Errorf("error while creating new client for _initializeComputeService : %s", err.Error())
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrGCPClientInitializationError, err)
	}
	client.Timeout = waitTimeoutMinutes

	svc, err := compute.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		logger.Errorf("compute.NewService error : %s", err.Error())
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrGCPClientInitializationError, err)
	}

	if endpoint != "" {
		svc.BasePath = endpoint
	}

	return svc, nil
}

// _initializePrivateCaService initializes the private CA API service in GCP
func _initializePrivateCaService(ctx context.Context) (*privateca.Service, error) {
	logger := util.GetLogger(ctx)
	scopesOption := option.WithScopes(privateca.CloudPlatformScope)
	opts := []option.ClientOption{scopesOption}

	if MockMetaDataHost != "" {
		opts = append(opts, option.WithTokenSource(google.ComputeTokenSource("", privateca.CloudPlatformScope)))
	}
	client, endpoint, err := newClient(ctx, opts...)
	if err != nil {
		logger.Errorf("error while creating new client for _initializePrivateCaService : %s", err.Error())
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrGCPClientInitializationError, err)
	}
	client.Timeout = waitTimeoutMinutes

	svc, err := privateca.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		logger.Errorf("privateca.NewService error : %v", err)
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrGCPClientInitializationError, err)
	}

	if endpoint != "" {
		svc.BasePath = endpoint
	}

	return svc, nil
}

// _initializeSecretManagerService initializes the Secret Manager API service in GCP
func _initializeSecretManagerService(ctx context.Context) (*secretmanager.Service, error) {
	logger := util.GetLogger(ctx)
	scopesOption := option.WithScopes(secretmanager.CloudPlatformScope)
	opts := []option.ClientOption{scopesOption}

	if MockMetaDataHost != "" {
		opts = append(opts, option.WithTokenSource(google.ComputeTokenSource("", secretmanager.CloudPlatformScope)))
	}
	client, endpoint, err := newClient(ctx, opts...)
	if err != nil {
		logger.Errorf("error while creating new client for _initializeSecretManagerService : %s", err.Error())
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrGCPClientInitializationError, err)
	}
	client.Timeout = waitTimeoutMinutes

	svc, err := secretmanager.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		logger.Errorf("secretmanager.NewService error : %v", err)
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrGCPClientInitializationError, err)
	}

	if endpoint != "" {
		svc.BasePath = endpoint
	}
	return svc, nil
}

// _initializeCloudDnsService initializes the Cloud DNS API service in GCP
func _initializeCloudDnsService(ctx context.Context) (*dns.Service, error) {
	logger := util.GetLogger(ctx)
	scopesOption := option.WithScopes(dns.CloudPlatformScope)
	opts := []option.ClientOption{scopesOption}

	if MockMetaDataHost != "" {
		opts = append(opts, option.WithTokenSource(google.ComputeTokenSource("", dns.CloudPlatformScope)))
	}
	client, endpoint, err := newClient(ctx, opts...)
	if err != nil {
		logger.Errorf("error while creating new client for _initializeCloudDnsService : %s", err.Error())
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrGCPClientInitializationError, err)
	}
	client.Timeout = waitTimeoutMinutes

	svc, err := dns.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		logger.Errorf("dns.NewService error : %v", err)
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrGCPClientInitializationError, err)
	}

	if endpoint != "" {
		svc.BasePath = endpoint
	}
	return svc, nil
}

// _initializeStorageV1Service initializes the Storage v1 API service in GCP
func _initializeStorageV1Service(ctx context.Context) (*storagev1.Service, error) {
	logger := util.GetLogger(ctx)
	scopesOption := option.WithScopes(storagev1.CloudPlatformScope)
	opts := []option.ClientOption{scopesOption}

	if MockMetaDataHost != "" {
		opts = append(opts, option.WithTokenSource(google.ComputeTokenSource("", storagev1.CloudPlatformScope)))
	}
	client, endpoint, err := newClient(ctx, opts...)
	if err != nil {
		logger.Errorf("error while creating new client for _initializeStorageV1Service : %s", err.Error())
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrGCPClientInitializationError, err)
	}
	client.Timeout = waitTimeoutMinutes

	svc, err := storagev1.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		logger.Errorf("storagev1.NewService error : %v", err)
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrGCPClientInitializationError, err)
	}

	if endpoint != "" {
		svc.BasePath = endpoint
	}
	return svc, nil
}

func _initializeStorageService(ctx context.Context) (*storage.Client, error) {
	scopesOption := option.WithScopes(storage.ScopeFullControl)
	opts := []option.ClientOption{scopesOption}

	if MockMetaDataHost != "" {
		opts = append(opts, option.WithTokenSource(
			google.ComputeTokenSource("", storage.ScopeFullControl),
		))
	}

	client, _, err := newClient(ctx, opts...)
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrGCPClientInitializationError, err)
	}

	return storage.NewClient(ctx, option.WithHTTPClient(client))
}

// StorageService returns an initialized Cloud Storage client, initializing
// admin clients if needed.
func (a *AdminGCPService) StorageService(ctx context.Context, gcpService *GcpServices) (*storage.Client, error) {
	if gcpService == nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrGCPClientInitializationError, fmt.Errorf("gcp service is nil"))
	}

	if gcpService.Ctx == nil {
		gcpService.Ctx = ctx
	}

	if gcpService.AdminGCPService == nil || gcpService.AdminGCPService.storageService == nil {
		if err := gcpService.InitializeClients(); err != nil {
			return nil, err
		}
	}

	return gcpService.AdminGCPService.storageService, nil
}

// _initializeCloudRunService initializes the Cloud Run API service in GCP
func _initializeCloudRunService(ctx context.Context) (*cloudrun.Service, error) {
	// Use the correct Cloud Run scope
	scopesOption := option.WithScopes("https://www.googleapis.com/auth/cloud-platform")
	opts := []option.ClientOption{scopesOption}

	if MockMetaDataHost != "" {
		opts = append(opts, option.WithTokenSource(
			google.ComputeTokenSource("", "https://www.googleapis.com/auth/cloud-platform"),
		))
	}

	client, err := cloudrun.NewService(ctx, opts...)
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrGCPClientInitializationError, err)
	}

	return client, nil
}

// Enhanced CreateBucketIfNotExists with better error handling
func (gcpService *GcpServices) CreateBucketIfNotExists(ctx context.Context, projectID, bucketName, region string, kmsGrant *string) error {
	logger := util.GetLogger(ctx)
	bucketAttrs := &storage.BucketAttrs{
		Location: region,
	}
	// If kmsGrant is provided, set CMEK encryption
	kmsGrantValue := nillable.GetString(kmsGrant, "")
	if kmsGrantValue != "" {
		bucketAttrs.Encryption = &storage.BucketEncryption{
			DefaultKMSKeyName: kmsGrantValue,
		}
		logger.Infof("Creating bucket %s with CMEK", bucketName)
	}
	err := gcpService.AdminGCPService.storageService.Bucket(bucketName).Create(ctx, projectID, bucketAttrs)
	if err != nil {
		var gErr *googleapi.Error
		if errors.As(err, &gErr) {
			switch gErr.Code {
			case 409: // Already exists
				logger.Infof("bucket %s already exists", bucketName)
				return nil
			default:
				return vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceProvisionError, err)
			}
		}
		return err
	}
	logger.Infof("created bucket %s in region %s", bucketName, region)
	return nil
}

func (gcpService *GcpServices) DeleteBucketWithLifecyclePolicy(ctx context.Context, bucketName string) (bool, error) {
	logger := util.GetLogger(ctx)
	logger.Debugf("Attempting to delete bucket: %s", bucketName)

	// Attempt to delete the bucket
	err := gcpService.AdminGCPService.storageService.Bucket(bucketName).Delete(ctx)
	if err != nil {
		var gErr *googleapi.Error
		if errors.As(err, &gErr) && gErr.Code == http.StatusNotFound {
			// Bucket does not exist, treat as success
			logger.Infof("Bucket %s does not exist. Treating as success.", bucketName)
			return true, nil
		}

		// Fetch bucket attributes to check for lifecycle policies
		bucketAttr, attrErr := gcpService.AdminGCPService.storageService.Bucket(bucketName).Attrs(ctx)
		if attrErr != nil {
			var apiErr *googleapi.Error
			if !errors.As(attrErr, &apiErr) || apiErr.Code != http.StatusNotFound {
				return false, fmt.Errorf("failed to fetch attributes for bucket %s: %w", bucketName, attrErr)
			}
		}

		// Check if a delete lifecycle policy is set
		if isBucketDeletePolicySet(bucketAttr) {
			logger.Infof("Bucket %s has a delete lifecycle policy set. Skipping deletion.", bucketName)
			return false, nil
		}

		// Attempt to set a lifecycle policy to delete all objects
		_, updateErr := gcpService.AdminGCPService.storageService.Bucket(bucketName).Update(ctx, storage.BucketAttrsToUpdate{
			Lifecycle: &storage.Lifecycle{
				Rules: []storage.LifecycleRule{
					{
						Action:    storage.LifecycleAction{Type: "Delete"},
						Condition: storage.LifecycleCondition{AllObjects: true},
					},
				},
			},
		})
		if updateErr != nil {
			logger.Errorf("Failed to set lifecycle policy for bucket %s: %v", bucketName, updateErr)
			return false, fmt.Errorf("error setting lifecycle policy for bucket %s: %w", bucketName, updateErr)
		}

		return false, nil
	}

	logger.Infof("Bucket %s successfully deleted.", bucketName)
	return true, nil
}

func _isBucketDeletePolicySet(bucket *storage.BucketAttrs) bool {
	if bucket == nil || bucket.Lifecycle.Rules == nil || len(bucket.Lifecycle.Rules) == 0 {
		return false
	}
	for _, rule := range bucket.Lifecycle.Rules {
		if rule.Action.Type == "Delete" && rule.Condition.AllObjects {
			return true
		}
	}
	return false
}

// GetBucket retrieves bucket details from GCP Storage API
func (gcpService *GcpServices) GetBucket(ctx context.Context, bucketName string) (*hyperscalermodels.BucketDetails, error) {
	logger := util.GetLogger(ctx)
	logger.Debugf("Getting bucket details for: %s", bucketName)

	// Get bucket attributes from GCP Storage API using the high-level client
	bucketAttrs, err := gcpService.AdminGCPService.storageService.Bucket(bucketName).Attrs(ctx)
	if err != nil {
		var gErr *googleapi.Error
		if errors.As(err, &gErr) && gErr.Code == http.StatusNotFound {
			logger.Errorf("Bucket %s not found", bucketName)
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceFetchError, fmt.Errorf("bucket %s not found", bucketName))
		}
		logger.Errorf("Failed to get bucket attributes for %s: %v", bucketName, err)
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceFetchError, err)
	}

	logger.Debugf("Successfully retrieved bucket attributes for: %s", bucketName)

	// Get bucket details from Storage v1 API to access satisfiesPzi and satisfiesPzs fields
	bucketV1, err := gcpService.AdminGCPService.storageV1Service.Buckets.Get(bucketName).Do()
	if err != nil {
		logger.Errorf("Failed to get bucket v1 details for %s: %v", bucketName, err)
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceFetchError, err)
	}

	// Extract PZI/PZS information from the v1 API response
	satisfiesPzi := bucketV1.SatisfiesPZI
	satisfiesPzs := bucketV1.SatisfiesPZS

	// Extract project number from bucket - this is the tenant project where the bucket resides
	if bucketV1.ProjectNumber <= 0 {
		logger.Errorf("Invalid or empty project number for bucket %s: %d", bucketName, bucketV1.ProjectNumber)
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceFetchError, fmt.Errorf("bucket %s has invalid project number: %d", bucketName, bucketV1.ProjectNumber))
	}

	projectNumber := fmt.Sprintf("%d", bucketV1.ProjectNumber)
	logger.Debugf("Bucket %s belongs to project number: %s", bucketName, projectNumber)

	// Create and return bucket details
	bucketDetails := &hyperscalermodels.BucketDetails{
		Name:          bucketAttrs.Name,
		Location:      bucketAttrs.Location,
		LocationType:  bucketAttrs.LocationType,
		StorageClass:  bucketAttrs.StorageClass,
		SatisfiesPzi:  satisfiesPzi,
		SatisfiesPzs:  satisfiesPzs,
		ProjectNumber: projectNumber,
		Region:        "", // Will be populated by the caller if needed
		Created:       bucketAttrs.Created.Format("2006-01-02T15:04:05Z"),
		Updated:       bucketAttrs.Updated.Format("2006-01-02T15:04:05Z"),
	}

	logger.Infof("Bucket %s - PZI: %t, PZS: %t, ProjectNumber: %s", bucketName, satisfiesPzi, satisfiesPzs, projectNumber)
	return bucketDetails, nil
}

func (gcpService *GcpServices) GetFileFromBucket(ctx context.Context, bucketName, fileName string) (*hyperscalermodels.BucketFileDetails, error) {
	logger := util.GetLogger(ctx)
	logger.Debugf("Getting bucket file details for: %s/%s", bucketName, fileName)

	object := gcpService.AdminGCPService.storageService.Bucket(bucketName).Object(fileName)
	reader, err := object.NewReader(ctx)
	if err != nil {
		logger.Errorf("failed to create object reader: %s/%s: %v", bucketName, fileName, err)
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceFetchError, err)
	}
	defer func(reader *storage.Reader) {
		err = reader.Close()
		if err != nil {
			gcpService.GetLogger().Warnf("failed to close object reader: %v", err)
		}
	}(reader)

	sha256Hasher := sha256.New()

	if _, err = io.Copy(sha256Hasher, reader); err != nil {
		logger.Errorf("failed to copy object reader: %s/%s: %v", bucketName, fileName, err)
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceFetchError, err)
	}

	fileSHA256Hash := base64.StdEncoding.EncodeToString(sha256Hasher.Sum(nil))
	bucketFileDetails := &hyperscalermodels.BucketFileDetails{
		BucketName:     bucketName,
		FileUrl:        fileName,
		FileHashSHA256: fileSHA256Hash,
	}

	logger.Infof("Bucket file Hash fetched successfully for %s/%s", bucketName, fileName)
	return bucketFileDetails, nil
}

func (gcpService *GcpServices) GetFileContentFromBucket(ctx context.Context, bucketName, fileName string) (*hyperscalermodels.BucketFileDetails, error) {
	logger := util.GetLogger(ctx)
	logger.Debugf("Getting bucket file content for: %s/%s", bucketName, fileName)

	object := gcpService.AdminGCPService.storageService.Bucket(bucketName).Object(fileName)
	reader, err := object.NewReader(ctx)
	if err != nil {
		logger.Errorf("failed to create object reader: %s/%s: %v", bucketName, fileName, err)
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceFetchError, err)
	}
	defer func(reader *storage.Reader) {
		err = reader.Close()
		if err != nil {
			gcpService.GetLogger().Warnf("failed to close object reader: %v", err)
		}
	}(reader)

	sha256Hasher := sha256.New()
	var contentBuffer bytes.Buffer
	teeReader := io.TeeReader(reader, &contentBuffer)
	if _, err = io.Copy(sha256Hasher, teeReader); err != nil {
		logger.Errorf("failed to copy object reader: %s/%s: %v", bucketName, fileName, err)
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceFetchError, err)
	}

	fileSHA256Hash := base64.StdEncoding.EncodeToString(sha256Hasher.Sum(nil))
	bucketFileDetails := &hyperscalermodels.BucketFileDetails{
		BucketName:     bucketName,
		FileUrl:        fileName,
		FileHashSHA256: fileSHA256Hash,
		FileContent:    contentBuffer.String(),
	}

	logger.Infof("Bucket file content fetched successfully for %s/%s", bucketName, fileName)
	return bucketFileDetails, nil
}

// normalizeKMSKey removes transient grant segments while preserving key version.
func normalizeKMSKey(kmsKey string) string {
	if kmsKey == "" {
		return kmsKey
	}

	if idx := strings.Index(kmsKey, "/grants/"); idx != -1 {
		prefix := kmsKey[:idx]
		rest := kmsKey[idx+len("/grants/"):]
		if vidx := strings.Index(rest, "/cryptoKeyVersions/"); vidx != -1 {
			return prefix + rest[vidx:]
		}
		kmsKey = prefix
	}

	return kmsKey
}

// compareKMSKeys compares two KMS keys after normalization.
func compareKMSKeys(key1, key2 string) bool {
	return normalizeKMSKey(key1) == normalizeKMSKey(key2)
}

func isGCSRetryableErrorCode(code int) bool {
	switch code {
	case http.StatusRequestTimeout, http.StatusTooManyRequests, http.StatusInternalServerError,
		http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

// RotateBucketCmek rotates CMEK for objects in a single GCS bucket.
// It returns the total number of processed and rotated objects.
func (gcpService *GcpServices) RotateBucketCmek(
	ctx context.Context,
	bucketName string,
	primaryKeyVersion string,
	pageSize int,
	maxWorkers int,
	maxPasses int,
) (int64, int64, error) {
	logger := util.GetLogger(ctx)

	if bucketName == "" {
		return 0, 0, fmt.Errorf("bucket name must not be empty")
	}

	storageClient, err := gcpService.AdminGCPService.StorageService(ctx, gcpService)
	if err != nil {
		logger.Errorf("Failed to get storage client for CMEK rotation: %v", err)
		return 0, 0, err
	}

	bucket := storageClient.Bucket(bucketName)

	var totalProcessed, totalRotated int64
	passes := 0

	for {
		passes++
		if passes > maxPasses {
			return totalProcessed, totalRotated, fmt.Errorf("exceeded maximum passes (%d) for CMEK rotation in bucket %s", maxPasses, bucketName)
		}

		var passProcessed, passRotated int64
		it := newBucketIterator(bucket, ctx)
		it.PageInfo().MaxSize = pageSize

		for {
			// Build a page of up to pageSize objects.
			var objects []*storage.ObjectAttrs
			for len(objects) < pageSize {
				obj, err := it.Next()
				if err == iterator.Done {
					break
				}
				if err != nil {
					logger.Errorf("Failed to list objects in bucket %s: %v", bucketName, err)
					return totalProcessed, totalRotated, fmt.Errorf("failed to list objects in bucket %s: %w", bucketName, err)
				}
				objects = append(objects, obj)
			}

			if len(objects) == 0 {
				break
			}

			rotatedInPage, err := rotateObjectsInParallel(
				ctx,
				bucket,
				bucketName,
				primaryKeyVersion,
				objects,
				maxWorkers,
			)
			if err != nil {
				return totalProcessed, totalRotated, err
			}

			passProcessed += int64(len(objects))
			passRotated += int64(rotatedInPage)
		}

		totalProcessed += passProcessed
		totalRotated += passRotated

		logger.Infof("CMEK rotation pass completed for bucket %s: processed=%d rotated=%d totalProcessed=%d totalRotated=%d",
			bucketName, passProcessed, passRotated, totalProcessed, totalRotated)

		// Converged: no more objects were rotated in this pass.
		if passRotated == 0 {
			break
		}
	}

	return totalProcessed, totalRotated, nil
}

// rotateObjectsInParallel rotates CMEK for a page of objects using a worker pool.
func rotateObjectsInParallel(
	ctx context.Context,
	bucket *storage.BucketHandle,
	bucketName string,
	primaryKeyVersion string,
	objects []*storage.ObjectAttrs,
	maxWorkers int,
) (int, error) {
	if len(objects) == 0 {
		return 0, nil
	}

	logger := util.GetLogger(ctx)

	objCh := make(chan *storage.ObjectAttrs, len(objects))
	resultCh := make(chan bool, len(objects))
	errCh := make(chan error, len(objects))

	// Start workers.
	workerCount := maxWorkers
	if workerCount <= 0 {
		workerCount = 1
	}
	for i := 0; i < workerCount; i++ {
		go func() {
			for obj := range objCh {
				// Skip if already using the target key.
				if compareKMSKeys(obj.KMSKeyName, primaryKeyVersion) {
					resultCh <- false
					continue
				}

				var resultObj *storage.ObjectAttrs
				var err error

			retryLoop:
				for attempt := 0; attempt < 3; attempt++ {
					// Create fresh copier for each attempt (GCS copiers are single-use)
					copier := newObjectCopier(bucket, obj.Name)
					resultObj, err = copier.Run(ctx)
					if err == nil {
						break retryLoop // Success, exit retry loop
					}
					// Use errors.As to handle wrapped errors
					var gerr *googleapi.Error
					if errors.As(err, &gerr) {
						if !isGCSRetryableErrorCode(gerr.Code) {
							break retryLoop
						}
					}
					if attempt < 2 {
						// Context-aware backoff: respect context cancellation
						backoff := time.Duration(attempt+1) * rotateObjectRetryBaseDelay
						timer := time.NewTimer(backoff)
						select {
						case <-timer.C:
							// proceed to next retry attempt
						case <-ctx.Done():
							if !timer.Stop() {
								<-timer.C
							}
							err = ctx.Err()
							break retryLoop
						}
					}
				}

				if err != nil {
					var gerr *googleapi.Error
					if errors.As(err, &gerr) && gerr.Code == http.StatusNotFound {
						// Object removed between listing and copy – treat as non-fatal.
						logger.Infof("Object %s/%s not found during CMEK rotation; skipping", bucketName, obj.Name)
						resultCh <- false
						continue
					}

					errCh <- fmt.Errorf("failed to rotate CMEK for object %s/%s: %w", bucketName, obj.Name, err)
					continue
				}

				// Safety check: ensure the resultant object's KMS key matches the requested
				// primaryKeyVersion (after normalizing grants, etc.). If it does not match,
				// fail fast to avoid silent divergence.
				if resultObj != nil && !compareKMSKeys(resultObj.KMSKeyName, primaryKeyVersion) {
					errCh <- fmt.Errorf("KMS key mismatch after rotation for object %s/%s: expected %s, got %s", bucketName, obj.Name, primaryKeyVersion, resultObj.KMSKeyName)
					continue
				}

				logger.Debugf("Successfully rotated CMEK for object %s/%s", bucketName, obj.Name)
				resultCh <- true
			}
		}()
	}

	// Feed objects to workers.
	for _, obj := range objects {
		select {
		case objCh <- obj:
		case <-ctx.Done():
			close(objCh)
			return 0, fmt.Errorf("context cancelled while rotating CMEK objects: %w", ctx.Err())
		}
	}
	close(objCh)

	rotated := 0
	for i := 0; i < len(objects); i++ {
		select {
		case err := <-errCh:
			return rotated, err
		case r := <-resultCh:
			if r {
				rotated++
			}
		case <-ctx.Done():
			return rotated, fmt.Errorf("context cancelled while collecting CMEK rotation results: %w", ctx.Err())
		}
	}

	return rotated, nil
}

func (gcpService *GcpServices) EmptyBucket(ctx context.Context, bucketName string) error {
	logger := util.GetLogger(ctx)
	logger.Debugf("Emptying bucket: %s", bucketName)

	bucket := gcpService.AdminGCPService.storageService.Bucket(bucketName)

	// List all objects in the bucket
	it := bucket.Objects(ctx, nil)
	objectCount := 0
	batchSize := 100 // Process objects in batches
	objectNames := make([]string, 0, batchSize)
	maxObjects := 10000 // Safety limit to prevent infinite loops
	iterationCount := 0

	for {
		iterationCount++

		// Safety check to prevent infinite loops
		if iterationCount > maxObjects {
			return fmt.Errorf("safety limit reached: processed %d objects, stopping to prevent infinite loop", maxObjects)
		}

		obj, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return fmt.Errorf("failed to list objects in bucket %s: %v", bucketName, err)
		}

		objectNames = append(objectNames, obj.Name)

		// Process batch when it reaches batchSize or when we're done
		if len(objectNames) >= batchSize {
			err = gcpService.deleteObjectBatch(ctx, bucket, objectNames, bucketName)
			if err != nil {
				return err
			}
			objectCount += len(objectNames)
			objectNames = objectNames[:0] // Reset slice but keep capacity
		}
	}

	// Process remaining objects in the final batch
	if len(objectNames) > 0 {
		err := gcpService.deleteObjectBatch(ctx, bucket, objectNames, bucketName)
		if err != nil {
			return err
		}
		objectCount += len(objectNames)
	}

	logger.Infof("Successfully emptied bucket: %s (deleted %d objects)", bucketName, objectCount)
	return nil
}

// deleteObjectBatch deletes a batch of objects from the bucket
func (gcpService *GcpServices) deleteObjectBatch(ctx context.Context, bucket *storage.BucketHandle, objectNames []string, bucketName string) error {
	logger := util.GetLogger(ctx)

	// Delete objects in parallel using goroutines
	type deleteResult struct {
		objectName string
		err        error
	}

	resultChan := make(chan deleteResult, len(objectNames))

	// Launch goroutines to delete objects in parallel
	for _, objectName := range objectNames {
		go func(name string) {
			logger.Debugf("Deleting object: %s", name)
			err := bucket.Object(name).Delete(ctx)
			resultChan <- deleteResult{objectName: name, err: err}
		}(objectName)
	}

	// Collect results
	var errors []error
	for i := 0; i < len(objectNames); i++ {
		result := <-resultChan
		if result.err != nil {
			errors = append(errors, fmt.Errorf("failed to delete object %s: %v", result.objectName, result.err))
		}
	}

	// Return error if any deletions failed
	if len(errors) > 0 {
		return fmt.Errorf("failed to delete %d objects from bucket %s: %v", len(errors), bucketName, errors)
	}

	return nil
}

// GetServiceNetworkingEndpoint returns the service consumer management endpoint
func (gcpService *GcpServices) GetServiceNetworkingEndpoint() string {
	if gcpService.serviceNetworkingEndpoint == "" {
		gcpService.serviceNetworkingEndpoint = serviceNetworkingEndpoint
	}
	gcpService.Logger.Debug("GetServiceNetworkingEndpoint : gcpService.serviceNetworkingEndpoint = ", gcpService.serviceNetworkingEndpoint)
	return gcpService.serviceNetworkingEndpoint
}

// GetServiceConsumerManagementEndpoint returns the service consumer management endpoint
func (gcpService *GcpServices) GetServiceConsumerManagementEndpoint() string {
	if gcpService.serviceConsumerManagementEndpoint == "" {
		gcpService.serviceConsumerManagementEndpoint = serviceConsumerManagementEndpoint
	}
	gcpService.Logger.Debug("GetServiceConsumerManagementEndpoint : gcpService.serviceConsumerManagementEndpoint = ", gcpService.serviceConsumerManagementEndpoint)
	return gcpService.serviceConsumerManagementEndpoint
}

func (gcpService *GcpServices) GetServiceAccount(projectID, email string) (*hyperscalermodels.ServiceAccount, error) {
	response, err := gcpService.AdminGCPService.iamService.Projects.ServiceAccounts.List("projects/" + projectID).Do()
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceFetchError, err)
	}
	for _, account := range response.Accounts {
		if account.Email == email {
			return convertServiceAccountToHyperscalerServiceAccount(account), nil
		}
	}
	return nil, vsaerrors.NewVCPError(vsaerrors.ErrResourceNotFound, fmt.Errorf("service account not found"))
}

func (gcpService *GcpServices) AttachOrUpdateRolesForServiceAccounts(roles []string, serviceAccountEmail, projectID string) error {
	var lastErr error
	for attempt := 0; attempt < iamPolicyConflictMaxAttempts; attempt++ {
		lastErr = gcpService.attachOrUpdateRolesReadModifyWrite(roles, serviceAccountEmail, projectID)
		if lastErr == nil {
			return nil
		}
		if !retryutils.IsRetryableIAMPolicyError(lastErr) {
			return lastErr
		}
		if attempt < iamPolicyConflictMaxAttempts-1 {
			backoff := iamPolicyConflictBackoff(attempt)
			gcpService.GetLogger().Warnf("IAM policy conflict on SetIamPolicy for project %s (attempt %d/%d), retrying in %v: %v",
				projectID, attempt+1, iamPolicyConflictMaxAttempts, backoff, unwrapErr(lastErr))
			if err := gcpService.sleepWithContext(backoff, projectID); err != nil {
				return err
			}
		}
	}
	gcpService.GetLogger().Errorf("IAM policy conflict retries exhausted for project %s after %d attempts: %v",
		projectID, iamPolicyConflictMaxAttempts, unwrapErr(lastErr))
	return lastErr
}

func (gcpService *GcpServices) attachOrUpdateRolesReadModifyWrite(roles []string, serviceAccountEmail, projectID string) error {
	policy, err := gcpService.getProjectIamPolicy(projectID)
	if err != nil {
		return vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceFetchError, err)
	}

	currentSvcAccountMember := "serviceAccount:" + serviceAccountEmail
	requiredRolesMap := gcpService.initializeRequiredRolesMap(roles)

	projectIAMPolicyBindings := gcpService.updatePolicyBindings(policy.Bindings, requiredRolesMap, currentSvcAccountMember)

	projectIAMPolicyBindings = gcpService.addMissingRoles(projectIAMPolicyBindings, requiredRolesMap, currentSvcAccountMember)

	return gcpService.setProjectIamPolicy(projectID, policy.Etag, projectIAMPolicyBindings)
}

// GetServiceAccountRoles retrieves all IAM roles assigned to a service account in a specified project.
// It matches both active members ("serviceAccount:<email>") and deleted members
// ("deleted:serviceAccount:<email>?uid=...") so that cleanup can find stale bindings
// even after the service account has been deleted from GCP.
func (gcpService *GcpServices) GetServiceAccountRoles(serviceAccountEmail, projectID string) ([]string, error) {
	policy, err := gcpService.getProjectIamPolicy(projectID)
	if err != nil {
		return nil, err
	}

	roles := []string{}
	serviceAccountMember := "serviceAccount:" + serviceAccountEmail
	deletedPrefix := "deleted:serviceAccount:" + serviceAccountEmail

	for _, binding := range policy.Bindings {
		for _, member := range binding.Members {
			if strings.EqualFold(member, serviceAccountMember) || strings.HasPrefix(member, deletedPrefix) {
				roles = append(roles, binding.Role)
				break
			}
		}
	}

	return roles, nil
}

// RemoveRolesFromServiceAccounts removes specified roles from a service account
func (gcpService *GcpServices) RemoveRolesFromServiceAccounts(roles []string, serviceAccountEmail, projectID string) error {
	var lastErr error
	for attempt := 0; attempt < iamPolicyConflictMaxAttempts; attempt++ {
		lastErr = gcpService.removeRolesReadModifyWrite(roles, serviceAccountEmail, projectID)
		if lastErr == nil {
			return nil
		}
		if !retryutils.IsRetryableIAMPolicyError(lastErr) {
			return lastErr
		}
		if attempt < iamPolicyConflictMaxAttempts-1 {
			backoff := iamPolicyConflictBackoff(attempt)
			gcpService.GetLogger().Warnf("IAM policy conflict on SetIamPolicy for project %s (attempt %d/%d), retrying in %v: %v",
				projectID, attempt+1, iamPolicyConflictMaxAttempts, backoff, unwrapErr(lastErr))
			if err := gcpService.sleepWithContext(backoff, projectID); err != nil {
				return err
			}
		}
	}
	gcpService.GetLogger().Errorf("IAM policy conflict retries exhausted for project %s after %d attempts: %v",
		projectID, iamPolicyConflictMaxAttempts, unwrapErr(lastErr))
	return lastErr
}

func (gcpService *GcpServices) removeRolesReadModifyWrite(roles []string, serviceAccountEmail, projectID string) error {
	policy, err := gcpService.getProjectIamPolicy(projectID)
	if err != nil {
		return err
	}

	currentSvcAccountMember := "serviceAccount:" + serviceAccountEmail
	deletedPrefix := "deleted:serviceAccount:" + serviceAccountEmail
	rolesToRemove := make(map[string]bool)
	for _, role := range roles {
		rolesToRemove[role] = true
	}

	var updatedBindings []*projectsManagement.Binding
	for _, binding := range policy.Bindings {
		if rolesToRemove[binding.Role] {
			var updatedMembers []string
			for _, member := range binding.Members {
				if !strings.EqualFold(member, currentSvcAccountMember) && !strings.HasPrefix(member, deletedPrefix) {
					updatedMembers = append(updatedMembers, member)
				}
			}

			if len(updatedMembers) > 0 {
				updatedBindings = append(updatedBindings, &projectsManagement.Binding{
					Role:    binding.Role,
					Members: updatedMembers,
				})
			}
		} else {
			updatedBindings = append(updatedBindings, binding)
		}
	}

	return gcpService.setProjectIamPolicy(projectID, policy.Etag, updatedBindings)
}

func (gcpService *GcpServices) getProjectIamPolicy(projectID string) (*projectsManagement.Policy, error) {
	getPolicyRequest := &projectsManagement.GetIamPolicyRequest{}
	iamPolicy, err := gcpService.AdminGCPService.cloudProjectsService.Projects.GetIamPolicy(projectID, getPolicyRequest).Do()
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceFetchError, err)
	}
	return iamPolicy, nil
}

func (gcpService *GcpServices) initializeRequiredRolesMap(roles []string) map[string]bool {
	requiredRolesMap := make(map[string]bool)
	for _, role := range roles {
		requiredRolesMap[role] = false
	}
	return requiredRolesMap
}

func (gcpService *GcpServices) updatePolicyBindings(policyBindings []*projectsManagement.Binding, requiredRolesMap map[string]bool, currentSvcAccountMember string) []*projectsManagement.Binding {
	var updatedBindings []*projectsManagement.Binding
	for _, policyBinding := range policyBindings {
		svcAccountPreExists := false
		if roleProcessed, ok := requiredRolesMap[policyBinding.Role]; ok && !roleProcessed {
			for _, member := range policyBinding.Members {
				if strings.EqualFold(strings.ToLower(member), strings.ToLower(currentSvcAccountMember)) {
					svcAccountPreExists = true
					break
				}
			}
			if !svcAccountPreExists {
				policyBinding.Members = append(policyBinding.Members, currentSvcAccountMember)
			}
			requiredRolesMap[policyBinding.Role] = true
		}
		updatedBindings = append(updatedBindings, &projectsManagement.Binding{
			Role:    policyBinding.Role,
			Members: policyBinding.Members,
		})
	}
	return updatedBindings
}

func (gcpService *GcpServices) addMissingRoles(projectIAMPolicyBindings []*projectsManagement.Binding, requiredRolesMap map[string]bool, currentSvcAccountMember string) []*projectsManagement.Binding {
	for role, isProcessed := range requiredRolesMap {
		if !isProcessed {
			projectIAMPolicyBindings = append(projectIAMPolicyBindings, &projectsManagement.Binding{
				Role: role,
				Members: []string{
					currentSvcAccountMember,
				},
			})
		}
	}
	return projectIAMPolicyBindings
}

func (gcpService *GcpServices) setProjectIamPolicy(projectID string, etag string, projectIAMPolicyBindings []*projectsManagement.Binding) error {
	policyRequest := &projectsManagement.SetIamPolicyRequest{
		Policy: &projectsManagement.Policy{
			Bindings: projectIAMPolicyBindings,
			Etag:     etag,
		},
	}
	_, err := gcpService.AdminGCPService.cloudProjectsService.Projects.SetIamPolicy(projectID, policyRequest).Do()
	if err != nil {
		return vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceProvisionError, err)
	}
	return nil
}

func convertServiceAccounttoGcpServiceAccount(sa *hyperscalermodels.ServiceAccount) *iam.ServiceAccount {
	return &iam.ServiceAccount{
		Name:        sa.Name,
		Description: sa.Description,
		Email:       sa.Email,
		ProjectId:   sa.ProjectId,
		UniqueId:    sa.UniqueId,
		Disabled:    sa.Disabled,
		DisplayName: sa.DisplayName,
	}
}

func convertCreateServiceAccountRequestToGcpCreateRequest(createRequest *hyperscalermodels.CreateServiceAccountRequest) *iam.CreateServiceAccountRequest {
	return &iam.CreateServiceAccountRequest{
		AccountId:      createRequest.AccountId,
		ServiceAccount: convertServiceAccounttoGcpServiceAccount(createRequest.ServiceAccount),
	}
}

func (gcpService *GcpServices) CreateServiceAccount(createRequest *hyperscalermodels.CreateServiceAccountRequest, projectNumber, email string) (*hyperscalermodels.ServiceAccount, error) {
	account, err := gcpService.AdminGCPService.iamService.Projects.ServiceAccounts.Create("projects/"+projectNumber, convertCreateServiceAccountRequestToGcpCreateRequest(createRequest)).Do()
	var gerr *googleapi.Error
	if errors.As(err, &gerr) {
		switch gerr.Code {
		case http.StatusConflict:
			serviceAccount, isSACreated, err := gcpService.IsServiceAccountCreated(email)
			if err != nil || !isSACreated {
				return nil, vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceFetchError, err)
			}
			account = convertServiceAccounttoGcpServiceAccount(serviceAccount)
			return convertServiceAccountToHyperscalerServiceAccount(account), nil
		}
	}

	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceProvisionError, err)
	}
	return convertServiceAccountToHyperscalerServiceAccount(account), nil
}

func (gcpService *GcpServices) IsServiceAccountCreated(email string) (account *hyperscalermodels.ServiceAccount, isSACreated bool, err error) {
	acc, err := gcpService.GetServiceAccountByEmail(email)
	if err != nil {
		return nil, false, vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceFetchError, err)
	}
	return acc, true, nil
}

func convertServiceAccountToHyperscalerServiceAccount(sa *iam.ServiceAccount) *hyperscalermodels.ServiceAccount {
	return &hyperscalermodels.ServiceAccount{
		Name:        sa.Name,
		Description: sa.Description,
		Email:       sa.Email,
		ProjectId:   sa.ProjectId,
		UniqueId:    sa.UniqueId,
		Disabled:    sa.Disabled,
		DisplayName: sa.DisplayName,
	}
}
func (gcpService *GcpServices) GetServiceAccountByEmail(email string) (*hyperscalermodels.ServiceAccount, error) {
	account, err := gcpService.AdminGCPService.iamService.Projects.ServiceAccounts.Get("projects/-/serviceAccounts/" + email).Do()
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceFetchError, err)
	}
	return convertServiceAccountToHyperscalerServiceAccount(account), nil
}

func (gcpService *GcpServices) DisableServiceAccount(saEmail string) error {
	name := "projects/-/serviceAccounts/" + saEmail
	_, err := gcpService.AdminGCPService.iamService.Projects.ServiceAccounts.Disable(name, &iam.DisableServiceAccountRequest{}).Do()
	if err != nil {
		var gerr *googleapi.Error
		if errors.As(err, &gerr) {
			switch gerr.Code {
			case http.StatusNotFound:
				// Service account does not exist, treat as success
				return nil
			case http.StatusConflict:
				// Already disabled, treat as success
				return nil
			}
		}
		return vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceProvisionError, fmt.Errorf("failed to disable service account %s: %v", saEmail, err))
	}
	return nil
}

func (gcpService *GcpServices) EnableServiceAccount(saEmail string) error {
	name := "projects/-/serviceAccounts/" + saEmail
	_, err := gcpService.AdminGCPService.iamService.Projects.ServiceAccounts.Enable(name, &iam.EnableServiceAccountRequest{}).Do()
	if err != nil {
		var gerr *googleapi.Error
		if errors.As(err, &gerr) {
			switch gerr.Code {
			case http.StatusNotFound:
				// Service account does not exist, cannot enable
				return vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceProvisionError, fmt.Errorf("service account %s not found", saEmail))
			case http.StatusConflict:
				// Already enabled, treat as success
				return nil
			}
		}
		return vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceProvisionError, fmt.Errorf("failed to enable service account %s: %v", saEmail, err))
	}
	return nil
}

func (gcpService *GcpServices) DeleteServiceAccount(projectNumber string, saEmail string) error {
	// Convert project number to project ID for the IAM API call
	projectID, err := getProjectIDFromNumber(gcpService, projectNumber)
	if err != nil {
		gcpService.GetLogger().Errorf("Failed to get project ID from project number %s: %v", projectNumber, err)
		return vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceFetchError, fmt.Errorf("failed to resolve project number %s to project ID: %v", projectNumber, err))
	}

	if strings.TrimSpace(projectID) == "" {
		return vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceFetchError, fmt.Errorf("resolved project ID is empty for project number: %s", projectNumber))
	}

	gcpService.GetLogger().Debugf("Resolved project number %s to project ID: %s", projectNumber, projectID)
	name := "projects/" + projectID + "/serviceAccounts/" + saEmail
	_, err = gcpService.AdminGCPService.iamService.Projects.ServiceAccounts.Delete(name).Do()
	if err != nil {
		if gerr, ok := err.(*googleapi.Error); ok && gerr.Code == http.StatusNotFound {
			// Service account does not exist, treat as success
			return nil
		}
		return gcpService.determineIsRetryableError(saEmail, err)
	}
	return nil
}

func (gcpService *GcpServices) determineIsRetryableError(saEmail string, err error) error {
	if retryutils.ShouldRetry(err) {
		gcpService.GetLogger().Debugf("Service account %s deletion failed with retriable error, returning for Temporal retry", saEmail)
		return vsaerrors.NewVCPError(vsaerrors.ErrGCPServiceAccountDeletionError,
			fmt.Errorf("Projects.ServiceAccounts.Delete: %v", err))
	}

	gcpService.GetLogger().Debugf("Service account %s deletion failed with non-retriable error, returning non-retriable error", saEmail)
	return vsaerrors.NewVCPError(vsaerrors.ErrGCPServiceAccountDeletionNonRetriableError,
		fmt.Errorf("Projects.ServiceAccounts.Delete: %v", err))
}

func (gcpService *GcpServices) CreateHmacKey(projectID string, serviceAccount string) (accessKey *string, secretKey *string, err error) {
	// Create the HMAC key
	key, err := gcpService.AdminGCPService.storageService.CreateHMACKey(gcpService.Ctx, projectID, serviceAccount, storage.ForHMACKeyServiceAccountEmail(serviceAccount))
	if err != nil {
		return nil, nil, err
	}

	// Extract the access key and secret key from the response
	accessKey = &key.AccessID
	secretKey = &key.Secret

	return accessKey, secretKey, nil
}

func (gcpService *GcpServices) DeleteHmacKey(projectID string, accessKey string, ServiceAccount string) error {
	// Delete the HMAC key
	_, err := gcpService.AdminGCPService.storageService.HMACKeyHandle(projectID, accessKey).Update(gcpService.Ctx, storage.HMACKeyAttrsToUpdate{State: INACTIVE}, storage.ForHMACKeyServiceAccountEmail(ServiceAccount))
	if err != nil {
		return fmt.Errorf("failed to update HMAC key state to INACTIVE: %v", err)
	}
	err = gcpService.AdminGCPService.storageService.HMACKeyHandle(projectID, accessKey).Delete(gcpService.Ctx)
	if err != nil {
		return err
	}

	return nil
}

func GetImpersonatedKmsService(ctx context.Context, targetEmail string, scopeCreds *google.Credentials) (*cloudkms.Service, error) {
	// Set up the impersonation token source using the sde service account email from the KMS config
	// Use the VSA service account key to impersonate the SDE service account
	// Note:- SDE service account should have roles/iam.serviceAccountTokenCreator and VSA service account should be the member of the project
	logger := util.GetLogger(ctx)
	scopes := []string{cloudkms.CloudPlatformScope}
	tokenSource, err := impersonate.CredentialsTokenSource(ctx, impersonate.CredentialsConfig{
		TargetPrincipal: targetEmail,
		Scopes:          scopes,
	}, option.WithCredentials(scopeCreds))
	if err != nil {
		logger.Errorf("Failed to create impersonated token source: %v. TargetPrincipal: %s, Scopes: %v", err, targetEmail, scopes)
		return nil, err
	}

	// Use the impersonated client to interact with Google Cloud KMS
	kmsService, err := cloudkms.NewService(ctx, option.WithTokenSource(tokenSource))
	if err != nil {
		return nil, fmt.Errorf("failed to create KMS service: %w", err)
	}
	return kmsService, nil
}

// GetDirectKmsService creates a Cloud KMS service using the provided credentials directly (no impersonation).
// Used by VCP-managed KMS configs where the VCP service account has direct access to the customer's KMS key.
func GetDirectKmsService(ctx context.Context, scopeCreds *google.Credentials) (*cloudkms.Service, error) {
	kmsService, err := cloudkms.NewService(ctx, option.WithCredentials(scopeCreds))
	if err != nil {
		return nil, fmt.Errorf("failed to create direct KMS service: %w", err)
	}
	return kmsService, nil
}

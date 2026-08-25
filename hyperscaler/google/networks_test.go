package google

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	models "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/models"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/temporal"
	"google.golang.org/api/compute/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
	"google.golang.org/api/serviceconsumermanagement/v1"
	"google.golang.org/api/servicenetworking/v1"
)

const (
	// Clearly fictional GCP path fragments for tests — do not substitute real
	// project numbers or VPC names from staging/production.
	testProviderProjectID    = "test-project-00000000"
	testProviderNetworkID    = "test-vpc-do-not-use"
	testProviderResourcePath = "projects/" + testProviderProjectID + "/networks/" + testProviderNetworkID
)

// retryTestOperation retries a test operation with exponential backoff
func retryTestOperation(t *testing.T, maxRetries int, operation func() error) error {
	var lastErr error
	for i := 0; i < maxRetries; i++ {
		err := operation()
		if err == nil {
			return nil
		}
		lastErr = err

		// Exponential backoff with jitter
		if i < maxRetries-1 {
			backoff := time.Duration(1<<uint(i)) * 100 * time.Millisecond
			jitter := time.Duration(i*10) * time.Millisecond
			time.Sleep(backoff + jitter)
		}
	}
	return lastErr
}

func testReset(t *testing.T) {
	waitTimeoutMinutes = time.Minute * time.Duration(env.GetInt("GCP_LRO_TIMEOUT_MINUTES", 20))
	serviceConsumerManagementEndpoint = env.GetString("GCP_CONSUMER_MGMT_ENDPOINT_URL", "mock-consumer-endpoint.com")
	serviceNetworkingEndpoint = env.GetString("GCP_SERVICE_NETWORKING_ENDPOINT_URL", "mock-endpoint.com")
	newClient = _newClient

	CreateTPSubnetOp = _createTPSubnetOp
	AddSecretVersion = _addSecretVersion
	GetSecretVersion = _getSecretVersion
	iamPolicyConflictBackoff = _iamPolicyConflictBackoff
}

// assertTerminalGCPOperationError asserts err is the non-retryable terminal GCP operation
// error and that providerDetails from the raw GCP response is not propagated.
func assertTerminalGCPOperationError(t *testing.T, err error, providerDetails string) {
	t.Helper()

	var appErr *temporal.ApplicationError
	if !assert.ErrorAs(t, err, &appErr) {
		return
	}
	assert.True(t, appErr.NonRetryable())

	var (
		trackingID   int
		errorDetails string
	)
	assert.NoError(t, appErr.Details(&trackingID, &errorDetails))
	assert.Equal(t, vsaerrors.ErrGCPTerminalOperationError, trackingID)
	assert.Equal(t, "The cloud provider could not complete the operation. Please try again.", errorDetails)
	assert.NotContains(t, errorDetails, providerDetails)
	assert.NotContains(t, err.Error(), "googleapi: Error 0")
	assert.NotContains(t, err.Error(), providerDetails)
	assert.Contains(t, err.Error(), "The cloud provider could not complete the operation")
}

func assertProviderDetailsRedacted(t *testing.T, err error, providerDetails string) {
	t.Helper()

	var appErr *temporal.ApplicationError
	if !assert.ErrorAs(t, err, &appErr) {
		return
	}
	assert.True(t, appErr.NonRetryable())

	var (
		trackingID   int
		errorDetails string
	)
	assert.NoError(t, appErr.Details(&trackingID, &errorDetails))
	assert.NotContains(t, errorDetails, providerDetails)
	assert.NotContains(t, err.Error(), providerDetails)
}

func Test_GetTenantProject(t *testing.T) {
	serviceConsumerManagementEndpoint = env.GetString("GCP_CONSUMER_MGMT_ENDPOINT_URL", "autopush-netapp.sandbox.googleapis.com")
	serviceNetworkingEndpoint = env.GetString("GCP_SERVICE_NETWORKING_ENDPOINT_URL", "netapp-tst-autopush-endpoint.appspot.com")
	ctx := context.Background()
	customerProjectNumber := "1079058383248"
	consumerNetwork := "projects/1079058383248/global/networks/network-to-netapp2"
	region := "US-East-4"
	t.Run("WhenGetTenantProjectFails", func(tt *testing.T) {
		defer testReset(tt)
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			if req.URL.Path == "/v1/services/"+serviceConsumerManagementEndpoint+"/projects/"+customerProjectNumber+"/tenancyUnits" {
				rw.WriteHeader(http.StatusInternalServerError)
				return
			}
		}))
		defer server.Close()
		mgmtSvc, err := serviceconsumermanagement.NewService(ctx, option.WithHTTPClient(&http.Client{Timeout: time.Second}), option.WithEndpoint(server.URL))
		if err != nil {
			t.Errorf("Error getting service up: '%s'", err.Error())
		}

		gService := &GcpServices{
			AdminGCPService: &AdminGCPService{
				managementService: mgmtSvc,
			},
			Ctx:                               ctx,
			Logger:                            util.GetLogger(ctx),
			serviceConsumerManagementEndpoint: serviceConsumerManagementEndpoint,
			serviceNetworkingEndpoint:         serviceNetworkingEndpoint,
		}
		_, err = gService.GetTenantProject(consumerNetwork, customerProjectNumber, region)
		if err == nil {
			tt.Error("Expected an error but got nothing")
		} else {
			if !strings.Contains(err.(*vsaerrors.CustomError).OriginalErr.Error(), "googleapi: got HTTP response code 500 with body") {
				tt.Errorf("Unexpected error: %s", err.(*vsaerrors.CustomError).OriginalErr.Error())
			}
		}
	})
	t.Run("WhenNotGettingTenantProject_usingSDK", func(tt *testing.T) {
		defer testReset(tt)
		ctx := context.Background()
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			if req.URL.Path == "/v1/services/"+serviceConsumerManagementEndpoint+"/projects/"+customerProjectNumber+"/tenancyUnits" {
				response, _ := json.Marshal(&serviceconsumermanagement.ListTenancyUnitsResponse{TenancyUnits: []*serviceconsumermanagement.TenancyUnit{{TenantResources: []*serviceconsumermanagement.TenantResource{{Resource: "projects/175643", Tag: consumerNetwork + "-" + region}}}}})
				_, _ = rw.Write(response)
				return
			}
		}))
		defer server.Close()
		mgmtSvc, err := serviceconsumermanagement.NewService(ctx, option.WithHTTPClient(&http.Client{Timeout: time.Second}), option.WithEndpoint(server.URL))
		if err != nil {
			t.Errorf("Error getting service up: '%s'", err.Error())
		}

		gService := &GcpServices{
			AdminGCPService: &AdminGCPService{
				managementService: mgmtSvc,
			},
			Ctx:                               ctx,
			Logger:                            util.GetLogger(ctx),
			serviceConsumerManagementEndpoint: serviceConsumerManagementEndpoint,
			serviceNetworkingEndpoint:         serviceNetworkingEndpoint,
		}
		_, err = gService.GetTenantProject(consumerNetwork, customerProjectNumber, region)
		if err != nil {
			tt.Errorf("Unexpected error: %s", err.Error())
		}
	})
	t.Run("WhenTenancyUnitFails", func(tt *testing.T) {
		defer testReset(tt)
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			if req.URL.Path == "/v1/services/"+serviceConsumerManagementEndpoint+"/projects/"+customerProjectNumber+"/tenancyUnits" {
				response, err := json.Marshal(&serviceconsumermanagement.ListTenancyUnitsResponse{TenancyUnits: []*serviceconsumermanagement.TenancyUnit{{TenantResources: []*serviceconsumermanagement.TenantResource{{Resource: "projects/175643", Tag: "incorrectconsumerNetwork" + "-" + region}}}}})
				if err != nil {
					rw.WriteHeader(http.StatusBadRequest)
					return
				}
				_, _ = rw.Write(response)
				return
			}
		}))
		defer server.Close()
		mgmtSvc, err := serviceconsumermanagement.NewService(ctx, option.WithHTTPClient(&http.Client{Timeout: time.Second}), option.WithEndpoint(server.URL))
		if err != nil {
			t.Errorf("Error getting service up: '%s'", err.Error())
		}

		gService := &GcpServices{
			AdminGCPService: &AdminGCPService{
				managementService: mgmtSvc,
			},
			Ctx:                               ctx,
			Logger:                            util.GetLogger(ctx),
			serviceConsumerManagementEndpoint: serviceConsumerManagementEndpoint,
			serviceNetworkingEndpoint:         serviceNetworkingEndpoint,
		}
		_, err = gService.GetTenantProject(consumerNetwork, customerProjectNumber, region)
		if err == nil {
			tt.Error("Expected an error but got nothing")
		} else {
			if !strings.Contains(err.(*temporal.ApplicationError).Error(), "Setup/Configure Private Service Access (PSA) Peering") {
				tt.Errorf("Unexpected error: %s", err.(*temporal.ApplicationError))
			}
		}
	})
	t.Run("WhenSuccess", func(tt *testing.T) {
		defer testReset(tt)
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			if req.URL.Path == "/v1/services/"+serviceConsumerManagementEndpoint+"/projects/"+customerProjectNumber+"/tenancyUnits" {
				response, err := json.Marshal(&serviceconsumermanagement.ListTenancyUnitsResponse{TenancyUnits: []*serviceconsumermanagement.TenancyUnit{{TenantResources: []*serviceconsumermanagement.TenantResource{{Resource: "projects/175643", Tag: consumerNetwork + "-" + region}}}}})
				if err != nil {
					rw.WriteHeader(http.StatusBadRequest)
					return
				}
				_, _ = rw.Write(response)
				return
			}
		}))
		defer server.Close()
		mgmtSvc, err := serviceconsumermanagement.NewService(ctx, option.WithHTTPClient(&http.Client{Timeout: time.Second}), option.WithEndpoint(server.URL))
		if err != nil {
			t.Errorf("Error getting service up: '%s'", err.Error())
		}
		gService := &GcpServices{
			AdminGCPService: &AdminGCPService{
				managementService: mgmtSvc,
			},
			Ctx:                               ctx,
			Logger:                            util.GetLogger(ctx),
			serviceConsumerManagementEndpoint: serviceConsumerManagementEndpoint,
			serviceNetworkingEndpoint:         serviceNetworkingEndpoint,
		}
		_, err = gService.GetTenantProject(consumerNetwork, customerProjectNumber, region)
		if err != nil {
			tt.Errorf("Unexpected error: %s", err.Error())
		}
	})
}

func Test_CreateTPSubnetOpInternal(t *testing.T) {
	tenantProjectNumber := "1234"
	url := fmt.Sprintf("/v1/services/endpoint.goog/projects/%s:addSubnetwork", tenantProjectNumber)
	t.Run("When_CreateTPSubnetOpFails", func(tt *testing.T) {
		defer testReset(tt)
		ctx := context.Background()
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			if req.URL.Path == url {
				rw.WriteHeader(http.StatusInternalServerError)
				return
			}
			rw.WriteHeader(http.StatusBadRequest)
		}))
		defer server.Close()
		svc, err := servicenetworking.NewService(
			ctx, option.WithHTTPClient(&http.Client{Timeout: time.Second}), option.WithEndpoint(server.URL))
		if err != nil {
			t.Errorf("Error getting service up: '%s'", err.Error())
		}
		gService := &GcpServices{
			AdminGCPService: &AdminGCPService{
				networkingService: svc,
			},
			Ctx:                               ctx,
			Logger:                            util.GetLogger(ctx),
			serviceConsumerManagementEndpoint: serviceConsumerManagementEndpoint,
			serviceNetworkingEndpoint:         serviceNetworkingEndpoint,
		}
		out, err := CreateTPSubnetOp(gService, &servicenetworking.AddSubnetworkRequest{}, tenantProjectNumber)
		if err == nil {
			tt.Error("Expected an error but got nothing")
		} else {
			if out != nil {
				tt.Errorf("Unexpected output: %+v\n", out)
			}
			if !strings.Contains(err.(*vsaerrors.CustomError).OriginalErr.Error(), "googleapi: got HTTP response code") {
				tt.Errorf("Unexpected error: %s", err.(*vsaerrors.CustomError).OriginalErr.Error())
			}
		}
		CreateTPSubnetOp = _createTPSubnetOp
	})
	t.Run("When_CreateTPSubnetOpGoogleFails", func(tt *testing.T) {
		defer testReset(tt)
		ctx := context.Background()
		errMsg := "Please create Service Networking connection with service 'test-endpoint.example.com' from consumer project '" + testProviderProjectID + "' network '" + testProviderNetworkID + "' again."
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			if req.URL.Path == url {
				response, err := json.Marshal(&servicenetworking.Operation{Name: "net-op-1", Error: &servicenetworking.Status{Message: errMsg}})
				if err != nil {
					rw.WriteHeader(http.StatusBadRequest)
					return
				}
				rw.WriteHeader(http.StatusOK)
				_, _ = rw.Write(response)
				return
			}
			rw.WriteHeader(http.StatusOK)
		}))
		defer server.Close()
		svc, err := servicenetworking.NewService(
			ctx, option.WithHTTPClient(&http.Client{Timeout: time.Second}), option.WithEndpoint(server.URL))
		if err != nil {
			t.Errorf("Error getting service up: '%s'", err.Error())
		}
		serviceConsumerManagementEndpoint = "endpoint.goog"
		serviceNetworkingEndpoint = "endpoint.goog"

		defer func() {
			serviceConsumerManagementEndpoint = "abc"
			serviceNetworkingEndpoint = "something"
		}()
		gService := &GcpServices{
			AdminGCPService: &AdminGCPService{
				networkingService: svc,
			},
			Ctx:                               ctx,
			Logger:                            util.GetLogger(ctx),
			serviceConsumerManagementEndpoint: serviceConsumerManagementEndpoint,
			serviceNetworkingEndpoint:         serviceNetworkingEndpoint,
		}
		out, err := CreateTPSubnetOp(gService, &servicenetworking.AddSubnetworkRequest{}, tenantProjectNumber)
		if err == nil {
			tt.Error("Expected an error but got nothing")
		} else {
			if out != nil {
				tt.Errorf("Unexpected output: %+v\n", out)
			}
			if !strings.Contains(err.(*temporal.ApplicationError).Message(), "Setup/Configure Private Service Access (PSA) Peering") {
				tt.Errorf("Unexpected error: %s", err.(*temporal.ApplicationError).Message())
			}
			assertProviderDetailsRedacted(tt, err, testProviderProjectID)
			assertProviderDetailsRedacted(tt, err, testProviderNetworkID)
		}
		CreateTPSubnetOp = _createTPSubnetOp
	})
	t.Run("When_CreateTPSubnetOpIPExhaustion", func(tt *testing.T) {
		defer testReset(tt)
		ctx := context.Background()
		errMsg := "Couldn't find free blocks in allocated IP ranges. Please allocate new ranges for this service provider"
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			if req.URL.Path == url {
				response, err := json.Marshal(&servicenetworking.Operation{Name: "net-op-1", Error: &servicenetworking.Status{Message: errMsg}})
				if err != nil {
					rw.WriteHeader(http.StatusBadRequest)
					return
				}
				rw.WriteHeader(http.StatusOK)
				_, _ = rw.Write(response)
				return
			}
			rw.WriteHeader(http.StatusOK)
		}))
		defer server.Close()
		svc, err := servicenetworking.NewService(
			ctx, option.WithHTTPClient(&http.Client{Timeout: time.Second}), option.WithEndpoint(server.URL))
		if err != nil {
			t.Errorf("Error getting service up: '%s'", err.Error())
		}
		serviceConsumerManagementEndpoint = "endpoint.goog"
		serviceNetworkingEndpoint = "endpoint.goog"

		defer func() {
			serviceConsumerManagementEndpoint = "abc"
			serviceNetworkingEndpoint = "something"
		}()
		gService := &GcpServices{
			AdminGCPService: &AdminGCPService{
				networkingService: svc,
			},
			Ctx:                               ctx,
			Logger:                            util.GetLogger(ctx),
			serviceConsumerManagementEndpoint: serviceConsumerManagementEndpoint,
			serviceNetworkingEndpoint:         serviceNetworkingEndpoint,
		}
		out, err := CreateTPSubnetOp(gService, &servicenetworking.AddSubnetworkRequest{}, tenantProjectNumber)
		if err == nil {
			tt.Error("Expected an error but got nothing")
		} else {
			if out != nil {
				tt.Errorf("Unexpected output: %+v\n", out)
			}
			if !strings.Contains(err.(*temporal.ApplicationError).Message(), "Couldn't find free blocks in allocated IP ranges. Please allocate new ranges for this service provider") {
				tt.Errorf("Unexpected error: %s", err.(*temporal.ApplicationError).Message())
			}
		}
		CreateTPSubnetOp = _createTPSubnetOp
	})
	t.Run("WhenOperationReturnsUnclassifiedTerminalError", func(tt *testing.T) {
		defer testReset(tt)
		ctx := context.Background()
		errMsg := "Operation for " + testProviderResourcePath + " terminated\nforbidden_resource=true"
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			if req.URL.Path == url {
				response, err := json.Marshal(&servicenetworking.Operation{Name: "net-op-1", Error: &servicenetworking.Status{Message: errMsg}})
				if err != nil {
					rw.WriteHeader(http.StatusBadRequest)
					return
				}
				rw.WriteHeader(http.StatusOK)
				_, _ = rw.Write(response)
				return
			}
			rw.WriteHeader(http.StatusOK)
		}))
		defer server.Close()
		svc, err := servicenetworking.NewService(
			ctx, option.WithHTTPClient(&http.Client{Timeout: time.Second}), option.WithEndpoint(server.URL))
		if err != nil {
			t.Errorf("Error getting service up: '%s'", err.Error())
		}
		serviceConsumerManagementEndpoint = "endpoint.goog"
		serviceNetworkingEndpoint = "endpoint.goog"

		gService := &GcpServices{
			AdminGCPService: &AdminGCPService{
				networkingService: svc,
			},
			Ctx:                               ctx,
			Logger:                            util.GetLogger(ctx),
			serviceConsumerManagementEndpoint: serviceConsumerManagementEndpoint,
			serviceNetworkingEndpoint:         serviceNetworkingEndpoint,
		}
		out, err := CreateTPSubnetOp(gService, &servicenetworking.AddSubnetworkRequest{}, tenantProjectNumber)
		if err == nil {
			tt.Error("Expected an error but got nothing")
		} else {
			if out != nil {
				tt.Errorf("Expected nil")
			}
			assertTerminalGCPOperationError(tt, err, testProviderResourcePath)
		}
		CreateTPSubnetOp = _createTPSubnetOp
	})
	t.Run("WhenOK", func(tt *testing.T) {
		defer testReset(tt)
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			if req.URL.Path == url {
				response, err := json.Marshal(&servicenetworking.Operation{Name: "net-op-1", Error: nil})
				if err != nil {
					rw.WriteHeader(http.StatusBadRequest)
					return
				}
				_, _ = rw.Write(response)
				return
			}
		}))
		defer server.Close()
		serviceConsumerManagementEndpoint = "endpoint.goog"
		serviceNetworkingEndpoint = "endpoint.goog"

		defer func() {
			serviceConsumerManagementEndpoint = "abc"
			serviceNetworkingEndpoint = "something"
		}()
		svc, err := servicenetworking.NewService(
			context.TODO(), option.WithHTTPClient(&http.Client{Timeout: time.Second}), option.WithEndpoint(server.URL))
		if err != nil {
			t.Errorf("Error getting service up: '%s'", err.Error())
		}
		consumerNetwork := "projects/1234/global/networks/network-to-netapp2"
		ctx := context.Background()
		gService := &GcpServices{
			AdminGCPService: &AdminGCPService{
				networkingService: svc,
			},
			Ctx:                               ctx,
			Logger:                            util.GetLogger(ctx),
			serviceConsumerManagementEndpoint: "endpoint.goog",
			serviceNetworkingEndpoint:         serviceNetworkingEndpoint,
		}
		req := &servicenetworking.AddSubnetworkRequest{
			Consumer:        "projects/1234",
			Region:          "us-east-4",
			Description:     "vsa-network",
			IpPrefixLength:  28,
			ConsumerNetwork: consumerNetwork,
			Subnetwork:      "vsa-" + "us-east-4",
		}
		out, err := CreateTPSubnetOp(gService, req, tenantProjectNumber)
		if err != nil {
			tt.Errorf("Unexpected error: %s", err.Error())
		} else {
			if out == nil {
				tt.Errorf("Output unexpectedly nil")
			} else {
				if out.Name != "net-op-1" {
					tt.Errorf("Unexpected interconnect name %s", out.Name)
				}
			}
		}
		CreateTPSubnetOp = _createTPSubnetOp
	})
}

func Test_CreateVPC(t *testing.T) {
	projectName := "test-project"
	vpcNetwork := &models.VPCNetwork{
		Name:        "test-vpc-network",
		ProjectName: projectName,
	}
	url := "/projects/" + projectName + "/global/networks"
	t.Run("WhenCreateVPCFails", func(tt *testing.T) {
		defer testReset(tt)
		ctx := context.Background()
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			if req.Method == http.MethodPost && req.URL.Path == url {
				rw.WriteHeader(http.StatusInternalServerError)
			}
		}))
		defer server.Close()
		computeSvc, err := compute.NewService(
			ctx, option.WithHTTPClient(&http.Client{Timeout: time.Second}), option.WithEndpoint(server.URL))
		if err != nil {
			tt.Fatalf("Failed to create compute service: %v", err)
		}

		gService := &GcpServices{
			AdminGCPService: &AdminGCPService{
				computeService: computeSvc,
			},
			Ctx:    ctx,
			Logger: util.GetLogger(ctx),
		}

		op, err := gService.CreateVPC(vpcNetwork)
		if err == nil {
			tt.Error("Expected an error but got none")
		}
		if op != "" {
			tt.Errorf("Expected nil operation but got: %+v", op)
		}
	})

	t.Run("WhenCreateVPCSucceeds", func(tt *testing.T) {
		defer testReset(tt)
		ctx := context.Background()
		operationName := "test-operation"
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			if req.Method == http.MethodPost && req.URL.Path == url {
				response, _ := json.Marshal(&compute.Operation{Name: operationName})
				rw.WriteHeader(http.StatusOK)
				_, _ = rw.Write(response)
			}
		}))
		defer server.Close()

		computeSvc, err := compute.NewService(
			ctx, option.WithHTTPClient(&http.Client{Timeout: time.Second}), option.WithEndpoint(server.URL))
		if err != nil {
			tt.Fatalf("Failed to create compute service: %v", err)
		}

		gcpService := &GcpServices{
			AdminGCPService: &AdminGCPService{
				computeService: computeSvc,
			},
			Ctx:    ctx,
			Logger: util.GetLogger(ctx),
		}

		op, err := gcpService.CreateVPC(vpcNetwork)
		if err != nil {
			tt.Errorf("Unexpected error: %v", err)
		}
		if op != operationName {
			tt.Errorf("Unexpected operation: %+v", op)
		}
	})
}

func Test_CreateSubnetwork(t *testing.T) {
	projectName := "test-project"
	region := "us-central1"
	subnetworkRequest := &models.Subnet{
		Name:        "test-subnetwork",
		Network:     "test-vpc-network",
		Region:      &region,
		ProjectName: projectName,
	}
	url := fmt.Sprintf("/projects/%s/regions/%s/subnetworks", projectName, region)
	t.Run("WhenCreateSubnetworkFails", func(tt *testing.T) {
		defer testReset(tt)
		ctx := context.Background()
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			if req.Method == http.MethodPost && req.URL.Path == url {
				rw.WriteHeader(http.StatusInternalServerError)
				return
			}
			rw.WriteHeader(http.StatusBadRequest)
		}))
		defer server.Close()

		computeSvc, err := compute.NewService(
			ctx, option.WithHTTPClient(&http.Client{Timeout: time.Second}), option.WithEndpoint(server.URL))
		if err != nil {
			tt.Fatalf("Failed to create compute service: %v", err)
		}

		gService := &GcpServices{
			AdminGCPService: &AdminGCPService{
				computeService: computeSvc,
			},
			Ctx:    ctx,
			Logger: util.GetLogger(ctx),
		}

		op, err := gService.CreateSubnetwork(subnetworkRequest)
		if err == nil {
			tt.Error("Expected an error but got none")
		}
		if op != "" {
			tt.Errorf("Expected nil operation but got: %+v", op)
		}
	})

	t.Run("WhenCreateSubnetworkSucceeds", func(tt *testing.T) {
		defer testReset(tt)
		ctx := context.Background()
		operationName := "test-operation"
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			if req.Method == http.MethodPost && req.URL.Path == url {
				response, _ := json.Marshal(&compute.Operation{Name: operationName})
				rw.WriteHeader(http.StatusOK)
				_, _ = rw.Write(response)
				return
			}
			rw.WriteHeader(http.StatusBadRequest)
		}))
		defer server.Close()

		computeSvc, err := compute.NewService(
			ctx, option.WithHTTPClient(&http.Client{Timeout: time.Second}), option.WithEndpoint(server.URL))
		if err != nil {
			tt.Fatalf("Failed to create compute service: %v", err)
		}

		gService := &GcpServices{
			AdminGCPService: &AdminGCPService{
				computeService: computeSvc,
			},
			Ctx:    ctx,
			Logger: util.GetLogger(ctx),
		}

		op, err := gService.CreateSubnetwork(subnetworkRequest)
		if err != nil {
			tt.Errorf("Unexpected error: %v", err)
		}
		if op != operationName {
			tt.Errorf("Unexpected operation: %+v", op)
		}
	})
}

func Test_GetSubnetwork(t *testing.T) {
	t.Run("WhenGetSubnetworkFails", func(tt *testing.T) {
		defer testReset(tt)
		ctx := context.Background()
		projectName := "1079058383248"
		subnetName := "vpc1-subnet"
		region := "US-East-4"
		url := "/projects/project/regions/region/subnetworks/r"
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			if req.URL.Path == url {
				rw.WriteHeader(http.StatusInternalServerError)
				return
			}
			rw.WriteHeader(http.StatusBadRequest)
		}))
		computeSvc, err := compute.NewService(
			ctx, option.WithHTTPClient(&http.Client{Timeout: time.Second}), option.WithEndpoint(server.URL))
		if err != nil {
			t.Errorf("Error getting service up: '%s'", err.Error())
		}

		gService := &GcpServices{
			AdminGCPService: &AdminGCPService{
				computeService: computeSvc,
			},
			Logger: util.GetLogger(ctx),
		}
		_, err = gService.GetSubnetwork(projectName, region, subnetName)
		if err == nil {
			tt.Error("Expected an error but got nothing")
		} else {
			if !strings.Contains(err.Error(), "googleapi: got HTTP response code 400 with body") {
				tt.Errorf("Unexpected error: %s", err.Error())
			}
		}
	})

	t.Run("WhenSuccess", func(tt *testing.T) {
		defer testReset(tt)
		ctx := context.Background()
		projectName := "1079058383248"
		subnetName := "vpc1-subnet"
		region := "US-East-4"
		url := fmt.Sprintf("/projects/%s/regions/%s/subnetworks/%s", projectName, region, subnetName)
		resp := &compute.Subnetwork{Name: subnetName}
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			if req.URL.Path == url {
				response, err := json.Marshal(resp)
				if err != nil {
					rw.WriteHeader(http.StatusBadRequest)
					return
				}
				_, _ = rw.Write(response)
				return
			}
			rw.WriteHeader(http.StatusBadRequest)
		}))
		computeSvc, err := compute.NewService(
			ctx, option.WithHTTPClient(&http.Client{Timeout: time.Second}), option.WithEndpoint(server.URL))
		if err != nil {
			t.Errorf("Error getting service up: '%s'", err.Error())
		}

		gService := &GcpServices{
			AdminGCPService: &AdminGCPService{
				computeService: computeSvc,
			},
			Ctx:    ctx,
			Logger: util.GetLogger(ctx),
		}
		out, err := gService.GetSubnetwork(projectName, region, subnetName)
		if err != nil {
			tt.Errorf("Unexpected error: %s", err.Error())
		} else {
			if out == nil {
				tt.Errorf("Output unexpectedly nil")
			} else {
				if out.Name != subnetName {
					tt.Errorf("Unexpected subnetwork name %s", out.Name)
				}
			}
		}
	})
}

func Test_GetVPCNetwork(t *testing.T) {
	projectName := "1079058383248"
	vpcName := "vpc1"
	url := "/projects/1079058383248/global/networks/vpc1"
	t.Run("WhenGetVPCNetworkFails", func(tt *testing.T) {
		defer testReset(tt)
		ctx := context.Background()
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			if req.URL.Path == url {
				rw.WriteHeader(http.StatusInternalServerError)
				return
			}
			rw.WriteHeader(http.StatusBadRequest)
		}))
		computeSvc, err := compute.NewService(
			ctx, option.WithHTTPClient(&http.Client{Timeout: time.Second}), option.WithEndpoint(server.URL))
		if err != nil {
			t.Errorf("Error getting service up: '%s'", err.Error())
		}

		gService := &GcpServices{
			AdminGCPService: &AdminGCPService{
				computeService: computeSvc,
			},
			Logger: util.GetLogger(ctx),
		}
		_, err = gService.GetVPCNetwork(projectName, vpcName)
		if err == nil {
			tt.Error("Expected an error but got nothing")
		} else {
			if !strings.Contains(err.Error(), "googleapi: got HTTP response code 500 with body") {
				tt.Errorf("Unexpected error: %s", err.Error())
			}
		}
	})

	t.Run("WhenSuccess", func(tt *testing.T) {
		defer testReset(tt)
		ctx := context.Background()
		resp := &compute.Network{Name: vpcName}
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			if req.URL.Path == url {
				response, err := json.Marshal(resp)
				if err != nil {
					rw.WriteHeader(http.StatusBadRequest)
					return
				}
				_, _ = rw.Write(response)
				return
			}
			rw.WriteHeader(http.StatusBadRequest)
		}))
		computeSvc, err := compute.NewService(
			ctx, option.WithHTTPClient(&http.Client{Timeout: time.Second}), option.WithEndpoint(server.URL))
		if err != nil {
			t.Errorf("Error getting service up: '%s'", err.Error())
		}

		gService := &GcpServices{
			AdminGCPService: &AdminGCPService{
				computeService: computeSvc,
			},
			Ctx:    ctx,
			Logger: util.GetLogger(ctx),
		}
		out, err := gService.GetVPCNetwork(projectName, vpcName)
		if err != nil {
			tt.Errorf("Unexpected error: %s", err.Error())
		} else {
			if out == nil {
				tt.Errorf("Output unexpectedly nil")
			} else {
				if out.Name != vpcName {
					tt.Errorf("Unexpected subnetwork name %s", out.Name)
				}
			}
		}
	})
}

func Test_GetFirewall(t *testing.T) {
	projectName := "1079058383248"
	vpcName := "vpc1"
	firewallRuleName := "ingress-" + vpcName
	url := "/projects/" + projectName + "/global/firewalls/" + firewallRuleName
	t.Run("WhenGetFirewallFails", func(tt *testing.T) {
		defer testReset(tt)
		ctx := context.Background()
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			if req.URL.Path == url {
				rw.WriteHeader(http.StatusInternalServerError)
				return
			}
			rw.WriteHeader(http.StatusBadRequest)
		}))
		computeSvc, err := compute.NewService(
			ctx, option.WithHTTPClient(&http.Client{Timeout: time.Second}), option.WithEndpoint(server.URL))
		if err != nil {
			t.Errorf("Error getting service up: '%s'", err.Error())
		}

		gService := &GcpServices{
			AdminGCPService: &AdminGCPService{
				computeService: computeSvc,
			},
			Logger: util.GetLogger(ctx),
		}
		_, err = gService.GetFirewall(projectName, firewallRuleName)
		if err == nil {
			tt.Error("Expected an error but got nothing")
		} else {
			if !strings.Contains(err.Error(), "googleapi: got HTTP response code 500 with body") {
				tt.Errorf("Unexpected error: %s", err.Error())
			}
		}
	})

	t.Run("WhenGetFirewallSuccess", func(tt *testing.T) {
		defer testReset(tt)
		ctx := context.Background()
		resp := &compute.Firewall{Name: firewallRuleName}
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			if req.URL.Path == url {
				response, err := json.Marshal(resp)
				if err != nil {
					rw.WriteHeader(http.StatusBadRequest)
					return
				}
				_, _ = rw.Write(response)
				return
			}
			rw.WriteHeader(http.StatusBadRequest)
		}))
		computeSvc, err := compute.NewService(
			ctx, option.WithHTTPClient(&http.Client{Timeout: time.Second}), option.WithEndpoint(server.URL))
		if err != nil {
			t.Errorf("Error getting service up: '%s'", err.Error())
		}

		gService := &GcpServices{
			AdminGCPService: &AdminGCPService{
				computeService: computeSvc,
			},
			Ctx:    ctx,
			Logger: util.GetLogger(ctx),
		}
		out, err := gService.GetFirewall(projectName, firewallRuleName)
		if err != nil {
			tt.Errorf("Unexpected error: %s", err.Error())
		} else {
			if out == nil {
				tt.Errorf("Output unexpectedly nil")
			} else {
				if out.Name != firewallRuleName {
					tt.Errorf("Unexpected firewall name %s", out.Name)
				}
			}
		}
	})
}

func Test_InsertFirewall(t *testing.T) {
	projectName := "1079058383248"
	vpcName := "vpc1"
	url := "/projects/1079058383248/global/firewalls"
	allowedPortRules := []string{"tcp", "icmp", "udp"}
	sourceRanges := []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"}
	firewallRule := &models.Firewall{
		Name:             "ingress-" + vpcName,
		Description:      "Allow incoming traffic on specific ports",
		VPCNetworkName:   fmt.Sprintf("projects/%s/global/networks/%s", projectName, vpcName),
		AllowedPortRules: allowedPortRules,
		ProjectName:      projectName,
		SourceRanges:     sourceRanges,
		Direction:        "INGRESS",
		Priority:         1000,
	}
	t.Run("WhenInsertFirewallFails", func(tt *testing.T) {
		defer testReset(tt)
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			if req.URL.Path == url {
				rw.WriteHeader(http.StatusInternalServerError)
				return
			}
			rw.WriteHeader(http.StatusBadRequest)
		}))
		computeSvc, err := compute.NewService(
			context.Background(), option.WithHTTPClient(&http.Client{Timeout: time.Second}), option.WithEndpoint(server.URL))
		if err != nil {
			t.Errorf("Error getting service up: '%s'", err.Error())
		}
		ctx := context.Background()
		gService := &GcpServices{
			AdminGCPService: &AdminGCPService{
				computeService: computeSvc,
			},
			Ctx:                               ctx,
			Logger:                            util.GetLogger(ctx),
			serviceConsumerManagementEndpoint: serviceConsumerManagementEndpoint,
			serviceNetworkingEndpoint:         serviceNetworkingEndpoint,
			Retry:                             NewExponentialRetryStrategy(time.Millisecond, 3),
		}
		_, err = gService.InsertFirewall(firewallRule)
		if err == nil {
			tt.Error("Expected an error but got nothing")
		} else {
			if !strings.Contains(err.(*vsaerrors.CustomError).OriginalErr.Error(), "googleapi: got HTTP response code 500 with body") {
				tt.Errorf("Unexpected error: %s", err.(*vsaerrors.CustomError).OriginalErr.Error())
			}
		}
	})

	t.Run("WhenSuccess", func(tt *testing.T) {
		defer testReset(tt)
		operationName := "random-operation-name"
		resp := &compute.Operation{Name: operationName}
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			if req.URL.Path == url {
				response, err := json.Marshal(resp)
				if err != nil {
					rw.WriteHeader(http.StatusBadRequest)
					return
				}
				_, _ = rw.Write(response)
				return
			}
			rw.WriteHeader(http.StatusBadRequest)
		}))
		ctx := context.Background()
		computeSvc, err := compute.NewService(
			ctx, option.WithHTTPClient(&http.Client{Timeout: time.Second}), option.WithEndpoint(server.URL))
		if err != nil {
			t.Errorf("Error getting service up: '%s'", err.Error())
		}

		gService := &GcpServices{
			AdminGCPService: &AdminGCPService{
				computeService: computeSvc,
			},
			Ctx:    ctx,
			Logger: util.GetLogger(ctx),
		}
		out, err := gService.InsertFirewall(firewallRule)
		if err != nil {
			tt.Errorf("Unexpected error: %s", err.Error())
		} else {
			if out != operationName {
				tt.Errorf("Unexpected operation name %s", out)
			}
		}
	})
}

// Unit tests for CreateTPSubnetOp
func Test_CreateTPSubnetOp(t *testing.T) {
	tenantProjectNumber := "123456789"
	consumerNetwork := "projects/123456789/global/networks/test-network"
	region := "us-central1"
	ctx := context.Background()
	subnetName := fmt.Sprintf("vsa-us-c1-%s", tenantProjectNumber)

	t.Run("WhenParseProjectIdFails", func(tt *testing.T) {
		gService := &GcpServices{Ctx: ctx, Logger: util.GetLogger(ctx)}
		consumerNetworkIncorrect := "projects/123456789/global/networks"

		_, err := gService.CreateTPSubnetOp(tenantProjectNumber, consumerNetworkIncorrect, region, subnetName, false, nil)
		if err == nil || !strings.Contains(err.Error(), "parseProjectId failed for network : "+consumerNetworkIncorrect) {
			tt.Errorf("Expected parse error, got: %v", err)
		}
	})

	t.Run("WhenCreateTPSubnetOpFails", func(tt *testing.T) {
		gService := &GcpServices{Ctx: ctx, Logger: util.GetLogger(ctx)}
		origCreate := CreateTPSubnetOp
		CreateTPSubnetOp = func(*GcpServices, *servicenetworking.AddSubnetworkRequest, string) (*models.ComputeOperation, error) {
			return nil, fmt.Errorf("create error")
		}
		defer func() { CreateTPSubnetOp = origCreate }()
		_, err := gService.CreateTPSubnetOp(tenantProjectNumber, consumerNetwork, region, subnetName, false, nil)
		if err == nil || !strings.Contains(err.Error(), "create error") {
			tt.Errorf("Expected create error, got: %v", err)
		}
	})

	t.Run("WhenSuccess", func(tt *testing.T) {
		gService := &GcpServices{Ctx: ctx, Logger: util.GetLogger(ctx)}
		origCreate := CreateTPSubnetOp
		CreateTPSubnetOp = func(*GcpServices, *servicenetworking.AddSubnetworkRequest, string) (*models.ComputeOperation, error) {
			return &models.ComputeOperation{Name: "op-1", Response: []byte("success")}, nil
		}
		defer func() { CreateTPSubnetOp = origCreate }()
		resp, err := gService.CreateTPSubnetOp(tenantProjectNumber, consumerNetwork, region, subnetName, false, nil)
		if err != nil {
			tt.Errorf("Unexpected error: %v", err)
		}
		if string(*resp) != "op-1" {
			tt.Errorf("Expected response 'success', got: %s", string(*resp))
		}
	})

	t.Run("WhenSuccessWithLargeCapacity", func(tt *testing.T) {
		gService := &GcpServices{Ctx: ctx, Logger: util.GetLogger(ctx)}
		origCreate := CreateTPSubnetOp
		CreateTPSubnetOp = func(*GcpServices, *servicenetworking.AddSubnetworkRequest, string) (*models.ComputeOperation, error) {
			return &models.ComputeOperation{Name: "op-large-1", Response: []byte("success")}, nil
		}
		defer func() { CreateTPSubnetOp = origCreate }()
		resp, err := gService.CreateTPSubnetOp(tenantProjectNumber, consumerNetwork, region, subnetName, true, nil)
		if err != nil {
			tt.Errorf("Unexpected error: %v", err)
		}
		if string(*resp) != "op-large-1" {
			tt.Errorf("Expected response 'op-large-1', got: %s", string(*resp))
		}
	})

	t.Run("WhenCreateTPSubnetOpFailsWithLargeCapacity", func(tt *testing.T) {
		gService := &GcpServices{Ctx: ctx, Logger: util.GetLogger(ctx)}
		origCreate := CreateTPSubnetOp
		CreateTPSubnetOp = func(*GcpServices, *servicenetworking.AddSubnetworkRequest, string) (*models.ComputeOperation, error) {
			return nil, fmt.Errorf("create error with large capacity")
		}
		defer func() { CreateTPSubnetOp = origCreate }()
		_, err := gService.CreateTPSubnetOp(tenantProjectNumber, consumerNetwork, region, subnetName, true, nil)
		if err == nil || !strings.Contains(err.Error(), "create error with large capacity") {
			tt.Errorf("Expected create error with large capacity, got: %v", err)
		}
	})

	t.Run("WhenParseProjectIdFailsWithLargeCapacity", func(tt *testing.T) {
		gService := &GcpServices{Ctx: ctx, Logger: util.GetLogger(ctx)}
		consumerNetworkIncorrect := "projects/123456789/global/networks"

		_, err := gService.CreateTPSubnetOp(tenantProjectNumber, consumerNetworkIncorrect, region, subnetName, true, nil)
		if err == nil || !strings.Contains(err.Error(), "parseProjectId failed for network : "+consumerNetworkIncorrect) {
			tt.Errorf("Expected parse error with large capacity, got: %v", err)
		}
	})

	t.Run("WhenNetworkSizeCalculationIsCorrect", func(tt *testing.T) {
		gService := &GcpServices{Ctx: ctx, Logger: util.GetLogger(ctx)}
		var capturedRequest *servicenetworking.AddSubnetworkRequest
		origCreate := CreateTPSubnetOp
		CreateTPSubnetOp = func(gService *GcpServices, request *servicenetworking.AddSubnetworkRequest, tenantProjectNumber string) (*models.ComputeOperation, error) {
			capturedRequest = request
			return &models.ComputeOperation{Name: "op-size-test", Response: []byte("success")}, nil
		}
		defer func() { CreateTPSubnetOp = origCreate }()

		// Test with isLargeCapacity = false (should use minimumTenantNetworkSize)
		_, err := gService.CreateTPSubnetOp(tenantProjectNumber, consumerNetwork, region, subnetName, false, nil)
		if err != nil {
			tt.Errorf("Unexpected error: %v", err)
		}
		if capturedRequest == nil {
			tt.Error("Expected request to be captured")
		} else {
			// Check that the request has the correct network size for regular capacity
			expectedSize := env.GetInt64("DATA_SUBNET_CIDR_BLOCK", int64(28))
			if capturedRequest.IpPrefixLength != expectedSize {
				tt.Errorf("Expected IpPrefixLength %d for regular capacity, got %d", expectedSize, capturedRequest.IpPrefixLength)
			}
		}

		// Test with isLargeCapacity = true (should use minimumLVTenantNetworkSize)
		_, err = gService.CreateTPSubnetOp(tenantProjectNumber, consumerNetwork, region, subnetName, true, nil)
		if err != nil {
			tt.Errorf("Unexpected error: %v", err)
		}
		if capturedRequest == nil {
			tt.Error("Expected request to be captured")
		} else {
			// Check that the request has the correct network size for large capacity
			expectedSize := env.GetInt64("DATA_SUBNET_CIDR_BLOCK_LV", int64(26))
			if capturedRequest.IpPrefixLength != expectedSize {
				tt.Errorf("Expected IpPrefixLength %d for large capacity, got %d", expectedSize, capturedRequest.IpPrefixLength)
			}
		}
	})

	t.Run("WhenRequestedRangesArePassedForRegularCapacity", func(tt *testing.T) {
		gService := &GcpServices{Ctx: ctx, Logger: util.GetLogger(ctx)}
		var capturedRequest *servicenetworking.AddSubnetworkRequest
		origCreate := CreateTPSubnetOp
		CreateTPSubnetOp = func(gService *GcpServices, request *servicenetworking.AddSubnetworkRequest, tenantProjectNumber string) (*models.ComputeOperation, error) {
			capturedRequest = request
			return &models.ComputeOperation{Name: "op-ranges-regular", Response: []byte("success")}, nil
		}
		defer func() { CreateTPSubnetOp = origCreate }()

		requestedRanges := []string{"my-range-1", "my-range-2"}
		resp, err := gService.CreateTPSubnetOp(tenantProjectNumber, consumerNetwork, region, subnetName, false, requestedRanges)
		if err != nil {
			tt.Errorf("Unexpected error: %v", err)
		}
		if resp == nil || *resp != "op-ranges-regular" {
			tt.Errorf("Expected operation name 'op-ranges-regular', got: %v", resp)
		}
		if capturedRequest == nil {
			tt.Fatal("Expected request to be captured")
		}
		if len(capturedRequest.RequestedRanges) != 2 {
			tt.Errorf("Expected 2 requested ranges, got %d", len(capturedRequest.RequestedRanges))
		} else {
			if capturedRequest.RequestedRanges[0] != "my-range-1" || capturedRequest.RequestedRanges[1] != "my-range-2" {
				tt.Errorf("Expected ranges [my-range-1 my-range-2], got %v", capturedRequest.RequestedRanges)
			}
		}
	})

	t.Run("WhenRequestedRangesArePassedForLargeCapacity", func(tt *testing.T) {
		gService := &GcpServices{Ctx: ctx, Logger: util.GetLogger(ctx)}
		var capturedRequest *servicenetworking.AddSubnetworkRequest
		origCreate := CreateTPSubnetOp
		CreateTPSubnetOp = func(gService *GcpServices, request *servicenetworking.AddSubnetworkRequest, tenantProjectNumber string) (*models.ComputeOperation, error) {
			capturedRequest = request
			return &models.ComputeOperation{Name: "op-ranges-large", Response: []byte("success")}, nil
		}
		defer func() { CreateTPSubnetOp = origCreate }()

		requestedRanges := []string{"lv-range-1"}
		resp, err := gService.CreateTPSubnetOp(tenantProjectNumber, consumerNetwork, region, subnetName, true, requestedRanges)
		if err != nil {
			tt.Errorf("Unexpected error: %v", err)
		}
		if resp == nil || *resp != "op-ranges-large" {
			tt.Errorf("Expected operation name 'op-ranges-large', got: %v", resp)
		}
		if capturedRequest == nil {
			tt.Fatal("Expected request to be captured")
		}
		if len(capturedRequest.RequestedRanges) != 1 || capturedRequest.RequestedRanges[0] != "lv-range-1" {
			tt.Errorf("Expected ranges [lv-range-1], got %v", capturedRequest.RequestedRanges)
		}
		expectedSize := env.GetInt64("DATA_SUBNET_CIDR_BLOCK_LV", int64(26))
		if capturedRequest.IpPrefixLength != expectedSize {
			tt.Errorf("Expected IpPrefixLength %d for large capacity, got %d", expectedSize, capturedRequest.IpPrefixLength)
		}
	})
}

// Test for getNetworkSize function
func Test_getNetworkSize(t *testing.T) {
	t.Run("WhenIsLargeCapacityIsTrue", func(tt *testing.T) {
		size := getNetworkSize(true)
		expectedSize := env.GetInt64("DATA_SUBNET_CIDR_BLOCK_LV", int64(26))
		if size != expectedSize {
			tt.Errorf("Expected network size %d for large capacity, got %d", expectedSize, size)
		}
	})

	t.Run("WhenIsLargeCapacityIsFalse", func(tt *testing.T) {
		size := getNetworkSize(false)
		expectedSize := env.GetInt64("DATA_SUBNET_CIDR_BLOCK", int64(28))
		if size != expectedSize {
			tt.Errorf("Expected network size %d for regular capacity, got %d", expectedSize, size)
		}
	})
}

func TestAddSubnetSecondaryRangeOp(t *testing.T) {
	ctx := context.Background()
	region := "us-central1"
	projectID := "test-project"
	subnetName := "test-subnet"
	rangeName := "ic-lif-10-50-166-0-29"
	ipCidrRange := "10.50.166.0/29"
	subnetURL := "/projects/test-project/regions/us-central1/subnetworks/test-subnet"

	newService := func(tt *testing.T, handler http.HandlerFunc) (*GcpServices, func()) {
		server := httptest.NewServer(handler)
		computeSvc, err := compute.NewService(ctx, option.WithHTTPClient(&http.Client{Timeout: time.Second}), option.WithEndpoint(server.URL))
		if err != nil {
			server.Close()
			tt.Fatalf("Failed to create compute service: %v", err)
		}
		return &GcpServices{
			AdminGCPService: &AdminGCPService{computeService: computeSvc},
			Ctx:             ctx,
			Logger:          util.GetLogger(ctx),
		}, server.Close
	}

	t.Run("WhenPatchSucceeds", func(tt *testing.T) {
		var patched *compute.Subnetwork
		gService, closeServer := newService(tt, func(rw http.ResponseWriter, req *http.Request) {
			if req.URL.Path != subnetURL {
				rw.WriteHeader(http.StatusNotFound)
				return
			}
			switch req.Method {
			case http.MethodGet:
				response, _ := json.Marshal(&compute.Subnetwork{
					Name:        subnetName,
					Fingerprint: "fingerprint-1",
					SecondaryIpRanges: []*compute.SubnetworkSecondaryRange{
						{RangeName: "existing-range", IpCidrRange: "10.60.0.0/29"},
					},
				})
				_, _ = rw.Write(response)
			case http.MethodPatch:
				patched = &compute.Subnetwork{}
				_ = json.NewDecoder(req.Body).Decode(patched)
				response, _ := json.Marshal(&compute.Operation{Name: "patch-op-1"})
				_, _ = rw.Write(response)
			default:
				rw.WriteHeader(http.StatusBadRequest)
			}
		})
		defer closeServer()

		opName, err := gService.AddSubnetSecondaryRangeOp(projectID, region, subnetName, rangeName, ipCidrRange)
		if err != nil {
			tt.Fatalf("Unexpected error: %v", err)
		}
		if opName != "patch-op-1" {
			tt.Errorf("Expected operation name patch-op-1, got %s", opName)
		}
		if patched == nil {
			tt.Fatal("Expected a patch request to be sent")
		}
		if patched.Fingerprint != "fingerprint-1" {
			tt.Errorf("Expected the current fingerprint to be sent, got %s", patched.Fingerprint)
		}
		if len(patched.SecondaryIpRanges) != 2 {
			tt.Fatalf("Expected the existing range to be preserved alongside the new one, got %d", len(patched.SecondaryIpRanges))
		}
		if patched.SecondaryIpRanges[1].RangeName != rangeName || patched.SecondaryIpRanges[1].IpCidrRange != ipCidrRange {
			tt.Errorf("Unexpected appended range: %+v", patched.SecondaryIpRanges[1])
		}
	})

	t.Run("WhenRangeAlreadyAttached", func(tt *testing.T) {
		patchCalled := false
		gService, closeServer := newService(tt, func(rw http.ResponseWriter, req *http.Request) {
			if req.Method == http.MethodPatch {
				patchCalled = true
			}
			response, _ := json.Marshal(&compute.Subnetwork{
				Name:        subnetName,
				Fingerprint: "fingerprint-1",
				SecondaryIpRanges: []*compute.SubnetworkSecondaryRange{
					{RangeName: rangeName, IpCidrRange: ipCidrRange},
				},
			})
			_, _ = rw.Write(response)
		})
		defer closeServer()

		opName, err := gService.AddSubnetSecondaryRangeOp(projectID, region, subnetName, rangeName, ipCidrRange)
		if err != nil {
			tt.Fatalf("Unexpected error: %v", err)
		}
		if opName != "" {
			tt.Errorf("Expected no operation when the range is already attached, got %s", opName)
		}
		if patchCalled {
			tt.Error("Expected no patch call when the range is already attached")
		}
	})

	t.Run("WhenAnotherRangeHoldsTheSameCIDR", func(tt *testing.T) {
		gService, closeServer := newService(tt, func(rw http.ResponseWriter, req *http.Request) {
			response, _ := json.Marshal(&compute.Subnetwork{
				Name:        subnetName,
				Fingerprint: "fingerprint-1",
				SecondaryIpRanges: []*compute.SubnetworkSecondaryRange{
					{RangeName: "other-range", IpCidrRange: ipCidrRange},
				},
			})
			_, _ = rw.Write(response)
		})
		defer closeServer()

		_, err := gService.AddSubnetSecondaryRangeOp(projectID, region, subnetName, rangeName, ipCidrRange)
		if err == nil {
			tt.Fatal("Expected an error but got nothing")
		}
		var customErr *vsaerrors.CustomError
		if !errors.As(err, &customErr) || customErr.OriginalErr == nil ||
			!strings.Contains(customErr.OriginalErr.Error(), "conflicts with existing range") {
			tt.Errorf("Expected a conflict error, got: %v", err)
		}
	})

	t.Run("WhenGetFails", func(tt *testing.T) {
		gService, closeServer := newService(tt, func(rw http.ResponseWriter, req *http.Request) {
			http.Error(rw, "internal error", http.StatusInternalServerError)
		})
		defer closeServer()

		opName, err := gService.AddSubnetSecondaryRangeOp(projectID, region, subnetName, rangeName, ipCidrRange)
		if err == nil || !strings.Contains(err.Error(), "internal error") {
			tt.Errorf("Expected the read failure to surface, got: %v", err)
		}
		if opName != "" {
			tt.Errorf("Expected empty operation name on error, got: %s", opName)
		}
	})

	t.Run("WhenPatchConflicts", func(tt *testing.T) {
		gService, closeServer := newService(tt, func(rw http.ResponseWriter, req *http.Request) {
			if req.Method == http.MethodPatch {
				rw.WriteHeader(http.StatusConflict)
				_, _ = rw.Write([]byte(`{"error": {"code": 409, "message": "IP range overlaps with an existing subnetwork"}}`))
				return
			}
			response, _ := json.Marshal(&compute.Subnetwork{Name: subnetName, Fingerprint: "fingerprint-1"})
			_, _ = rw.Write(response)
		})
		defer closeServer()

		_, err := gService.AddSubnetSecondaryRangeOp(projectID, region, subnetName, rangeName, ipCidrRange)
		if err == nil {
			tt.Fatal("Expected an error but got nothing")
		}
		var appErr *temporal.ApplicationError
		if !errors.As(err, &appErr) || !appErr.NonRetryable() {
			tt.Errorf("Expected a non-retryable application error, got: %v", err)
		}
	})

	t.Run("WhenFingerprintIsStale", func(tt *testing.T) {
		gService, closeServer := newService(tt, func(rw http.ResponseWriter, req *http.Request) {
			if req.Method == http.MethodPatch {
				rw.WriteHeader(http.StatusPreconditionFailed)
				_, _ = rw.Write([]byte(`{"error": {"code": 412, "message": "fingerprint mismatch"}}`))
				return
			}
			response, _ := json.Marshal(&compute.Subnetwork{Name: subnetName, Fingerprint: "fingerprint-1"})
			_, _ = rw.Write(response)
		})
		defer closeServer()

		_, err := gService.AddSubnetSecondaryRangeOp(projectID, region, subnetName, rangeName, ipCidrRange)
		if err == nil {
			tt.Fatal("Expected an error but got nothing")
		}
		var appErr *temporal.ApplicationError
		if errors.As(err, &appErr) && appErr.NonRetryable() {
			tt.Error("Expected a stale fingerprint to stay retryable")
		}
	})
}

func TestReleaseSubnetworkOp(t *testing.T) {
	ctx := context.Background()
	region := "us-central1"
	projectId := "test-project"
	subnetwork := "test-subnet"
	deleteUrl := "/projects/test-project/regions/us-central1/subnetworks/test-subnet"

	operationName := "test-operation"
	t.Run("WhenDeleteSucceeds", func(tt *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			if req.Method == http.MethodDelete && req.URL.Path == deleteUrl {
				response, _ := json.Marshal(&compute.Operation{Name: operationName})
				rw.WriteHeader(http.StatusOK)
				_, _ = rw.Write(response)
			}
		}))
		defer server.Close()

		computeSvc, err := compute.NewService(
			ctx, option.WithHTTPClient(&http.Client{Timeout: time.Second}), option.WithEndpoint(server.URL))
		if err != nil {
			tt.Fatalf("Failed to create compute service: %v", err)
		}

		gService := &GcpServices{
			AdminGCPService: &AdminGCPService{
				computeService: computeSvc,
			},
			Ctx:    ctx,
			Logger: util.GetLogger(ctx),
		}

		opName, err := gService.ReleaseSubnetworkOp(region, projectId, subnetwork)
		if err != nil {
			tt.Errorf("Unexpected error: %v", err)
		}
		if opName != operationName {
			tt.Errorf("Expected operation name %s, got %s", operationName, opName)
		}
	})
	t.Run("WhenDeleteReturnsOtherError", func(tt *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			http.Error(rw, "internal error", http.StatusInternalServerError)
		}))
		defer server.Close()

		computeSvc, err := compute.NewService(
			ctx, option.WithHTTPClient(&http.Client{Timeout: time.Second}), option.WithEndpoint(server.URL))
		if err != nil {
			tt.Fatalf("Failed to create compute service: %v", err)
		}

		gService := &GcpServices{
			AdminGCPService: &AdminGCPService{
				computeService: computeSvc,
			},
			Ctx:    ctx,
			Logger: util.GetLogger(ctx),
		}

		opName, err := gService.ReleaseSubnetworkOp(region, projectId, subnetwork)
		if err == nil || !strings.Contains(err.Error(), "internal error") {
			tt.Errorf("Expected internal error, got: %v", err)
		}
		if opName != "" {
			tt.Errorf("Expected empty operation name on error, got: %s", opName)
		}
	})
	t.Run("WhenDeleteReturnsNotFoundError", func(tt *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			rw.WriteHeader(http.StatusNotFound)
			_, _ = rw.Write([]byte(`{"error": {"message": "notFound"}}`))
		}))
		defer server.Close()

		computeSvc, err := compute.NewService(ctx, option.WithHTTPClient(&http.Client{Timeout: time.Second}), option.WithEndpoint(server.URL))
		if err != nil {
			tt.Fatalf("Failed to create compute service: %v", err)
		}

		gService := &GcpServices{
			AdminGCPService: &AdminGCPService{
				computeService: computeSvc,
			},
			Ctx:    ctx,
			Logger: util.GetLogger(ctx),
		}
		opName, err := gService.ReleaseSubnetworkOp(region, projectId, subnetwork)
		if err != nil {
			tt.Errorf("Subnet not found means, the subnet doesn't exist or is deleted. Unexpected error, got: %v", err)
		}
		if opName != "" {
			tt.Errorf("Expected empty operation name when subnet not found, got: %s", opName)
		}
	})
	t.Run("WhenDeleteReturnsResourceInUseError", func(tt *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			rw.WriteHeader(http.StatusBadRequest)
			_, _ = rw.Write([]byte(`{"error": {"message": "resourceInUseByAnotherResource"}}`))
		}))
		defer server.Close()

		computeSvc, err := compute.NewService(ctx, option.WithHTTPClient(&http.Client{Timeout: time.Second}), option.WithEndpoint(server.URL))
		if err != nil {
			tt.Fatalf("Failed to create compute service: %v", err)
		}

		gService := &GcpServices{
			AdminGCPService: &AdminGCPService{
				computeService: computeSvc,
			},
			Ctx:    ctx,
			Logger: util.GetLogger(ctx),
		}
		opName, err := gService.ReleaseSubnetworkOp(region, projectId, subnetwork)
		if err != nil {
			tt.Errorf("Subnet in use by another resource should not return error, got: %v", err)
		}
		if opName != "" {
			tt.Errorf("Expected empty operation name when subnet is in use, got: %s", opName)
		}
	})
	t.Run("WhenAreNotSuccessfullyConnectedYetError", func(tt *testing.T) {
		ctx := context.Background()
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			rw.WriteHeader(http.StatusBadRequest)
			_, _ = rw.Write([]byte(`{"error": {"message": "are not successfully connected yet"}}`))
		}))
		defer server.Close()
		svc, err := servicenetworking.NewService(ctx, option.WithHTTPClient(&http.Client{Timeout: time.Second}), option.WithEndpoint(server.URL))
		if err != nil {
			tt.Fatalf("Error getting service up: '%s'", err.Error())
		}
		gService := &GcpServices{AdminGCPService: &AdminGCPService{networkingService: svc}, Ctx: ctx, Logger: util.GetLogger(ctx)}
		_, err = _createTPSubnetOp(gService, &servicenetworking.AddSubnetworkRequest{}, "test-project")
		if err == nil || !strings.Contains(err.(*temporal.ApplicationError).Error(), "Setup/Configure Private Service Access (PSA) Peering") {
			tt.Errorf("Expected Setup/Configure Private Service Access (PSA) Peering error, got: %v", err)
		}
	})
}

func Test_CreateAddress(t *testing.T) {
	projectName := "test-project"
	region := "us-central1"
	addressRequest := &models.Address{
		AddressName: "test-address",
		Region:      region,
		ProjectId:   projectName,
	}
	url := fmt.Sprintf("/projects/%s/regions/%s/addresses", projectName, region)
	t.Run("WhenCreateAddressFails", func(tt *testing.T) {
		defer testReset(tt)
		ctx := context.Background()
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			if req.Method == http.MethodPost && req.URL.Path == url {
				rw.WriteHeader(http.StatusInternalServerError)
				return
			}
			rw.WriteHeader(http.StatusBadRequest)
		}))
		defer server.Close()

		computeSvc, err := compute.NewService(
			ctx, option.WithHTTPClient(&http.Client{Timeout: time.Second}), option.WithEndpoint(server.URL))
		if err != nil {
			tt.Fatalf("Failed to create compute service: %v", err)
		}

		gService := &GcpServices{
			AdminGCPService: &AdminGCPService{
				computeService: computeSvc,
			},
			Ctx:    ctx,
			Logger: util.GetLogger(ctx),
			Retry:  NewExponentialRetryStrategy(time.Millisecond, 3),
		}

		op, err := createAddress(gService, addressRequest)
		if err == nil {
			tt.Error("Expected an error but got none")
		}
		if op != nil {
			tt.Errorf("Expected nil operation but got: %+v", op)
		}
		createAddress = _createAddress
	})

	t.Run("WhenCreateAddressSucceeds", func(tt *testing.T) {
		defer testReset(tt)
		ctx := context.Background()
		operationName := "test-operation"
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			if req.Method == http.MethodPost && req.URL.Path == url {
				response, _ := json.Marshal(&compute.Operation{Name: operationName})
				rw.WriteHeader(http.StatusOK)
				_, _ = rw.Write(response)
				return
			}
			rw.WriteHeader(http.StatusBadRequest)
		}))
		defer server.Close()

		computeSvc, err := compute.NewService(
			ctx, option.WithHTTPClient(&http.Client{Timeout: time.Second}), option.WithEndpoint(server.URL))
		if err != nil {
			tt.Fatalf("Failed to create compute service: %v", err)
		}

		gService := &GcpServices{
			AdminGCPService: &AdminGCPService{
				computeService: computeSvc,
			},
			Ctx:    ctx,
			Logger: util.GetLogger(ctx),
			Retry:  NewExponentialRetryStrategy(time.Millisecond, 3),
		}

		op, err := createAddress(gService, addressRequest)
		if err != nil {
			tt.Errorf("Unexpected error: %v", err)
		}
		if op == nil || op.Name != operationName {
			tt.Errorf("Unexpected operation: %+v", op)
		}
		createAddress = _createAddress
	})
}

func Test_GetAddress(t *testing.T) {
	t.Run("WhenGetAddressFails", func(tt *testing.T) {
		defer testReset(tt)
		ctx := context.Background()
		projectName := "1079058383248"
		addressName := "test-address"
		region := "US-East-4"
		url := "/projects/project/regions/region/addresses/r"
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			if req.URL.Path == url {
				rw.WriteHeader(http.StatusInternalServerError)
				return
			}
			rw.WriteHeader(http.StatusBadRequest)
		}))
		computeSvc, err := compute.NewService(
			ctx, option.WithHTTPClient(&http.Client{Timeout: time.Second}), option.WithEndpoint(server.URL))
		if err != nil {
			t.Errorf("Error getting service up: '%s'", err.Error())
		}

		gService := &GcpServices{
			AdminGCPService: &AdminGCPService{
				computeService: computeSvc,
			},
			Logger: util.GetLogger(ctx),
			Retry:  NewExponentialRetryStrategy(time.Millisecond, 3),
		}
		_, err = gService.GetAddress(projectName, region, addressName)
		if err == nil {
			tt.Error("Expected an error but got nothing")
		} else {
			if !strings.Contains(err.Error(), "googleapi: got HTTP response code 400 with body") {
				tt.Errorf("Unexpected error: %s", err.Error())
			}
		}
	})

	t.Run("WhenSuccess", func(tt *testing.T) {
		defer testReset(tt)
		ctx := context.Background()
		projectName := "1079058383248"
		addressName := "test-address"
		region := "US-East-4"
		url := fmt.Sprintf("/projects/%s/regions/%s/addresses/%s", projectName, region, addressName)
		resp := &compute.Address{Name: addressName}
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			if req.URL.Path == url {
				response, err := json.Marshal(resp)
				if err != nil {
					rw.WriteHeader(http.StatusBadRequest)
					return
				}
				_, _ = rw.Write(response)
				return
			}
			rw.WriteHeader(http.StatusBadRequest)
		}))
		computeSvc, err := compute.NewService(
			ctx, option.WithHTTPClient(&http.Client{Timeout: time.Second}), option.WithEndpoint(server.URL))
		if err != nil {
			t.Errorf("Error getting service up: '%s'", err.Error())
		}

		gService := &GcpServices{
			AdminGCPService: &AdminGCPService{
				computeService: computeSvc,
			},
			Ctx:    ctx,
			Logger: util.GetLogger(ctx),
			Retry:  NewExponentialRetryStrategy(time.Millisecond, 3),
		}
		out, err := gService.GetAddress(projectName, region, addressName)
		if err != nil {
			tt.Errorf("Unexpected error: %s", err.Error())
		} else {
			if out == nil {
				tt.Errorf("Output unexpectedly nil")
			} else {
				if out.AddressName != addressName {
					tt.Errorf("Unexpected subnetwork name %s", out.AddressName)
				}
			}
		}
	})
}

func Test_CreateAddressWithOperation(t *testing.T) {
	projectName := "test-project"
	address := &models.Address{
		AddressName: "test-address",
		ProjectId:   projectName,
	}
	ctx := context.Background()
	t.Run("WhenCreateAddressFails", func(tt *testing.T) {
		gService := &GcpServices{
			Ctx:    ctx,
			Logger: util.GetLogger(ctx),
		}
		origCreate := createAddress
		createAddress = func(gcpService *GcpServices, request *models.Address) (*models.ComputeOperation, error) {
			return nil, fmt.Errorf("create error")
		}
		defer func() { createAddress = origCreate }()
		_, err := gService.CreateAddressOperation(address)
		if err == nil || !strings.Contains(err.Error(), "create error") {
			tt.Errorf("Expected create error, got: %v", err.Error())
		}
	})

	t.Run("WhenSuccess", func(tt *testing.T) {
		gService := &GcpServices{
			Ctx:    ctx,
			Logger: util.GetLogger(ctx),
		}
		origCreate := createAddress
		createAddress = func(_ *GcpServices, _ *models.Address) (*models.ComputeOperation, error) {
			return &models.ComputeOperation{Name: "op-1"}, nil
		}
		defer func() { createAddress = origCreate }()

		_, err := gService.CreateAddressOperation(address)
		if err != nil {
			tt.Errorf("Unexpected error: %v", err)
		}
	})
}

func TestReleaseAddress(t *testing.T) {
	ctx := context.Background()
	region := "us-central1"
	snhost := "test-project"
	addressName := "test-address"
	deleteUrl := "/projects/test-project/regions/us-central1/addresses/test-address"

	operationName := "test-operation"
	t.Run("WhenDeleteSucceeds", func(tt *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			if req.Method == http.MethodDelete && req.URL.Path == deleteUrl {
				response, _ := json.Marshal(&compute.Operation{Name: operationName})
				rw.WriteHeader(http.StatusOK)
				_, _ = rw.Write(response)
			}
		}))
		defer server.Close()

		defer func() {}()
		computeSvc, err := compute.NewService(
			ctx, option.WithHTTPClient(&http.Client{Timeout: time.Second}), option.WithEndpoint(server.URL))
		if err != nil {
			tt.Fatalf("Failed to create compute service: %v", err)
		}

		gService := &GcpServices{
			AdminGCPService: &AdminGCPService{
				computeService: computeSvc,
			},
			Ctx:    ctx,
			Logger: util.GetLogger(ctx),
		}

		resp, err := gService.ReleaseAddress(region, snhost, addressName)
		if err != nil {
			tt.Errorf("Unexpected error: %v", err)
		}
		assert.Equal(tt, operationName, resp)
	})
	t.Run("WhenDeleteReturnsOtherError", func(tt *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			http.Error(rw, "internal error", http.StatusInternalServerError)
		}))
		defer server.Close()

		computeSvc, err := compute.NewService(
			ctx, option.WithHTTPClient(&http.Client{Timeout: time.Second}), option.WithEndpoint(server.URL))
		if err != nil {
			tt.Fatalf("Failed to create compute service: %v", err)
		}

		gService := &GcpServices{
			AdminGCPService: &AdminGCPService{
				computeService: computeSvc,
			},
			Ctx:    ctx,
			Logger: util.GetLogger(ctx),
		}

		resp, err := gService.ReleaseAddress(region, snhost, addressName)
		if err == nil || !strings.Contains(err.Error(), "internal error") {
			tt.Errorf("Expected internal error, got: %v", err)
		}
		assert.Equal(tt, "", resp)
	})
	t.Run("WhenDeleteReturnsNotFoundError", func(tt *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			rw.WriteHeader(http.StatusNotFound)
			_, _ = rw.Write([]byte(`{"error": {"message": "notFound"}}`))
		}))
		defer server.Close()

		computeSvc, err := compute.NewService(ctx, option.WithHTTPClient(&http.Client{Timeout: time.Second}), option.WithEndpoint(server.URL))
		if err != nil {
			tt.Fatalf("Failed to create compute service: %v", err)
		}

		gService := &GcpServices{
			AdminGCPService: &AdminGCPService{
				computeService: computeSvc,
			},
			Ctx:    ctx,
			Logger: util.GetLogger(ctx),
		}
		resp, err := gService.ReleaseAddress(region, snhost, addressName)
		if err == nil || (!strings.Contains(err.Error(), "notFound") && !strings.Contains(err.Error(), "not found")) {
			tt.Errorf("Expected notFound error, got: %v", err)
		}
		assert.Equal(tt, "", resp)
	})
}

func Test_CreateForwardingRule(t *testing.T) {
	projectName := "test-project"
	region := "us-central1"
	forwardingRuleRequest := &models.ForwardingRule{
		Name:      "test-forwarding-rule",
		Region:    region,
		ProjectId: projectName,
	}
	url := fmt.Sprintf("/projects/%s/regions/%s/forwardingRules", projectName, region)
	t.Run("WhenCreateForwardingRuleFails", func(tt *testing.T) {
		defer testReset(tt)
		ctx := context.Background()
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			if req.Method == http.MethodPost && req.URL.Path == url {
				rw.WriteHeader(http.StatusInternalServerError)
				return
			}
			rw.WriteHeader(http.StatusBadRequest)
		}))
		defer server.Close()

		computeSvc, err := compute.NewService(
			ctx, option.WithHTTPClient(&http.Client{Timeout: time.Second}), option.WithEndpoint(server.URL))
		if err != nil {
			tt.Fatalf("Failed to create compute service: %v", err)
		}

		gService := &GcpServices{
			AdminGCPService: &AdminGCPService{
				computeService: computeSvc,
			},
			Ctx:    ctx,
			Logger: util.GetLogger(ctx),
			Retry:  NewExponentialRetryStrategy(time.Millisecond, 3),
		}

		op, err := createForwardingRule(gService, forwardingRuleRequest)
		if err == nil {
			tt.Error("Expected an error but got none")
		}
		if op != nil {
			tt.Errorf("Expected nil operation but got: %+v", op)
		}
		createForwardingRule = _createForwardingRule
	})

	t.Run("WhenCreateForwardingRuleSucceeds", func(tt *testing.T) {
		defer testReset(tt)
		ctx := context.Background()
		operationName := "test-operation"
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			if req.Method == http.MethodPost && req.URL.Path == url {
				response, _ := json.Marshal(&compute.Operation{Name: operationName})
				rw.WriteHeader(http.StatusOK)
				_, _ = rw.Write(response)
				return
			}
			rw.WriteHeader(http.StatusBadRequest)
		}))
		defer server.Close()

		computeSvc, err := compute.NewService(
			ctx, option.WithHTTPClient(&http.Client{Timeout: time.Second}), option.WithEndpoint(server.URL))
		if err != nil {
			tt.Fatalf("Failed to create compute service: %v", err)
		}

		gService := &GcpServices{
			AdminGCPService: &AdminGCPService{
				computeService: computeSvc,
			},
			Ctx:    ctx,
			Logger: util.GetLogger(ctx),
			Retry:  NewExponentialRetryStrategy(time.Millisecond, 3),
		}

		op, err := createForwardingRule(gService, forwardingRuleRequest)
		if err != nil {
			tt.Errorf("Unexpected error: %v", err)
		}
		if op == nil || op.Name != operationName {
			tt.Errorf("Unexpected operation: %+v", op)
		}
		createForwardingRule = _createForwardingRule
	})
}

func Test_GetForwardingRule(t *testing.T) {
	t.Run("WhenGetForwardingRuleFails", func(tt *testing.T) {
		defer testReset(tt)
		ctx := context.Background()
		projectName := "1079058383248"
		forwardingRuleName := "test-forwarding-rule"
		region := "US-East-4"
		url := "/projects/project/regions/region/forwardingRules/r"
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			if req.URL.Path == url {
				rw.WriteHeader(http.StatusInternalServerError)
				return
			}
			rw.WriteHeader(http.StatusBadRequest)
		}))
		computeSvc, err := compute.NewService(
			ctx, option.WithHTTPClient(&http.Client{Timeout: time.Second}), option.WithEndpoint(server.URL))
		if err != nil {
			t.Errorf("Error getting service up: '%s'", err.Error())
		}

		gService := &GcpServices{
			AdminGCPService: &AdminGCPService{
				computeService: computeSvc,
			},
			Logger: util.GetLogger(ctx),
			Retry:  NewExponentialRetryStrategy(time.Millisecond, 3),
		}
		_, err = gService.GetForwardingRule(projectName, region, forwardingRuleName)
		if err == nil {
			tt.Error("Expected an error but got nothing")
		} else {
			if !strings.Contains(err.Error(), "googleapi: got HTTP response code 400 with body") {
				tt.Errorf("Unexpected error: %s", err.Error())
			}
		}
	})

	t.Run("WhenSuccess", func(tt *testing.T) {
		defer testReset(tt)
		ctx := context.Background()
		projectName := "1079058383248"
		forwardingRuleName := "test-forwarding-rule"
		region := "US-East-4"
		url := fmt.Sprintf("/projects/%s/regions/%s/forwardingRules/%s", projectName, region, forwardingRuleName)
		resp := &compute.ForwardingRule{Name: forwardingRuleName}
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			if req.URL.Path == url {
				response, err := json.Marshal(resp)
				if err != nil {
					rw.WriteHeader(http.StatusBadRequest)
					return
				}
				_, _ = rw.Write(response)
				return
			}
			rw.WriteHeader(http.StatusBadRequest)
		}))
		computeSvc, err := compute.NewService(
			ctx, option.WithHTTPClient(&http.Client{Timeout: time.Second}), option.WithEndpoint(server.URL))
		if err != nil {
			t.Errorf("Error getting service up: '%s'", err.Error())
		}

		gService := &GcpServices{
			AdminGCPService: &AdminGCPService{
				computeService: computeSvc,
			},
			Ctx:    ctx,
			Logger: util.GetLogger(ctx),
			Retry:  NewExponentialRetryStrategy(time.Millisecond, 3),
		}
		out, err := gService.GetForwardingRule(projectName, region, forwardingRuleName)
		if err != nil {
			tt.Errorf("Unexpected error: %s", err.Error())
		} else {
			if out == nil {
				tt.Errorf("Output unexpectedly nil")
			} else {
				if out.Name != forwardingRuleName {
					tt.Errorf("Unexpected subnetwork name %s", out.Name)
				}
			}
		}
	})
}

func Test_CreateForwardingRuleWithOperation(t *testing.T) {
	projectName := "test-project"
	forwardingRule := &models.ForwardingRule{
		Name:      "test-forwarding-rule",
		ProjectId: projectName,
		Region:    "test-region",
	}
	ctx := context.Background()
	t.Run("WhenCreateForwardingRuleFails", func(tt *testing.T) {
		gService := &GcpServices{
			Ctx:    ctx,
			Logger: util.GetLogger(ctx),
		}
		origCreate := createForwardingRule
		createForwardingRule = func(_ *GcpServices, _ *models.ForwardingRule) (*models.ComputeOperation, error) {
			return nil, fmt.Errorf("create error")
		}
		defer func() { createForwardingRule = origCreate }()
		_, err := gService.CreateForwardingRuleOperation(forwardingRule)
		if err == nil || !strings.Contains(err.Error(), "create error") {
			tt.Errorf("Expected create error, got: %v", err)
		}
	})

	t.Run("WhenSuccess", func(tt *testing.T) {
		gService := &GcpServices{
			Ctx:    ctx,
			Logger: util.GetLogger(ctx),
		}
		origCreate := createForwardingRule
		createForwardingRule = func(_ *GcpServices, _ *models.ForwardingRule) (*models.ComputeOperation, error) {
			return &models.ComputeOperation{Name: "op-1"}, nil
		}
		defer func() { createForwardingRule = origCreate }()

		_, err := gService.CreateForwardingRuleOperation(forwardingRule)
		if err != nil {
			tt.Errorf("Unexpected error: %v", err)
		}
	})
}

func TestDeleteForwardingRule(t *testing.T) {
	ctx := context.Background()
	region := "us-central1"
	snhost := "test-project"
	forwardingRuleName := "test-forwarding-rule"
	deleteUrl := "/projects/test-project/regions/us-central1/forwardingRules/test-forwarding-rule"

	operationName := "test-operation"
	t.Run("WhenDeleteSucceeds", func(tt *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			if req.Method == http.MethodDelete && req.URL.Path == deleteUrl {
				response, _ := json.Marshal(&compute.Operation{Name: operationName})
				rw.WriteHeader(http.StatusOK)
				_, _ = rw.Write(response)
			}
		}))
		defer server.Close()

		defer func() {}()
		computeSvc, err := compute.NewService(
			ctx, option.WithHTTPClient(&http.Client{Timeout: time.Second}), option.WithEndpoint(server.URL))
		if err != nil {
			tt.Fatalf("Failed to create compute service: %v", err)
		}

		gService := &GcpServices{
			AdminGCPService: &AdminGCPService{
				computeService: computeSvc,
			},
			Ctx:    ctx,
			Logger: util.GetLogger(ctx),
		}

		resp, err := gService.DeleteForwardingRule(region, snhost, forwardingRuleName)
		if err != nil {
			tt.Errorf("Unexpected error: %v", err)
		}
		assert.Equal(tt, operationName, resp)
	})
	t.Run("WhenDeleteReturnsOtherError", func(tt *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			http.Error(rw, "internal error", http.StatusInternalServerError)
		}))
		defer server.Close()

		computeSvc, err := compute.NewService(
			ctx, option.WithHTTPClient(&http.Client{Timeout: time.Second}), option.WithEndpoint(server.URL))
		if err != nil {
			tt.Fatalf("Failed to create compute service: %v", err)
		}

		gService := &GcpServices{
			AdminGCPService: &AdminGCPService{
				computeService: computeSvc,
			},
			Ctx:    ctx,
			Logger: util.GetLogger(ctx),
		}

		resp, err := gService.DeleteForwardingRule(region, snhost, forwardingRuleName)
		if err == nil || !strings.Contains(err.Error(), "internal error") {
			tt.Errorf("Expected internal error, got: %v", err)
		}
		assert.Equal(tt, "", resp)
	})
	t.Run("WhenDeleteReturnsNotFoundError", func(tt *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			rw.WriteHeader(http.StatusNotFound)
			_, _ = rw.Write([]byte(`{"error": {"message": "notFound"}}`))
		}))
		defer server.Close()

		computeSvc, err := compute.NewService(ctx, option.WithHTTPClient(&http.Client{Timeout: time.Second}), option.WithEndpoint(server.URL))
		if err != nil {
			tt.Fatalf("Failed to create compute service: %v", err)
		}

		gService := &GcpServices{
			AdminGCPService: &AdminGCPService{
				computeService: computeSvc,
			},
			Ctx:    ctx,
			Logger: util.GetLogger(ctx),
		}
		resp, err := gService.DeleteForwardingRule(region, snhost, forwardingRuleName)
		if err == nil || (!strings.Contains(err.Error(), "notFound") && !strings.Contains(err.Error(), "not found")) {
			tt.Errorf("Expected notFound error, got: %v", err)
		}
		assert.Equal(tt, "", resp)
	})
}

// Unit tests for ListSubnetworks
func Test_ListSubnetworks(t *testing.T) {
	projectName := "test-project"
	region := "us-central1"
	url := fmt.Sprintf("/projects/%s/regions/%s/subnetworks", projectName, region)

	t.Run("WhenListSubnetworksFails", func(tt *testing.T) {
		ctx := context.Background()
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			if req.Method == http.MethodGet && req.URL.Path == url {
				rw.WriteHeader(http.StatusInternalServerError)
				return
			}
		}))
		defer server.Close()

		computeSvc, err := compute.NewService(
			ctx, option.WithHTTPClient(&http.Client{Timeout: time.Second}), option.WithEndpoint(server.URL))
		if err != nil {
			tt.Fatalf("Failed to create compute service: %v", err)
		}

		gService := &GcpServices{
			AdminGCPService: &AdminGCPService{
				computeService: computeSvc,
			},
			Ctx:    ctx,
			Logger: util.GetLogger(ctx),
		}

		_, err = gService.ListSubnetworks(projectName, region)
		if err == nil {
			tt.Error("Expected an error but got none")
		}
	})

	t.Run("WhenListSubnetworksSucceeds", func(tt *testing.T) {
		ctx := context.Background()
		resp := &compute.Subnetwork{Name: "test-subnet"}
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			if req.Method == http.MethodGet && req.URL.Path == url {
				response, _ := json.Marshal(&compute.SubnetworkList{Items: []*compute.Subnetwork{resp}})
				rw.WriteHeader(http.StatusOK)
				_, _ = rw.Write(response)
				return
			}
		}))
		defer server.Close()

		computeSvc, err := compute.NewService(
			ctx, option.WithHTTPClient(&http.Client{Timeout: time.Second}), option.WithEndpoint(server.URL))
		if err != nil {
			tt.Fatalf("Failed to create compute service: %v", err)
		}

		gService := &GcpServices{
			AdminGCPService: &AdminGCPService{
				computeService: computeSvc,
			},
			Ctx:    ctx,
			Logger: util.GetLogger(ctx),
		}

		out, err := gService.ListSubnetworks(projectName, region)
		if err != nil {
			tt.Errorf("Unexpected error: %v", err)
		}
		if out == nil || len(*out) != 1 || (*out)[0].Name != "test-subnet" {
			tt.Errorf("Unexpected subnetwork list: %+v", out)
		}
	})
}

func Test_isServiceDisabledError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "NilError",
			err:  nil,
			want: false,
		},
		{
			name: "PlainErrorWithServiceDisabledText",
			err:  errors.New("rpc error: SERVICE_DISABLED for compute.googleapis.com"),
			want: true,
		},
		{
			name: "PlainErrorWithAccessNotConfiguredText",
			err:  errors.New("accessNotConfigured: API not enabled"),
			want: true,
		},
		{
			name: "PlainErrorUnrelated",
			err:  errors.New("connection reset by peer"),
			want: false,
		},
		{
			name: "GoogleapiErrorWithLegacyReasonServiceDisabled",
			err: &googleapi.Error{
				Code:   403,
				Errors: []googleapi.ErrorItem{{Reason: "SERVICE_DISABLED", Message: "API disabled"}},
			},
			want: true,
		},
		{
			name: "GoogleapiErrorWithLegacyReasonAccessNotConfigured",
			err: &googleapi.Error{
				Code:   403,
				Errors: []googleapi.ErrorItem{{Reason: "accessNotConfigured", Message: "API not enabled"}},
			},
			want: true,
		},
		{
			name: "GoogleapiErrorWithReasonInBody",
			err: &googleapi.Error{
				Code: 403,
				Body: `{"error":{"details":[{"reason":"SERVICE_DISABLED"}]}}`,
			},
			want: true,
		},
		{
			name: "GoogleapiError403Unrelated",
			err: &googleapi.Error{
				Code:    403,
				Message: "Permission denied",
			},
			want: false,
		},
		{
			name: "GoogleapiErrorNon403",
			err: &googleapi.Error{
				Code:    500,
				Message: "Internal error",
			},
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(tt *testing.T) {
			assert.Equal(tt, tc.want, isServiceDisabledError(tc.err))
		})
	}
}

func Test_GetSnHost(t *testing.T) {
	t.Run("WhenGetSnHostFails", func(tt *testing.T) {
		defer testReset(tt)
		ctx := context.Background()
		projectName := "1079058383248"
		url := fmt.Sprintf("/projects/%s/getXpnHost", projectName)
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			if req.URL.Path == url {
				rw.WriteHeader(http.StatusBadRequest)
				return
			}
		}))
		computeSvc, err := compute.NewService(
			ctx, option.WithHTTPClient(&http.Client{Timeout: time.Second}), option.WithEndpoint(server.URL))
		if err != nil {
			t.Errorf("Error getting service up: '%s'", err.Error())
		}

		gService := &GcpServices{
			AdminGCPService: &AdminGCPService{
				computeService: computeSvc,
			},
			Logger: util.GetLogger(ctx),
		}
		_, err = gService.GetSnHost(projectName)
		if err == nil {
			tt.Error("Expected an error but got nothing")
		} else {
			if !strings.Contains(err.Error(), "googleapi: got HTTP response code 400 with body") {
				tt.Errorf("Unexpected error: %s", err.Error())
			}
		}
	})
	t.Run("WhenGetSnHostNoPeering", func(tt *testing.T) {
		defer testReset(tt)
		ctx := context.Background()
		projectName := "1079058383248"
		url := fmt.Sprintf("/projects/%s/getXpnHost", projectName)
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			if req.URL.Path == url {
				rw.WriteHeader(http.StatusNotFound)
				_, _ = rw.Write([]byte(`{"error": {"message": "Please create Service Networking connection with service"}}`))
				return
			}
		}))
		computeSvc, err := compute.NewService(
			ctx, option.WithHTTPClient(&http.Client{Timeout: time.Second}), option.WithEndpoint(server.URL))
		if err != nil {
			t.Errorf("Error getting service up: '%s'", err.Error())
		}

		gService := &GcpServices{
			AdminGCPService: &AdminGCPService{
				computeService: computeSvc,
			},
			Logger: util.GetLogger(ctx),
		}
		_, err = gService.GetSnHost(projectName)
		if err == nil {
			tt.Error("Expected an error but got nothing")
		} else {
			if !strings.Contains(err.Error(), "Setup/Configure Private Service Access (PSA) Peering") {
				tt.Errorf("Unexpected error: %s", err.Error())
			}
		}
	})
	t.Run("WhenGetSnHostNotFound", func(tt *testing.T) {
		defer testReset(tt)
		ctx := context.Background()
		projectName := "1079058383248"
		url := fmt.Sprintf("/projects/%s/getXpnHost", projectName)
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			if req.URL.Path == url {
				rw.WriteHeader(http.StatusNotFound)
				_, _ = rw.Write([]byte(`{"error": {"message": "notFound"}}`))
				return
			}
		}))
		computeSvc, err := compute.NewService(
			ctx, option.WithHTTPClient(&http.Client{Timeout: time.Second}), option.WithEndpoint(server.URL))
		if err != nil {
			t.Errorf("Error getting service up: '%s'", err.Error())
		}

		gService := &GcpServices{
			AdminGCPService: &AdminGCPService{
				computeService: computeSvc,
			},
			Logger: util.GetLogger(ctx),
		}
		_, err = gService.GetSnHost(projectName)
		if err == nil {
			tt.Error("Expected an error but got nothing")
		} else {
			if !strings.Contains(strings.ToLower(err.Error()), "notfound") {
				tt.Errorf("Unexpected error: %s", err.Error())
			}
		}
	})

	t.Run("WhenSnHostEmpty", func(tt *testing.T) {
		defer testReset(tt)
		ctx := context.Background()
		projectName := "1079058383248"
		url := fmt.Sprintf("/projects/%s/getXpnHost", projectName)
		resp := &compute.Project{Name: ""}
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			if req.URL.Path == url {
				response, err := json.Marshal(resp)
				if err != nil {
					rw.WriteHeader(http.StatusBadRequest)
					return
				}
				_, _ = rw.Write(response)
				return
			}
			rw.WriteHeader(http.StatusBadRequest)
		}))
		computeSvc, err := compute.NewService(
			ctx, option.WithHTTPClient(&http.Client{Timeout: time.Second}), option.WithEndpoint(server.URL))
		if err != nil {
			t.Errorf("Error getting service up: '%s'", err.Error())
		}

		gService := &GcpServices{
			AdminGCPService: &AdminGCPService{
				computeService: computeSvc,
			},
			Ctx:    ctx,
			Logger: util.GetLogger(ctx),
		}
		_, err = gService.GetSnHost(projectName)
		if err != nil {
			tt.Error("Unexpected error")
		}
	})
	t.Run("WhenSuccess", func(tt *testing.T) {
		defer testReset(tt)
		ctx := context.Background()
		projectName := "1079058383248"
		url := fmt.Sprintf("/projects/%s/getXpnHost", projectName)
		resp := &compute.Project{Name: "sn-host-project"}
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			if req.URL.Path == url {
				response, err := json.Marshal(resp)
				if err != nil {
					rw.WriteHeader(http.StatusBadRequest)
					return
				}
				_, _ = rw.Write(response)
				return
			}
			rw.WriteHeader(http.StatusBadRequest)
		}))
		computeSvc, err := compute.NewService(
			ctx, option.WithHTTPClient(&http.Client{Timeout: time.Second}), option.WithEndpoint(server.URL))
		if err != nil {
			t.Errorf("Error getting service up: '%s'", err.Error())
		}

		gService := &GcpServices{
			AdminGCPService: &AdminGCPService{
				computeService: computeSvc,
			},
			Ctx:    ctx,
			Logger: util.GetLogger(ctx),
		}
		out, err := gService.GetSnHost(projectName)
		if err != nil {
			tt.Errorf("Unexpected error: %s", err.Error())
		} else {
			if out == "" {
				tt.Errorf("Output unexpectedly nil")
			}
		}
	})

	t.Run("WhenServerError500", func(tt *testing.T) {
		defer testReset(tt)
		ctx := context.Background()
		projectName := "1079058383248"
		url := fmt.Sprintf("/projects/%s/getXpnHost", projectName)
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			if req.URL.Path == url {
				rw.WriteHeader(http.StatusInternalServerError)
				_, _ = rw.Write([]byte(`{"error": {"message": "Internal server error"}}`))
				return
			}
		}))
		computeSvc, err := compute.NewService(
			ctx, option.WithHTTPClient(&http.Client{Timeout: time.Second}), option.WithEndpoint(server.URL))
		if err != nil {
			t.Errorf("Error getting service up: '%s'", err.Error())
		}

		gService := &GcpServices{
			AdminGCPService: &AdminGCPService{
				computeService: computeSvc,
			},
			Logger: util.GetLogger(ctx),
		}
		_, err = gService.GetSnHost(projectName)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "Internal server error")
	})

	t.Run("WhenServerError503", func(tt *testing.T) {
		defer testReset(tt)
		ctx := context.Background()
		projectName := "1079058383248"
		url := fmt.Sprintf("/projects/%s/getXpnHost", projectName)
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			if req.URL.Path == url {
				rw.WriteHeader(http.StatusServiceUnavailable)
				_, _ = rw.Write([]byte(`{"error": {"message": "Service unavailable"}}`))
				return
			}
		}))
		computeSvc, err := compute.NewService(
			ctx, option.WithHTTPClient(&http.Client{Timeout: time.Second}), option.WithEndpoint(server.URL))
		if err != nil {
			t.Errorf("Error getting service up: '%s'", err.Error())
		}

		gService := &GcpServices{
			AdminGCPService: &AdminGCPService{
				computeService: computeSvc,
			},
			Logger: util.GetLogger(ctx),
		}
		_, err = gService.GetSnHost(projectName)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "Service unavailable")
	})

	t.Run("WhenForbiddenError403", func(tt *testing.T) {
		defer testReset(tt)
		ctx := context.Background()
		projectName := "1079058383248"
		url := fmt.Sprintf("/projects/%s/getXpnHost", projectName)
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			if req.URL.Path == url {
				rw.WriteHeader(http.StatusForbidden)
				_, _ = rw.Write([]byte(`{"error": {"message": "Permission denied"}}`))
				return
			}
		}))
		computeSvc, err := compute.NewService(
			ctx, option.WithHTTPClient(&http.Client{Timeout: time.Second}), option.WithEndpoint(server.URL))
		if err != nil {
			t.Errorf("Error getting service up: '%s'", err.Error())
		}

		gService := &GcpServices{
			AdminGCPService: &AdminGCPService{
				computeService: computeSvc,
			},
			Logger: util.GetLogger(ctx),
		}
		_, err = gService.GetSnHost(projectName)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "Permission denied")
	})

	t.Run("WhenComputeEngineAPIDisabledOnTenantProject", func(tt *testing.T) {
		defer testReset(tt)
		ctx := context.Background()
		tenantProjectNumber := "771375917265"
		url := fmt.Sprintf("/projects/%s/getXpnHost", tenantProjectNumber)
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			if req.URL.Path == url {
				rw.WriteHeader(http.StatusForbidden)
				_, _ = rw.Write([]byte(fmt.Sprintf(`{
					"error": {
						"code": 403,
						"message": "Compute Engine API has not been used in project %s before or it is disabled.",
						"details": [
							{"reason": "SERVICE_DISABLED"}
						]
					}
				}`, tenantProjectNumber)))
				return
			}
		}))
		defer server.Close()
		computeSvc, err := compute.NewService(
			ctx, option.WithHTTPClient(&http.Client{Timeout: time.Second}), option.WithEndpoint(server.URL))
		if err != nil {
			t.Errorf("Error getting service up: '%s'", err.Error())
		}

		gService := &GcpServices{
			AdminGCPService: &AdminGCPService{
				computeService: computeSvc,
			},
			Logger: util.GetLogger(ctx),
		}
		_, err = gService.GetSnHost(tenantProjectNumber)
		assert.Error(tt, err)
		// Verify it's a non-retryable temporal application error, not a generic Internal Server Error
		var appErr *temporal.ApplicationError
		assert.True(tt, errors.As(err, &appErr))
		assert.True(tt, appErr.NonRetryable())
		var trackingID int
		_ = appErr.Details(&trackingID)
		assert.Equal(tt, vsaerrors.ErrGCPServiceDisabledError, trackingID)
		assert.Contains(tt, err.Error(), "Enable the API on the tenant project and retry")
	})

	t.Run("WhenTooManyRequests429", func(tt *testing.T) {
		defer testReset(tt)
		ctx := context.Background()
		projectName := "1079058383248"
		url := fmt.Sprintf("/projects/%s/getXpnHost", projectName)
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			if req.URL.Path == url {
				rw.WriteHeader(http.StatusTooManyRequests)
				_, _ = rw.Write([]byte(`{"error": {"message": "Rate limit exceeded"}}`))
				return
			}
		}))
		computeSvc, err := compute.NewService(
			ctx, option.WithHTTPClient(&http.Client{Timeout: time.Second}), option.WithEndpoint(server.URL))
		if err != nil {
			t.Errorf("Error getting service up: '%s'", err.Error())
		}

		gService := &GcpServices{
			AdminGCPService: &AdminGCPService{
				computeService: computeSvc,
			},
			Logger: util.GetLogger(ctx),
		}
		_, err = gService.GetSnHost(projectName)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "Rate limit exceeded")
	})

	t.Run("WhenMalformedJSONResponse", func(tt *testing.T) {
		defer testReset(tt)
		ctx := context.Background()
		projectName := "1079058383248"
		url := fmt.Sprintf("/projects/%s/getXpnHost", projectName)
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			if req.URL.Path == url {
				rw.WriteHeader(http.StatusOK)
				_, _ = rw.Write([]byte(`{"invalid": json}`))
				return
			}
		}))
		computeSvc, err := compute.NewService(
			ctx, option.WithHTTPClient(&http.Client{Timeout: time.Second}), option.WithEndpoint(server.URL))
		if err != nil {
			t.Errorf("Error getting service up: '%s'", err.Error())
		}

		gService := &GcpServices{
			AdminGCPService: &AdminGCPService{
				computeService: computeSvc,
			},
			Logger: util.GetLogger(ctx),
		}
		_, err = gService.GetSnHost(projectName)
		assert.Error(tt, err)
	})

	t.Run("WhenEmptyProjectName", func(tt *testing.T) {
		defer testReset(tt)
		ctx := context.Background()
		projectName := ""
		url := fmt.Sprintf("/projects/%s/getXpnHost", projectName)
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			if req.URL.Path == url {
				rw.WriteHeader(http.StatusBadRequest)
				_, _ = rw.Write([]byte(`{"error": {"message": "Invalid project"}}`))
				return
			}
		}))
		computeSvc, err := compute.NewService(
			ctx, option.WithHTTPClient(&http.Client{Timeout: time.Second}), option.WithEndpoint(server.URL))
		if err != nil {
			t.Errorf("Error getting service up: '%s'", err.Error())
		}

		gService := &GcpServices{
			AdminGCPService: &AdminGCPService{
				computeService: computeSvc,
			},
			Logger: util.GetLogger(ctx),
		}
		_, err = gService.GetSnHost(projectName)
		assert.Error(tt, err)
	})

	t.Run("WhenGenericErrorNotPeering", func(tt *testing.T) {
		defer testReset(tt)
		ctx := context.Background()
		projectName := "1079058383248"
		url := fmt.Sprintf("/projects/%s/getXpnHost", projectName)
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			if req.URL.Path == url {
				rw.WriteHeader(http.StatusBadRequest)
				_, _ = rw.Write([]byte(`{"error": {"message": "Some other error message"}}`))
				return
			}
		}))
		computeSvc, err := compute.NewService(
			ctx, option.WithHTTPClient(&http.Client{Timeout: time.Second}), option.WithEndpoint(server.URL))
		if err != nil {
			t.Errorf("Error getting service up: '%s'", err.Error())
		}

		gService := &GcpServices{
			AdminGCPService: &AdminGCPService{
				computeService: computeSvc,
			},
			Logger: util.GetLogger(ctx),
		}
		_, err = gService.GetSnHost(projectName)
		assert.Error(tt, err)
		// Should not be wrapped as non-retryable error since it doesn't contain peering message
		assert.NotContains(tt, err.Error(), "PSAPeeringNotFoundError")
	})

	t.Run("WhenPeeringErrorWithExactCase", func(tt *testing.T) {
		defer testReset(tt)
		ctx := context.Background()
		projectName := "1079058383248"
		url := fmt.Sprintf("/projects/%s/getXpnHost", projectName)
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			if req.URL.Path == url {
				rw.WriteHeader(http.StatusNotFound)
				_, _ = rw.Write([]byte(`{"error": {"message": "Please create Service Networking connection with service"}}`))
				return
			}
		}))
		computeSvc, err := compute.NewService(
			ctx, option.WithHTTPClient(&http.Client{Timeout: time.Second}), option.WithEndpoint(server.URL))
		if err != nil {
			t.Errorf("Error getting service up: '%s'", err.Error())
		}

		gService := &GcpServices{
			AdminGCPService: &AdminGCPService{
				computeService: computeSvc,
			},
			Logger: util.GetLogger(ctx),
		}
		_, err = gService.GetSnHost(projectName)
		assert.Error(tt, err)
		// Should be wrapped as non-retryable error with PSA peering message
		assert.Contains(tt, err.Error(), "Setup/Configure Private Service Access (PSA) Peering")
	})

	t.Run("WhenSuccessWithLongProjectName", func(tt *testing.T) {
		defer testReset(tt)
		ctx := context.Background()
		projectName := "very-long-project-name-12345678901234567890"
		url := fmt.Sprintf("/projects/%s/getXpnHost", projectName)
		resp := &compute.Project{Name: "sn-host-project-long-name"}
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			if req.URL.Path == url {
				response, err := json.Marshal(resp)
				if err != nil {
					rw.WriteHeader(http.StatusBadRequest)
					return
				}
				_, _ = rw.Write(response)
				return
			}
			rw.WriteHeader(http.StatusBadRequest)
		}))
		computeSvc, err := compute.NewService(
			ctx, option.WithHTTPClient(&http.Client{Timeout: time.Second}), option.WithEndpoint(server.URL))
		if err != nil {
			t.Errorf("Error getting service up: '%s'", err.Error())
		}

		gService := &GcpServices{
			AdminGCPService: &AdminGCPService{
				computeService: computeSvc,
			},
			Ctx:    ctx,
			Logger: util.GetLogger(ctx),
		}
		out, err := gService.GetSnHost(projectName)
		assert.NoError(tt, err)
		assert.Equal(tt, "sn-host-project-long-name", out)
	})

	t.Run("WhenSuccessWithSpecialCharactersInProjectName", func(tt *testing.T) {
		defer testReset(tt)
		ctx := context.Background()
		projectName := "project-123-test"
		url := fmt.Sprintf("/projects/%s/getXpnHost", projectName)
		resp := &compute.Project{Name: "sn-host-project-456"}
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			if req.URL.Path == url {
				response, err := json.Marshal(resp)
				if err != nil {
					rw.WriteHeader(http.StatusBadRequest)
					return
				}
				_, _ = rw.Write(response)
				return
			}
			rw.WriteHeader(http.StatusBadRequest)
		}))
		computeSvc, err := compute.NewService(
			ctx, option.WithHTTPClient(&http.Client{Timeout: time.Second}), option.WithEndpoint(server.URL))
		if err != nil {
			t.Errorf("Error getting service up: '%s'", err.Error())
		}

		gService := &GcpServices{
			AdminGCPService: &AdminGCPService{
				computeService: computeSvc,
			},
			Ctx:    ctx,
			Logger: util.GetLogger(ctx),
		}
		out, err := gService.GetSnHost(projectName)
		assert.NoError(tt, err)
		assert.Equal(tt, "sn-host-project-456", out)
	})
}

func Test_newClient(t *testing.T) {
	origNewClientScopes := newClientScopes
	defer func() { newClientScopes = origNewClientScopes }()

	ctx := context.Background()

	t.Run("returns client and endpoint", func(t *testing.T) {
		expectedClient := &http.Client{}
		expectedEndpoint := "test-endpoint"
		newClientScopes = func(ctx context.Context, opts ...option.ClientOption) (*http.Client, string, error) {
			return expectedClient, expectedEndpoint, nil
		}
		client, endpoint, err := _newClient(ctx)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if client != expectedClient {
			t.Errorf("expected client %v, got %v", expectedClient, client)
		}
		if endpoint != expectedEndpoint {
			t.Errorf("expected endpoint %v, got %v", expectedEndpoint, endpoint)
		}
	})

	t.Run("returns error", func(t *testing.T) {
		newClientScopes = func(ctx context.Context, opts ...option.ClientOption) (*http.Client, string, error) {
			return nil, "", fmt.Errorf("Error getting service up")
		}
		client, endpoint, err := _newClient(ctx)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if client != nil {
			t.Errorf("expected nil client, got %v", client)
		}
		if endpoint != "" {
			t.Errorf("expected empty endpoint, got %v", endpoint)
		}
	})
}

// Unit tests for GetContext
func TestGcpServices_GetContext(t *testing.T) {
	t.Run("ReturnsExistingContext", func(t *testing.T) {
		ctx := context.Background()
		gcpService := &GcpServices{
			Ctx: ctx,
		}
		got := gcpService.GetContext()
		assert.Equal(t, ctx, got)
	})

	t.Run("InitializesContextIfNil", func(t *testing.T) {
		gcpService := &GcpServices{}
		got := gcpService.GetContext()
		assert.NotNil(t, got)
		assert.Equal(t, gcpService.Ctx, got)
	})
}

func Test_updateFirewall(t *testing.T) {
	projectName := "1079058383248"
	vpcName := "vpc1"
	firewallName := "ingress-" + vpcName
	url := "/projects/1079058383248/global/firewalls/" + firewallName
	allowedPortRules := []string{"tcp", "icmp", "udp"}
	sourceRanges := []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"}
	firewallRule := &models.Firewall{
		Name:             firewallName,
		Description:      "Allow incoming traffic on specific ports",
		VPCNetworkName:   fmt.Sprintf("projects/%s/global/networks/%s", projectName, vpcName),
		AllowedPortRules: allowedPortRules,
		ProjectName:      projectName,
		SourceRanges:     sourceRanges,
		Direction:        "INGRESS",
		Priority:         1000,
	}

	t.Run("WhenUpdateFails", func(tt *testing.T) {
		defer testReset(tt)
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			if req.URL.Path == url {
				rw.WriteHeader(http.StatusBadRequest)
				return
			}
		}))
		ctx := context.Background()
		computeSvc, err := compute.NewService(
			ctx, option.WithHTTPClient(&http.Client{Timeout: time.Second}), option.WithEndpoint(server.URL))
		if err != nil {
			t.Errorf("Error getting service up: '%s'", err.Error())
		}

		gService := &GcpServices{
			AdminGCPService: &AdminGCPService{
				computeService: computeSvc,
			},
			Ctx:    ctx,
			Logger: util.GetLogger(ctx),
		}
		_, err = gService.UpdateFirewall(firewallRule)
		if err == nil {
			tt.Error("Expected an error but got nothing")
		} else {
			if !strings.Contains(err.(*vsaerrors.CustomError).OriginalErr.Error(), "googleapi: got HTTP response code 400 with body") {
				tt.Errorf("Unexpected error: %s", err.(*vsaerrors.CustomError).OriginalErr.Error())
			}
		}
	})
	t.Run("WhenSuccess", func(tt *testing.T) {
		defer testReset(tt)
		operationName := "random-operation-name"
		resp := &compute.Operation{Name: operationName}
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			if req.URL.Path == url {
				response, err := json.Marshal(resp)
				if err != nil {
					rw.WriteHeader(http.StatusBadRequest)
					return
				}
				_, _ = rw.Write(response)
				return
			}
			rw.WriteHeader(http.StatusBadRequest)
		}))
		ctx := context.Background()
		computeSvc, err := compute.NewService(
			ctx, option.WithHTTPClient(&http.Client{Timeout: time.Second}), option.WithEndpoint(server.URL))
		if err != nil {
			t.Errorf("Error getting service up: '%s'", err.Error())
		}

		gService := &GcpServices{
			AdminGCPService: &AdminGCPService{
				computeService: computeSvc,
			},
			Ctx:    ctx,
			Logger: util.GetLogger(ctx),
		}
		out, err := gService.UpdateFirewall(firewallRule)
		if err != nil {
			tt.Errorf("Unexpected error: %s", err.Error())
		} else if out != operationName {
			tt.Errorf("Unexpected subnetwork name %s", out)
		}
	})
}

func Test_GetServiceNetOpStatus(t *testing.T) {
	defer testReset(t)

	t.Run("Success", func(tt *testing.T) {
		operationName := "operations/operation-1234567890123456789"
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			if strings.Contains(req.URL.Path, "operations/operation-1234567890123456789") {
				response := &servicenetworking.Operation{
					Name: operationName,
					Done: true,
				}
				responseJson, _ := json.Marshal(response)
				rw.WriteHeader(http.StatusOK)
				_, _ = rw.Write(responseJson)
				return
			}
			rw.WriteHeader(http.StatusNotFound)
		}))
		defer server.Close()

		serviceNetworkingEndpoint = server.URL
		ctx := context.Background()

		networkingService, err := servicenetworking.NewService(ctx, option.WithEndpoint(server.URL), option.WithoutAuthentication())
		if err != nil {
			tt.Fatalf("Failed to create networking service: %v", err)
		}

		gcpService := &GcpServices{
			AdminGCPService: &AdminGCPService{
				networkingService: networkingService,
			},
			Ctx:    ctx,
			Logger: util.GetLogger(ctx),
		}

		result, err := gcpService.GetServiceNetOpStatus(operationName)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, operationName, result.Name)
		assert.True(tt, result.Done)
	})

	t.Run("Error_OperationError", func(tt *testing.T) {
		operationName := "operations/operation-error"
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			if strings.Contains(req.URL.Path, "operations/operation-error") {
				response := &servicenetworking.Operation{
					Name: operationName,
					Done: true,
					Error: &servicenetworking.Status{
						Message: "Operation failed for " + testProviderResourcePath + "\nforged_field=true",
					},
				}
				responseJson, _ := json.Marshal(response)
				rw.WriteHeader(http.StatusOK)
				_, _ = rw.Write(responseJson)
				return
			}
			rw.WriteHeader(http.StatusNotFound)
		}))
		defer server.Close()

		serviceNetworkingEndpoint = server.URL
		ctx := context.Background()

		networkingService, err := servicenetworking.NewService(ctx, option.WithEndpoint(server.URL), option.WithoutAuthentication())
		if err != nil {
			tt.Fatalf("Failed to create networking service: %v", err)
		}

		gcpService := &GcpServices{
			AdminGCPService: &AdminGCPService{
				networkingService: networkingService,
			},
			Ctx:    ctx,
			Logger: util.GetLogger(ctx),
		}

		result, err := gcpService.GetServiceNetOpStatus(operationName)

		assert.Error(tt, err)
		assert.Nil(tt, result)
		assertTerminalGCPOperationError(tt, err, testProviderResourcePath)
	})

	t.Run("Error_HTTPError", func(tt *testing.T) {
		operationName := "operations/operation-notfound"
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			rw.WriteHeader(http.StatusNotFound)
		}))
		defer server.Close()

		serviceNetworkingEndpoint = server.URL
		ctx := context.Background()

		networkingService, err := servicenetworking.NewService(ctx, option.WithEndpoint(server.URL), option.WithoutAuthentication())
		if err != nil {
			tt.Fatalf("Failed to create networking service: %v", err)
		}

		gcpService := &GcpServices{
			AdminGCPService: &AdminGCPService{
				networkingService: networkingService,
			},
			Ctx:    ctx,
			Logger: util.GetLogger(ctx),
		}

		result, err := gcpService.GetServiceNetOpStatus(operationName)

		assert.Error(tt, err)
		assert.Nil(tt, result)
	})
	t.Run("Error_IPExhaustion", func(tt *testing.T) {
		operationName := "operations/operation-ip-exhaustion"
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			if strings.Contains(req.URL.Path, "operations/operation-ip-exhaustion") {
				response := &servicenetworking.Operation{
					Name: operationName,
					Done: true,
					Error: &servicenetworking.Status{
						Message: ipExhaustionErrorMsg + " for " + testProviderResourcePath,
					},
				}
				responseJson, _ := json.Marshal(response)
				rw.WriteHeader(http.StatusOK)
				_, _ = rw.Write(responseJson)
				return
			}
			rw.WriteHeader(http.StatusNotFound)
		}))
		defer server.Close()

		serviceNetworkingEndpoint = server.URL
		ctx := context.Background()

		networkingService, err := servicenetworking.NewService(ctx, option.WithEndpoint(server.URL), option.WithoutAuthentication())
		if err != nil {
			tt.Fatalf("Failed to create networking service: %v", err)
		}

		gcpService := &GcpServices{
			AdminGCPService: &AdminGCPService{
				networkingService: networkingService,
			},
			Ctx:    ctx,
			Logger: util.GetLogger(ctx),
		}

		result, err := gcpService.GetServiceNetOpStatus(operationName)

		assert.Error(tt, err)
		assert.Nil(tt, result)
		// Verify it's a non-retryable temporal application error
		var appErr *temporal.ApplicationError
		assert.True(tt, errors.As(err, &appErr))
		assert.True(tt, appErr.NonRetryable())
		assert.Contains(tt, err.Error(), "Couldn't find free blocks in allocated IP ranges")
		assertProviderDetailsRedacted(tt, err, testProviderResourcePath)
	})
	t.Run("Error_ConsumerNetworkNotFound", func(tt *testing.T) {
		operationName := "operations/operation-network-not-found"
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			if strings.Contains(req.URL.Path, "operations/operation-network-not-found") {
				response := &servicenetworking.Operation{
					Name: operationName,
					Done: true,
					Error: &servicenetworking.Status{
						Message: "Consumer network " + testProviderResourcePath + " does not exist.\nforged_field=true",
					},
				}
				responseJson, _ := json.Marshal(response)
				rw.WriteHeader(http.StatusOK)
				_, _ = rw.Write(responseJson)
				return
			}
			rw.WriteHeader(http.StatusNotFound)
		}))
		defer server.Close()

		serviceNetworkingEndpoint = server.URL
		ctx := context.Background()

		networkingService, err := servicenetworking.NewService(ctx, option.WithEndpoint(server.URL), option.WithoutAuthentication())
		if err != nil {
			tt.Fatalf("Failed to create networking service: %v", err)
		}

		gcpService := &GcpServices{
			AdminGCPService: &AdminGCPService{
				networkingService: networkingService,
			},
			Ctx:    ctx,
			Logger: util.GetLogger(ctx),
		}

		result, err := gcpService.GetServiceNetOpStatus(operationName)

		assert.Error(tt, err)
		assert.Nil(tt, result)
		// Verify it's a non-retryable temporal application error
		var appErr *temporal.ApplicationError
		assert.True(tt, errors.As(err, &appErr))
		assert.True(tt, appErr.NonRetryable())
		// Verify the tracking ID is ErrGCPConsumerNetworkNotFound (3010)
		var trackingID int
		_ = appErr.Details(&trackingID)
		assert.Equal(tt, vsaerrors.ErrGCPConsumerNetworkNotFound, trackingID)
		// Verify customer-facing message indicates network not found (not a generic 500)
		assert.Contains(tt, err.Error(), "The specified network does not exist")
		assertProviderDetailsRedacted(tt, err, testProviderResourcePath)
	})
}

func Test_GetZones(t *testing.T) {
	projectNumber := "123456789"
	projectID := "test-project"
	region := "us-central1"
	regionUrl := "https://www.googleapis.com/compute/v1/projects/" + projectID + "/regions/" + region
	zones := []string{"us-central1-a", "us-central1-b"}

	t.Run("WhenGetZonesSucceeds", func(tt *testing.T) {
		ctx := context.Background()
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			if strings.Contains(req.URL.Path, "/zones") {
				resp := map[string]interface{}{
					"items": []map[string]interface{}{
						{"name": zones[0], "region": regionUrl},
						{"name": zones[1], "region": regionUrl},
					},
				}
				_ = json.NewEncoder(rw).Encode(resp)
			}
		}))
		defer server.Close()

		computeSvc, err := compute.NewService(
			ctx, option.WithHTTPClient(&http.Client{Timeout: time.Second}), option.WithEndpoint(server.URL))
		if err != nil {
			t.Errorf("Error getting service up: '%s'", err.Error())
		}

		gService := &GcpServices{
			AdminGCPService: &AdminGCPService{
				computeService: computeSvc,
			},
			Ctx:    ctx,
			Logger: util.GetLogger(ctx),
		}

		// Mock getProjectIDFromNumber
		getProjectIDFromNumber = func(_ *GcpServices, _ string) (string, error) {
			return projectID, nil
		}
		defer func() { getProjectIDFromNumber = _getProjectIDFromNumber }()

		out, err := gService.GetZones(projectNumber, region)
		if err != nil {
			tt.Errorf("Unexpected error: %v", err)
		}
		if len(out) != 2 || out[0] != zones[0] || out[1] != zones[1] {
			tt.Errorf("Unexpected zones: %+v", out)
		}
	})

	t.Run("WhenGetZonesFails", func(tt *testing.T) {
		ctx := context.Background()
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			http.Error(rw, "internal error", http.StatusInternalServerError)
		}))
		defer server.Close()

		computeSvc, err := compute.NewService(
			ctx, option.WithHTTPClient(&http.Client{Timeout: time.Second}), option.WithEndpoint(server.URL))
		if err != nil {
			t.Errorf("Error getting service up: '%s'", err.Error())
		}

		gService := &GcpServices{
			AdminGCPService: &AdminGCPService{
				computeService: computeSvc,
			},
			Ctx:    ctx,
			Logger: util.GetLogger(ctx),
			Retry:  NewExponentialRetryStrategy(time.Millisecond, 1),
		}

		// Mock getProjectIDFromNumber
		getProjectIDFromNumber = func(_ *GcpServices, _ string) (string, error) {
			return projectID, nil
		}
		defer func() { getProjectIDFromNumber = _getProjectIDFromNumber }()

		_, err = gService.GetZones(projectNumber, region)
		if err == nil {
			tt.Error("Expected error but got none")
		}
	})
}

func Test_getProjectIDFromNumber(t *testing.T) {
	ctx := context.Background()
	projectNumber := "123456789"
	projectName := "test-project"

	t.Run("WhenGetProjectSucceeds", func(tt *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			if strings.Contains(req.URL.Path, "/projects/"+projectNumber) {
				resp := &compute.Project{Name: projectName}
				_ = json.NewEncoder(rw).Encode(resp)
			} else {
				http.Error(rw, "not found", http.StatusNotFound)
			}
		}))
		defer server.Close()

		computeSvc, err := compute.NewService(
			ctx, option.WithHTTPClient(&http.Client{Timeout: time.Second}), option.WithEndpoint(server.URL))
		if err != nil {
			t.Errorf("Error getting service up: '%s'", err.Error())
		}

		gService := &GcpServices{
			AdminGCPService: &AdminGCPService{computeService: computeSvc},
			Ctx:             ctx,
		}

		name, err := _getProjectIDFromNumber(gService, projectNumber)
		if err != nil {
			tt.Fatalf("unexpected error: %v", err)
		}
		if name != projectName {
			tt.Errorf("expected %s, got %s", projectName, name)
		}
	})

	t.Run("WhenGetProjectFails", func(tt *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			http.Error(rw, "internal error", http.StatusInternalServerError)
		}))
		defer server.Close()

		computeSvc, err := compute.NewService(
			ctx, option.WithHTTPClient(&http.Client{Timeout: time.Second}), option.WithEndpoint(server.URL))
		if err != nil {
			t.Errorf("Error getting service up: '%s'", err.Error())
		}

		gService := &GcpServices{
			AdminGCPService: &AdminGCPService{computeService: computeSvc},
			Ctx:             ctx,
		}

		_, err = _getProjectIDFromNumber(gService, projectNumber)
		if err == nil {
			tt.Error("expected error, got nil")
		}
	})
}

func Test_IsMachineTypeAvailable(t *testing.T) {
	ctx := context.Background()
	projectNumber := "123456789"
	projectName := "test-project"
	zone := "us-central1-a"
	machineType := "n2-standard-4"

	t.Run("WhenMachineTypeIsAvailable", func(tt *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			if strings.Contains(req.URL.Path, "/projects/"+projectName+"/zones/"+zone+"/machineTypes/"+machineType) {
				// Return a successful response indicating machine type exists
				resp := &compute.MachineType{Name: machineType, Zone: zone}
				_ = json.NewEncoder(rw).Encode(resp)
			} else if strings.Contains(req.URL.Path, "/projects/"+projectNumber) {
				// Mock project lookup
				resp := &compute.Project{Name: projectName}
				_ = json.NewEncoder(rw).Encode(resp)
			} else {
				http.Error(rw, "not found", http.StatusNotFound)
			}
		}))
		defer server.Close()

		computeSvc, err := compute.NewService(
			ctx, option.WithHTTPClient(&http.Client{Timeout: time.Second}), option.WithEndpoint(server.URL))
		if err != nil {
			t.Errorf("Error getting service up: '%s'", err.Error())
		}

		gService := &GcpServices{
			AdminGCPService: &AdminGCPService{computeService: computeSvc},
			Ctx:             ctx,
			Logger:          util.GetLogger(ctx),
			Retry:           NewExponentialRetryStrategy(time.Millisecond, 1),
		}

		available, err := gService.IsMachineTypeAvailable(projectNumber, zone, machineType)
		if err != nil {
			tt.Fatalf("unexpected error: %v", err)
		}
		if !available {
			tt.Error("expected machine type to be available")
		}
	})

	t.Run("WhenMachineTypeIsNotAvailable", func(tt *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			if strings.Contains(req.URL.Path, "/projects/"+projectName+"/zones/"+zone+"/machineTypes/"+machineType) {
				// Return 404 indicating machine type doesn't exist
				http.Error(rw, "not found", http.StatusNotFound)
			} else if strings.Contains(req.URL.Path, "/projects/"+projectNumber) {
				// Mock project lookup
				resp := &compute.Project{Name: projectName}
				_ = json.NewEncoder(rw).Encode(resp)
			} else {
				http.Error(rw, "not found", http.StatusNotFound)
			}
		}))
		defer server.Close()

		computeSvc, err := compute.NewService(
			ctx, option.WithHTTPClient(&http.Client{Timeout: time.Second}), option.WithEndpoint(server.URL))
		if err != nil {
			t.Errorf("Error getting service up: '%s'", err.Error())
		}

		gService := &GcpServices{
			AdminGCPService: &AdminGCPService{computeService: computeSvc},
			Ctx:             ctx,
			Logger:          util.GetLogger(ctx),
			Retry:           NewExponentialRetryStrategy(time.Millisecond, 1),
		}

		available, err := gService.IsMachineTypeAvailable(projectNumber, zone, machineType)
		if err != nil {
			tt.Fatalf("unexpected error: %v", err)
		}
		if available {
			tt.Error("expected machine type to not be available")
		}
	})

	t.Run("WhenGetProjectIDFromNumberFails", func(tt *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			// Return error for project lookup
			http.Error(rw, "internal error", http.StatusInternalServerError)
		}))
		defer server.Close()

		computeSvc, err := compute.NewService(
			ctx, option.WithHTTPClient(&http.Client{Timeout: time.Second}), option.WithEndpoint(server.URL))
		if err != nil {
			t.Errorf("Error getting service up: '%s'", err.Error())
		}

		gService := &GcpServices{
			AdminGCPService: &AdminGCPService{computeService: computeSvc},
			Ctx:             ctx,
			Logger:          util.GetLogger(ctx),
			Retry:           NewExponentialRetryStrategy(time.Millisecond, 1),
		}

		available, err := gService.IsMachineTypeAvailable(projectNumber, zone, machineType)
		if err == nil {
			tt.Error("expected error but got none")
		}
		if available {
			tt.Error("expected false when error occurs")
		}
	})
}

func Test_getComputeRegionalOpStatus(t *testing.T) {
	url := "/projects/1079058383248/regions/us-central1/operations/op"
	projectNumber := "1079058383248"
	region := "us-central1"
	operationName := "op"

	t.Run("When_getComputeRegionalOpStatus", func(tt *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			if req.URL.Path == url {
				rw.WriteHeader(http.StatusInternalServerError)
				return
			}
			rw.WriteHeader(http.StatusBadRequest)
		}))
		defer server.Close()
		svc, err := compute.NewService(
			context.TODO(), option.WithHTTPClient(&http.Client{Timeout: time.Second}), option.WithEndpoint(server.URL))
		if err != nil {
			t.Errorf("Error getting service up: '%s'", err.Error())
		}
		adminService := AdminGCPService{computeService: svc}

		gService := &GcpServices{
			serviceNetworkingEndpoint: "endpoint.goog",
			AdminGCPService:           &adminService,
			Logger:                    log.NewLogger(),
		}
		out, err := gService.GetComputeRegionalOpStatus(projectNumber, region, operationName)
		if err == nil {
			tt.Error("Expected an error but got nothing")
		} else {
			if out != nil {
				tt.Errorf("Unexpected output: %+v\n", out)
			}
			if !strings.Contains(err.(*vsaerrors.CustomError).OriginalErr.Error(), "response code 500 with body") {
				tt.Errorf("Unexpected error: %s", err.(*vsaerrors.CustomError).OriginalErr.Error())
			}
		}
	})
	t.Run("WhenOperationErrored", func(tt *testing.T) {
		resp := &compute.Operation{Error: &compute.OperationError{Errors: []*compute.OperationErrorErrors{{Message: "operation not found"}}}}
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			if req.URL.Path == url {
				response, err := json.Marshal(resp)
				if err != nil {
					rw.WriteHeader(http.StatusBadRequest)
					return
				}
				_, _ = rw.Write(response)
				return
			}
		}))
		defer server.Close()
		svc, err := compute.NewService(
			context.TODO(), option.WithHTTPClient(&http.Client{Timeout: time.Second}), option.WithEndpoint(server.URL))
		if err != nil {
			t.Errorf("Error getting service up: '%s'", err.Error())
		}
		adminService := AdminGCPService{computeService: svc}

		gService := &GcpServices{
			serviceNetworkingEndpoint: "endpoint.goog",
			AdminGCPService:           &adminService,
			Logger:                    log.NewLogger(),
		}
		out, err := gService.GetComputeRegionalOpStatus(projectNumber, region, operationName)
		if err == nil {
			tt.Error("Expected an error but got nothing")
		} else {
			if out != nil {
				tt.Errorf("Unexpected output: %+v\n", out)
			}
			assertTerminalGCPOperationError(tt, err, "operation not found")
		}
	})
	t.Run("WhenOperationErroredWithoutDetails", func(tt *testing.T) {
		resp := &compute.Operation{Error: &compute.OperationError{}}
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			if req.URL.Path == url {
				response, err := json.Marshal(resp)
				if err != nil {
					rw.WriteHeader(http.StatusBadRequest)
					return
				}
				_, _ = rw.Write(response)
				return
			}
		}))
		defer server.Close()
		svc, err := compute.NewService(
			context.TODO(), option.WithHTTPClient(&http.Client{Timeout: time.Second}), option.WithEndpoint(server.URL))
		if err != nil {
			t.Errorf("Error getting service up: '%s'", err.Error())
		}
		adminService := AdminGCPService{computeService: svc}

		gService := &GcpServices{
			serviceNetworkingEndpoint: "endpoint.goog",
			AdminGCPService:           &adminService,
			Logger:                    log.NewLogger(),
		}
		out, err := gService.GetComputeRegionalOpStatus(projectNumber, region, operationName)
		if err == nil {
			tt.Error("Expected an error but got nothing")
		} else {
			if out != nil {
				tt.Errorf("Unexpected output: %+v\n", out)
			}
			assertTerminalGCPOperationError(tt, err, projectNumber)
		}
	})
	t.Run("WhenOK", func(tt *testing.T) {
		resp := &compute.Operation{Name: "op1"}
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			if req.URL.Path == url {
				response, err := json.Marshal(resp)
				if err != nil {
					rw.WriteHeader(http.StatusBadRequest)
					return
				}
				_, _ = rw.Write(response)
				return
			}
		}))
		defer server.Close()
		svc, err := compute.NewService(
			context.TODO(), option.WithHTTPClient(&http.Client{Timeout: time.Second}), option.WithEndpoint(server.URL))
		if err != nil {
			t.Errorf("Error getting service up: '%s'", err.Error())
		}
		adminService := AdminGCPService{computeService: svc}

		gService := &GcpServices{
			serviceNetworkingEndpoint: "endpoint.goog",
			AdminGCPService:           &adminService,
			Logger:                    log.NewLogger(),
		}
		out, err := gService.GetComputeRegionalOpStatus(projectNumber, region, operationName)
		if err != nil {
			tt.Errorf("Unexpected error: %s", err.Error())
		} else {
			if out == nil {
				tt.Errorf("Output unexpectedly nil")
			} else {
				if out.Name != "op1" {
					tt.Errorf("Unexpected operation name %s", out.Name)
				}
			}
		}
	})
}

func TestGetComputeGlobalOpStatus(t *testing.T) {
	projectNumber := "1079058383248"
	operationName := "op"
	url := fmt.Sprintf("/projects/%s/global/operations/%s", projectNumber, operationName)

	t.Run("WhenGetComputeGlobalOpStatus", func(tt *testing.T) {
		ctx := context.Background()
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			if req.URL.Path == url {
				rw.WriteHeader(http.StatusInternalServerError)
				return
			}
			rw.WriteHeader(http.StatusBadRequest)
		}))
		defer server.Close()
		svc, err := compute.NewService(
			ctx, option.WithHTTPClient(&http.Client{Timeout: time.Second}), option.WithEndpoint(server.URL))
		if err != nil {
			t.Errorf("Error getting service up: '%s'", err.Error())
		}
		adminService := AdminGCPService{computeService: svc}

		gService := &GcpServices{
			Ctx:             ctx,
			AdminGCPService: &adminService,
			Logger:          log.NewLogger(),
		}
		out, err := gService.GetComputeGlobalOpStatus(projectNumber, operationName)
		if err == nil {
			tt.Error("Expected an error but got nothing")
		} else {
			if out != nil {
				tt.Errorf("Unexpected output: %+v\n", out)
			}
			if !strings.Contains(err.(*vsaerrors.CustomError).OriginalErr.Error(), "response code 500 with body") {
				tt.Errorf("Unexpected error: %s", err.(*vsaerrors.CustomError).OriginalErr.Error())
			}
		}
	})
	t.Run("WhenOperationErrored", func(tt *testing.T) {
		resp := &compute.Operation{Error: &compute.OperationError{Errors: []*compute.OperationErrorErrors{{Message: "operation not found"}}}}
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			if req.URL.Path == url {
				response, err := json.Marshal(resp)
				if err != nil {
					rw.WriteHeader(http.StatusBadRequest)
					return
				}
				_, _ = rw.Write(response)
				return
			}
		}))
		defer server.Close()
		svc, err := compute.NewService(
			context.TODO(), option.WithHTTPClient(&http.Client{Timeout: time.Second}), option.WithEndpoint(server.URL))
		if err != nil {
			t.Errorf("Error getting service up: '%s'", err.Error())
		}
		adminService := AdminGCPService{computeService: svc}

		gService := &GcpServices{
			serviceNetworkingEndpoint: "endpoint.goog",
			AdminGCPService:           &adminService,
			Logger:                    log.NewLogger(),
		}
		out, err := gService.GetComputeGlobalOpStatus(projectNumber, operationName)
		if err == nil {
			tt.Error("Expected an error but got nothing")
		} else {
			if out != nil {
				tt.Errorf("Unexpected output: %+v\n", out)
			}
			assertTerminalGCPOperationError(tt, err, "operation not found")
		}
	})
	t.Run("WhenOperationErroredWithoutDetails", func(tt *testing.T) {
		// GCP may return a non-nil Error with an empty Errors slice; must not panic.
		resp := &compute.Operation{Error: &compute.OperationError{}}
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			if req.URL.Path == url {
				response, err := json.Marshal(resp)
				if err != nil {
					rw.WriteHeader(http.StatusBadRequest)
					return
				}
				_, _ = rw.Write(response)
				return
			}
		}))
		defer server.Close()
		svc, err := compute.NewService(
			context.TODO(), option.WithHTTPClient(&http.Client{Timeout: time.Second}), option.WithEndpoint(server.URL))
		if err != nil {
			t.Errorf("Error getting service up: '%s'", err.Error())
		}
		adminService := AdminGCPService{computeService: svc}

		gService := &GcpServices{
			serviceNetworkingEndpoint: "endpoint.goog",
			AdminGCPService:           &adminService,
			Logger:                    log.NewLogger(),
		}
		out, err := gService.GetComputeGlobalOpStatus(projectNumber, operationName)
		if err == nil {
			tt.Error("Expected an error but got nothing")
		} else {
			if out != nil {
				tt.Errorf("Unexpected output: %+v\n", out)
			}
			assertTerminalGCPOperationError(tt, err, projectNumber)
		}
	})
	t.Run("WhenOK", func(tt *testing.T) {
		resp := &compute.Operation{Name: "op1"}
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			if req.URL.Path == url {
				response, err := json.Marshal(resp)
				if err != nil {
					rw.WriteHeader(http.StatusBadRequest)
					return
				}
				_, _ = rw.Write(response)
				return
			}
		}))
		defer server.Close()
		svc, err := compute.NewService(
			context.TODO(), option.WithHTTPClient(&http.Client{Timeout: time.Second}), option.WithEndpoint(server.URL))
		if err != nil {
			t.Errorf("Error getting service up: '%s'", err.Error())
		}
		adminService := AdminGCPService{computeService: svc}

		gService := &GcpServices{
			AdminGCPService: &adminService,
			Logger:          log.NewLogger(),
		}
		out, err := gService.GetComputeGlobalOpStatus(projectNumber, operationName)
		if err != nil {
			tt.Errorf("Unexpected error: %s", err.Error())
		} else {
			if out == nil {
				tt.Errorf("Output unexpectedly nil")
			} else {
				if out.Name != "op1" {
					tt.Errorf("Unexpected operation name %s", out.Name)
				}
			}
		}
	})
}

func Test_ListAddressesWithFilter(t *testing.T) {
	t.Run("Failure_ProjectNameEmpty", func(t *testing.T) {
		defer testReset(t)

		region := "us-central1"
		subnetName := "test-subnet"
		deploymentID := "test-deployment"
		additionalLabels := map[string]string{
			"environment": "test",
			"team":        "platform",
		}

		result, err := (&GcpServices{
			AdminGCPService: &AdminGCPService{},
			Logger:          log.NewLogger(),
		}).ListAddressesWithFilter("", region, subnetName, deploymentID, additionalLabels)

		assert.Error(t, err)
		assert.Nil(t, result)
	})

	t.Run("Success_WithAllFilters", func(t *testing.T) {
		defer testReset(t)

		// Use context with timeout to prevent hanging
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		projectName := "test-project"
		region := "us-central1"
		subnetName := "test-subnet"
		deploymentID := "test-deployment"
		additionalLabels := map[string]string{
			"environment": "test",
			"team":        "platform",
		}

		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			expectedPath := fmt.Sprintf("/projects/%s/regions/%s/addresses", projectName, region)
			if req.URL.Path != expectedPath {
				t.Errorf("Expected path %s, got %s", expectedPath, req.URL.Path)
				return
			}

			// Check filter parameters
			filter := req.URL.Query().Get("filter")
			if filter == "" {
				t.Errorf("Expected filter got %s", filter)
				return
			}

			// Return mock response
			response := &compute.AddressList{
				Items: []*compute.Address{
					{
						Name:        "test-address-1",
						Address:     "10.0.0.1",
						AddressType: "INTERNAL",
						Subnetwork:  fmt.Sprintf("projects/%s/regions/%s/subnetworks/%s", projectName, region, subnetName),
						Labels: map[string]string{
							"deployment_id": deploymentID,
							"environment":   additionalLabels["environment"],
							"team":          additionalLabels["team"],
						},
					},
				},
			}

			rw.Header().Set("Content-Type", "application/json")
			err := json.NewEncoder(rw).Encode(response)
			if err != nil {
				t.Errorf("Failed to encode response: %v", err)
				return
			}
		}))
		defer server.Close()

		// Use consistent HTTP client timeout
		httpClient := &http.Client{Timeout: 2 * time.Second}
		svc, err := compute.NewService(
			ctx, option.WithHTTPClient(httpClient), option.WithEndpoint(server.URL))
		if err != nil {
			t.Fatalf("Error getting service up: '%s'", err.Error())
		}
		adminService := AdminGCPService{computeService: svc}

		gcpService := &GcpServices{
			AdminGCPService: &adminService,
			Ctx:             ctx,
			Logger:          log.NewLogger(),
		}

		result, err := gcpService.ListAddressesWithFilter(projectName, region, subnetName, deploymentID, additionalLabels)

		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Len(t, *result, 1)
	})

	t.Run("Success_WithOnlySubnetFilter", func(t *testing.T) {
		defer testReset(t)

		// Use context with timeout to prevent hanging
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		projectName := "test-project"
		region := "us-central1"
		subnetName := "test-subnet"

		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			expectedPath := fmt.Sprintf("/projects/%s/regions/%s/addresses", projectName, region)
			if req.URL.Path != expectedPath {
				t.Errorf("Expected path %s, got %s", expectedPath, req.URL.Path)
				return
			}

			// Check filter parameters
			filter := req.URL.Query().Get("filter")
			expectedFilter := fmt.Sprintf("subnetwork=\"%s\"", subnetName)
			if filter != expectedFilter {
				t.Errorf("Expected filter %s, got %s", expectedFilter, filter)
				return
			}

			// Return mock response
			response := &compute.AddressList{
				Items: []*compute.Address{
					{
						Name:        "test-address-1",
						Address:     "10.0.0.1",
						AddressType: "INTERNAL",
						Subnetwork:  fmt.Sprintf("projects/%s/regions/%s/subnetworks/%s", projectName, region, subnetName),
					},
				},
			}

			rw.Header().Set("Content-Type", "application/json")
			err := json.NewEncoder(rw).Encode(response)
			if err != nil {
				t.Errorf("Failed to encode response: %v", err)
				return
			}
		}))
		defer server.Close()

		// Use consistent HTTP client timeout
		httpClient := &http.Client{Timeout: 2 * time.Second}
		svc, err := compute.NewService(
			ctx, option.WithHTTPClient(httpClient), option.WithEndpoint(server.URL))
		if err != nil {
			t.Fatalf("Error getting service up: '%s'", err.Error())
		}
		adminService := AdminGCPService{computeService: svc}

		gcpService := &GcpServices{
			AdminGCPService: &adminService,
			Ctx:             ctx,
			Logger:          log.NewLogger(),
		}

		result, err := gcpService.ListAddressesWithFilter(projectName, region, subnetName, "", nil)

		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Len(t, *result, 1)
	})

	t.Run("Success_WithOnlyDeploymentFilter", func(t *testing.T) {
		defer testReset(t)

		// Use context with timeout to prevent hanging
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		projectName := "test-project"
		region := "us-central1"
		deploymentID := "test-deployment"

		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			expectedPath := fmt.Sprintf("/projects/%s/regions/%s/addresses", projectName, region)
			if req.URL.Path != expectedPath {
				t.Errorf("Expected path %s, got %s", expectedPath, req.URL.Path)
				return
			}

			// Check filter parameters
			filter := req.URL.Query().Get("filter")
			expectedFilter := fmt.Sprintf("labels.deployment_id=\"%s\"", deploymentID)
			if filter != expectedFilter {
				t.Errorf("Expected filter %s, got %s", expectedFilter, filter)
				return
			}

			// Return mock response
			response := &compute.AddressList{
				Items: []*compute.Address{
					{
						Name:        "test-address-1",
						Address:     "10.0.0.1",
						AddressType: "INTERNAL",
						Labels: map[string]string{
							"deployment_id": deploymentID,
						},
					},
				},
			}

			rw.Header().Set("Content-Type", "application/json")
			err := json.NewEncoder(rw).Encode(response)
			if err != nil {
				t.Errorf("Failed to encode response: %v", err)
				return
			}
		}))
		defer server.Close()

		// Use consistent HTTP client timeout
		httpClient := &http.Client{Timeout: 2 * time.Second}
		svc, err := compute.NewService(
			ctx, option.WithHTTPClient(httpClient), option.WithEndpoint(server.URL))
		if err != nil {
			t.Fatalf("Error getting service up: '%s'", err.Error())
		}
		adminService := AdminGCPService{computeService: svc}

		gcpService := &GcpServices{
			AdminGCPService: &adminService,
			Ctx:             ctx,
			Logger:          log.NewLogger(),
		}

		result, err := gcpService.ListAddressesWithFilter(projectName, region, "", deploymentID, nil)

		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Len(t, *result, 1)
	})

	t.Run("Success_WithOnlyAdditionalLabels", func(t *testing.T) {
		defer testReset(t)

		// Use context with timeout to prevent hanging
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		projectName := "test-project"
		region := "us-central1"
		additionalLabels := map[string]string{
			"environment": "test",
			"team":        "platform",
		}

		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			expectedPath := fmt.Sprintf("/projects/%s/regions/%s/addresses", projectName, region)
			if req.URL.Path != expectedPath {
				t.Errorf("Expected path %s, got %s", expectedPath, req.URL.Path)
				return
			}

			// Check filter parameters
			filter := req.URL.Query().Get("filter")
			// Since map iteration order is not deterministic, check that both labels are present
			if !strings.Contains(filter, "labels.environment=\"test\"") || !strings.Contains(filter, "labels.team=\"platform\"") {
				t.Errorf("Expected filter to contain both labels.environment=\"test\" and labels.team=\"platform\", got %s", filter)
				return
			}
			if !strings.Contains(filter, " AND ") {
				t.Errorf("Expected filter to contain AND separator, got %s", filter)
				return
			}

			// Return mock response
			response := &compute.AddressList{
				Items: []*compute.Address{
					{
						Name:        "test-address-1",
						Address:     "10.0.0.1",
						AddressType: "INTERNAL",
						Labels:      additionalLabels,
					},
				},
			}

			rw.Header().Set("Content-Type", "application/json")
			err := json.NewEncoder(rw).Encode(response)
			if err != nil {
				t.Errorf("Failed to encode response: %v", err)
				return
			}
		}))
		defer server.Close()

		// Use consistent HTTP client timeout
		httpClient := &http.Client{Timeout: 2 * time.Second}
		svc, err := compute.NewService(
			ctx, option.WithHTTPClient(httpClient), option.WithEndpoint(server.URL))
		if err != nil {
			t.Fatalf("Error getting service up: '%s'", err.Error())
		}
		adminService := AdminGCPService{computeService: svc}

		gcpService := &GcpServices{
			AdminGCPService: &adminService,
			Ctx:             ctx,
			Logger:          log.NewLogger(),
		}

		result, err := gcpService.ListAddressesWithFilter(projectName, region, "", "", additionalLabels)

		assert.NoError(t, err)
		assert.NotNil(t, result)
		if result != nil {
			assert.Len(t, *result, 1)
		}
	})

	t.Run("Success_NoFilters", func(t *testing.T) {
		defer testReset(t)

		// Use context with timeout to prevent hanging
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		projectName := "test-project"
		region := "us-central1"

		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			expectedPath := fmt.Sprintf("/projects/%s/regions/%s/addresses", projectName, region)
			if req.URL.Path != expectedPath {
				t.Errorf("Expected path %s, got %s", expectedPath, req.URL.Path)
				return
			}

			// Check that no filter is applied
			filter := req.URL.Query().Get("filter")
			if filter != "" {
				t.Errorf("Expected no filter, got %s", filter)
				return
			}

			// Return mock response
			response := &compute.AddressList{
				Items: []*compute.Address{
					{
						Name:        "test-address-1",
						Address:     "10.0.0.1",
						AddressType: "INTERNAL",
					},
				},
			}

			rw.Header().Set("Content-Type", "application/json")
			err := json.NewEncoder(rw).Encode(response)
			if err != nil {
				t.Errorf("Failed to encode response: %v", err)
				return
			}
		}))
		defer server.Close()

		// Use consistent HTTP client timeout
		httpClient := &http.Client{Timeout: 2 * time.Second}
		svc, err := compute.NewService(
			ctx, option.WithHTTPClient(httpClient), option.WithEndpoint(server.URL))
		if err != nil {
			t.Fatalf("Error getting service up: '%s'", err.Error())
		}
		adminService := AdminGCPService{computeService: svc}

		gcpService := &GcpServices{
			AdminGCPService: &adminService,
			Ctx:             ctx,
			Logger:          log.NewLogger(),
		}

		result, err := gcpService.ListAddressesWithFilter(projectName, region, "", "", nil)

		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Len(t, *result, 1)
	})

	t.Run("Success_GlobalRegion", func(t *testing.T) {
		defer testReset(t)

		// Use context with timeout to prevent hanging
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		projectName := "test-project"

		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			expectedPath := fmt.Sprintf("/projects/%s/global/addresses", projectName)
			if req.URL.Path != expectedPath {
				t.Errorf("Expected path %s, got %s", expectedPath, req.URL.Path)
				return
			}

			// Return mock response
			response := &compute.AddressList{
				Items: []*compute.Address{
					{
						Name:        "test-global-address",
						Address:     "1.2.3.4",
						AddressType: "EXTERNAL",
					},
				},
			}

			rw.Header().Set("Content-Type", "application/json")
			err := json.NewEncoder(rw).Encode(response)
			if err != nil {
				t.Errorf("Failed to encode response: %v", err)
				return
			}
		}))
		defer server.Close()

		// Use consistent HTTP client timeout
		httpClient := &http.Client{Timeout: 2 * time.Second}
		svc, err := compute.NewService(
			ctx, option.WithHTTPClient(httpClient), option.WithEndpoint(server.URL))
		if err != nil {
			t.Fatalf("Error getting service up: '%s'", err.Error())
		}
		adminService := AdminGCPService{computeService: svc}

		gcpService := &GcpServices{
			AdminGCPService: &adminService,
			Ctx:             ctx,
			Logger:          log.NewLogger(),
		}

		result, err := gcpService.ListAddressesWithFilter(projectName, "global", "", "", nil)

		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Len(t, *result, 1)
	})

	t.Run("Success_EmptyResponse", func(t *testing.T) {
		defer testReset(t)

		// Use context with timeout to prevent hanging
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		projectName := "test-project"
		region := "us-central1"

		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			expectedPath := fmt.Sprintf("/projects/%s/regions/%s/addresses", projectName, region)
			if req.URL.Path != expectedPath {
				t.Errorf("Expected path %s, got %s", expectedPath, req.URL.Path)
				return
			}

			// Return empty response
			response := &compute.AddressList{
				Items: []*compute.Address{},
			}

			rw.Header().Set("Content-Type", "application/json")
			err := json.NewEncoder(rw).Encode(response)
			if err != nil {
				t.Errorf("Failed to encode response: %v", err)
				return
			}
		}))
		defer server.Close()

		// Use consistent HTTP client timeout
		httpClient := &http.Client{Timeout: 2 * time.Second}
		svc, err := compute.NewService(
			ctx, option.WithHTTPClient(httpClient), option.WithEndpoint(server.URL))
		if err != nil {
			t.Fatalf("Error getting service up: '%s'", err.Error())
		}
		adminService := AdminGCPService{computeService: svc}

		gcpService := &GcpServices{
			AdminGCPService: &adminService,
			Ctx:             ctx,
			Logger:          log.NewLogger(),
		}

		result, err := gcpService.ListAddressesWithFilter(projectName, region, "", "", nil)

		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Len(t, *result, 0)
	})

	t.Run("Error_GCPAPIError", func(t *testing.T) {
		defer testReset(t)

		// Use context with timeout to prevent hanging
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		projectName := "test-project"
		region := "us-central1"

		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			expectedPath := fmt.Sprintf("/projects/%s/regions/%s/addresses", projectName, region)
			if req.URL.Path != expectedPath {
				t.Errorf("Expected path %s, got %s", expectedPath, req.URL.Path)
				return
			}

			rw.WriteHeader(http.StatusInternalServerError)
			_, err := rw.Write([]byte("Internal Server Error"))
			if err != nil {
				t.Errorf("Failed to write error response: %v", err)
				return
			}
		}))
		defer server.Close()

		// Use consistent HTTP client timeout
		httpClient := &http.Client{Timeout: 2 * time.Second}
		svc, err := compute.NewService(
			ctx, option.WithHTTPClient(httpClient), option.WithEndpoint(server.URL))
		if err != nil {
			t.Fatalf("Error getting service up: '%s'", err.Error())
		}
		adminService := AdminGCPService{computeService: svc}

		gcpService := &GcpServices{
			AdminGCPService: &adminService,
			Ctx:             ctx,
			Logger:          log.NewLogger(),
		}

		result, err := gcpService.ListAddressesWithFilter(projectName, region, "", "", nil)

		assert.Error(t, err)
		assert.Nil(t, result)
	})

	t.Run("Success_WithSpecialCharactersInLabels", func(t *testing.T) {
		defer testReset(t)

		// Use context with timeout to prevent hanging
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		projectName := "test-project"
		region := "us-central1"
		deploymentID := "test-deployment-with-special-chars"
		additionalLabels := map[string]string{
			"environment": "test-env-with-dashes",
			"team":        "platform-team_underscore",
		}

		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			expectedPath := fmt.Sprintf("/projects/%s/regions/%s/addresses", projectName, region)
			if req.URL.Path != expectedPath {
				t.Errorf("Expected path %s, got %s", expectedPath, req.URL.Path)
				return
			}

			// Check filter parameters
			filter := req.URL.Query().Get("filter")
			if filter == "" {
				t.Errorf("Expected filter got %s", filter)
				return
			}

			// Verify that special characters are properly escaped
			expectedDeploymentFilter := fmt.Sprintf("labels.deployment_id=\"%s\"", deploymentID)
			if !strings.Contains(filter, expectedDeploymentFilter) {
				t.Errorf("Expected deployment filter %s in filter %s", expectedDeploymentFilter, filter)
				return
			}

			// Return mock response
			response := &compute.AddressList{
				Items: []*compute.Address{
					{
						Name:        "test-address-special",
						Address:     "10.0.0.1",
						AddressType: "INTERNAL",
						Labels: map[string]string{
							"deployment_id": deploymentID,
							"environment":   additionalLabels["environment"],
							"team":          additionalLabels["team"],
						},
					},
				},
			}

			rw.Header().Set("Content-Type", "application/json")
			err := json.NewEncoder(rw).Encode(response)
			if err != nil {
				t.Errorf("Failed to encode response: %v", err)
				return
			}
		}))
		defer server.Close()

		// Use consistent HTTP client timeout
		httpClient := &http.Client{Timeout: 2 * time.Second}
		svc, err := compute.NewService(
			ctx, option.WithHTTPClient(httpClient), option.WithEndpoint(server.URL))
		if err != nil {
			t.Fatalf("Error getting service up: '%s'", err.Error())
		}
		adminService := AdminGCPService{computeService: svc}

		gcpService := &GcpServices{
			AdminGCPService: &adminService,
			Ctx:             ctx,
			Logger:          log.NewLogger(),
		}

		result, err := gcpService.ListAddressesWithFilter(projectName, region, "", deploymentID, additionalLabels)

		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Len(t, *result, 1)
	})

	t.Run("Success_WithRetryMechanism", func(t *testing.T) {
		defer testReset(t)

		// Use context with timeout to prevent hanging
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		projectName := "test-project"
		region := "us-central1"
		attemptCount := 0

		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			attemptCount++

			// Simulate transient failure on first attempt
			if attemptCount == 1 {
				rw.WriteHeader(http.StatusInternalServerError)
				_, _ = rw.Write([]byte("Internal Server Error"))
				return
			}

			// Success on retry
			response := &compute.AddressList{
				Items: []*compute.Address{
					{
						Name:        "test-address-retry",
						Address:     "10.0.0.1",
						AddressType: "INTERNAL",
					},
				},
			}

			rw.Header().Set("Content-Type", "application/json")
			err := json.NewEncoder(rw).Encode(response)
			if err != nil {
				t.Errorf("Failed to encode response: %v", err)
				return
			}
		}))
		defer server.Close()

		// Use consistent HTTP client timeout
		httpClient := &http.Client{Timeout: 2 * time.Second}
		svc, err := compute.NewService(
			ctx, option.WithHTTPClient(httpClient), option.WithEndpoint(server.URL))
		if err != nil {
			t.Fatalf("Error getting service up: '%s'", err.Error())
		}
		adminService := AdminGCPService{computeService: svc}

		gcpService := &GcpServices{
			AdminGCPService: &adminService,
			Ctx:             ctx,
			Logger:          log.NewLogger(),
		}

		// Use retry mechanism for the test
		var result *[]models.Address
		err = retryTestOperation(t, 3, func() error {
			var err error
			result, err = gcpService.ListAddressesWithFilter(projectName, region, "", "", nil)
			return err
		})

		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Len(t, *result, 1)
		assert.Equal(t, 2, attemptCount) // Should have retried once
	})
}

func Test_ListAddressesByDeployment(t *testing.T) {
	ctx := context.Background()
	projectName := "test-project"
	region := "us-central1"
	deploymentID := "test-deployment"

	t.Run("Success", func(t *testing.T) {
		defer testReset(t)

		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			expectedPath := fmt.Sprintf("/projects/%s/regions/%s/addresses", projectName, region)
			if req.URL.Path != expectedPath {
				t.Errorf("Expected path %s, got %s", expectedPath, req.URL.Path)
			}

			// Check filter parameters
			filter := req.URL.Query().Get("filter")
			expectedFilter := fmt.Sprintf("labels.deployment_id=\"%s\"", deploymentID)
			if filter != expectedFilter {
				t.Errorf("Expected filter %s, got %s", expectedFilter, filter)
			}

			// Return mock response
			response := &compute.AddressList{
				Items: []*compute.Address{
					{
						Name:        "test-address-1",
						Address:     "10.0.0.1",
						AddressType: "INTERNAL",
						Labels: map[string]string{
							"deployment_id": deploymentID,
						},
					},
				},
			}

			rw.Header().Set("Content-Type", "application/json")
			err := json.NewEncoder(rw).Encode(response)
			if err != nil {
				return
			}
		}))
		defer server.Close()

		svc, err := compute.NewService(
			ctx, option.WithHTTPClient(&http.Client{Timeout: time.Second}), option.WithEndpoint(server.URL))
		if err != nil {
			t.Errorf("Error getting service up: '%s'", err.Error())
		}
		adminService := AdminGCPService{computeService: svc}

		gcpService := &GcpServices{
			AdminGCPService: &adminService,
			Logger:          log.NewLogger(),
		}

		result, err := gcpService.ListAddressesByDeployment(projectName, region, deploymentID)

		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Len(t, *result, 1)
	})

	t.Run("Error_FromListAddressesWithFilter", func(t *testing.T) {
		defer testReset(t)

		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			expectedPath := fmt.Sprintf("/projects/%s/regions/%s/addresses", projectName, region)
			if req.URL.Path != expectedPath {
				t.Errorf("Expected path %s, got %s", expectedPath, req.URL.Path)
			}

			rw.WriteHeader(http.StatusInternalServerError)
			_, err := rw.Write([]byte("Internal Server Error"))
			if err != nil {
				return
			}
		}))
		defer server.Close()

		svc, err := compute.NewService(
			ctx, option.WithHTTPClient(&http.Client{Timeout: time.Second}), option.WithEndpoint(server.URL))
		if err != nil {
			t.Errorf("Error getting service up: '%s'", err.Error())
		}
		adminService := AdminGCPService{computeService: svc}

		gcpService := &GcpServices{
			AdminGCPService: &adminService,
			Logger:          log.NewLogger(),
		}

		result, err := gcpService.ListAddressesByDeployment(projectName, region, deploymentID)

		assert.Error(t, err)
		assert.Nil(t, result)
	})
}

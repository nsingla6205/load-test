package api

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/factory"
	ocifactory "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/factory/oci"
	ociworkflows "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows/oci"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	ociserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/oci-proxy/api/oci-servergen"
	utilserrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/workflowquery"
	workflowenginemock "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine"
	"go.temporal.io/sdk/client"
)

// defaultTestOPC matches middleware: a stable UUID used when tests do not care about the exact value.
const defaultTestOPC = "aaaaaaaa-bbbb-4ccc-dddd-eeeeeeeeeeee"

const defaultTestTenancyOCID = "ocid1.tenancy.oc1..aaaaaaaatestaaa"

func defaultCreatePoolParams() ociserver.CreatePoolParams {
	return ociserver.CreatePoolParams{TenancyOcid: defaultTestTenancyOCID}
}

// validCreatePoolRequest returns a request that passes validateCreatePoolRequest:
// shared HA (no secondary AD set) so the non-shared-HA rule does not apply, and
// DataEndpointCount=2 to satisfy the parity check. Tests that exercise individual
// validation branches mutate exactly one field at a time.
func validCreatePoolRequest() *ociserver.CreatePoolRequest {
	return &ociserver.CreatePoolRequest{
		PoolOCID:                  "ocid1.pool.oc1..valid",
		CompartmentOCID:           "ocid1.compartment.oc1..valid",
		ResourceName:               "valid-pool",
		SubnetId:                  "ocid1.subnet.oc1.iad.valid",
		DataNicSubnetId:           "ocid1.subnet.oc1.iad.validdata",
		PrimaryAvailabilityDomain: "ad1",
		SizeInGiB:                 1024,
		ThroughputGBps:            1.0,
		DataEndpointCount:         2,
		OciAdminPassword:          ociserver.OCIOCIDVersionRef{Ocid: "ocid1.secret.oc1..valid", Version: "1"},
	}
}

// validTieringConfig returns an OptTieringConfig with Set=true and all four
// required fields populated with values that satisfy validateCreatePoolRequest.
// Test cases that exercise individual tieringConfig branches start from this
// shape and mutate exactly one field.
func validTieringConfig() ociserver.OptTieringConfig {
	return ociserver.OptTieringConfig{
		Set: true,
		Value: ociserver.TieringConfig{
			SecretId:   "ocid1.vaultsecret.oc1..tiervalid",
			Namespace:  "axqogasfjw45",
			BucketName: "cold-tier-bucket",
			ServerName: "compat.objectstorage.us-ashburn-1.oraclecloud.com",
		},
	}
}

// gbpsToMibps converts GBps (SI, 10^9 bytes/s) to MiBps (2^20 bytes/s).
func gbpsToMibps(gbps float64) int64 {
	return int64(gbps * workflowquery.MiBpsPerGBps)
}

func contextWithOpcRequestID(parent context.Context, opc string) context.Context {
	if parent == nil {
		parent = context.Background()
	}
	h := make(http.Header)
	h.Set(string(middleware.OPCRequestIDHeaderName), opc)
	h.Set(string(middleware.OPCWorkRequestIDHeaderName), opc)
	return context.WithValue(parent, middleware.HeaderContextKey, h)
}

func TestNewHandler(t *testing.T) {
	t.Run("NewHandler returns non-nil handler with nil ServerState when passed nil", func(tt *testing.T) {
		h := NewHandler(nil)
		assert.NotNil(tt, h)
		assert.Nil(tt, h.ServerState)
	})
	t.Run("NewHandler returns handler with given ServerState", func(tt *testing.T) {
		serverState := NewServerState()
		h := NewHandler(serverState)
		assert.NotNil(tt, h)
		assert.Same(tt, serverState, h.ServerState)
	})
}

func TestCreatePool(t *testing.T) {
	t.Run("CreatePool returns 202 with workflow body when orchestrator returns success", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().CreatePool(
			mock.Anything,
			mock.MatchedBy(func(p *commonparams.CreatePoolParams) bool {
				return p != nil &&
					p.SizeInBytes == 1024*1024*1024*1024 && // 1024 GiB
					p.HAPairs == 1 && // 2 dataEndpoints / 2
					p.CustomPerformanceParams != nil &&
					p.CustomPerformanceParams.ThroughputMibps == gbpsToMibps(1.0) &&
					p.PrimaryZone == "ad1" &&
					p.SecondaryZone == "ad2" &&
					p.IsRegionalHA == true // ad1 != ad2 → derived as regional HA
			}),
		).Return(&models.Pool{
			BaseModel:      models.BaseModel{UUID: "pool-uuid-1", CreatedAt: time.Now(), UpdatedAt: time.Now()},
			Name:           "test-pool",
			AccountName:    "ocid1.compartment.oc1..aaa",
			Region:         "us-phoenix-1",
			VendorSubNetID: "ocid1.subnet.oc1..aaa",
			SizeInBytes:    1099511627776,
			State:          "CREATING",
			PoolAttributes: &models.PoolAttributes{PrimaryZone: "ad1", SecondaryZone: "ad2"},
		}, "work-request-id", nil)
		h := Handler{Orchestrator: mockOrchestrator}
		params := defaultCreatePoolParams()
		req := &ociserver.CreatePoolRequest{
			PoolOCID:                    "ocid1.pool.oc1..aaa",
			CompartmentOCID:             "ocid1.compartment.oc1..aaa",
			ResourceName:                 "test-pool",
			SubnetId:                    "ocid1.subnet.oc1..aaa",
			DataNicSubnetId:             "ocid1.subnet.oc1..data",
			PrimaryAvailabilityDomain:   "ad1",
			SecondaryAvailabilityDomain: ociserver.NewOptString("ad2"),
			SizeInGiB:                   1024,
			ThroughputGBps:              1.0,
			DataEndpointCount:           2,
			OciAdminPassword:            ociserver.OCIOCIDVersionRef{Ocid: "ocid1.secret.oc1..aaa", Version: "1"},
		}

		res, err := h.CreatePool(contextWithOpcRequestID(nil, defaultTestOPC), req, params)

		assert.NoError(tt, err)
		headers, ok := res.(*ociserver.CreatePoolAcceptedResponseHeaders)
		assert.True(tt, ok, "response should be *ociserver.CreatePoolAcceptedResponseHeaders")
		assert.Equal(tt, defaultTestOPC, headers.OpcWorkRequestID)
		assert.Equal(tt, "in_progress", headers.Response.Status)
		assert.Equal(tt, "work-request-id", headers.Response.WorkflowId)
		assert.Equal(tt, req.PoolOCID, headers.Response.PoolOCID)
	})

	t.Run("CreatePool echoes opc-request-id when provided", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		req := &ociserver.CreatePoolRequest{
			PoolOCID:                  "ocid1.pool.oc1..bbb",
			CompartmentOCID:           "ocid1.compartment.oc1..aaa",
			ResourceName:               "test-pool",
			SubnetId:                  "ocid1.subnet.oc1..aaa",
			DataNicSubnetId:           "ocid1.subnet.oc1..data",
			PrimaryAvailabilityDomain: "ad1",
			SizeInGiB:                 1024,
			ThroughputGBps:            1.0,
			DataEndpointCount:         2,
			OciAdminPassword:          ociserver.OCIOCIDVersionRef{Ocid: "ocid1.secret.oc1..aaa", Version: "1"},
		}
		mockOrchestrator.EXPECT().CreatePool(mock.Anything, mock.Anything).Return(&models.Pool{
			BaseModel:      models.BaseModel{UUID: "pool-echo-id", CreatedAt: time.Now(), UpdatedAt: time.Now()},
			Name:           req.ResourceName,
			AccountName:    req.CompartmentOCID,
			VendorSubNetID: req.SubnetId,
			SizeInBytes:    1024 * 1024 * 1024 * 1024,
			State:          "ACTIVE",
		}, "work-req-echo", nil)
		h := Handler{Orchestrator: mockOrchestrator}
		params := defaultCreatePoolParams()

		res, err := h.CreatePool(contextWithOpcRequestID(nil, "client-request-id-123"), req, params)

		assert.NoError(tt, err)
		headers, ok := res.(*ociserver.CreatePoolAcceptedResponseHeaders)
		assert.True(tt, ok, "response should be *ociserver.CreatePoolAcceptedResponseHeaders")
		assert.Equal(tt, "client-request-id-123", headers.OpcWorkRequestID)
		assert.Equal(tt, "in_progress", headers.Response.Status)
		assert.Equal(tt, "work-req-echo", headers.Response.WorkflowId)
		assert.Equal(tt, req.PoolOCID, headers.Response.PoolOCID)
	})

	t.Run("CreatePool populates pool from request", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		req := &ociserver.CreatePoolRequest{
			PoolOCID:                  "ocid1.pool.oc1..ccc",
			CompartmentOCID:           "ocid1.compartment.oc1..testcomp",
			ResourceName:               "my-pool",
			SubnetId:                  "ocid1.subnet.oc1.iad.testsubnet",
			DataNicSubnetId:           "ocid1.subnet.oc1..data",
			PrimaryAvailabilityDomain: "ad1",
			SizeInGiB:                 1024,
			ThroughputGBps:            1.0,
			DataEndpointCount:         2,
			OciAdminPassword:          ociserver.OCIOCIDVersionRef{Ocid: "ocid1.secret.oc1..aaa", Version: "1"},
		}
		mockOrchestrator.EXPECT().CreatePool(mock.Anything, mock.Anything).Return(&models.Pool{
			BaseModel:      models.BaseModel{UUID: "pool-from-req", CreatedAt: time.Now(), UpdatedAt: time.Now()},
			Name:           req.ResourceName,
			AccountName:    req.CompartmentOCID,
			VendorSubNetID: req.SubnetId,
			SizeInBytes:    2 * 1024 * 1024 * 1024 * 1024,
			State:          "ACTIVE",
		}, "work-req-populate", nil)
		h := Handler{Orchestrator: mockOrchestrator}
		params := defaultCreatePoolParams()

		res, err := h.CreatePool(contextWithOpcRequestID(nil, defaultTestOPC), req, params)

		assert.NoError(tt, err)
		headers, ok := res.(*ociserver.CreatePoolAcceptedResponseHeaders)
		assert.True(tt, ok, "response should be *ociserver.CreatePoolAcceptedResponseHeaders")
		assert.Equal(tt, "in_progress", headers.Response.Status)
		assert.Equal(tt, "work-req-populate", headers.Response.WorkflowId)
		assert.Equal(tt, req.PoolOCID, headers.Response.PoolOCID)
	})

	t.Run("CreatePool forwards required DataNicSubnetId to orchestrator params", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		req := &ociserver.CreatePoolRequest{
			PoolOCID:                  "ocid1.pool.oc1..datanic",
			CompartmentOCID:           "ocid1.compartment.oc1..testcomp",
			ResourceName:               "pool-with-data-nic",
			SubnetId:                  "ocid1.subnet.oc1.iad.testsubnet",
			DataNicSubnetId:           "ocid1.subnet.oc1.iad.testdatasubnet",
			PrimaryAvailabilityDomain: "ad1",
			SizeInGiB:                 1024,
			ThroughputGBps:            1.0,
			DataEndpointCount:         2,
			OciAdminPassword:          ociserver.OCIOCIDVersionRef{Ocid: "ocid1.secret.oc1..aaa", Version: "1"},
		}
		mockOrchestrator.EXPECT().
			CreatePool(mock.Anything, mock.MatchedBy(func(p *commonparams.CreatePoolParams) bool {
				return p != nil && p.DataNICSubnetID == req.DataNicSubnetId
			})).
			Return(&models.Pool{
				BaseModel:      models.BaseModel{UUID: "pool-data-nic", CreatedAt: time.Now(), UpdatedAt: time.Now()},
				Name:           req.ResourceName,
				AccountName:    req.CompartmentOCID,
				VendorSubNetID: req.SubnetId,
				SizeInBytes:    1024 * 1024 * 1024 * 1024,
				State:          "CREATING",
			}, "work-req-data-nic", nil)
		h := Handler{Orchestrator: mockOrchestrator}

		res, err := h.CreatePool(contextWithOpcRequestID(nil, defaultTestOPC), req, defaultCreatePoolParams())

		assert.NoError(tt, err)
		headers, ok := res.(*ociserver.CreatePoolAcceptedResponseHeaders)
		assert.True(tt, ok, "response should be *ociserver.CreatePoolAcceptedResponseHeaders")
		assert.Equal(tt, "work-req-data-nic", headers.Response.WorkflowId)
		assert.Equal(tt, req.PoolOCID, headers.Response.PoolOCID)
	})

	t.Run("CreatePool returns 400 when orchestrator returns validation error", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().CreatePool(mock.Anything, mock.Anything).Return(nil, "", utilserrors.NewUserInputValidationErr("invalid region"))
		h := Handler{Orchestrator: mockOrchestrator}
		params := defaultCreatePoolParams()
		req := &ociserver.CreatePoolRequest{
			PoolOCID:                  "ocid1.pool.oc1..aaa",
			CompartmentOCID:           "ocid1.compartment.oc1..aaa",
			ResourceName:               "test-pool",
			SubnetId:                  "ocid1.subnet.oc1..aaa",
			DataNicSubnetId:           "ocid1.subnet.oc1..data",
			PrimaryAvailabilityDomain: "ad1",
			SizeInGiB:                 1024,
			ThroughputGBps:            1.0,
			DataEndpointCount:         2,
			OciAdminPassword:          ociserver.OCIOCIDVersionRef{Ocid: "ocid1.secret.oc1..aaa", Version: "1"},
		}

		res, err := h.CreatePool(contextWithOpcRequestID(nil, defaultTestOPC), req, params)

		assert.NoError(tt, err)
		badReq, ok := res.(*ociserver.CreatePoolBadRequest)
		assert.True(tt, ok, "response should be *ociserver.CreatePoolBadRequest")
		assert.Equal(tt, defaultTestOPC, badReq.OpcWorkRequestID)
		assert.Equal(tt, "failed", badReq.Response.Status)
		assert.Equal(tt, req.PoolOCID, badReq.Response.PoolOCID)
		assert.Contains(tt, badReq.Response.ErrorMessage, "invalid region")
	})

	t.Run("CreatePool conflict uses unwrapped error message when wrapped", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		inner := utilserrors.NewConflictErr("inner detail")
		wrapped := fmt.Errorf("outer: %w", inner)
		mockOrchestrator.EXPECT().CreatePool(mock.Anything, mock.Anything).Return(nil, "", wrapped)
		h := Handler{Orchestrator: mockOrchestrator}
		params := defaultCreatePoolParams()
		req := &ociserver.CreatePoolRequest{
			PoolOCID:                  "ocid1.pool.oc1..aaa",
			CompartmentOCID:           "ocid1.compartment.oc1..aaa",
			ResourceName:               "test-pool",
			SubnetId:                  "ocid1.subnet.oc1..aaa",
			DataNicSubnetId:           "ocid1.subnet.oc1..data",
			PrimaryAvailabilityDomain: "ad1",
			SizeInGiB:                 1024,
			ThroughputGBps:            1.0,
			DataEndpointCount:         2,
			OciAdminPassword:          ociserver.OCIOCIDVersionRef{Ocid: "ocid1.secret.oc1..aaa", Version: "1"},
		}

		res, err := h.CreatePool(contextWithOpcRequestID(nil, defaultTestOPC), req, params)

		assert.NoError(tt, err)
		conflict, ok := res.(*ociserver.CreatePoolConflict)
		assert.True(tt, ok, "response should be *ociserver.CreatePoolConflict")
		assert.Equal(tt, inner.Error(), conflict.Response.ErrorMessage)
	})

	t.Run("CreatePool returns 409 when orchestrator returns conflict", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().CreatePool(mock.Anything, mock.Anything).Return(nil, "", utilserrors.NewConflictErr("pool already exists"))
		h := Handler{Orchestrator: mockOrchestrator}
		params := defaultCreatePoolParams()
		req := &ociserver.CreatePoolRequest{
			PoolOCID:                  "ocid1.pool.oc1..aaa",
			CompartmentOCID:           "ocid1.compartment.oc1..aaa",
			ResourceName:               "test-pool",
			SubnetId:                  "ocid1.subnet.oc1..aaa",
			DataNicSubnetId:           "ocid1.subnet.oc1..data",
			PrimaryAvailabilityDomain: "ad1",
			SizeInGiB:                 1024,
			ThroughputGBps:            1.0,
			DataEndpointCount:         2,
			OciAdminPassword:          ociserver.OCIOCIDVersionRef{Ocid: "ocid1.secret.oc1..aaa", Version: "1"},
		}

		res, err := h.CreatePool(contextWithOpcRequestID(nil, defaultTestOPC), req, params)

		assert.NoError(tt, err)
		conflict, ok := res.(*ociserver.CreatePoolConflict)
		assert.True(tt, ok, "response should be *ociserver.CreatePoolConflict")
		assert.Equal(tt, defaultTestOPC, conflict.OpcWorkRequestID)
		assert.Equal(tt, "failed", conflict.Response.Status)
		assert.Equal(tt, req.PoolOCID, conflict.Response.PoolOCID)
		assert.Contains(tt, conflict.Response.ErrorMessage, "pool already exists")
	})

	t.Run("CreatePool returns 500 when orchestrator returns generic error", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().CreatePool(mock.Anything, mock.Anything).Return(nil, "", errors.New("internal failure"))
		h := Handler{Orchestrator: mockOrchestrator}
		params := defaultCreatePoolParams()
		req := &ociserver.CreatePoolRequest{
			PoolOCID:                  "ocid1.pool.oc1..aaa",
			CompartmentOCID:           "ocid1.compartment.oc1..aaa",
			ResourceName:               "test-pool",
			SubnetId:                  "ocid1.subnet.oc1..aaa",
			DataNicSubnetId:           "ocid1.subnet.oc1..data",
			PrimaryAvailabilityDomain: "ad1",
			SizeInGiB:                 1024,
			ThroughputGBps:            1.0,
			DataEndpointCount:         2,
			OciAdminPassword:          ociserver.OCIOCIDVersionRef{Ocid: "ocid1.secret.oc1..aaa", Version: "1"},
		}

		res, err := h.CreatePool(contextWithOpcRequestID(nil, defaultTestOPC), req, params)

		assert.NoError(tt, err)
		serverErr, ok := res.(*ociserver.CreatePoolInternalServerError)
		assert.True(tt, ok, "response should be *ociserver.CreatePoolInternalServerError")
		assert.Equal(tt, defaultTestOPC, serverErr.OpcWorkRequestID)
		assert.Equal(tt, "failed", serverErr.Response.Status)
		assert.Equal(tt, req.PoolOCID, serverErr.Response.PoolOCID)
		assert.Equal(tt, "Internal server error", serverErr.Response.ErrorMessage)
	})
}

func TestGetHealth(t *testing.T) {
	t.Run("GetHealth returns healthy when ServerState is nil", func(tt *testing.T) {
		h := NewHandler(nil)
		res, err := h.GetHealth(context.Background())
		assert.NoError(tt, err)
		health, ok := res.(*ociserver.Health)
		assert.True(tt, ok, "response should be *ociserver.Health")
		assert.True(tt, health.Status.IsSet())
		status, _ := health.Status.Get()
		assert.Equal(tt, "healthy", status)
	})

	t.Run("GetHealth returns healthy when not shutting down", func(tt *testing.T) {
		serverState := NewServerState()
		h := NewHandler(serverState)
		res, err := h.GetHealth(context.Background())
		assert.NoError(tt, err)
		health, ok := res.(*ociserver.Health)
		assert.True(tt, ok)
		status, _ := health.Status.Get()
		assert.Equal(tt, "healthy", status)
	})

	t.Run("GetHealth returns 500 when shutting down", func(tt *testing.T) {
		serverState := NewServerState()
		serverState.SetShuttingDown()
		h := NewHandler(serverState)
		res, err := h.GetHealth(context.Background())
		assert.NoError(tt, err)
		errRes, ok := res.(*ociserver.StandardError500Headers)
		assert.True(tt, ok, "response should be *ociserver.StandardError500Headers")
		assert.NotEmpty(tt, errRes.OpcWorkRequestID)
		assert.Equal(tt, float64(500), errRes.Response.Code)
		assert.Equal(tt, "Server is shutting down", errRes.Response.Message)
	})
}

func TestGetWorkflow(t *testing.T) {
	const opcWf = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"

	t.Run("GetWorkflow maps metadata VMs when query succeeds", func(tt *testing.T) {
		orig := workflowQueryFn
		workflowQueryFn = func(ctx context.Context, c client.Client, workflowID, runID string) (workflowquery.Result, error) {
			return workflowquery.Result{
				Status:       workflowquery.WorkflowStatusCompleted,
				WorkflowType: "OCICreatePoolWorkflow",
				PoolMetadata: &workflowquery.OCICreatePoolMetadata{
					PoolOCID: "ocid1.pool.oc1.iad.testpool",
					Vms: []workflowquery.OCICreatePoolVMMetadata{
						{
							Name:            "vm-01",
							SerialNumber:    "1234501",
							VSAManagementIP: "10.0.0.3",
							InterclusterIP:  "10.0.0.1",
							HAPair:          "ha_pair-1",
							InstanceID:      "ocid1.instance.oc1.iad.vm01",
						},
					},
				},
			}, nil
		}
		tt.Cleanup(func() { workflowQueryFn = orig })

		h := Handler{}
		res, err := h.GetWorkflow(contextWithOpcRequestID(nil, opcWf), ociserver.GetWorkflowParams{WorkRequestId: "wf-1"})
		assert.NoError(tt, err)
		headers, ok := res.(*ociserver.GetWorkflowStatusResponseHeaders)
		assert.True(tt, ok, "response should be *ociserver.GetWorkflowStatusResponseHeaders")
		assert.Equal(tt, opcWf, headers.OpcWorkRequestID)
		assert.Equal(tt, "completed", headers.Response.Status)
		assert.True(tt, headers.Response.PoolMetadata.IsSet())
		meta, ok := headers.Response.PoolMetadata.Get()
		assert.True(tt, ok)
		assert.Equal(tt, "ocid1.pool.oc1.iad.testpool", meta.PoolOCID,
			"poolOCID must round-trip from workflowquery.OCICreatePoolMetadata into OCICreatePoolWorkflowMetadata; the spec marks it required when poolMetadata is emitted (i.e. on completed OCICreatePoolWorkflow runs)")
		if assert.Len(tt, meta.Vms, 1) {
			assert.Equal(tt, "vm-01", meta.Vms[0].Name)
			assert.Equal(tt, "1234501", meta.Vms[0].SerialNumber)
			assert.Equal(tt, "10.0.0.3", meta.Vms[0].VsaManagementIP)
			assert.Equal(tt, "10.0.0.1", meta.Vms[0].InterclusterIP)
			assert.Equal(tt, "ha_pair-1", meta.Vms[0].HaPair,
				"haPair must be propagated verbatim from internal metadata to the OAS response; "+
					"per the schema contract haPair is 1-indexed (ha_pair-1, ha_pair-2, ...), so ha_pair-0 is not a legal wire value")
			assert.Equal(tt, "ocid1.instance.oc1.iad.vm01", meta.Vms[0].InstanceId,
				"instanceId must round-trip from workflowquery VM metadata into the OAS response VM")
		}
	})

	t.Run("GetWorkflow surfaces credentials.secret from completed pool workflow metadata", func(tt *testing.T) {
		orig := workflowQueryFn
		workflowQueryFn = func(ctx context.Context, c client.Client, workflowID, runID string) (workflowquery.Result, error) {
			return workflowquery.Result{
				Status:       workflowquery.WorkflowStatusCompleted,
				WorkflowType: "OCICreatePoolWorkflow",
				PoolMetadata: &workflowquery.OCICreatePoolMetadata{
					Credentials: &workflowquery.OCICreatePoolCredentialsMetadata{
						Secret: &workflowquery.OCICredentialRefMetadata{
							Ocid:    "FsnIdocnv-2bbd1dd79fa45f97-ontap-admin",
							Version: "3",
						},
					},
				},
			}, nil
		}
		tt.Cleanup(func() { workflowQueryFn = orig })

		h := Handler{}
		res, err := h.GetWorkflow(contextWithOpcRequestID(nil, opcWf), ociserver.GetWorkflowParams{WorkRequestId: "wf-2"})
		assert.NoError(tt, err)
		headers, ok := res.(*ociserver.GetWorkflowStatusResponseHeaders)
		assert.True(tt, ok)
		assert.Equal(tt, "completed", headers.Response.Status)
		assert.True(tt, headers.Response.PoolMetadata.IsSet())
		meta, ok := headers.Response.PoolMetadata.Get()
		assert.True(tt, ok)
		assert.Empty(tt, meta.Vms)
		assert.Equal(tt, "FsnIdocnv-2bbd1dd79fa45f97-ontap-admin", meta.Credentials.Secret.Ocid)
		assert.Equal(tt, "3", meta.Credentials.Secret.Version)
		// Certificate is reserved for future PRs and must collapse to empty placeholder.
		assert.Equal(tt, "", meta.Credentials.Certificate.Ocid)
		assert.Equal(tt, "", meta.Credentials.Certificate.Version)
	})

	t.Run("GetWorkflow emits empty credentials when pool metadata has none", func(tt *testing.T) {
		orig := workflowQueryFn
		workflowQueryFn = func(ctx context.Context, c client.Client, workflowID, runID string) (workflowquery.Result, error) {
			return workflowquery.Result{
				Status:       workflowquery.WorkflowStatusCompleted,
				WorkflowType: "OCICreatePoolWorkflow",
				PoolMetadata: &workflowquery.OCICreatePoolMetadata{
					Vms: []workflowquery.OCICreatePoolVMMetadata{{Name: "vm-x"}},
				},
			}, nil
		}
		tt.Cleanup(func() { workflowQueryFn = orig })

		h := Handler{}
		res, err := h.GetWorkflow(contextWithOpcRequestID(nil, opcWf), ociserver.GetWorkflowParams{WorkRequestId: "wf-3"})
		assert.NoError(tt, err)
		headers, ok := res.(*ociserver.GetWorkflowStatusResponseHeaders)
		assert.True(tt, ok)
		meta, ok := headers.Response.PoolMetadata.Get()
		assert.True(tt, ok)
		assert.Equal(tt, "", meta.Credentials.Secret.Ocid)
		assert.Equal(tt, "", meta.Credentials.Secret.Version)
		assert.Equal(tt, "", meta.Credentials.Certificate.Ocid)
		assert.Equal(tt, "", meta.Credentials.Certificate.Version)
	})

	t.Run("GetWorkflow surfaces clusterIP from pool metadata when assigned", func(tt *testing.T) {
		orig := workflowQueryFn
		workflowQueryFn = func(ctx context.Context, c client.Client, workflowID, runID string) (workflowquery.Result, error) {
			return workflowquery.Result{
				Status:       workflowquery.WorkflowStatusCompleted,
				WorkflowType: "OCICreatePoolWorkflow",
				PoolMetadata: &workflowquery.OCICreatePoolMetadata{
					ClusterIP: "10.38.23.99",
					Vms: []workflowquery.OCICreatePoolVMMetadata{
						{Name: "vm-01"},
					},
				},
			}, nil
		}
		tt.Cleanup(func() { workflowQueryFn = orig })

		h := Handler{}
		res, err := h.GetWorkflow(contextWithOpcRequestID(nil, opcWf), ociserver.GetWorkflowParams{WorkRequestId: "wf-cluster-ip"})
		assert.NoError(tt, err)
		headers, ok := res.(*ociserver.GetWorkflowStatusResponseHeaders)
		assert.True(tt, ok)
		meta, ok := headers.Response.PoolMetadata.Get()
		assert.True(tt, ok)
		assert.True(tt, meta.ClusterIP.IsSet(), "clusterIP must be set when VLM has assigned the cluster's RBAC LIF IP")
		assert.Equal(tt, "10.38.23.99", meta.ClusterIP.Value,
			"clusterIP is the pool's cluster-scoped address — it must be propagated unchanged from the workflow query result")
	})

	t.Run("GetWorkflow omits clusterIP when not yet assigned", func(tt *testing.T) {
		orig := workflowQueryFn
		workflowQueryFn = func(ctx context.Context, c client.Client, workflowID, runID string) (workflowquery.Result, error) {
			return workflowquery.Result{
				Status:       workflowquery.WorkflowStatusCompleted,
				WorkflowType: "OCICreatePoolWorkflow",
				PoolMetadata: &workflowquery.OCICreatePoolMetadata{
					Vms: []workflowquery.OCICreatePoolVMMetadata{{Name: "vm-01"}},
				},
			}, nil
		}
		tt.Cleanup(func() { workflowQueryFn = orig })

		h := Handler{}
		res, err := h.GetWorkflow(contextWithOpcRequestID(nil, opcWf), ociserver.GetWorkflowParams{WorkRequestId: "wf-cluster-ip-empty"})
		assert.NoError(tt, err)
		headers, ok := res.(*ociserver.GetWorkflowStatusResponseHeaders)
		assert.True(tt, ok)
		meta, ok := headers.Response.PoolMetadata.Get()
		assert.True(tt, ok)
		assert.False(tt, meta.ClusterIP.IsSet(),
			"clusterIP must read as absent (not empty string) until VLM assigns the RBAC LIF — the schema documents this contract for clients")
	})

	t.Run("GetWorkflow surfaces mediator and pool-level iops/throughput", func(tt *testing.T) {
		orig := workflowQueryFn
		workflowQueryFn = func(ctx context.Context, c client.Client, workflowID, runID string) (workflowquery.Result, error) {
			return workflowquery.Result{
				Status:       workflowquery.WorkflowStatusCompleted,
				WorkflowType: "OCICreatePoolWorkflow",
				PoolMetadata: &workflowquery.OCICreatePoolMetadata{
					Vms: []workflowquery.OCICreatePoolVMMetadata{{Name: "vm-01"}},
					Mediator: &workflowquery.OCICreatePoolMediatorMetadata{
						Name:       "mediator-01",
						IP:         "10.20.27.35",
						HAPair:     "ha_pair-1",
						InstanceID: "ocid1.instance.oc1.iad.mediator01",
					},
					IOPS:           15248,
					ThroughputGBps: 1.23456,
				},
			}, nil
		}
		tt.Cleanup(func() { workflowQueryFn = orig })

		h := Handler{}
		res, err := h.GetWorkflow(contextWithOpcRequestID(nil, opcWf), ociserver.GetWorkflowParams{WorkRequestId: "wf-mediator"})
		assert.NoError(tt, err)
		headers, ok := res.(*ociserver.GetWorkflowStatusResponseHeaders)
		assert.True(tt, ok)
		meta, ok := headers.Response.PoolMetadata.Get()
		assert.True(tt, ok)

		mediator, ok := meta.Mediator.Get()
		assert.True(tt, ok, "mediator must be set when the pool has a mediator configured")
		assert.Equal(tt, "mediator-01", mediator.Name,
			"mediator name must propagate unchanged from the workflow query result")
		assert.Equal(tt, "10.20.27.35", mediator.IP,
			"mediator ip must propagate unchanged from the workflow query result")
		assert.Equal(tt, "ha_pair-1", mediator.HaPair,
			"mediator haPair label must propagate unchanged")
		assert.Equal(tt, "ocid1.instance.oc1.iad.mediator01", mediator.InstanceId,
			"mediator instanceId must propagate unchanged from the workflow query result")

		assert.Equal(tt, int64(15248), meta.Iops, "pool-level iops must be set")
		assert.Equal(tt, 1.23, meta.ThroughputGBps,
			"throughputGBps must be rounded to 2 decimal places in the final response (1.23456 -> 1.23)")
	})

	t.Run("GetWorkflow omits mediator and returns zero iops/throughput when absent", func(tt *testing.T) {
		orig := workflowQueryFn
		workflowQueryFn = func(ctx context.Context, c client.Client, workflowID, runID string) (workflowquery.Result, error) {
			return workflowquery.Result{
				Status:       workflowquery.WorkflowStatusCompleted,
				WorkflowType: "OCICreatePoolWorkflow",
				PoolMetadata: &workflowquery.OCICreatePoolMetadata{
					Vms: []workflowquery.OCICreatePoolVMMetadata{{Name: "vm-01"}},
				},
			}, nil
		}
		tt.Cleanup(func() { workflowQueryFn = orig })

		h := Handler{}
		res, err := h.GetWorkflow(contextWithOpcRequestID(nil, opcWf), ociserver.GetWorkflowParams{WorkRequestId: "wf-no-mediator"})
		assert.NoError(tt, err)
		headers, ok := res.(*ociserver.GetWorkflowStatusResponseHeaders)
		assert.True(tt, ok)
		meta, ok := headers.Response.PoolMetadata.Get()
		assert.True(tt, ok)
		assert.False(tt, meta.Mediator.IsSet(), "mediator must be absent when the pool has none configured")
		assert.Equal(tt, int64(0), meta.Iops, "iops must be present with zero when not reported")
		assert.Equal(tt, float64(0), meta.ThroughputGBps, "throughputGBps must be present with zero when not reported")
	})

	t.Run("GetWorkflow maps SVM metadata when query returns SVM result", func(tt *testing.T) {
		orig := workflowQueryFn
		haPair := "ha_pair-1"
		workflowQueryFn = func(ctx context.Context, c client.Client, workflowID, runID string) (workflowquery.Result, error) {
			return workflowquery.Result{
				Status:       workflowquery.WorkflowStatusCompleted,
				WorkflowType: "OCICreateSVMWorkflow",
				SvmMetadata: &workflowquery.OCICreateSVMMetadata{
					Name:    "svm-1",
					SvmOCID: "ocid1.svm",
					Lifs: []workflowquery.OCICreateSVMLifMetadata{
						{Name: "lif1", IP: "10.0.0.1", Node: "node1", NodeUUID: "node-uuid-1", HaPair: &haPair, Protocols: []string{"nfs", "cifs", "s3"}},
					},
				},
			}, nil
		}
		tt.Cleanup(func() { workflowQueryFn = orig })

		h := Handler{}
		res, err := h.GetWorkflow(contextWithOpcRequestID(nil, opcWf), ociserver.GetWorkflowParams{WorkRequestId: "wf-svm"})
		assert.NoError(tt, err)
		headers, ok := res.(*ociserver.GetWorkflowStatusResponseHeaders)
		assert.True(tt, ok)
		assert.Equal(tt, "completed", headers.Response.Status)
		assert.True(tt, headers.Response.SvmMetadata.IsSet())
		svmMeta, ok := headers.Response.SvmMetadata.Get()
		assert.True(tt, ok)
		name, _ := svmMeta.Name.Get()
		assert.Equal(tt, "svm-1", name)
		svmOCID, _ := svmMeta.SvmOCID.Get()
		assert.Equal(tt, "ocid1.svm", svmOCID)
		if assert.Len(tt, svmMeta.Lifs, 1) {
			nodeUUID, _ := svmMeta.Lifs[0].NodeUUID.Get()
			assert.Equal(tt, "node-uuid-1", nodeUUID)
			haPair, ok := svmMeta.Lifs[0].HaPair.Get()
			assert.True(tt, ok)
			assert.Equal(tt, "ha_pair-1", haPair)
		}
	})

	t.Run("mapGetWorkflowQueryError returns 404 for not found error", func(tt *testing.T) {
		res := mapGetWorkflowQueryError(opcWf, errors.New("workflow not found"))
		errRes, ok := res.(*ociserver.GetWorkflowNotFound)
		assert.True(tt, ok, "response should be *ociserver.GetWorkflowNotFound")
		assert.Equal(tt, opcWf, errRes.OpcWorkRequestID)
		assert.Equal(tt, "workflow not found", errRes.Response.ErrorMessage)
	})

	t.Run("mapGetWorkflowQueryError returns 500 for other errors", func(tt *testing.T) {
		res := mapGetWorkflowQueryError(opcWf, errors.New("temporal unavailable"))
		errRes, ok := res.(*ociserver.GetWorkflowInternalServerError)
		assert.True(tt, ok, "response should be *ociserver.GetWorkflowInternalServerError")
		assert.Equal(tt, opcWf, errRes.OpcWorkRequestID)
		assert.Equal(tt, "temporal unavailable", errRes.Response.ErrorMessage)
	})
}

func TestDeletePool(t *testing.T) {
	t.Run("DeletePool returns 202 with OperationV1beta and headers when orchestrator returns in-progress", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().DeletePool(mock.Anything, mock.Anything).Return(&models.Pool{
			BaseModel:      models.BaseModel{UUID: "550e8400-e29b-41d4-a716-446655440000", CreatedAt: time.Now(), UpdatedAt: time.Now()},
			Name:           "mypool",
			State:          datamodel.LifeCycleStateDeleting,
			PoolAttributes: &models.PoolAttributes{PrimaryZone: "ad1", SecondaryZone: "ad2"},
		}, "op-123", nil)
		h := Handler{Orchestrator: mockOrchestrator}
		params := ociserver.DeletePoolParams{PoolOCID: "ocid1.pool.oc1.iad.testdelete", TenancyOcid: "ocid1.tenancy.oc1..aaa"}
		res, err := h.DeletePool(contextWithOpcRequestID(nil, defaultTestOPC), params)

		assert.NoError(tt, err)
		headers, ok := res.(*ociserver.DeletePoolAcceptedResponseHeaders)
		assert.True(tt, ok, "response should be *ociserver.DeletePoolAcceptedResponseHeaders")
		assert.Equal(tt, defaultTestOPC, headers.OpcWorkRequestID)
		assert.Equal(tt, "in_progress", headers.Response.Status)
		assert.Equal(tt, "op-123", headers.Response.WorkflowId)
		assert.Equal(tt, params.PoolOCID, headers.Response.PoolOCID)
	})

	t.Run("DeletePool returns 204 NoContent when delete completed", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().DeletePool(mock.Anything, mock.Anything).Return(&models.Pool{
			BaseModel:      models.BaseModel{UUID: "550e8400-e29b-41d4-a716-446655440000", CreatedAt: time.Now(), UpdatedAt: time.Now()},
			Name:           "mypool",
			State:          datamodel.LifeCycleStateDeleted,
			PoolAttributes: &models.PoolAttributes{PrimaryZone: "ad1", SecondaryZone: "ad2"},
		}, "op-456", nil)
		h := Handler{Orchestrator: mockOrchestrator}
		params := ociserver.DeletePoolParams{PoolOCID: "ocid1.pool.oc1.iad.testdelete", TenancyOcid: "ocid1.tenancy.oc1..aaa"}
		res, err := h.DeletePool(contextWithOpcRequestID(nil, defaultTestOPC), params)

		assert.NoError(tt, err)
		noContent, ok := res.(*ociserver.DeletePoolNoContent)
		assert.True(tt, ok, "response should be *ociserver.DeletePoolNoContent")
		assert.Equal(tt, defaultTestOPC, noContent.OpcWorkRequestID)
	})

	t.Run("DeletePool echoes opc-request-id when provided", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().DeletePool(mock.Anything, mock.Anything).Return(&models.Pool{
			BaseModel:      models.BaseModel{UUID: "pool-uuid", CreatedAt: time.Now(), UpdatedAt: time.Now()},
			Name:           "mypool",
			State:          datamodel.LifeCycleStateDeleting,
			PoolAttributes: &models.PoolAttributes{PrimaryZone: "ad1", SecondaryZone: "ad2"},
		}, "op-echo", nil)
		h := Handler{Orchestrator: mockOrchestrator}
		params := ociserver.DeletePoolParams{PoolOCID: "ocid1.pool.oc1.iad.testecho", TenancyOcid: "ocid1.tenancy.oc1..aaa"}
		res, err := h.DeletePool(contextWithOpcRequestID(nil, "delete-request-id"), params)

		assert.NoError(tt, err)
		headers := res.(*ociserver.DeletePoolAcceptedResponseHeaders)
		assert.Equal(tt, "delete-request-id", headers.OpcWorkRequestID)
	})

	t.Run("DeletePool returns 404 style body when pool not found", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().DeletePool(mock.Anything, mock.Anything).Return(nil, "", utilserrors.NewNotFoundErr("pool not found", nil))
		h := Handler{Orchestrator: mockOrchestrator}
		params := ociserver.DeletePoolParams{PoolOCID: "ocid1.pool.oc1.iad.testdelete", TenancyOcid: "ocid1.tenancy.oc1..aaa"}
		res, err := h.DeletePool(contextWithOpcRequestID(nil, defaultTestOPC), params)

		assert.NoError(tt, err)
		headers, ok := res.(*ociserver.DeletePoolNotFound)
		assert.True(tt, ok)
		assert.Equal(tt, defaultTestOPC, headers.OpcWorkRequestID)
		assert.Equal(tt, "failed", headers.Response.Status)
		assert.Equal(tt, params.PoolOCID, headers.Response.PoolOCID)
		assert.Contains(tt, headers.Response.ErrorMessage, "not found")
	})

	t.Run("DeletePool returns 400 when orchestrator returns bad request", func(tt *testing.T) {
		// Handler echoes err.Error(); assert against the same error so mock text and expectation cannot drift.
		orchestratorErr := utilserrors.NewBadRequestErr("pool cannot be deleted with active volumes")
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().DeletePool(mock.Anything, mock.Anything).Return(nil, "", orchestratorErr)
		h := Handler{Orchestrator: mockOrchestrator}
		params := ociserver.DeletePoolParams{PoolOCID: "ocid1.pool.oc1..x", TenancyOcid: "ocid1.compartment.oc1..aaa"}
		res, err := h.DeletePool(contextWithOpcRequestID(nil, defaultTestOPC), params)
		assert.NoError(tt, err)
		bad, ok := res.(*ociserver.DeletePoolBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, defaultTestOPC, bad.OpcWorkRequestID)
		assert.Equal(tt, orchestratorErr.Error(), bad.Response.ErrorMessage)
	})

	t.Run("DeletePool returns 409 when orchestrator returns conflict", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().DeletePool(mock.Anything, mock.Anything).Return(nil, "", utilserrors.NewConflictErr("in transition"))
		h := Handler{Orchestrator: mockOrchestrator}
		params := ociserver.DeletePoolParams{PoolOCID: "ocid1.pool.oc1..x", TenancyOcid: "ocid1.tenancy.oc1..aaa"}
		res, err := h.DeletePool(contextWithOpcRequestID(nil, defaultTestOPC), params)
		assert.NoError(tt, err)
		conf, ok := res.(*ociserver.DeletePoolConflict)
		assert.True(tt, ok)
		assert.Equal(tt, defaultTestOPC, conf.OpcWorkRequestID)
		assert.Equal(tt, params.PoolOCID, conf.Response.PoolOCID)
		assert.Contains(tt, conf.Response.ErrorMessage, "Error deleting pool",
			"handler must prefix the wrapped orchestrator conflict error so callers can distinguish from validation 409s")
		assert.Contains(tt, conf.Response.ErrorMessage, "in transition",
			"original orchestrator error must be preserved (not flattened) when wrapping")
	})

	t.Run("DeletePool returns 500 on generic orchestrator error", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().DeletePool(mock.Anything, mock.Anything).Return(nil, "", errors.New("upstream failure"))
		h := Handler{Orchestrator: mockOrchestrator}
		params := ociserver.DeletePoolParams{PoolOCID: "ocid1.pool.oc1..x", TenancyOcid: "ocid1.compartment.oc1..aaa"}
		res, err := h.DeletePool(contextWithOpcRequestID(nil, defaultTestOPC), params)
		assert.NoError(tt, err)
		srv, ok := res.(*ociserver.DeletePoolInternalServerError)
		assert.True(tt, ok)
		assert.Equal(tt, "Internal server error", srv.Response.ErrorMessage)
	})

	t.Run("DeletePool returns 202 when pool is still CREATING", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().DeletePool(mock.Anything, mock.Anything).Return(&models.Pool{
			BaseModel: models.BaseModel{UUID: "u1"},
			State:     datamodel.LifeCycleStateCreating,
		}, "wf-del", nil)
		h := Handler{Orchestrator: mockOrchestrator}
		params := ociserver.DeletePoolParams{PoolOCID: "ocid1.pool.oc1..x", TenancyOcid: "ocid1.compartment.oc1..aaa"}
		res, err := h.DeletePool(contextWithOpcRequestID(nil, defaultTestOPC), params)
		assert.NoError(tt, err)
		acc, ok := res.(*ociserver.DeletePoolAcceptedResponseHeaders)
		assert.True(tt, ok)
		assert.Equal(tt, "wf-del", acc.Response.WorkflowId)
	})
}

func TestOpcWorkRequestIDFromContext(t *testing.T) {
	t.Run("no header map in context", func(t *testing.T) {
		_, err := opcWorkRequestIDFromContext(context.Background())
		assert.Error(t, err)
	})
	t.Run("missing opc-request-id", func(t *testing.T) {
		h := make(http.Header)
		h.Set(string(middleware.OPCWorkRequestIDHeaderName), "my-id")
		ctx := context.WithValue(context.Background(), middleware.HeaderContextKey, h)
		_, err := opcWorkRequestIDFromContext(ctx)
		assert.Error(t, err)
	})
	t.Run("empty opc-work-request-id after trim", func(t *testing.T) {
		h := make(http.Header)
		h.Set(string(middleware.OPCRequestIDHeaderName), "corr-1")
		h.Set(string(middleware.OPCWorkRequestIDHeaderName), "   ")
		ctx := context.WithValue(context.Background(), middleware.HeaderContextKey, h)
		_, err := opcWorkRequestIDFromContext(ctx)
		assert.Error(t, err)
	})
	t.Run("returns trimmed id", func(t *testing.T) {
		h := make(http.Header)
		h.Set(string(middleware.OPCRequestIDHeaderName), "corr-1")
		h.Set(string(middleware.OPCWorkRequestIDHeaderName), "  my-id  ")
		ctx := context.WithValue(context.Background(), middleware.HeaderContextKey, h)
		id, err := opcWorkRequestIDFromContext(ctx)
		assert.NoError(t, err)
		assert.Equal(t, "my-id", id)
	})
}

func TestCreatePool_MissingOPCRequestID(t *testing.T) {
	mockOrchestrator := factory.NewMockOrchestratorFactory(t)
	h := Handler{Orchestrator: mockOrchestrator}
	req := &ociserver.CreatePoolRequest{
		PoolOCID:                  "ocid1.pool.oc1..aaa",
		CompartmentOCID:           "ocid1.compartment.oc1..aaa",
		ResourceName:               "p",
		SubnetId:                  "ocid1.subnet.oc1..aaa",
		DataNicSubnetId:           "ocid1.subnet.oc1..data",
		PrimaryAvailabilityDomain: "ad1",
		SizeInGiB:                 1024,
		ThroughputGBps:            1.0,
		DataEndpointCount:         2,
		OciAdminPassword:          ociserver.OCIOCIDVersionRef{Ocid: "ocid1.secret.oc1..aaa", Version: "1"},
	}
	res, err := h.CreatePool(context.Background(), req, defaultCreatePoolParams())
	assert.NoError(t, err)
	bad, ok := res.(*ociserver.CreatePoolBadRequest)
	assert.True(t, ok)
	assert.Equal(t, invalidOPCRequestID, bad.Response.ErrorMessage)
}

func TestCreatePool_EmptyWorkflowID(t *testing.T) {
	mockOrchestrator := factory.NewMockOrchestratorFactory(t)
	mockOrchestrator.EXPECT().CreatePool(mock.Anything, mock.Anything).Return(&models.Pool{
		BaseModel: models.BaseModel{UUID: "pool-uuid-1", CreatedAt: time.Now(), UpdatedAt: time.Now()},
		Name:      "test-pool",
		State:     "CREATING",
	}, "", nil)
	h := Handler{Orchestrator: mockOrchestrator}
	req := &ociserver.CreatePoolRequest{
		PoolOCID:                  "ocid1.pool.oc1..aaa",
		CompartmentOCID:           "ocid1.compartment.oc1..aaa",
		ResourceName:               "test-pool",
		SubnetId:                  "ocid1.subnet.oc1..aaa",
		DataNicSubnetId:           "ocid1.subnet.oc1..data",
		PrimaryAvailabilityDomain: "ad1",
		SizeInGiB:                 1024,
		ThroughputGBps:            1.0,
		DataEndpointCount:         2,
		OciAdminPassword:          ociserver.OCIOCIDVersionRef{Ocid: "ocid1.secret.oc1..aaa", Version: "1"},
	}
	res, err := h.CreatePool(contextWithOpcRequestID(nil, defaultTestOPC), req, defaultCreatePoolParams())
	assert.NoError(t, err)
	headers, ok := res.(*ociserver.CreatePoolAcceptedResponseHeaders)
	assert.True(t, ok)
	assert.Equal(t, "", headers.Response.WorkflowId)
}

func TestCreatePool_OddDataEndpointCountRejected(t *testing.T) {
	mockOrchestrator := factory.NewMockOrchestratorFactory(t)
	h := Handler{Orchestrator: mockOrchestrator}
	req := &ociserver.CreatePoolRequest{
		PoolOCID:                  "ocid1.pool.oc1..odd-count",
		CompartmentOCID:           "ocid1.compartment.oc1..aaa",
		ResourceName:               "odd-count-pool",
		SubnetId:                  "ocid1.subnet.oc1..aaa",
		DataNicSubnetId:           "ocid1.subnet.oc1..data",
		PrimaryAvailabilityDomain: "ad1",
		SizeInGiB:                 512,
		ThroughputGBps:            1.0,
		DataEndpointCount:         3, // odd: must be rejected
		OciAdminPassword:          ociserver.OCIOCIDVersionRef{Ocid: "ocid1.secret.oc1..aaa", Version: "1"},
	}

	res, err := h.CreatePool(contextWithOpcRequestID(nil, defaultTestOPC), req, defaultCreatePoolParams())

	assert.NoError(t, err)
	bad, ok := res.(*ociserver.CreatePoolBadRequest)
	assert.True(t, ok, "response should be *ociserver.CreatePoolBadRequest")
	assert.Equal(t, defaultTestOPC, bad.OpcWorkRequestID)
	assert.Equal(t, req.PoolOCID, bad.Response.PoolOCID)
	assert.Equal(t, string(workflowquery.WorkflowStatusFailed), bad.Response.Status)
	assert.Equal(t, errMsgOddDataEndpointCount, bad.Response.ErrorMessage)
}

func TestGetWorkflow_MissingOPCRequestID(t *testing.T) {
	orig := workflowQueryFn
	workflowQueryFn = func(ctx context.Context, c client.Client, workflowID, runID string) (workflowquery.Result, error) {
		return workflowquery.Result{
			Status:       workflowquery.WorkflowStatusInProgress,
			WorkflowType: "OCICreatePoolWorkflow",
		}, nil
	}
	t.Cleanup(func() { workflowQueryFn = orig })

	h := Handler{}
	res, err := h.GetWorkflow(context.Background(), ociserver.GetWorkflowParams{WorkRequestId: "wf-1"})
	assert.NoError(t, err)
	headers, ok := res.(*ociserver.GetWorkflowStatusResponseHeaders)
	assert.True(t, ok)
	assert.Equal(t, "wf-1", headers.OpcWorkRequestID)
}

func TestCreatePool_AdminPasswordVersionNotParseable(t *testing.T) {
	mockOrchestrator := factory.NewMockOrchestratorFactory(t)
	h := Handler{Orchestrator: mockOrchestrator}
	req := &ociserver.CreatePoolRequest{
		PoolOCID:                  "ocid1.pool.oc1..aaa",
		CompartmentOCID:           "ocid1.compartment.oc1..aaa",
		ResourceName:               "p",
		SubnetId:                  "ocid1.subnet.oc1..aaa",
		DataNicSubnetId:           "ocid1.subnet.oc1..data",
		PrimaryAvailabilityDomain: "ad1",
		SizeInGiB:                 1024,
		ThroughputGBps:            1.0,
		DataEndpointCount:         2,
		OciAdminPassword:          ociserver.OCIOCIDVersionRef{Ocid: "ocid1.secret.oc1..aaa", Version: "not-a-number"},
	}

	res, err := h.CreatePool(contextWithOpcRequestID(nil, defaultTestOPC), req, defaultCreatePoolParams())

	assert.NoError(t, err)
	bad, ok := res.(*ociserver.CreatePoolBadRequest)
	assert.True(t, ok, "response should be *ociserver.CreatePoolBadRequest")
	assert.Equal(t, defaultTestOPC, bad.OpcWorkRequestID)
	assert.Equal(t, req.PoolOCID, bad.Response.PoolOCID)
	assert.Equal(t, errMsgInvalidAdminPasswordVersion, bad.Response.ErrorMessage)
}

func TestCreatePool_AdminPasswordVersionLessThanOne(t *testing.T) {
	mockOrchestrator := factory.NewMockOrchestratorFactory(t)
	h := Handler{Orchestrator: mockOrchestrator}
	req := &ociserver.CreatePoolRequest{
		PoolOCID:                  "ocid1.pool.oc1..aaa",
		CompartmentOCID:           "ocid1.compartment.oc1..aaa",
		ResourceName:               "p",
		SubnetId:                  "ocid1.subnet.oc1..aaa",
		DataNicSubnetId:           "ocid1.subnet.oc1..data",
		PrimaryAvailabilityDomain: "ad1",
		SizeInGiB:                 1024,
		ThroughputGBps:            1.0,
		DataEndpointCount:         2,
		OciAdminPassword:          ociserver.OCIOCIDVersionRef{Ocid: "ocid1.secret.oc1..aaa", Version: "0"},
	}

	res, err := h.CreatePool(contextWithOpcRequestID(nil, defaultTestOPC), req, defaultCreatePoolParams())

	assert.NoError(t, err)
	bad, ok := res.(*ociserver.CreatePoolBadRequest)
	assert.True(t, ok, "response should be *ociserver.CreatePoolBadRequest")
	assert.Equal(t, defaultTestOPC, bad.OpcWorkRequestID)
	assert.Equal(t, req.PoolOCID, bad.Response.PoolOCID)
	assert.Equal(t, errMsgAdminPasswordVersionLessThanOne, bad.Response.ErrorMessage)
}

func TestGetWorkflow_NodeUUIDEnrichment(t *testing.T) {
	const opcWf = "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"

	t.Run("NodeUUIDPopulatedWhenOrchestratorReturnsNodes", func(tt *testing.T) {
		orig := workflowQueryFn
		workflowQueryFn = func(ctx context.Context, c client.Client, workflowID, runID string) (workflowquery.Result, error) {
			return workflowquery.Result{
				Status:       workflowquery.WorkflowStatusCompleted,
				WorkflowType: "OCICreatePoolWorkflow",
				PoolMetadata: &workflowquery.OCICreatePoolMetadata{
					PoolUUID: "pool-uuid-enriched",
					Vms: []workflowquery.OCICreatePoolVMMetadata{
						{Name: "vm-01"},
					},
				},
			}, nil
		}
		tt.Cleanup(func() { workflowQueryFn = orig })

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().
			GetNodesByPoolUUID(mock.Anything, "pool-uuid-enriched").
			Return([]*datamodel.Node{
				{
					BaseModel:      datamodel.BaseModel{UUID: "node-uuid-1"},
					Name:           "vm-01",
					NodeAttributes: &datamodel.NodeDetails{ExternalUUID: "ontap-uuid-1"},
				},
			}, nil)

		h := Handler{Orchestrator: mockOrchestrator}
		res, err := h.GetWorkflow(contextWithOpcRequestID(nil, opcWf), ociserver.GetWorkflowParams{WorkRequestId: "wf-enrich"})

		assert.NoError(tt, err)
		headers, ok := res.(*ociserver.GetWorkflowStatusResponseHeaders)
		assert.True(tt, ok)
		meta, ok := headers.Response.PoolMetadata.Get()
		assert.True(tt, ok)
		if assert.Len(tt, meta.Vms, 1) {
			assert.Equal(tt, "vm-01", meta.Vms[0].Name)
			assert.Equal(tt, "node-uuid-1", meta.Vms[0].NodeUUID)
			assert.Equal(tt, "ontap-uuid-1", meta.Vms[0].OntapNodeUUID,
				"ontapNodeUUID must be sourced from node_attributes.external_uuid")
		}
	})

	t.Run("GetWorkflowReturnsErrorWhenNodeUUIDLookupFails", func(tt *testing.T) {
		orig := workflowQueryFn
		workflowQueryFn = func(ctx context.Context, c client.Client, workflowID, runID string) (workflowquery.Result, error) {
			return workflowquery.Result{
				Status:       workflowquery.WorkflowStatusCompleted,
				WorkflowType: "OCICreatePoolWorkflow",
				PoolMetadata: &workflowquery.OCICreatePoolMetadata{
					PoolUUID: "pool-uuid-fail",
					Vms: []workflowquery.OCICreatePoolVMMetadata{
						{Name: "vm-01"},
					},
				},
			}, nil
		}
		tt.Cleanup(func() { workflowQueryFn = orig })

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().
			GetNodesByPoolUUID(mock.Anything, "pool-uuid-fail").
			Return(nil, fmt.Errorf("db down"))

		h := Handler{Orchestrator: mockOrchestrator}
		res, err := h.GetWorkflow(contextWithOpcRequestID(nil, opcWf), ociserver.GetWorkflowParams{WorkRequestId: "wf-fail"})

		assert.NoError(tt, err)
		headers, ok := res.(*ociserver.GetWorkflowInternalServerError)
		assert.True(tt, ok)
		assert.Equal(tt, opcWf, headers.OpcWorkRequestID)
		assert.Contains(tt, headers.Response.ErrorMessage, "node UUID enrichment failed")
		assert.Contains(tt, headers.Response.ErrorMessage, "db down")
	})
}

func TestDeletePool_MissingOPCRequestID(t *testing.T) {
	h := Handler{}
	res, err := h.DeletePool(context.Background(), ociserver.DeletePoolParams{PoolOCID: "ocid1.pool.oc1..aaa"})
	assert.NoError(t, err)
	bad, ok := res.(*ociserver.DeletePoolBadRequest)
	assert.True(t, ok)
	assert.Equal(t, invalidOPCRequestID, bad.Response.ErrorMessage)
}

func TestCreatePool_MapsTopLevelFieldsToOrchestratorParams(t *testing.T) {
	mockOrchestrator := factory.NewMockOrchestratorFactory(t)
	// Use shared HA (secondary unset) so DataEndpointCount=4 is allowed; this test
	// pins the SizeInGiB→bytes, DataEndpointCount→HAPairs, and ThroughputGBps→MiBps
	// mappings. Non-shared HA forces DataEndpointCount=2 (covered separately).
	req := &ociserver.CreatePoolRequest{
		PoolOCID:                  "ocid1.pool.oc1..top-level",
		CompartmentOCID:           "ocid1.compartment.oc1..testcomp",
		ResourceName:               "top-level-pool",
		SubnetId:                  "ocid1.subnet.oc1.iad.testsubnet",
		DataNicSubnetId:           "ocid1.subnet.oc1.iad.testdatasubnet",
		PrimaryAvailabilityDomain: "ad1",
		SizeInGiB:                 200,
		ThroughputGBps:            2,
		DataEndpointCount:         4,
		OciAdminPassword:          ociserver.OCIOCIDVersionRef{Ocid: "ocid1.secret.oc1..aaa", Version: "1"},
	}
	mockOrchestrator.EXPECT().
		CreatePool(mock.Anything, mock.MatchedBy(func(p *commonparams.CreatePoolParams) bool {
			return p != nil &&
				p.SizeInBytes == 200*1024*1024*1024 &&
				p.HAPairs == 2 && // DataEndpointCount(4) / 2
				p.CustomPerformanceParams != nil &&
				p.CustomPerformanceParams.ThroughputMibps == gbpsToMibps(2) &&
				p.CustomPerformanceParams.Iops == nil &&
				p.PrimaryZone == "ad1" &&
				p.SecondaryZone == "ad1" && // shared HA → secondary defaults to primary
				p.IsRegionalHA == false // shared HA
		})).
		Return(&models.Pool{
			BaseModel: models.BaseModel{UUID: "pool-top-level", CreatedAt: time.Now(), UpdatedAt: time.Now()},
			Name:      req.ResourceName,
			State:     "CREATING",
		}, "work-req-top-level", nil)
	h := Handler{Orchestrator: mockOrchestrator}

	res, err := h.CreatePool(contextWithOpcRequestID(nil, defaultTestOPC), req, defaultCreatePoolParams())

	assert.NoError(t, err)
	headers, ok := res.(*ociserver.CreatePoolAcceptedResponseHeaders)
	assert.True(t, ok, "response should be *ociserver.CreatePoolAcceptedResponseHeaders")
	assert.Equal(t, "work-req-top-level", headers.Response.WorkflowId)
}

// TestCreatePool_AvailabilityDomainDerivation pins the post-validation derivation
// of SecondaryZone, MediatorZone and IsRegionalHA on the orchestrator params.
//
// Semantics under the current handler (mediator handling is uniform across both
// HA modes; only secondary/IsRegionalHA differ):
//   - SecondaryZone:
//   - Shared HA (secondary unset, empty, or equal to primary): defaults to primary.
//   - Non-shared HA: SecondaryZone = the supplied secondary value.
//   - IsRegionalHA: false when shared HA, true when non-shared HA.
//   - MediatorZone (same rule in both branches):
//   - = MediatorAvailabilityDomain.Value when non-empty (passed through), otherwise
//   - = primary (defaulted) when MediatorAvailabilityDomain is unset or empty.
func TestCreatePool_AvailabilityDomainDerivation(t *testing.T) {
	cases := []struct {
		name          string
		primary       string
		secondary     ociserver.OptString
		mediator      ociserver.OptString
		endpointCount int64
		wantSecondary string // expected SecondaryZone on the orchestrator-bound params
		wantMediator  string // expected MediatorZone on the orchestrator-bound params
		wantRegHA     bool
		description   string
	}{
		{
			name:          "NonSharedHA_MediatorProvided_PassesThrough",
			primary:       "ad1",
			secondary:     ociserver.OptString{Value: "ad2", Set: true},
			mediator:      ociserver.OptString{Value: "ad3", Set: true},
			endpointCount: 2,
			wantSecondary: "ad2",
			wantMediator:  "ad3", // non-empty mediator passed through verbatim
			wantRegHA:     true,
			description:   "non-shared HA with mediator provided → mediatorAD = supplied value",
		},
		{
			name:          "NonSharedHA_MediatorUnset_DefaultsToPrimary",
			primary:       "ad1",
			secondary:     ociserver.OptString{Value: "ad2", Set: true},
			mediator:      ociserver.OptString{},
			endpointCount: 2,
			wantSecondary: "ad2",
			wantMediator:  "ad1", // mediator unset → defaulted to primary
			wantRegHA:     true,
			description:   "non-shared HA without mediator → mediatorAD = primary",
		},
		{
			name:          "NonSharedHA_MediatorEmptyButSet_DefaultsToPrimary",
			primary:       "ad1",
			secondary:     ociserver.OptString{Value: "ad2", Set: true},
			mediator:      ociserver.OptString{Value: "", Set: true},
			endpointCount: 2,
			wantSecondary: "ad2",
			wantMediator:  "ad1", // empty value treated identically to unset
			wantRegHA:     true,
			description:   "non-shared HA with empty-but-set mediator → mediatorAD = primary",
		},
		{
			name:          "SharedHA_MediatorProvided_PassesThrough",
			primary:       "ad1",
			secondary:     ociserver.OptString{Value: "ad1", Set: true},
			mediator:      ociserver.OptString{Value: "ad9", Set: true}, // distinct value: proves pass-through, not coincidence
			endpointCount: 2,
			wantSecondary: "ad1",
			wantMediator:  "ad9", // shared HA does NOT clear mediator; supplied value flows through
			wantRegHA:     false,
			description:   "shared HA with mediator provided → mediatorAD = supplied value (not cleared)",
		},
		{
			name:          "SharedHA_SecondaryAndMediatorUnset_MediatorDefaultsToPrimary",
			primary:       "ad1",
			secondary:     ociserver.OptString{},
			mediator:      ociserver.OptString{},
			endpointCount: 4, // shared HA imposes no DataEndpointCount==2 rule
			wantSecondary: "ad1",
			wantMediator:  "ad1", // defaulted to primary because mediator was empty
			wantRegHA:     false,
			description:   "secondary unset → shared HA → secondary = primary, mediator defaulted to primary; count > 2 allowed",
		},
		{
			name:          "SharedHA_SecondaryAndMediatorEmptyButSet_MediatorDefaultsToPrimary",
			primary:       "ad1",
			secondary:     ociserver.OptString{Value: "", Set: true},
			mediator:      ociserver.OptString{Value: "", Set: true},
			endpointCount: 2,
			wantSecondary: "ad1",
			wantMediator:  "ad1", // defaulted to primary because mediator was empty-but-set
			wantRegHA:     false,
			description:   "secondary and mediator explicitly empty → shared HA, mediator defaulted to primary",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(tt *testing.T) {
			mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
			req := validCreatePoolRequest()
			req.PoolOCID = "ocid1.pool.oc1..ad-deriv"
			req.PrimaryAvailabilityDomain = tc.primary
			req.SecondaryAvailabilityDomain = tc.secondary
			req.MediatorAvailabilityDomain = tc.mediator
			req.DataEndpointCount = tc.endpointCount

			mockOrchestrator.EXPECT().
				CreatePool(mock.Anything, mock.MatchedBy(func(p *commonparams.CreatePoolParams) bool {
					return p != nil &&
						p.PrimaryZone == tc.primary &&
						p.SecondaryZone == tc.wantSecondary &&
						p.MediatorZone == tc.wantMediator &&
						p.IsRegionalHA == tc.wantRegHA
				})).
				Return(&models.Pool{
					BaseModel: models.BaseModel{UUID: "pool-ad-deriv", CreatedAt: time.Now(), UpdatedAt: time.Now()},
					Name:      req.ResourceName,
					State:     "CREATING",
				}, "work-req-ad-deriv", nil)
			h := Handler{Orchestrator: mockOrchestrator}

			res, err := h.CreatePool(contextWithOpcRequestID(nil, defaultTestOPC), req, defaultCreatePoolParams())

			assert.NoError(tt, err, tc.description)
			_, ok := res.(*ociserver.CreatePoolAcceptedResponseHeaders)
			assert.True(tt, ok, "expected accepted response for %s", tc.description)
		})
	}
}

func TestNewError(t *testing.T) {
	t.Run("NewError returns 500 when err is nil", func(tt *testing.T) {
		var h Handler
		res := h.NewError(context.Background(), nil)
		assert.Equal(tt, http.StatusInternalServerError, res.StatusCode)
		assert.Equal(tt, float64(500), res.Response.Code)
		assert.Equal(tt, "internal error", res.Response.Message)
	})

	t.Run("NewError returns 499 for context.Canceled", func(tt *testing.T) {
		var h Handler
		res := h.NewError(context.Background(), context.Canceled)
		assert.Equal(tt, 499, res.StatusCode)
		assert.Equal(tt, float64(499), res.Response.Code)
	})

	t.Run("NewError returns 504 for context.DeadlineExceeded", func(tt *testing.T) {
		var h Handler
		res := h.NewError(context.Background(), context.DeadlineExceeded)
		assert.Equal(tt, http.StatusGatewayTimeout, res.StatusCode)
		assert.Equal(tt, float64(504), res.Response.Code)
	})

	t.Run("NewError returns 404 for not found message", func(tt *testing.T) {
		var h Handler
		res := h.NewError(context.Background(), errors.New("resource not found"))
		assert.Equal(tt, http.StatusNotFound, res.StatusCode)
		assert.Equal(tt, float64(404), res.Response.Code)
		assert.Contains(tt, res.Response.Message, "not found")
	})

	t.Run("NewError returns 401 for unauthorized message", func(tt *testing.T) {
		var h Handler
		res := h.NewError(context.Background(), errors.New("unauthorized"))
		assert.Equal(tt, http.StatusUnauthorized, res.StatusCode)
		assert.Equal(tt, float64(401), res.Response.Code)
	})

	t.Run("NewError returns 401 for invalid token message", func(tt *testing.T) {
		var h Handler
		res := h.NewError(context.Background(), errors.New("invalid token"))
		assert.Equal(tt, http.StatusUnauthorized, res.StatusCode)
	})

	t.Run("NewError returns 403 for forbidden message", func(tt *testing.T) {
		var h Handler
		res := h.NewError(context.Background(), errors.New("access denied"))
		assert.Equal(tt, http.StatusForbidden, res.StatusCode)
		assert.Equal(tt, float64(403), res.Response.Code)
	})

	t.Run("NewError returns 409 for conflict message", func(tt *testing.T) {
		var h Handler
		res := h.NewError(context.Background(), errors.New("resource already exists"))
		assert.Equal(tt, http.StatusConflict, res.StatusCode)
		assert.Equal(tt, float64(409), res.Response.Code)
	})

	t.Run("NewError returns 400 for bad request message", func(tt *testing.T) {
		var h Handler
		res := h.NewError(context.Background(), errors.New("invalid input"))
		assert.Equal(tt, http.StatusBadRequest, res.StatusCode)
		assert.Equal(tt, float64(400), res.Response.Code)
	})

	t.Run("NewError returns 429 for rate limit message", func(tt *testing.T) {
		var h Handler
		res := h.NewError(context.Background(), errors.New("too many requests"))
		assert.Equal(tt, http.StatusTooManyRequests, res.StatusCode)
		assert.Equal(tt, float64(429), res.Response.Code)
	})

	t.Run("NewError returns 422 for unprocessable message", func(tt *testing.T) {
		var h Handler
		res := h.NewError(context.Background(), errors.New("unprocessable entity"))
		assert.Equal(tt, http.StatusUnprocessableEntity, res.StatusCode)
		assert.Equal(tt, float64(422), res.Response.Code)
	})

	t.Run("NewError returns 500 for unknown error", func(tt *testing.T) {
		var h Handler
		res := h.NewError(context.Background(), errors.New("something went wrong"))
		assert.Equal(tt, http.StatusInternalServerError, res.StatusCode)
		assert.Equal(tt, float64(500), res.Response.Code)
		assert.Equal(tt, "something went wrong", res.Response.Message)
	})

	t.Run("NewError uses status text when error message is empty", func(tt *testing.T) {
		var h Handler
		res := h.NewError(context.Background(), errors.New(""))
		assert.Equal(tt, http.StatusInternalServerError, res.StatusCode)
		assert.Equal(tt, http.StatusText(http.StatusInternalServerError), res.Response.Message)
	})
}

func TestUpdatePool(t *testing.T) {
	const poolOCID = "ocid1.pool.oc1..update-test"
	const tenancy = "ocid1.tenancy.oc1..aaa"

	t.Run("returns 202 when orchestrator succeeds", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().UpdatePool(mock.Anything, mock.MatchedBy(func(p *commonparams.UpdatePoolParams) bool {
			return p != nil &&
				p.AccountName == tenancy &&
				p.TotalThroughputMibps == gbpsToMibps(2) &&
				p.CustomPerformanceEnabled
		})).Return(&models.Pool{}, "wf-update-1", nil)
		h := Handler{Orchestrator: mockOrchestrator}
		req := &ociserver.UpdatePoolRequest{
			ThroughputGBps:    ociserver.OptFloat64{Value: 2, Set: true},
			DataEndpointCount: ociserver.OptInt64{Value: 4, Set: true},
		}
		params := ociserver.UpdatePoolParams{PoolOCID: poolOCID, TenancyOcid: tenancy}

		res, err := h.UpdatePool(contextWithOpcRequestID(nil, defaultTestOPC), req, params)

		assert.NoError(tt, err)
		headers, ok := res.(*ociserver.UpdatePoolAcceptedResponseHeaders)
		assert.True(tt, ok)
		assert.Equal(tt, defaultTestOPC, headers.OpcWorkRequestID)
		assert.Equal(tt, "in_progress", headers.Response.Status)
		assert.Equal(tt, "wf-update-1", headers.Response.WorkflowId)
		assert.Equal(tt, poolOCID, headers.Response.PoolOCID)
	})

	t.Run("returns 202 and forwards trimmed valid kmsKeyId", func(tt *testing.T) {
		const kmsKey = "ocid1.key.oc1.iad.update-cmek"
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().UpdatePool(mock.Anything, mock.MatchedBy(func(p *commonparams.UpdatePoolParams) bool {
			return p != nil && p.KmsKeyId == kmsKey
		})).Return(&models.Pool{}, "wf-kms", nil)
		h := Handler{Orchestrator: mockOrchestrator}
		req := &ociserver.UpdatePoolRequest{
			DataEndpointCount: ociserver.OptInt64{Value: 4, Set: true},
			KmsKeyId:          ociserver.OptString{Value: "  " + kmsKey + "  ", Set: true},
		}
		params := ociserver.UpdatePoolParams{PoolOCID: poolOCID, TenancyOcid: tenancy}

		res, err := h.UpdatePool(contextWithOpcRequestID(nil, defaultTestOPC), req, params)

		assert.NoError(tt, err)
		headers, ok := res.(*ociserver.UpdatePoolAcceptedResponseHeaders)
		assert.True(tt, ok)
		assert.Equal(tt, "wf-kms", headers.Response.WorkflowId)
	})

	t.Run("returns 400 when kmsKeyId is set but empty", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		h := Handler{Orchestrator: mockOrchestrator}
		req := &ociserver.UpdatePoolRequest{
			DataEndpointCount: ociserver.OptInt64{Value: 4, Set: true},
			KmsKeyId:          ociserver.OptString{Value: "   ", Set: true},
		}
		params := ociserver.UpdatePoolParams{PoolOCID: poolOCID, TenancyOcid: tenancy}

		res, err := h.UpdatePool(contextWithOpcRequestID(nil, defaultTestOPC), req, params)

		assert.NoError(tt, err)
		bad, ok := res.(*ociserver.UpdatePoolBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, defaultTestOPC, bad.OpcWorkRequestID)
		assert.Equal(tt, poolOCID, bad.Response.PoolOCID)
		assert.Equal(tt, errMsgEmptyKmsKeyId, bad.Response.ErrorMessage)
	})

	t.Run("returns 400 when kmsKeyId is not a valid OCID", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		h := Handler{Orchestrator: mockOrchestrator}
		req := &ociserver.UpdatePoolRequest{
			DataEndpointCount: ociserver.OptInt64{Value: 4, Set: true},
			KmsKeyId:          ociserver.OptString{Value: "not-an-ocid", Set: true},
		}
		params := ociserver.UpdatePoolParams{PoolOCID: poolOCID, TenancyOcid: tenancy}

		res, err := h.UpdatePool(contextWithOpcRequestID(nil, defaultTestOPC), req, params)

		assert.NoError(tt, err)
		bad, ok := res.(*ociserver.UpdatePoolBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, errMsgInvalidKmsKeyId, bad.Response.ErrorMessage)
	})

	t.Run("returns 202 and forwards trimmed valid nsgIds", func(tt *testing.T) {
		nsgA := "ocid1.networksecuritygroup.oc1.iad.nsg-a"
		nsgB := "ocid1.networksecuritygroup.oc1.iad.nsg-b"
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().UpdatePool(mock.Anything, mock.MatchedBy(func(p *commonparams.UpdatePoolParams) bool {
			return p != nil && len(p.NsgIds) == 2 && p.NsgIds[0] == nsgA && p.NsgIds[1] == nsgB
		})).Return(&models.Pool{}, "wf-nsg", nil)
		h := Handler{Orchestrator: mockOrchestrator}
		req := &ociserver.UpdatePoolRequest{
			DataEndpointCount: ociserver.OptInt64{Value: 4, Set: true},
			NsgIds:            []string{"  " + nsgA + "  ", nsgB},
		}
		params := ociserver.UpdatePoolParams{PoolOCID: poolOCID, TenancyOcid: tenancy}

		res, err := h.UpdatePool(contextWithOpcRequestID(nil, defaultTestOPC), req, params)

		assert.NoError(tt, err)
		headers, ok := res.(*ociserver.UpdatePoolAcceptedResponseHeaders)
		assert.True(tt, ok)
		assert.Equal(tt, "wf-nsg", headers.Response.WorkflowId)
	})

	t.Run("returns 400 when an nsgId is empty", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		h := Handler{Orchestrator: mockOrchestrator}
		req := &ociserver.UpdatePoolRequest{
			DataEndpointCount: ociserver.OptInt64{Value: 4, Set: true},
			NsgIds:            []string{"ocid1.networksecuritygroup.oc1.iad.nsg-a", "   "},
		}
		params := ociserver.UpdatePoolParams{PoolOCID: poolOCID, TenancyOcid: tenancy}

		res, err := h.UpdatePool(contextWithOpcRequestID(nil, defaultTestOPC), req, params)

		assert.NoError(tt, err)
		bad, ok := res.(*ociserver.UpdatePoolBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, defaultTestOPC, bad.OpcWorkRequestID)
		assert.Equal(tt, poolOCID, bad.Response.PoolOCID)
		assert.Equal(tt, fmt.Sprintf(errMsgEmptyNsgID, 1), bad.Response.ErrorMessage)
	})

	t.Run("returns 400 when an nsgId is not a valid OCID", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		h := Handler{Orchestrator: mockOrchestrator}
		req := &ociserver.UpdatePoolRequest{
			DataEndpointCount: ociserver.OptInt64{Value: 4, Set: true},
			NsgIds:            []string{"not-an-ocid"},
		}
		params := ociserver.UpdatePoolParams{PoolOCID: poolOCID, TenancyOcid: tenancy}

		res, err := h.UpdatePool(contextWithOpcRequestID(nil, defaultTestOPC), req, params)

		assert.NoError(tt, err)
		bad, ok := res.(*ociserver.UpdatePoolBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, fmt.Sprintf(errMsgInvalidNsgID, 0), bad.Response.ErrorMessage)
	})

	t.Run("omitted nsgIds leaves UpdatePoolParams.NsgIds nil", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().UpdatePool(mock.Anything, mock.MatchedBy(func(p *commonparams.UpdatePoolParams) bool {
			return p != nil && p.NsgIds == nil
		})).Return(&models.Pool{}, "wf-nsg-omit", nil)
		h := Handler{Orchestrator: mockOrchestrator}
		req := &ociserver.UpdatePoolRequest{
			DataEndpointCount: ociserver.OptInt64{Value: 4, Set: true},
		}
		params := ociserver.UpdatePoolParams{PoolOCID: poolOCID, TenancyOcid: tenancy}

		res, err := h.UpdatePool(contextWithOpcRequestID(nil, defaultTestOPC), req, params)

		assert.NoError(tt, err)
		_, ok := res.(*ociserver.UpdatePoolAcceptedResponseHeaders)
		assert.True(tt, ok)
	})

	t.Run("empty nsgIds forwards non-nil empty slice", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().UpdatePool(mock.Anything, mock.MatchedBy(func(p *commonparams.UpdatePoolParams) bool {
			return p != nil && p.NsgIds != nil && len(p.NsgIds) == 0
		})).Return(&models.Pool{}, "wf-nsg-clear", nil)
		h := Handler{Orchestrator: mockOrchestrator}
		req := &ociserver.UpdatePoolRequest{
			DataEndpointCount: ociserver.OptInt64{Value: 4, Set: true},
			NsgIds:            []string{},
		}
		params := ociserver.UpdatePoolParams{PoolOCID: poolOCID, TenancyOcid: tenancy}

		res, err := h.UpdatePool(contextWithOpcRequestID(nil, defaultTestOPC), req, params)

		assert.NoError(tt, err)
		_, ok := res.(*ociserver.UpdatePoolAcceptedResponseHeaders)
		assert.True(tt, ok)
	})

	t.Run("returns 400 when orchestrator returns validation error", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().UpdatePool(mock.Anything, mock.Anything).Return(nil, "", utilserrors.NewUserInputValidationErr("size too small"))
		h := Handler{Orchestrator: mockOrchestrator}
		req := &ociserver.UpdatePoolRequest{
			DataEndpointCount: ociserver.OptInt64{Value: 4, Set: true},
		}
		params := ociserver.UpdatePoolParams{PoolOCID: poolOCID, TenancyOcid: tenancy}

		res, err := h.UpdatePool(contextWithOpcRequestID(nil, defaultTestOPC), req, params)

		assert.NoError(tt, err)
		bad, ok := res.(*ociserver.UpdatePoolBadRequest)
		assert.True(tt, ok)
		assert.Contains(tt, bad.Response.ErrorMessage, "size too small")
	})

	t.Run("returns 400 when orchestrator returns bad request error", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().UpdatePool(mock.Anything, mock.Anything).Return(nil, "", utilserrors.NewBadRequestErr("PoolExternalIdentifier is required"))
		h := Handler{Orchestrator: mockOrchestrator}
		req := &ociserver.UpdatePoolRequest{
			DataEndpointCount: ociserver.OptInt64{Value: 4, Set: true},
		}
		params := ociserver.UpdatePoolParams{PoolOCID: poolOCID, TenancyOcid: tenancy}

		res, err := h.UpdatePool(contextWithOpcRequestID(nil, defaultTestOPC), req, params)

		assert.NoError(tt, err)
		bad, ok := res.(*ociserver.UpdatePoolBadRequest)
		assert.True(tt, ok)
		assert.Contains(tt, bad.Response.ErrorMessage, "PoolExternalIdentifier is required")
	})

	t.Run("returns 404 when orchestrator returns not found", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().UpdatePool(mock.Anything, mock.Anything).Return(nil, "", utilserrors.NewNotFoundErr("pool not found", nil))
		h := Handler{Orchestrator: mockOrchestrator}
		req := &ociserver.UpdatePoolRequest{
			DataEndpointCount: ociserver.OptInt64{Value: 4, Set: true},
		}
		params := ociserver.UpdatePoolParams{PoolOCID: poolOCID, TenancyOcid: tenancy}

		res, err := h.UpdatePool(contextWithOpcRequestID(nil, defaultTestOPC), req, params)

		assert.NoError(tt, err)
		notFound, ok := res.(*ociserver.UpdatePoolNotFound)
		assert.True(tt, ok)
		assert.Equal(tt, defaultTestOPC, notFound.OpcWorkRequestID)
		assert.Contains(tt, notFound.Response.ErrorMessage, "not found")
	})

	t.Run("returns 409 when orchestrator returns conflict", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().UpdatePool(mock.Anything, mock.Anything).Return(nil, "", utilserrors.NewConflictErr("pool in transition"))
		h := Handler{Orchestrator: mockOrchestrator}
		req := &ociserver.UpdatePoolRequest{
			DataEndpointCount: ociserver.OptInt64{Value: 4, Set: true},
		}
		params := ociserver.UpdatePoolParams{PoolOCID: poolOCID, TenancyOcid: tenancy}

		res, err := h.UpdatePool(contextWithOpcRequestID(nil, defaultTestOPC), req, params)

		assert.NoError(tt, err)
		conflict, ok := res.(*ociserver.UpdatePoolConflict)
		assert.True(tt, ok)
		assert.Equal(tt, "pool in transition", conflict.Response.ErrorMessage)
	})

	t.Run("returns 500 on generic orchestrator error", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().UpdatePool(mock.Anything, mock.Anything).Return(nil, "", errors.New("unexpected"))
		h := Handler{Orchestrator: mockOrchestrator}
		req := &ociserver.UpdatePoolRequest{
			DataEndpointCount: ociserver.OptInt64{Value: 4, Set: true},
		}
		params := ociserver.UpdatePoolParams{PoolOCID: poolOCID, TenancyOcid: tenancy}

		res, err := h.UpdatePool(contextWithOpcRequestID(nil, defaultTestOPC), req, params)

		assert.NoError(tt, err)
		srv, ok := res.(*ociserver.UpdatePoolInternalServerError)
		assert.True(tt, ok)
		assert.Equal(tt, "Internal server error", srv.Response.ErrorMessage)
	})

	t.Run("missing opc-request-id returns 400", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		h := Handler{Orchestrator: mockOrchestrator}
		req := &ociserver.UpdatePoolRequest{
			DataEndpointCount: ociserver.OptInt64{Value: 4, Set: true},
		}
		params := ociserver.UpdatePoolParams{PoolOCID: poolOCID, TenancyOcid: tenancy}

		res, err := h.UpdatePool(context.Background(), req, params)

		assert.NoError(tt, err)
		bad, ok := res.(*ociserver.UpdatePoolBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, invalidOPCRequestID, bad.Response.ErrorMessage)
	})

	t.Run("returns 202 with throughputGBps and dataEndpointCount update", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().UpdatePool(mock.Anything, mock.MatchedBy(func(p *commonparams.UpdatePoolParams) bool {
			return p != nil &&
				p.TotalThroughputMibps == gbpsToMibps(4) &&
				p.CustomPerformanceEnabled &&
				p.SizeInBytes == 0
		})).Return(&models.Pool{}, "wf-throughput-only", nil)
		h := Handler{Orchestrator: mockOrchestrator}
		req := &ociserver.UpdatePoolRequest{
			ThroughputGBps:    ociserver.OptFloat64{Value: 4, Set: true},
			DataEndpointCount: ociserver.OptInt64{Value: 4, Set: true},
		}
		params := ociserver.UpdatePoolParams{PoolOCID: poolOCID, TenancyOcid: tenancy}

		res, err := h.UpdatePool(contextWithOpcRequestID(nil, defaultTestOPC), req, params)

		assert.NoError(tt, err)
		headers, ok := res.(*ociserver.UpdatePoolAcceptedResponseHeaders)
		assert.True(tt, ok)
		assert.Equal(tt, "wf-throughput-only", headers.Response.WorkflowId)
		assert.Equal(tt, poolOCID, headers.Response.PoolOCID)
	})

	t.Run("returns 202 echoes custom opc-request-id", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().UpdatePool(mock.Anything, mock.Anything).Return(&models.Pool{}, "wf-echo", nil)
		h := Handler{Orchestrator: mockOrchestrator}
		req := &ociserver.UpdatePoolRequest{
			DataEndpointCount: ociserver.OptInt64{Value: 4, Set: true},
		}
		params := ociserver.UpdatePoolParams{PoolOCID: poolOCID, TenancyOcid: tenancy}

		res, err := h.UpdatePool(contextWithOpcRequestID(nil, "custom-opc-id-123"), req, params)

		assert.NoError(tt, err)
		headers, ok := res.(*ociserver.UpdatePoolAcceptedResponseHeaders)
		assert.True(tt, ok)
		assert.Equal(tt, "custom-opc-id-123", headers.OpcWorkRequestID)
		assert.Equal(tt, poolOCID, headers.Response.PoolOCID)
	})

	t.Run("returns 400 when neither nodeCapacities nor dataEndpointCount nor any other field provided", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		h := Handler{Orchestrator: mockOrchestrator}
		req := &ociserver.UpdatePoolRequest{}
		params := ociserver.UpdatePoolParams{PoolOCID: poolOCID, TenancyOcid: tenancy}

		res, err := h.UpdatePool(contextWithOpcRequestID(nil, defaultTestOPC), req, params)

		assert.NoError(tt, err)
		bad, ok := res.(*ociserver.UpdatePoolBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, defaultTestOPC, bad.OpcWorkRequestID)
		assert.Equal(tt, poolOCID, bad.Response.PoolOCID)
		assert.Equal(tt, errMsgNoUpdatableFields, bad.Response.ErrorMessage)
	})

	t.Run("returns 400 when only empty nodeCapacities provided", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		h := Handler{Orchestrator: mockOrchestrator}
		req := &ociserver.UpdatePoolRequest{
			NodeCapacities: []ociserver.NodeCapacity{},
		}
		params := ociserver.UpdatePoolParams{PoolOCID: poolOCID, TenancyOcid: tenancy}

		res, err := h.UpdatePool(contextWithOpcRequestID(nil, defaultTestOPC), req, params)

		assert.NoError(tt, err)
		bad, ok := res.(*ociserver.UpdatePoolBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, defaultTestOPC, bad.OpcWorkRequestID)
		assert.Equal(tt, poolOCID, bad.Response.PoolOCID)
		assert.Equal(tt, errMsgNoUpdatableFields, bad.Response.ErrorMessage)
	})

	t.Run("returns 202 when only empty nsgIds provided to clear NSGs", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().UpdatePool(mock.Anything, mock.MatchedBy(func(p *commonparams.UpdatePoolParams) bool {
			return p != nil && p.NsgIds != nil && len(p.NsgIds) == 0
		})).Return(&models.Pool{}, "wf-nsg-clear-only", nil)
		h := Handler{Orchestrator: mockOrchestrator}
		req := &ociserver.UpdatePoolRequest{
			NsgIds: []string{},
		}
		params := ociserver.UpdatePoolParams{PoolOCID: poolOCID, TenancyOcid: tenancy}

		res, err := h.UpdatePool(contextWithOpcRequestID(nil, defaultTestOPC), req, params)

		assert.NoError(tt, err)
		_, ok := res.(*ociserver.UpdatePoolAcceptedResponseHeaders)
		assert.True(tt, ok)
	})

	t.Run("metadata-only kmsKeyId reaches OCI update pool workflow without throughput", func(tt *testing.T) {
		ctx := contextWithOpcRequestID(context.Background(), defaultTestOPC)
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, log.NewLogger())

		mockStorage := database.NewMockStorage(tt)
		acc := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: tenancy}
		mockStorage.EXPECT().GetAccount(mock.Anything, tenancy).Return(acc, nil)
		mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(&datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel:      datamodel.BaseModel{UUID: "pool-uuid-meta"},
				Name:           "p-meta",
				State:          datamodel.LifeCycleStateREADY,
				SizeInBytes:    1024 * 1024 * 1024 * 1024,
				PoolAttributes: &datamodel.PoolAttributes{ThroughputMibps: 128, IsRegionalHA: false},
			},
		}, nil)
		mockStorage.EXPECT().GetClusterUpgradeJobsByClusterID(mock.Anything, "pool-uuid-meta").Return([]*datamodel.ClusterUpgradeJob{}, nil)
		pool := &datamodel.Pool{
			BaseModel:      datamodel.BaseModel{UUID: "pool-uuid-meta"},
			Name:           "p-meta",
			SizeInBytes:    1024 * 1024 * 1024 * 1024,
			PoolAttributes: &datamodel.PoolAttributes{ThroughputMibps: 128},
		}
		mockStorage.EXPECT().UpdatingPool(mock.Anything, mock.Anything).Return(pool, nil)

		kmsKey := "ocid1.key.oc1..kkk"
		var capturedWorkflow interface{}
		var capturedParams *commonparams.UpdatePoolParams
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		mockTemporal.EXPECT().
			ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Run(func(_ context.Context, _ client.StartWorkflowOptions, workflow interface{}, args ...interface{}) {
				capturedWorkflow = workflow
				if len(args) > 0 {
					if p, ok := args[0].(*commonparams.UpdatePoolParams); ok {
						capturedParams = p
					}
				}
			}).
			Return(nil, nil)

		h := Handler{Orchestrator: ocifactory.NewOCIOrchestrator(mockStorage, mockTemporal)}
		req := &ociserver.UpdatePoolRequest{
			KmsKeyId: ociserver.OptString{Value: kmsKey, Set: true},
		}
		params := ociserver.UpdatePoolParams{PoolOCID: poolOCID, TenancyOcid: tenancy}

		res, err := h.UpdatePool(ctx, req, params)

		assert.NoError(tt, err)
		headers, ok := res.(*ociserver.UpdatePoolAcceptedResponseHeaders)
		assert.True(tt, ok)
		assert.NotEmpty(tt, headers.Response.WorkflowId)
		require.Equal(tt,
			reflect.ValueOf(ociworkflows.OCIUpdatePoolWorkflow).Pointer(),
			reflect.ValueOf(capturedWorkflow).Pointer(),
		)
		require.NotNil(tt, capturedParams)
		assert.Equal(tt, kmsKey, capturedParams.KmsKeyId)
		assert.False(tt, capturedParams.CustomPerformanceEnabled)
		assert.Equal(tt, int64(0), capturedParams.TotalThroughputMibps)
	})

	t.Run("returns 404 verifies full response body", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().UpdatePool(mock.Anything, mock.Anything).Return(nil, "", utilserrors.NewNotFoundErr("pool not found", nil))
		h := Handler{Orchestrator: mockOrchestrator}
		req := &ociserver.UpdatePoolRequest{
			DataEndpointCount: ociserver.OptInt64{Value: 4, Set: true},
		}
		params := ociserver.UpdatePoolParams{PoolOCID: poolOCID, TenancyOcid: tenancy}

		res, err := h.UpdatePool(contextWithOpcRequestID(nil, defaultTestOPC), req, params)

		assert.NoError(tt, err)
		notFound, ok := res.(*ociserver.UpdatePoolNotFound)
		assert.True(tt, ok)
		assert.Equal(tt, defaultTestOPC, notFound.OpcWorkRequestID)
		assert.Equal(tt, poolOCID, notFound.Response.PoolOCID)
		assert.Equal(tt, "failed", notFound.Response.Status)
		assert.Contains(tt, notFound.Response.ErrorMessage, "not found")
	})

	t.Run("returns 409 verifies full response body preserves orchestrator error", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().UpdatePool(mock.Anything, mock.Anything).Return(nil, "", utilserrors.NewConflictErr("pool in transition"))
		h := Handler{Orchestrator: mockOrchestrator}
		req := &ociserver.UpdatePoolRequest{
			DataEndpointCount: ociserver.OptInt64{Value: 4, Set: true},
		}
		params := ociserver.UpdatePoolParams{PoolOCID: poolOCID, TenancyOcid: tenancy}

		res, err := h.UpdatePool(contextWithOpcRequestID(nil, defaultTestOPC), req, params)

		assert.NoError(tt, err)
		conflict, ok := res.(*ociserver.UpdatePoolConflict)
		assert.True(tt, ok)
		assert.Equal(tt, defaultTestOPC, conflict.OpcWorkRequestID)
		assert.Equal(tt, poolOCID, conflict.Response.PoolOCID)
		assert.Equal(tt, "failed", conflict.Response.Status)
		assert.Equal(tt, "pool in transition", conflict.Response.ErrorMessage)
	})

	t.Run("returns 500 verifies full response body", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().UpdatePool(mock.Anything, mock.Anything).Return(nil, "", errors.New("unexpected"))
		h := Handler{Orchestrator: mockOrchestrator}
		req := &ociserver.UpdatePoolRequest{
			DataEndpointCount: ociserver.OptInt64{Value: 4, Set: true},
		}
		params := ociserver.UpdatePoolParams{PoolOCID: poolOCID, TenancyOcid: tenancy}

		res, err := h.UpdatePool(contextWithOpcRequestID(nil, defaultTestOPC), req, params)

		assert.NoError(tt, err)
		srv, ok := res.(*ociserver.UpdatePoolInternalServerError)
		assert.True(tt, ok)
		assert.Equal(tt, defaultTestOPC, srv.OpcWorkRequestID)
		assert.Equal(tt, poolOCID, srv.Response.PoolOCID)
		assert.Equal(tt, "failed", srv.Response.Status)
		assert.Equal(tt, "Internal server error", srv.Response.ErrorMessage)
	})

	t.Run("returns 409 with wrapped conflict error", func(tt *testing.T) {
		inner := utilserrors.NewConflictErr("inner conflict")
		wrapped := fmt.Errorf("outer: %w", inner)
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().UpdatePool(mock.Anything, mock.Anything).Return(nil, "", wrapped)
		h := Handler{Orchestrator: mockOrchestrator}
		req := &ociserver.UpdatePoolRequest{
			DataEndpointCount: ociserver.OptInt64{Value: 4, Set: true},
		}
		params := ociserver.UpdatePoolParams{PoolOCID: poolOCID, TenancyOcid: tenancy}

		res, err := h.UpdatePool(contextWithOpcRequestID(nil, defaultTestOPC), req, params)

		assert.NoError(tt, err)
		conflict, ok := res.(*ociserver.UpdatePoolConflict)
		assert.True(tt, ok)
		assert.Equal(tt, "outer: inner conflict", conflict.Response.ErrorMessage)
	})

	t.Run("passes PoolId from URL path param to orchestrator", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().UpdatePool(mock.Anything, mock.MatchedBy(func(p *commonparams.UpdatePoolParams) bool {
			return p != nil &&
				p.PoolExternalIdentifier == poolOCID &&
				p.AccountName == tenancy
		})).Return(&models.Pool{}, "wf-poolid", nil)
		h := Handler{Orchestrator: mockOrchestrator}
		req := &ociserver.UpdatePoolRequest{
			DataEndpointCount: ociserver.OptInt64{Value: 4, Set: true},
		}
		params := ociserver.UpdatePoolParams{PoolOCID: poolOCID, TenancyOcid: tenancy}

		res, err := h.UpdatePool(contextWithOpcRequestID(nil, defaultTestOPC), req, params)

		assert.NoError(tt, err)
		headers, ok := res.(*ociserver.UpdatePoolAcceptedResponseHeaders)
		assert.True(tt, ok)
		assert.Equal(tt, "wf-poolid", headers.Response.WorkflowId)
	})

	t.Run("returns 400 when both dataEndpointCount and nodeCapacities are set", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		h := Handler{Orchestrator: mockOrchestrator}
		req := &ociserver.UpdatePoolRequest{
			DataEndpointCount: ociserver.OptInt64{Value: 4, Set: true},
			NodeCapacities:    []ociserver.NodeCapacity{{SizeInGiB: 100}},
		}
		params := ociserver.UpdatePoolParams{PoolOCID: poolOCID, TenancyOcid: tenancy}

		res, err := h.UpdatePool(contextWithOpcRequestID(nil, defaultTestOPC), req, params)

		assert.NoError(tt, err)
		bad, ok := res.(*ociserver.UpdatePoolBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, errMsgBothNodeCapacitiesAndDEC, bad.Response.ErrorMessage)
	})

	t.Run("returns 202 when only throughputGBps provided without nodeCapacities or dataEndpointCount", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().UpdatePool(mock.Anything, mock.MatchedBy(func(p *commonparams.UpdatePoolParams) bool {
			return p != nil &&
				p.TotalThroughputMibps == gbpsToMibps(2) &&
				p.CustomPerformanceEnabled
		})).Return(&models.Pool{}, "wf-throughput-only-no-dec", nil)
		h := Handler{Orchestrator: mockOrchestrator}
		req := &ociserver.UpdatePoolRequest{
			ThroughputGBps: ociserver.OptFloat64{Value: 2, Set: true},
		}
		params := ociserver.UpdatePoolParams{PoolOCID: poolOCID, TenancyOcid: tenancy}

		res, err := h.UpdatePool(contextWithOpcRequestID(nil, defaultTestOPC), req, params)

		assert.NoError(tt, err)
		headers, ok := res.(*ociserver.UpdatePoolAcceptedResponseHeaders)
		assert.True(tt, ok)
		assert.Equal(tt, "wf-throughput-only-no-dec", headers.Response.WorkflowId)
	})

	t.Run("maps nodeCapacities to UpdatePoolParams", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().UpdatePool(mock.Anything, mock.MatchedBy(func(p *commonparams.UpdatePoolParams) bool {
			return p != nil &&
				len(p.NodeCapacities) == 2 &&
				p.NodeCapacities[0].Name == "node-a" &&
				p.NodeCapacities[0].NodeUUID == "uuid-a" &&
				p.NodeCapacities[0].SizeInGiB == 100 &&
				p.NodeCapacities[1].Name == "node-b" &&
				p.NodeCapacities[1].NodeUUID == "uuid-b" &&
				p.NodeCapacities[1].SizeInGiB == 200
		})).Return(&models.Pool{}, "wf-nc", nil)
		h := Handler{Orchestrator: mockOrchestrator}
		req := &ociserver.UpdatePoolRequest{
			NodeCapacities: []ociserver.NodeCapacity{
				{Name: "node-a", NodeUUID: "uuid-a", SizeInGiB: 100},
				{Name: "node-b", NodeUUID: "uuid-b", SizeInGiB: 200},
			},
		}
		params := ociserver.UpdatePoolParams{PoolOCID: poolOCID, TenancyOcid: tenancy}

		res, err := h.UpdatePool(contextWithOpcRequestID(nil, defaultTestOPC), req, params)

		assert.NoError(tt, err)
		_, ok := res.(*ociserver.UpdatePoolAcceptedResponseHeaders)
		assert.True(tt, ok)
	})

	t.Run("maps kmsKeyId to UpdatePoolParams", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().UpdatePool(mock.Anything, mock.MatchedBy(func(p *commonparams.UpdatePoolParams) bool {
			return p != nil && p.KmsKeyId == "ocid1.key.oc1..kkk"
		})).Return(&models.Pool{}, "wf-kms", nil)
		h := Handler{Orchestrator: mockOrchestrator}
		req := &ociserver.UpdatePoolRequest{
			DataEndpointCount: ociserver.OptInt64{Value: 4, Set: true},
			KmsKeyId:          ociserver.OptString{Value: "ocid1.key.oc1..kkk", Set: true},
		}
		params := ociserver.UpdatePoolParams{PoolOCID: poolOCID, TenancyOcid: tenancy}

		res, err := h.UpdatePool(contextWithOpcRequestID(nil, defaultTestOPC), req, params)

		assert.NoError(tt, err)
		_, ok := res.(*ociserver.UpdatePoolAcceptedResponseHeaders)
		assert.True(tt, ok)
	})

	t.Run("maps nsgIds to UpdatePoolParams", func(tt *testing.T) {
		nsg1 := "ocid1.networksecuritygroup.oc1.iad.nsg-1"
		nsg2 := "ocid1.networksecuritygroup.oc1.iad.nsg-2"
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().UpdatePool(mock.Anything, mock.MatchedBy(func(p *commonparams.UpdatePoolParams) bool {
			return p != nil &&
				len(p.NsgIds) == 2 &&
				p.NsgIds[0] == nsg1 &&
				p.NsgIds[1] == nsg2
		})).Return(&models.Pool{}, "wf-nsg", nil)
		h := Handler{Orchestrator: mockOrchestrator}
		req := &ociserver.UpdatePoolRequest{
			DataEndpointCount: ociserver.OptInt64{Value: 4, Set: true},
			NsgIds:            []string{nsg1, nsg2},
		}
		params := ociserver.UpdatePoolParams{PoolOCID: poolOCID, TenancyOcid: tenancy}

		res, err := h.UpdatePool(contextWithOpcRequestID(nil, defaultTestOPC), req, params)

		assert.NoError(tt, err)
		_, ok := res.(*ociserver.UpdatePoolAcceptedResponseHeaders)
		assert.True(tt, ok)
	})

	t.Run("maps securityAttributes to UpdatePoolParams", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().UpdatePool(mock.Anything, mock.MatchedBy(func(p *commonparams.UpdatePoolParams) bool {
			if p == nil || p.SecurityAttributes == nil {
				return false
			}
			ns1, ok := p.SecurityAttributes["ns1"]
			if !ok {
				return false
			}
			app, ok := ns1["app"].(map[string]string)
			if !ok {
				return false
			}
			return app["value"] == "app1" && app["mode"] == "enforce"
		})).Return(&models.Pool{}, "wf-sa", nil)
		h := Handler{Orchestrator: mockOrchestrator}
		req := &ociserver.UpdatePoolRequest{
			DataEndpointCount: ociserver.OptInt64{Value: 4, Set: true},
			SecurityAttributes: ociserver.OptSecurityAttributes{
				Value: ociserver.SecurityAttributes{
					"ns1": {
						"app": ociserver.SecurityAttributeValue{
							Value: "app1",
							Mode:  ociserver.SecurityAttributeValueModeEnforce,
						},
					},
				},
				Set: true,
			},
		}
		params := ociserver.UpdatePoolParams{PoolOCID: poolOCID, TenancyOcid: tenancy}

		res, err := h.UpdatePool(contextWithOpcRequestID(nil, defaultTestOPC), req, params)

		assert.NoError(tt, err)
		_, ok := res.(*ociserver.UpdatePoolAcceptedResponseHeaders)
		assert.True(tt, ok)
	})

	t.Run("omitted securityAttributes leaves UpdatePoolParams.SecurityAttributes nil", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().UpdatePool(mock.Anything, mock.MatchedBy(func(p *commonparams.UpdatePoolParams) bool {
			return p != nil && p.SecurityAttributes == nil
		})).Return(&models.Pool{}, "wf-sa-omit", nil)
		h := Handler{Orchestrator: mockOrchestrator}
		req := &ociserver.UpdatePoolRequest{
			DataEndpointCount: ociserver.OptInt64{Value: 4, Set: true},
		}
		params := ociserver.UpdatePoolParams{PoolOCID: poolOCID, TenancyOcid: tenancy}

		res, err := h.UpdatePool(contextWithOpcRequestID(nil, defaultTestOPC), req, params)

		assert.NoError(tt, err)
		_, ok := res.(*ociserver.UpdatePoolAcceptedResponseHeaders)
		assert.True(tt, ok)
	})

	t.Run("empty securityAttributes forwards non-nil empty map", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().UpdatePool(mock.Anything, mock.MatchedBy(func(p *commonparams.UpdatePoolParams) bool {
			return p != nil && p.SecurityAttributes != nil && len(p.SecurityAttributes) == 0
		})).Return(&models.Pool{}, "wf-sa-clear", nil)
		h := Handler{Orchestrator: mockOrchestrator}
		req := &ociserver.UpdatePoolRequest{
			DataEndpointCount: ociserver.OptInt64{Value: 4, Set: true},
			SecurityAttributes: ociserver.OptSecurityAttributes{
				Value: ociserver.SecurityAttributes{},
				Set:   true,
			},
		}
		params := ociserver.UpdatePoolParams{PoolOCID: poolOCID, TenancyOcid: tenancy}

		res, err := h.UpdatePool(contextWithOpcRequestID(nil, defaultTestOPC), req, params)

		assert.NoError(tt, err)
		_, ok := res.(*ociserver.UpdatePoolAcceptedResponseHeaders)
		assert.True(tt, ok)
	})

	t.Run("maps ociAdminPassword to UpdatePoolParams", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().UpdatePool(mock.Anything, mock.MatchedBy(func(p *commonparams.UpdatePoolParams) bool {
			return p != nil &&
				p.OciAdminPassword != nil &&
				p.OciAdminPassword.Ocid == "ocid1.secret.oc1..sss" &&
				p.OciAdminPassword.Version == 7
		})).Return(&models.Pool{}, "wf-pwd", nil)
		h := Handler{Orchestrator: mockOrchestrator}
		req := &ociserver.UpdatePoolRequest{
			DataEndpointCount: ociserver.OptInt64{Value: 4, Set: true},
			OciAdminPassword: ociserver.OptOCIOCIDVersionRef{
				Value: ociserver.OCIOCIDVersionRef{Ocid: "ocid1.secret.oc1..sss", Version: "7"},
				Set:   true,
			},
		}
		params := ociserver.UpdatePoolParams{PoolOCID: poolOCID, TenancyOcid: tenancy}

		res, err := h.UpdatePool(contextWithOpcRequestID(nil, defaultTestOPC), req, params)

		assert.NoError(tt, err)
		_, ok := res.(*ociserver.UpdatePoolAcceptedResponseHeaders)
		assert.True(tt, ok)
	})

	t.Run("returns 400 when ociAdminPassword version is not parseable", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		h := Handler{Orchestrator: mockOrchestrator}
		req := &ociserver.UpdatePoolRequest{
			DataEndpointCount: ociserver.OptInt64{Value: 4, Set: true},
			OciAdminPassword: ociserver.OptOCIOCIDVersionRef{
				Value: ociserver.OCIOCIDVersionRef{Ocid: "ocid1.secret.oc1..sss", Version: "not-a-number"},
				Set:   true,
			},
		}
		params := ociserver.UpdatePoolParams{PoolOCID: poolOCID, TenancyOcid: tenancy}

		res, err := h.UpdatePool(contextWithOpcRequestID(nil, defaultTestOPC), req, params)

		assert.NoError(tt, err)
		bad, ok := res.(*ociserver.UpdatePoolBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, errMsgInvalidAdminPasswordVersion, bad.Response.ErrorMessage)
	})

	t.Run("returns 400 when ociAdminPassword version is below 1", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		h := Handler{Orchestrator: mockOrchestrator}
		req := &ociserver.UpdatePoolRequest{
			DataEndpointCount: ociserver.OptInt64{Value: 4, Set: true},
			OciAdminPassword: ociserver.OptOCIOCIDVersionRef{
				Value: ociserver.OCIOCIDVersionRef{Ocid: "ocid1.secret.oc1..sss", Version: "0"},
				Set:   true,
			},
		}
		params := ociserver.UpdatePoolParams{PoolOCID: poolOCID, TenancyOcid: tenancy}

		res, err := h.UpdatePool(contextWithOpcRequestID(nil, defaultTestOPC), req, params)

		assert.NoError(tt, err)
		bad, ok := res.(*ociserver.UpdatePoolBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, errMsgAdminPasswordVersionLessThanOne, bad.Response.ErrorMessage)
	})

	t.Run("returns 400 when throughputGBps is zero", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		h := Handler{Orchestrator: mockOrchestrator}
		req := &ociserver.UpdatePoolRequest{
			ThroughputGBps:    ociserver.OptFloat64{Value: 0, Set: true},
			DataEndpointCount: ociserver.OptInt64{Value: 4, Set: true},
		}
		params := ociserver.UpdatePoolParams{PoolOCID: poolOCID, TenancyOcid: tenancy}

		res, err := h.UpdatePool(contextWithOpcRequestID(context.Background(), defaultTestOPC), req, params)

		assert.NoError(tt, err)
		bad, ok := res.(*ociserver.UpdatePoolBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, errMsgInvalidUnitConversionInput, bad.Response.ErrorMessage)
	})

	t.Run("returns 400 when throughputGBps is negative", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		h := Handler{Orchestrator: mockOrchestrator}
		req := &ociserver.UpdatePoolRequest{
			ThroughputGBps:    ociserver.OptFloat64{Value: -2.5, Set: true},
			DataEndpointCount: ociserver.OptInt64{Value: 4, Set: true},
		}
		params := ociserver.UpdatePoolParams{PoolOCID: poolOCID, TenancyOcid: tenancy}

		res, err := h.UpdatePool(contextWithOpcRequestID(context.Background(), defaultTestOPC), req, params)

		assert.NoError(tt, err)
		bad, ok := res.(*ociserver.UpdatePoolBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, errMsgInvalidUnitConversionInput, bad.Response.ErrorMessage)
	})

	t.Run("returns 400 when throughputGBps is NaN", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		h := Handler{Orchestrator: mockOrchestrator}
		req := &ociserver.UpdatePoolRequest{
			ThroughputGBps:    ociserver.OptFloat64{Value: math.NaN(), Set: true},
			DataEndpointCount: ociserver.OptInt64{Value: 4, Set: true},
		}
		params := ociserver.UpdatePoolParams{PoolOCID: poolOCID, TenancyOcid: tenancy}

		res, err := h.UpdatePool(contextWithOpcRequestID(context.Background(), defaultTestOPC), req, params)

		assert.NoError(tt, err)
		bad, ok := res.(*ociserver.UpdatePoolBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, errMsgInvalidUnitConversionInput, bad.Response.ErrorMessage)
	})

	t.Run("returns 400 when throughputGBps is +Inf", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		h := Handler{Orchestrator: mockOrchestrator}
		req := &ociserver.UpdatePoolRequest{
			ThroughputGBps:    ociserver.OptFloat64{Value: math.Inf(1), Set: true},
			DataEndpointCount: ociserver.OptInt64{Value: 4, Set: true},
		}
		params := ociserver.UpdatePoolParams{PoolOCID: poolOCID, TenancyOcid: tenancy}

		res, err := h.UpdatePool(contextWithOpcRequestID(context.Background(), defaultTestOPC), req, params)

		assert.NoError(tt, err)
		bad, ok := res.(*ociserver.UpdatePoolBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, errMsgInvalidUnitConversionInput, bad.Response.ErrorMessage)
	})

	t.Run("returns 400 when a nodeCapacity sizeInGiB is zero", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		h := Handler{Orchestrator: mockOrchestrator}
		req := &ociserver.UpdatePoolRequest{
			NodeCapacities: []ociserver.NodeCapacity{
				{Name: "n1", NodeUUID: "uuid-1", SizeInGiB: 1024},
				{Name: "n2", NodeUUID: "uuid-2", SizeInGiB: 0},
			},
		}
		params := ociserver.UpdatePoolParams{PoolOCID: poolOCID, TenancyOcid: tenancy}

		res, err := h.UpdatePool(contextWithOpcRequestID(context.Background(), defaultTestOPC), req, params)

		assert.NoError(tt, err)
		bad, ok := res.(*ociserver.UpdatePoolBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, errMsgInvalidUnitConversionInput, bad.Response.ErrorMessage)
	})

	t.Run("returns 400 when a nodeCapacity sizeInGiB is negative", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		h := Handler{Orchestrator: mockOrchestrator}
		req := &ociserver.UpdatePoolRequest{
			NodeCapacities: []ociserver.NodeCapacity{
				{Name: "n1", NodeUUID: "uuid-1", SizeInGiB: -10},
			},
		}
		params := ociserver.UpdatePoolParams{PoolOCID: poolOCID, TenancyOcid: tenancy}

		res, err := h.UpdatePool(contextWithOpcRequestID(context.Background(), defaultTestOPC), req, params)

		assert.NoError(tt, err)
		bad, ok := res.(*ociserver.UpdatePoolBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, errMsgInvalidUnitConversionInput, bad.Response.ErrorMessage)
	})

	t.Run("returns 400 when a nodeCapacity name is missing", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		h := Handler{Orchestrator: mockOrchestrator}
		req := &ociserver.UpdatePoolRequest{
			NodeCapacities: []ociserver.NodeCapacity{
				{NodeUUID: "uuid-a", SizeInGiB: 100},
			},
		}
		params := ociserver.UpdatePoolParams{PoolOCID: poolOCID, TenancyOcid: tenancy}

		res, err := h.UpdatePool(contextWithOpcRequestID(context.Background(), defaultTestOPC), req, params)

		assert.NoError(tt, err)
		bad, ok := res.(*ociserver.UpdatePoolBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, errMsgMissingRequiredNodeCapacityField, bad.Response.ErrorMessage)
	})

	t.Run("returns 400 when a nodeCapacity name is blank", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		h := Handler{Orchestrator: mockOrchestrator}
		req := &ociserver.UpdatePoolRequest{
			NodeCapacities: []ociserver.NodeCapacity{
				{Name: "   ", NodeUUID: "uuid-a", SizeInGiB: 100},
			},
		}
		params := ociserver.UpdatePoolParams{PoolOCID: poolOCID, TenancyOcid: tenancy}

		res, err := h.UpdatePool(contextWithOpcRequestID(context.Background(), defaultTestOPC), req, params)

		assert.NoError(tt, err)
		bad, ok := res.(*ociserver.UpdatePoolBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, errMsgMissingRequiredNodeCapacityField, bad.Response.ErrorMessage)
	})

	t.Run("returns 400 when a nodeCapacity nodeUUID is missing", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		h := Handler{Orchestrator: mockOrchestrator}
		req := &ociserver.UpdatePoolRequest{
			NodeCapacities: []ociserver.NodeCapacity{
				{Name: "node-a", SizeInGiB: 100},
			},
		}
		params := ociserver.UpdatePoolParams{PoolOCID: poolOCID, TenancyOcid: tenancy}

		res, err := h.UpdatePool(contextWithOpcRequestID(context.Background(), defaultTestOPC), req, params)

		assert.NoError(tt, err)
		bad, ok := res.(*ociserver.UpdatePoolBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, errMsgMissingRequiredNodeCapacityField, bad.Response.ErrorMessage)
	})

	t.Run("returns 400 when a nodeCapacity nodeUUID is blank", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		h := Handler{Orchestrator: mockOrchestrator}
		req := &ociserver.UpdatePoolRequest{
			NodeCapacities: []ociserver.NodeCapacity{
				{Name: "node-a", NodeUUID: "", SizeInGiB: 100},
			},
		}
		params := ociserver.UpdatePoolParams{PoolOCID: poolOCID, TenancyOcid: tenancy}

		res, err := h.UpdatePool(contextWithOpcRequestID(context.Background(), defaultTestOPC), req, params)

		assert.NoError(tt, err)
		bad, ok := res.(*ociserver.UpdatePoolBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, errMsgMissingRequiredNodeCapacityField, bad.Response.ErrorMessage)
	})

	t.Run("reports the offending nodeCapacity index when a later entry is missing nodeUUID", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		h := Handler{Orchestrator: mockOrchestrator}
		req := &ociserver.UpdatePoolRequest{
			NodeCapacities: []ociserver.NodeCapacity{
				{Name: "node-a", NodeUUID: "uuid-a", SizeInGiB: 100},
				{Name: "node-b", SizeInGiB: 200},
			},
		}
		params := ociserver.UpdatePoolParams{PoolOCID: poolOCID, TenancyOcid: tenancy}

		res, err := h.UpdatePool(contextWithOpcRequestID(context.Background(), defaultTestOPC), req, params)

		assert.NoError(tt, err)
		bad, ok := res.(*ociserver.UpdatePoolBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, errMsgMissingRequiredNodeCapacityField, bad.Response.ErrorMessage)
	})

	t.Run("returns 400 when nodeCapacities contains duplicate node_uuid (orchestrator not called)", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		h := Handler{Orchestrator: mockOrchestrator}
		req := &ociserver.UpdatePoolRequest{
			NodeCapacities: []ociserver.NodeCapacity{
				{Name: "node-a", NodeUUID: "uuid-dup", SizeInGiB: 100},
				{Name: "node-b", NodeUUID: "uuid-dup", SizeInGiB: 200},
			},
		}
		params := ociserver.UpdatePoolParams{PoolOCID: poolOCID, TenancyOcid: tenancy}

		res, err := h.UpdatePool(contextWithOpcRequestID(context.Background(), defaultTestOPC), req, params)

		assert.NoError(tt, err)
		bad, ok := res.(*ociserver.UpdatePoolBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, errMsgDuplicateNodeUUID, bad.Response.ErrorMessage)
		assert.Equal(tt, poolOCID, bad.Response.PoolOCID)
	})
}

func TestValidateUpdatePoolUnitConversionInputs(t *testing.T) {
	t.Run("accepts a valid request with throughput and DEC", func(tt *testing.T) {
		req := &ociserver.UpdatePoolRequest{
			ThroughputGBps:    ociserver.OptFloat64{Value: 2.5, Set: true},
			DataEndpointCount: ociserver.OptInt64{Value: 4, Set: true},
		}
		assert.Equal(tt, "", validateUpdatePoolUnitConversionInputs(req))
	})

	t.Run("accepts an empty request (no fields set)", func(tt *testing.T) {
		req := &ociserver.UpdatePoolRequest{}
		assert.Equal(tt, "", validateUpdatePoolUnitConversionInputs(req))
	})

	t.Run("accepts unset optional fields without inspecting Value", func(tt *testing.T) {
		req := &ociserver.UpdatePoolRequest{
			ThroughputGBps: ociserver.OptFloat64{Value: -1, Set: false},
		}
		assert.Equal(tt, "", validateUpdatePoolUnitConversionInputs(req))
	})

	t.Run("rejects zero throughputGBps", func(tt *testing.T) {
		req := &ociserver.UpdatePoolRequest{ThroughputGBps: ociserver.OptFloat64{Value: 0, Set: true}}
		assert.Equal(tt, errMsgThroughputGBpsNotPositive, validateUpdatePoolUnitConversionInputs(req))
	})

	t.Run("rejects negative throughputGBps", func(tt *testing.T) {
		req := &ociserver.UpdatePoolRequest{ThroughputGBps: ociserver.OptFloat64{Value: -0.5, Set: true}}
		assert.Equal(tt, errMsgThroughputGBpsNotPositive, validateUpdatePoolUnitConversionInputs(req))
	})

	t.Run("rejects NaN throughputGBps", func(tt *testing.T) {
		req := &ociserver.UpdatePoolRequest{ThroughputGBps: ociserver.OptFloat64{Value: math.NaN(), Set: true}}
		assert.Equal(tt, errMsgThroughputGBpsNotFinite, validateUpdatePoolUnitConversionInputs(req))
	})

	t.Run("rejects +Inf throughputGBps", func(tt *testing.T) {
		req := &ociserver.UpdatePoolRequest{ThroughputGBps: ociserver.OptFloat64{Value: math.Inf(1), Set: true}}
		assert.Equal(tt, errMsgThroughputGBpsNotFinite, validateUpdatePoolUnitConversionInputs(req))
	})

	t.Run("rejects -Inf throughputGBps", func(tt *testing.T) {
		req := &ociserver.UpdatePoolRequest{ThroughputGBps: ociserver.OptFloat64{Value: math.Inf(-1), Set: true}}
		assert.Equal(tt, errMsgThroughputGBpsNotFinite, validateUpdatePoolUnitConversionInputs(req))
	})

	t.Run("rejects a non-positive node capacity and reports its index", func(tt *testing.T) {
		req := &ociserver.UpdatePoolRequest{
			NodeCapacities: []ociserver.NodeCapacity{
				{SizeInGiB: 100},
				{SizeInGiB: 200},
				{SizeInGiB: -1},
			},
		}
		assert.Equal(tt, fmt.Sprintf(errMsgNodeCapacitySizeNotPositive, 2), validateUpdatePoolUnitConversionInputs(req))
	})

	t.Run("accepts node capacities that are all positive", func(tt *testing.T) {
		req := &ociserver.UpdatePoolRequest{
			NodeCapacities: []ociserver.NodeCapacity{
				{SizeInGiB: 100},
				{SizeInGiB: 200},
			},
		}
		assert.Equal(tt, "", validateUpdatePoolUnitConversionInputs(req))
	})
}

func testNodeCapacity(name, nodeUUID string, sizeInGiB int64) ociserver.NodeCapacity {
	return ociserver.NodeCapacity{Name: name, NodeUUID: nodeUUID, SizeInGiB: sizeInGiB}
}

func TestValidateUpdatePoolNodeCapacityRequiredFields(t *testing.T) {
	full := testNodeCapacity

	t.Run("accepts an empty nodeCapacities array", func(tt *testing.T) {
		req := &ociserver.UpdatePoolRequest{}
		assert.Equal(tt, "", validateUpdatePoolNodeCapacityRequiredFields(req))
	})

	t.Run("accepts entries with all three fields populated", func(tt *testing.T) {
		req := &ociserver.UpdatePoolRequest{
			NodeCapacities: []ociserver.NodeCapacity{
				full("node-a", "uuid-a", 512),
				full("node-b", "uuid-b", 1024),
			},
		}
		assert.Equal(tt, "", validateUpdatePoolNodeCapacityRequiredFields(req))
	})

	t.Run("rejects when name is not set", func(tt *testing.T) {
		req := &ociserver.UpdatePoolRequest{
			NodeCapacities: []ociserver.NodeCapacity{
				{NodeUUID: "uuid-a", SizeInGiB: 512},
			},
		}
		assert.Equal(tt, fmt.Sprintf(errMsgNodeCapacityNameRequired, 0),
			validateUpdatePoolNodeCapacityRequiredFields(req))
	})

	t.Run("rejects when name is set but empty/whitespace", func(tt *testing.T) {
		req := &ociserver.UpdatePoolRequest{
			NodeCapacities: []ociserver.NodeCapacity{
				{Name: "  \t  ", NodeUUID: "uuid-a", SizeInGiB: 512},
			},
		}
		assert.Equal(tt, fmt.Sprintf(errMsgNodeCapacityNameRequired, 0),
			validateUpdatePoolNodeCapacityRequiredFields(req))
	})

	t.Run("rejects when nodeUUID is not set", func(tt *testing.T) {
		req := &ociserver.UpdatePoolRequest{
			NodeCapacities: []ociserver.NodeCapacity{
				{Name: "node-a", SizeInGiB: 512},
			},
		}
		assert.Equal(tt, fmt.Sprintf(errMsgNodeCapacityNodeUUIDRequired, 0),
			validateUpdatePoolNodeCapacityRequiredFields(req))
	})

	t.Run("rejects when nodeUUID is set but empty", func(tt *testing.T) {
		req := &ociserver.UpdatePoolRequest{
			NodeCapacities: []ociserver.NodeCapacity{
				{Name: "node-a", NodeUUID: "", SizeInGiB: 512},
			},
		}
		assert.Equal(tt, fmt.Sprintf(errMsgNodeCapacityNodeUUIDRequired, 0),
			validateUpdatePoolNodeCapacityRequiredFields(req))
	})

	t.Run("reports the index of the first offending entry", func(tt *testing.T) {
		req := &ociserver.UpdatePoolRequest{
			NodeCapacities: []ociserver.NodeCapacity{
				full("node-a", "uuid-a", 512),
				full("node-b", "uuid-b", 1024),
				{Name: "node-c", SizeInGiB: 256},
			},
		}
		assert.Equal(tt, fmt.Sprintf(errMsgNodeCapacityNodeUUIDRequired, 2),
			validateUpdatePoolNodeCapacityRequiredFields(req))
	})

	t.Run("name is checked before nodeUUID for the same entry", func(tt *testing.T) {
		req := &ociserver.UpdatePoolRequest{
			NodeCapacities: []ociserver.NodeCapacity{
				{SizeInGiB: 100},
			},
		}
		assert.Equal(tt, fmt.Sprintf(errMsgNodeCapacityNameRequired, 0),
			validateUpdatePoolNodeCapacityRequiredFields(req))
	})
}

func TestValidateUpdatePoolNodeCapacityUniqueness(t *testing.T) {
	entry := testNodeCapacity

	t.Run("accepts empty nodeCapacities", func(tt *testing.T) {
		req := &ociserver.UpdatePoolRequest{}
		assert.Equal(tt, "", validateUpdatePoolNodeCapacityUniqueness(req))
	})

	t.Run("accepts a single entry", func(tt *testing.T) {
		req := &ociserver.UpdatePoolRequest{
			NodeCapacities: []ociserver.NodeCapacity{entry("n1", "uuid-1", 100)},
		}
		assert.Equal(tt, "", validateUpdatePoolNodeCapacityUniqueness(req))
	})

	t.Run("accepts distinct UUIDs across multiple entries", func(tt *testing.T) {
		req := &ociserver.UpdatePoolRequest{
			NodeCapacities: []ociserver.NodeCapacity{
				entry("n1", "uuid-1", 100),
				entry("n2", "uuid-2", 200),
				entry("n3", "uuid-3", 300),
			},
		}
		assert.Equal(tt, "", validateUpdatePoolNodeCapacityUniqueness(req))
	})

	t.Run("rejects a duplicate UUID and reports the second occurrence index", func(tt *testing.T) {
		req := &ociserver.UpdatePoolRequest{
			NodeCapacities: []ociserver.NodeCapacity{
				entry("n1", "uuid-dup", 100),
				entry("n2", "uuid-dup", 200),
			},
		}
		assert.Equal(tt, fmt.Sprintf(errMsgNodeCapacityNodeUUIDDuplicate, 1, "uuid-dup"),
			validateUpdatePoolNodeCapacityUniqueness(req))
	})

	t.Run("trims whitespace before comparing UUIDs", func(tt *testing.T) {
		req := &ociserver.UpdatePoolRequest{
			NodeCapacities: []ociserver.NodeCapacity{
				entry("n1", "uuid-a", 100),
				entry("n2", "  uuid-a  ", 200),
			},
		}
		assert.Equal(tt, fmt.Sprintf(errMsgNodeCapacityNodeUUIDDuplicate, 1, "uuid-a"),
			validateUpdatePoolNodeCapacityUniqueness(req))
	})

	t.Run("skips entries with unset or blank nodeUUID (delegated to required-fields validator)", func(tt *testing.T) {
		req := &ociserver.UpdatePoolRequest{
			NodeCapacities: []ociserver.NodeCapacity{
				entry("n1", "uuid-1", 100),
				{Name: "n2", SizeInGiB: 200},
				{Name: "n3", NodeUUID: "   ", SizeInGiB: 300},
			},
		}
		assert.Equal(tt, "", validateUpdatePoolNodeCapacityUniqueness(req))
	})
}

// TestIsSharedHA pins the shared-HA classification on the (primary, secondary) pair.
// "Shared HA" means the pool runs in a single AD: the secondary is either unset
// (empty) or identical to the primary. Anything else is non-shared (regional) HA.
func TestIsSharedHA(t *testing.T) {
	cases := []struct {
		name      string
		primary   string
		secondary string
		want      bool
	}{
		{name: "empty secondary is shared", primary: "ad1", secondary: "", want: true},
		{name: "secondary equals primary is shared", primary: "ad1", secondary: "ad1", want: true},
		{name: "secondary differs from primary is non-shared", primary: "ad1", secondary: "ad2", want: false},
		{name: "both empty is shared", primary: "", secondary: "", want: true},
		{name: "primary empty but secondary set is non-shared", primary: "", secondary: "ad1", want: false},
		{name: "secondary differs by whitespace is non-shared (no trim)", primary: "ad1", secondary: " ad1", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(tt *testing.T) {
			assert.Equal(tt, tc.want, isSharedHA(tc.primary, tc.secondary))
		})
	}
}

// TestCreatePool_NonSharedHARequiresDataEndpointCount2 covers the new validation
// rule: when the request describes a non-shared (regional) HA pool, DataEndpointCount
// must be exactly 2. Any other (even) value must be rejected with a 400.
func TestCreatePool_NonSharedHARequiresDataEndpointCount2(t *testing.T) {
	mockOrchestrator := factory.NewMockOrchestratorFactory(t)
	h := Handler{Orchestrator: mockOrchestrator}
	req := validCreatePoolRequest()
	req.PoolOCID = "ocid1.pool.oc1..non-shared-bad-count"
	req.SecondaryAvailabilityDomain = ociserver.OptString{Value: "ad2", Set: true} // non-shared
	req.DataEndpointCount = 4                                                      // even but != 2

	res, err := h.CreatePool(contextWithOpcRequestID(nil, defaultTestOPC), req, defaultCreatePoolParams())

	assert.NoError(t, err)
	bad, ok := res.(*ociserver.CreatePoolBadRequest)
	assert.True(t, ok, "response should be *ociserver.CreatePoolBadRequest")
	assert.Equal(t, defaultTestOPC, bad.OpcWorkRequestID)
	assert.Equal(t, req.PoolOCID, bad.Response.PoolOCID)
	assert.Equal(t, string(workflowquery.WorkflowStatusFailed), bad.Response.Status)
	assert.Equal(t, errMsgInvalidConfigForNonSharedHA, bad.Response.ErrorMessage)
	// Orchestrator must not be invoked when validation fails up front.
	mockOrchestrator.AssertNotCalled(t, "CreatePool", mock.Anything, mock.Anything)
}

// TestValidateCreatePoolRequest exercises every branch of validateCreatePoolRequest
// directly (no Handler, no mock). Each non-nil-request case starts from a known-good
// base and mutates exactly one field so the failing branch is unambiguous.
func TestValidateCreatePoolRequest(t *testing.T) {
	t.Run("nil request returns error", func(tt *testing.T) {
		params := defaultCreatePoolParams()
		err := validateCreatePoolRequest(nil, &params)
		assert.EqualError(tt, err, "request body is required")
	})

	t.Run("nil params returns error", func(tt *testing.T) {
		err := validateCreatePoolRequest(validCreatePoolRequest(), nil)
		assert.EqualError(tt, err, "request params are required")
	})

	cases := []struct {
		name    string
		mutate  func(*ociserver.CreatePoolRequest, *ociserver.CreatePoolParams)
		wantErr string // empty string means expect nil
	}{
		{
			name:    "valid request passes",
			mutate:  nil,
			wantErr: "",
		},
		{
			name:    "empty pool OCID rejected with empty error",
			mutate:  func(r *ociserver.CreatePoolRequest, _ *ociserver.CreatePoolParams) { r.PoolOCID = "" },
			wantErr: errMsgEmptyPoolOCID,
		},
		{
			name:    "whitespace-only pool OCID rejected as empty after TrimSpace",
			mutate:  func(r *ociserver.CreatePoolRequest, _ *ociserver.CreatePoolParams) { r.PoolOCID = "   " },
			wantErr: errMsgEmptyPoolOCID,
		},
		{
			name:    "non-empty malformed pool OCID rejected with structural error",
			mutate:  func(r *ociserver.CreatePoolRequest, _ *ociserver.CreatePoolParams) { r.PoolOCID = "not-an-ocid" },
			wantErr: errMsgInvalidPoolOCID,
		},
		{
			name:    "empty compartment OCID rejected with empty error",
			mutate:  func(r *ociserver.CreatePoolRequest, _ *ociserver.CreatePoolParams) { r.CompartmentOCID = "" },
			wantErr: errMsgEmptyCompartmentOCID,
		},
		{
			name:    "non-empty malformed compartment OCID rejected with structural error",
			mutate:  func(r *ociserver.CreatePoolRequest, _ *ociserver.CreatePoolParams) { r.CompartmentOCID = "x" },
			wantErr: errMsgInvalidCompartmentOCID,
		},
		{
			name:    "empty subnet OCID rejected with empty error",
			mutate:  func(r *ociserver.CreatePoolRequest, _ *ociserver.CreatePoolParams) { r.SubnetId = "" },
			wantErr: errMsgEmptySubnetID,
		},
		{
			name:    "non-empty malformed subnet OCID rejected with structural error",
			mutate:  func(r *ociserver.CreatePoolRequest, _ *ociserver.CreatePoolParams) { r.SubnetId = "subnet-123" },
			wantErr: errMsgInvalidSubnetID,
		},
		{
			name:    "zero size rejected",
			mutate:  func(r *ociserver.CreatePoolRequest, _ *ociserver.CreatePoolParams) { r.SizeInGiB = 0 },
			wantErr: errMsgNonPositiveSizeInGiB,
		},
		{
			name:    "negative size rejected",
			mutate:  func(r *ociserver.CreatePoolRequest, _ *ociserver.CreatePoolParams) { r.SizeInGiB = -1 },
			wantErr: errMsgNonPositiveSizeInGiB,
		},
		{
			name:    "empty data NIC subnet OCID rejected with empty error",
			mutate:  func(r *ociserver.CreatePoolRequest, _ *ociserver.CreatePoolParams) { r.DataNicSubnetId = "" },
			wantErr: errMsgEmptyDataNicSubnetID,
		},
		{
			name: "non-empty malformed data NIC subnet OCID rejected with structural error",
			mutate: func(r *ociserver.CreatePoolRequest, _ *ociserver.CreatePoolParams) {
				r.DataNicSubnetId = "data-subnet-1"
			},
			wantErr: errMsgInvalidDataNicSubnetID,
		},
		{
			name:    "empty admin password OCID rejected with empty error",
			mutate:  func(r *ociserver.CreatePoolRequest, _ *ociserver.CreatePoolParams) { r.OciAdminPassword.Ocid = "" },
			wantErr: errMsgEmptyAdminPasswordOCID,
		},
		{
			name:    "non-empty malformed admin password OCID rejected with structural error",
			mutate:  func(r *ociserver.CreatePoolRequest, _ *ociserver.CreatePoolParams) { r.OciAdminPassword.Ocid = "x" },
			wantErr: errMsgInvalidAdminPasswordOCID,
		},
		{
			name:    "empty admin password version rejected with empty error",
			mutate:  func(r *ociserver.CreatePoolRequest, _ *ociserver.CreatePoolParams) { r.OciAdminPassword.Version = "" },
			wantErr: errMsgEmptyAdminPasswordVersion,
		},
		{
			name:    "zero throughput rejected",
			mutate:  func(r *ociserver.CreatePoolRequest, _ *ociserver.CreatePoolParams) { r.ThroughputGBps = 0 },
			wantErr: errMsgNonPositiveThroughputGBps,
		},
		{
			name:    "negative throughput rejected",
			mutate:  func(r *ociserver.CreatePoolRequest, _ *ociserver.CreatePoolParams) { r.ThroughputGBps = -0.5 },
			wantErr: errMsgNonPositiveThroughputGBps,
		},
		{
			name:    "odd DataEndpointCount rejected",
			mutate:  func(r *ociserver.CreatePoolRequest, _ *ociserver.CreatePoolParams) { r.DataEndpointCount = 3 },
			wantErr: errMsgOddDataEndpointCount,
		},
		{
			name:    "empty tenancy OCID (from params) rejected with empty error",
			mutate:  func(_ *ociserver.CreatePoolRequest, p *ociserver.CreatePoolParams) { p.TenancyOcid = "" },
			wantErr: errMsgEmptyTenancyOcid,
		},
		{
			name:    "non-empty malformed tenancy OCID (from params) rejected with structural error",
			mutate:  func(_ *ociserver.CreatePoolRequest, p *ociserver.CreatePoolParams) { p.TenancyOcid = "not-an-ocid" },
			wantErr: errMsgInvalidTenancyOcid,
		},
		{
			name:    "empty resourceName rejected",
			mutate:  func(r *ociserver.CreatePoolRequest, _ *ociserver.CreatePoolParams) { r.ResourceName = "" },
			wantErr: errMsgEmptyResourceName,
		},
		{
			name:    "whitespace-only resourceName rejected (TrimSpace)",
			mutate:  func(r *ociserver.CreatePoolRequest, _ *ociserver.CreatePoolParams) { r.ResourceName = "   \t\n " },
			wantErr: errMsgEmptyResourceName,
		},
		{
			name:    "empty primaryAvailabilityDomain rejected",
			mutate:  func(r *ociserver.CreatePoolRequest, _ *ociserver.CreatePoolParams) { r.PrimaryAvailabilityDomain = "" },
			wantErr: errMsgEmptyPrimaryAD,
		},
		{
			name: "whitespace-only primaryAvailabilityDomain rejected (TrimSpace)",
			mutate: func(r *ociserver.CreatePoolRequest, _ *ociserver.CreatePoolParams) {
				r.PrimaryAvailabilityDomain = " \t"
			},
			wantErr: errMsgEmptyPrimaryAD,
		},
		{
			name: "non-shared HA with DataEndpointCount=4 rejected",
			mutate: func(r *ociserver.CreatePoolRequest, _ *ociserver.CreatePoolParams) {
				r.SecondaryAvailabilityDomain = ociserver.OptString{Value: "ad2", Set: true}
				r.DataEndpointCount = 4
			},
			wantErr: errMsgInvalidConfigForNonSharedHA,
		},
		{
			name: "non-shared HA with DataEndpointCount=2 passes",
			mutate: func(r *ociserver.CreatePoolRequest, _ *ociserver.CreatePoolParams) {
				r.SecondaryAvailabilityDomain = ociserver.OptString{Value: "ad2", Set: true}
				r.DataEndpointCount = 2
			},
			wantErr: "",
		},
		{
			name: "shared HA via secondary equal to primary with DataEndpointCount=4 passes",
			mutate: func(r *ociserver.CreatePoolRequest, _ *ociserver.CreatePoolParams) {
				r.SecondaryAvailabilityDomain = ociserver.OptString{Value: "ad1", Set: true}
				r.DataEndpointCount = 4
			},
			wantErr: "",
		},
		{
			name: "shared HA via empty secondary with DataEndpointCount=4 passes",
			mutate: func(r *ociserver.CreatePoolRequest, _ *ociserver.CreatePoolParams) {
				r.DataEndpointCount = 4
			},
			wantErr: "",
		},
		{
			name: "valid tieringConfig passes",
			mutate: func(r *ociserver.CreatePoolRequest, _ *ociserver.CreatePoolParams) {
				r.TieringConfig = validTieringConfig()
			},
			wantErr: "",
		},
		{
			name: "tieringConfig empty secretId rejected",
			mutate: func(r *ociserver.CreatePoolRequest, _ *ociserver.CreatePoolParams) {
				tc := validTieringConfig()
				tc.Value.SecretId = ""
				r.TieringConfig = tc
			},
			wantErr: errMsgEmptyTieringSecretID,
		},
		{
			name: "tieringConfig whitespace-only secretId rejected (TrimSpace)",
			mutate: func(r *ociserver.CreatePoolRequest, _ *ociserver.CreatePoolParams) {
				tc := validTieringConfig()
				tc.Value.SecretId = " \t"
				r.TieringConfig = tc
			},
			wantErr: errMsgEmptyTieringSecretID,
		},
		{
			name: "tieringConfig non-empty malformed secretId rejected (invalid OCID)",
			mutate: func(r *ociserver.CreatePoolRequest, _ *ociserver.CreatePoolParams) {
				tc := validTieringConfig()
				tc.Value.SecretId = "not-an-ocid"
				r.TieringConfig = tc
			},
			wantErr: errMsgInvalidTieringSecretID,
		},
		{
			name: "tieringConfig empty namespace rejected",
			mutate: func(r *ociserver.CreatePoolRequest, _ *ociserver.CreatePoolParams) {
				tc := validTieringConfig()
				tc.Value.Namespace = ""
				r.TieringConfig = tc
			},
			wantErr: errMsgEmptyTieringNamespace,
		},
		{
			name: "tieringConfig whitespace-only namespace rejected (TrimSpace)",
			mutate: func(r *ociserver.CreatePoolRequest, _ *ociserver.CreatePoolParams) {
				tc := validTieringConfig()
				tc.Value.Namespace = "   "
				r.TieringConfig = tc
			},
			wantErr: errMsgEmptyTieringNamespace,
		},
		{
			name: "tieringConfig empty bucketName rejected",
			mutate: func(r *ociserver.CreatePoolRequest, _ *ociserver.CreatePoolParams) {
				tc := validTieringConfig()
				tc.Value.BucketName = ""
				r.TieringConfig = tc
			},
			wantErr: errMsgEmptyTieringBucketName,
		},
		{
			name: "tieringConfig whitespace-only bucketName rejected (TrimSpace)",
			mutate: func(r *ociserver.CreatePoolRequest, _ *ociserver.CreatePoolParams) {
				tc := validTieringConfig()
				tc.Value.BucketName = "\n\t "
				r.TieringConfig = tc
			},
			wantErr: errMsgEmptyTieringBucketName,
		},
		{
			name: "tieringConfig empty serverName rejected",
			mutate: func(r *ociserver.CreatePoolRequest, _ *ociserver.CreatePoolParams) {
				tc := validTieringConfig()
				tc.Value.ServerName = ""
				r.TieringConfig = tc
			},
			wantErr: errMsgEmptyTieringServerName,
		},
		{
			name: "tieringConfig whitespace-only serverName rejected (TrimSpace)",
			mutate: func(r *ociserver.CreatePoolRequest, _ *ociserver.CreatePoolParams) {
				tc := validTieringConfig()
				tc.Value.ServerName = "\t"
				r.TieringConfig = tc
			},
			wantErr: errMsgEmptyTieringServerName,
		},
		{
			name: "tieringConfig unset is allowed (no validation triggered)",
			mutate: func(r *ociserver.CreatePoolRequest, _ *ociserver.CreatePoolParams) {
				r.TieringConfig = ociserver.OptTieringConfig{Set: false}
			},
			wantErr: "",
		},
		{
			name: "kmsKeyId unset is allowed (no validation triggered)",
			mutate: func(r *ociserver.CreatePoolRequest, _ *ociserver.CreatePoolParams) {
				r.KmsKeyId = ociserver.OptString{Set: false}
			},
			wantErr: "",
		},
		{
			name: "kmsKeyId set with valid OCID passes",
			mutate: func(r *ociserver.CreatePoolRequest, _ *ociserver.CreatePoolParams) {
				r.KmsKeyId = ociserver.NewOptString("ocid1.key.oc1.iad.kkk")
			},
			wantErr: "",
		},
		{
			name: "kmsKeyId set but empty rejected",
			mutate: func(r *ociserver.CreatePoolRequest, _ *ociserver.CreatePoolParams) {
				r.KmsKeyId = ociserver.NewOptString("")
			},
			wantErr: errMsgEmptyKmsKeyId,
		},
		{
			name: "kmsKeyId set whitespace-only rejected (TrimSpace)",
			mutate: func(r *ociserver.CreatePoolRequest, _ *ociserver.CreatePoolParams) {
				r.KmsKeyId = ociserver.NewOptString("  \t\n ")
			},
			wantErr: errMsgEmptyKmsKeyId,
		},
		{
			name: "kmsKeyId set with malformed OCID rejected",
			mutate: func(r *ociserver.CreatePoolRequest, _ *ociserver.CreatePoolParams) {
				r.KmsKeyId = ociserver.NewOptString("not-an-ocid")
			},
			wantErr: errMsgInvalidKmsKeyId,
		},
		{
			name: "nsgIds nil is allowed (no validation triggered)",
			mutate: func(r *ociserver.CreatePoolRequest, _ *ociserver.CreatePoolParams) {
				r.NsgIds = nil
			},
			wantErr: "",
		},
		{
			name: "nsgIds with two valid OCIDs passes",
			mutate: func(r *ociserver.CreatePoolRequest, _ *ociserver.CreatePoolParams) {
				r.NsgIds = []string{
					"ocid1.networksecuritygroup.oc1.iad.nsg-a",
					"ocid1.networksecuritygroup.oc1.iad.nsg-b",
				}
			},
			wantErr: "",
		},
		{
			name: "nsgIds with empty entry at index 1 rejected (index is in the message)",
			mutate: func(r *ociserver.CreatePoolRequest, _ *ociserver.CreatePoolParams) {
				r.NsgIds = []string{
					"ocid1.networksecuritygroup.oc1.iad.nsg-a",
					"",
				}
			},
			wantErr: "nsgIds[1] must not be empty",
		},
		{
			name: "nsgIds with whitespace-only entry at index 0 rejected (TrimSpace)",
			mutate: func(r *ociserver.CreatePoolRequest, _ *ociserver.CreatePoolParams) {
				r.NsgIds = []string{
					"  \t",
					"ocid1.networksecuritygroup.oc1.iad.nsg-b",
				}
			},
			wantErr: "nsgIds[0] must not be empty",
		},
		{
			name: "nsgIds with malformed entry at index 1 rejected (index is in the message)",
			mutate: func(r *ociserver.CreatePoolRequest, _ *ociserver.CreatePoolParams) {
				r.NsgIds = []string{
					"ocid1.networksecuritygroup.oc1.iad.nsg-a",
					"not-an-ocid",
				}
			},
			wantErr: "nsgIds[1] must be a valid OCID",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(tt *testing.T) {
			req := validCreatePoolRequest()
			params := defaultCreatePoolParams()
			if tc.mutate != nil {
				tc.mutate(req, &params)
			}
			err := validateCreatePoolRequest(req, &params)
			if tc.wantErr == "" {
				assert.NoError(tt, err)
				return
			}
			assert.EqualError(tt, err, tc.wantErr)
		})
	}
}

func TestValidateCreatePoolRequest_TrimsInPlace(t *testing.T) {
	req := validCreatePoolRequest()
	req.PoolOCID = "  ocid1.pool.oc1..valid  "
	req.CompartmentOCID = "\tocid1.compartment.oc1..valid\n"
	req.ResourceName = "  My Pool  "
	req.SubnetId = " ocid1.subnet.oc1.iad.valid "
	req.DataNicSubnetId = "\nocid1.subnet.oc1.iad.validdata\t"
	req.PrimaryAvailabilityDomain = "  ad1\t"
	req.SecondaryAvailabilityDomain = ociserver.OptString{Value: " ad1 ", Set: true}
	req.MediatorAvailabilityDomain = ociserver.OptString{Value: "  ad9  ", Set: true}
	req.OciAdminPassword = ociserver.OCIOCIDVersionRef{
		Ocid:    " ocid1.secret.oc1..valid ",
		Version: " 1 ",
	}
	req.Description = ociserver.OptString{Value: "  some desc  ", Set: true}
	req.DataEndpointCount = 2
	req.TieringConfig = ociserver.OptTieringConfig{
		Set: true,
		Value: ociserver.TieringConfig{
			SecretId:   " ocid1.vaultsecret.oc1..tiervalid ",
			Namespace:  "\taxqogasfjw45\n",
			BucketName: "  cold-tier-bucket  ",
			ServerName: " compat.objectstorage.us-ashburn-1.oraclecloud.com\t",
		},
	}
	req.KmsKeyId = ociserver.NewOptString("  ocid1.key.oc1.iad.kkk  ")
	req.NsgIds = []string{
		"  ocid1.networksecuritygroup.oc1.iad.nsg-a\t",
		"\nocid1.networksecuritygroup.oc1.iad.nsg-b  ",
	}

	params := ociserver.CreatePoolParams{TenancyOcid: "  ocid1.tenancy.oc1..aaaaaaaatestaaa  "}

	err := validateCreatePoolRequest(req, &params)
	require.NoError(t, err, "padded-but-otherwise-valid request must validate after the in-place trim")

	assert.Equal(t, "ocid1.pool.oc1..valid", req.PoolOCID,
		"PoolOCID must be trimmed in place so the orchestrator and DB store the canonical form")
	assert.Equal(t, "ocid1.compartment.oc1..valid", req.CompartmentOCID)
	assert.Equal(t, "My Pool", req.ResourceName,
		"ResourceName trim is critical: human-readable name must not carry copy/paste whitespace into the DB")
	assert.Equal(t, "ocid1.subnet.oc1.iad.valid", req.SubnetId)
	assert.Equal(t, "ocid1.subnet.oc1.iad.validdata", req.DataNicSubnetId)
	assert.Equal(t, "ad1", req.PrimaryAvailabilityDomain)
	assert.Equal(t, "ad1", req.SecondaryAvailabilityDomain.Value,
		"OptString-wrapped optional fields must also be trimmed so isSharedHA's equality check is not defeated by whitespace")
	assert.Equal(t, "ad9", req.MediatorAvailabilityDomain.Value)
	assert.Equal(t, "ocid1.secret.oc1..valid", req.OciAdminPassword.Ocid)
	assert.Equal(t, "1", req.OciAdminPassword.Version,
		"Version is parsed by strconv.ParseInt downstream, which rejects ' 1 '; trim before validation lets the parse succeed")
	assert.Equal(t, "some desc", req.Description.Value)
	assert.Equal(t, "ocid1.tenancy.oc1..aaaaaaaatestaaa", params.TenancyOcid,
		"path/query param TenancyOcid must be trimmed via the *params handle so the orchestrator AccountName matches the DB key")
	assert.Equal(t, "ocid1.vaultsecret.oc1..tiervalid", req.TieringConfig.Value.SecretId,
		"TieringConfig.SecretId must be trimmed in place so the orchestrator stores the canonical OCID")
	assert.Equal(t, "axqogasfjw45", req.TieringConfig.Value.Namespace)
	assert.Equal(t, "cold-tier-bucket", req.TieringConfig.Value.BucketName)
	assert.Equal(t, "compat.objectstorage.us-ashburn-1.oraclecloud.com", req.TieringConfig.Value.ServerName)
	assert.Equal(t, "ocid1.key.oc1.iad.kkk", req.KmsKeyId.Value,
		"KmsKeyId must be trimmed in place so the orchestrator forwards the canonical OCID to VLM")
	assert.Equal(t, []string{
		"ocid1.networksecuritygroup.oc1.iad.nsg-a",
		"ocid1.networksecuritygroup.oc1.iad.nsg-b",
	}, req.NsgIds, "NsgIds entries must be trimmed in place so each NSG OCID is forwarded in canonical form")
}

// TestValidateDeletePoolRequest covers each validation branch for the Delete
// path (no request body, only path/query params) and pins that nil params is
// rejected before any field access.
func TestValidateDeletePoolRequest(t *testing.T) {
	t.Run("nil params returns error", func(tt *testing.T) {
		err := validateDeletePoolRequest(nil)
		assert.EqualError(tt, err, "request params are required")
	})

	t.Run("valid params pass", func(tt *testing.T) {
		params := ociserver.DeletePoolParams{
			PoolOCID:    "ocid1.pool.oc1.iad.valid",
			TenancyOcid: defaultTestTenancyOCID,
		}
		assert.NoError(tt, validateDeletePoolRequest(&params))
	})

	t.Run("non-empty malformed PoolOCID rejected with structural error", func(tt *testing.T) {
		params := ociserver.DeletePoolParams{
			PoolOCID:    "not-an-ocid",
			TenancyOcid: defaultTestTenancyOCID,
		}
		assert.EqualError(tt, validateDeletePoolRequest(&params), errMsgInvalidPoolOCID)
	})

	t.Run("empty PoolOCID rejected with empty error (not structural)", func(tt *testing.T) {
		params := ociserver.DeletePoolParams{
			PoolOCID:    "",
			TenancyOcid: defaultTestTenancyOCID,
		}
		// errMsgEmptyPoolOCID, NOT errMsgInvalidPoolOCID — distinct caller mistake,
		// distinct diagnostic.
		assert.EqualError(tt, validateDeletePoolRequest(&params), errMsgEmptyPoolOCID)
	})

	t.Run("whitespace-only PoolOCID is empty-after-trim", func(tt *testing.T) {
		params := ociserver.DeletePoolParams{
			PoolOCID:    "  \t",
			TenancyOcid: defaultTestTenancyOCID,
		}
		// The normalize step turns "  \t" into "" before validation,
		// so this collapses to the empty-error path (not the structural one).
		assert.EqualError(tt, validateDeletePoolRequest(&params), errMsgEmptyPoolOCID)
	})

	t.Run("empty TenancyOcid rejected with empty error", func(tt *testing.T) {
		params := ociserver.DeletePoolParams{
			PoolOCID:    "ocid1.pool.oc1.iad.valid",
			TenancyOcid: "",
		}
		assert.EqualError(tt, validateDeletePoolRequest(&params), errMsgEmptyTenancyOcid)
	})

	t.Run("non-empty malformed TenancyOcid rejected with structural error", func(tt *testing.T) {
		params := ociserver.DeletePoolParams{
			PoolOCID:    "ocid1.pool.oc1.iad.valid",
			TenancyOcid: "not-an-ocid",
		}
		assert.EqualError(tt, validateDeletePoolRequest(&params), errMsgInvalidTenancyOcid)
	})
}

func TestValidateDeletePoolRequest_TrimsInPlace(t *testing.T) {
	params := ociserver.DeletePoolParams{
		PoolOCID:    " ocid1.pool.oc1.iad.valid\t",
		TenancyOcid: "  ocid1.tenancy.oc1..aaaaaaaatestaaa  ",
	}

	require.NoError(t, validateDeletePoolRequest(&params),
		"padded-but-otherwise-valid params must validate after the in-place trim")

	assert.Equal(t, "ocid1.pool.oc1.iad.valid", params.PoolOCID,
		"PoolOCID must be trimmed in place so the orchestrator's pool lookup matches the create-time value")
	assert.Equal(t, "ocid1.tenancy.oc1..aaaaaaaatestaaa", params.TenancyOcid,
		"TenancyOcid must be trimmed in place so AccountName matches the DB key")
}

func TestCreatePool_MapsKmsKeyIdToCmekOcid(t *testing.T) {
	const cmek = "ocid1.key.oc1.iad.cmek-test-1"
	mockOrchestrator := factory.NewMockOrchestratorFactory(t)
	mockOrchestrator.EXPECT().CreatePool(
		mock.Anything,
		mock.MatchedBy(func(p *commonparams.CreatePoolParams) bool {
			return p != nil && p.KmsKeyId == cmek
		}),
	).Return(&models.Pool{
		BaseModel: models.BaseModel{UUID: "pool-cmek", CreatedAt: time.Now(), UpdatedAt: time.Now()},
		Name:      "cmek-pool",
		State:     "CREATING",
	}, "wf-cmek", nil)
	h := Handler{Orchestrator: mockOrchestrator}

	req := validCreatePoolRequest()
	req.KmsKeyId = ociserver.NewOptString(cmek)

	res, err := h.CreatePool(contextWithOpcRequestID(nil, defaultTestOPC), req, defaultCreatePoolParams())

	assert.NoError(t, err)
	_, ok := res.(*ociserver.CreatePoolAcceptedResponseHeaders)
	assert.True(t, ok)
}

func TestCreatePool_MapsNsgIdsToCustomerNSGs(t *testing.T) {
	nsgs := []string{
		"ocid1.networksecuritygroup.oc1.iad.nsg-1",
		"ocid1.networksecuritygroup.oc1.iad.nsg-2",
	}
	mockOrchestrator := factory.NewMockOrchestratorFactory(t)
	mockOrchestrator.EXPECT().CreatePool(
		mock.Anything,
		mock.MatchedBy(func(p *commonparams.CreatePoolParams) bool {
			return p != nil &&
				len(p.NsgIds) == len(nsgs) &&
				p.NsgIds[0] == nsgs[0] &&
				p.NsgIds[1] == nsgs[1]
		}),
	).Return(&models.Pool{
		BaseModel: models.BaseModel{UUID: "pool-nsg", CreatedAt: time.Now(), UpdatedAt: time.Now()},
		Name:      "nsg-pool",
		State:     "CREATING",
	}, "wf-nsg", nil)
	h := Handler{Orchestrator: mockOrchestrator}

	req := validCreatePoolRequest()
	req.NsgIds = nsgs

	res, err := h.CreatePool(contextWithOpcRequestID(nil, defaultTestOPC), req, defaultCreatePoolParams())

	assert.NoError(t, err)
	_, ok := res.(*ociserver.CreatePoolAcceptedResponseHeaders)
	assert.True(t, ok)
}

func TestCreatePool_DefensiveCopiesNsgIds(t *testing.T) {
	original := []string{
		"ocid1.networksecuritygroup.oc1.iad.nsg-1",
		"ocid1.networksecuritygroup.oc1.iad.nsg-2",
	}

	var captured []string
	mockOrchestrator := factory.NewMockOrchestratorFactory(t)
	mockOrchestrator.EXPECT().CreatePool(mock.Anything, mock.Anything).
		Run(func(_ context.Context, p *commonparams.CreatePoolParams) {
			captured = p.NsgIds
		}).
		Return(&models.Pool{
			BaseModel: models.BaseModel{UUID: "pool-defensive", CreatedAt: time.Now(), UpdatedAt: time.Now()},
			Name:      "defensive-pool",
			State:     "CREATING",
		}, "wf-defensive", nil)
	h := Handler{Orchestrator: mockOrchestrator}

	req := validCreatePoolRequest()
	req.NsgIds = original

	_, err := h.CreatePool(contextWithOpcRequestID(nil, defaultTestOPC), req, defaultCreatePoolParams())
	require.NoError(t, err)
	require.Len(t, captured, 2, "orchestrator must receive both NSG OCIDs")

	req.NsgIds[0] = "ocid1.networksecuritygroup.oc1.iad.mutated-after-return"

	assert.Equal(t, "ocid1.networksecuritygroup.oc1.iad.nsg-1", captured[0],
		"captured NsgIds must be insulated from post-handler mutation of req.NsgIds; the handler must defensive-copy the slice")
	assert.Equal(t, "ocid1.networksecuritygroup.oc1.iad.nsg-2", captured[1],
		"second NSG OCID must also survive post-handler mutation, confirming the whole slice was copied")
}

func TestCreatePool_DefensiveCopyOfNsgIdsPreservesNilSemantics(t *testing.T) {
	var captured []string
	captured = []string{"sentinel-to-prove-overwrite"}

	mockOrchestrator := factory.NewMockOrchestratorFactory(t)
	mockOrchestrator.EXPECT().CreatePool(mock.Anything, mock.Anything).
		Run(func(_ context.Context, p *commonparams.CreatePoolParams) {
			captured = p.NsgIds
		}).
		Return(&models.Pool{
			BaseModel: models.BaseModel{UUID: "pool-nilnsg", CreatedAt: time.Now(), UpdatedAt: time.Now()},
			Name:      "nilnsg-pool",
			State:     "CREATING",
		}, "wf-nilnsg", nil)
	h := Handler{Orchestrator: mockOrchestrator}

	req := validCreatePoolRequest()
	_, err := h.CreatePool(contextWithOpcRequestID(nil, defaultTestOPC), req, defaultCreatePoolParams())
	require.NoError(t, err)

	assert.Nil(t, captured,
		"defensive copy of a nil source slice must remain nil; the orchestrator distinguishes nil (\"no NSGs specified\") from an empty slice (\"explicitly clear NSGs\")")
}

func TestCreatePool_TieringConfigSet_MapsToFabricPoolConfig(t *testing.T) {
	tc := ociserver.TieringConfig{
		SecretId:   "ocid1.vaultsecret.oc1.iad.fp-secret",
		Namespace:  "fp-ns",
		BucketName: "fp-bucket",
		ServerName: "compat.objectstorage.us-ashburn-1.oraclecloud.com",
	}
	mockOrchestrator := factory.NewMockOrchestratorFactory(t)
	mockOrchestrator.EXPECT().CreatePool(
		mock.Anything,
		mock.MatchedBy(func(p *commonparams.CreatePoolParams) bool {
			return p != nil &&
				p.FabricPoolConfig != nil &&
				p.FabricPoolConfig.SecretOcid == tc.SecretId &&
				p.FabricPoolConfig.Namespace == tc.Namespace &&
				p.FabricPoolConfig.BucketName == tc.BucketName &&
				p.FabricPoolConfig.ServerURL == tc.ServerName
		}),
	).Return(&models.Pool{
		BaseModel: models.BaseModel{UUID: "pool-fp", CreatedAt: time.Now(), UpdatedAt: time.Now()},
		Name:      "fp-pool",
		State:     "CREATING",
	}, "wf-fp", nil)
	h := Handler{Orchestrator: mockOrchestrator}

	req := validCreatePoolRequest()
	req.TieringConfig = ociserver.OptTieringConfig{Value: tc, Set: true}

	res, err := h.CreatePool(contextWithOpcRequestID(nil, defaultTestOPC), req, defaultCreatePoolParams())

	assert.NoError(t, err)
	_, ok := res.(*ociserver.CreatePoolAcceptedResponseHeaders)
	assert.True(t, ok)
}

func TestCreatePool_TieringConfigUnset_LeavesFabricPoolConfigNil(t *testing.T) {
	mockOrchestrator := factory.NewMockOrchestratorFactory(t)
	mockOrchestrator.EXPECT().CreatePool(
		mock.Anything,
		mock.MatchedBy(func(p *commonparams.CreatePoolParams) bool {
			return p != nil && p.FabricPoolConfig == nil
		}),
	).Return(&models.Pool{
		BaseModel: models.BaseModel{UUID: "pool-no-fp", CreatedAt: time.Now(), UpdatedAt: time.Now()},
		Name:      "no-fp-pool",
		State:     "CREATING",
	}, "wf-no-fp", nil)
	h := Handler{Orchestrator: mockOrchestrator}

	req := validCreatePoolRequest()
	// req.TieringConfig left zero-valued (Set == false)

	res, err := h.CreatePool(contextWithOpcRequestID(nil, defaultTestOPC), req, defaultCreatePoolParams())

	assert.NoError(t, err)
	_, ok := res.(*ociserver.CreatePoolAcceptedResponseHeaders)
	assert.True(t, ok)
}

func TestCreatePool_SecurityAttributesSet_MapsToCustomerSecurityAttributes(t *testing.T) {
	mockOrchestrator := factory.NewMockOrchestratorFactory(t)
	mockOrchestrator.EXPECT().CreatePool(
		mock.Anything,
		mock.MatchedBy(func(p *commonparams.CreatePoolParams) bool {
			if p == nil || p.SecurityAttributes == nil {
				return false
			}
			ns1, ok := p.SecurityAttributes["ns1"]
			if !ok {
				return false
			}
			app, ok := ns1["app"].(map[string]string)
			if !ok {
				return false
			}
			return app["value"] == "app1" && app["mode"] == "enforce"
		}),
	).Return(&models.Pool{
		BaseModel: models.BaseModel{UUID: "pool-sa", CreatedAt: time.Now(), UpdatedAt: time.Now()},
		Name:      "sa-pool",
		State:     "CREATING",
	}, "wf-sa", nil)
	h := Handler{Orchestrator: mockOrchestrator}

	req := validCreatePoolRequest()
	req.SecurityAttributes = ociserver.OptSecurityAttributes{
		Value: ociserver.SecurityAttributes{
			"ns1": {
				"app": ociserver.SecurityAttributeValue{
					Value: "app1",
					Mode:  ociserver.SecurityAttributeValueModeEnforce,
				},
			},
		},
		Set: true,
	}

	res, err := h.CreatePool(contextWithOpcRequestID(nil, defaultTestOPC), req, defaultCreatePoolParams())

	assert.NoError(t, err)
	_, ok := res.(*ociserver.CreatePoolAcceptedResponseHeaders)
	assert.True(t, ok)
}

func TestCreatePool_SecurityAttributesUnset_LeavesCustomerSecurityAttributesNil(t *testing.T) {
	mockOrchestrator := factory.NewMockOrchestratorFactory(t)
	mockOrchestrator.EXPECT().CreatePool(
		mock.Anything,
		mock.MatchedBy(func(p *commonparams.CreatePoolParams) bool {
			return p != nil && p.SecurityAttributes == nil
		}),
	).Return(&models.Pool{
		BaseModel: models.BaseModel{UUID: "pool-no-sa", CreatedAt: time.Now(), UpdatedAt: time.Now()},
		Name:      "no-sa-pool",
		State:     "CREATING",
	}, "wf-no-sa", nil)
	h := Handler{Orchestrator: mockOrchestrator}

	req := validCreatePoolRequest()
	// req.SecurityAttributes left zero-valued (Set == false)

	res, err := h.CreatePool(contextWithOpcRequestID(nil, defaultTestOPC), req, defaultCreatePoolParams())

	assert.NoError(t, err)
	_, ok := res.(*ociserver.CreatePoolAcceptedResponseHeaders)
	assert.True(t, ok)
}

func TestToCustomerSecurityAttributes(t *testing.T) {
	t.Run("nil input returns nil", func(tt *testing.T) {
		got := toCustomerSecurityAttributes(nil)
		assert.Nil(tt, got, "nil input must return nil so callers can use a single nil-check guard")
	})

	t.Run("empty map returns nil", func(tt *testing.T) {
		got := toCustomerSecurityAttributes(ociserver.SecurityAttributes{})
		assert.Nil(tt, got, "empty input must return nil; the helper uses len(in)==0 short-circuit")
	})

	t.Run("namespace with empty inner map is skipped", func(tt *testing.T) {
		in := ociserver.SecurityAttributes{
			"empty-ns": ociserver.SecurityAttributesItem{},
		}
		got := toCustomerSecurityAttributes(in)
		assert.NotNil(tt, got, "non-empty top-level map allocates the outer result map")
		_, present := got["empty-ns"]
		assert.False(tt, present, "namespaces with no attributes must not appear in the output")
	})

	t.Run("zero-value mode is always emitted as empty string (mode is required by the OAS contract)", func(tt *testing.T) {
		// mode is now `required` on SecurityAttributeValue in the OAS spec, so
		// ogen rejects HTTP requests that omit it before they reach this helper.
		// This case constructs the struct directly in Go to pin the in-memory
		// fall-through behaviour: a zero-value Mode round-trips as "".
		in := ociserver.SecurityAttributes{
			"ns1": ociserver.SecurityAttributesItem{
				"app": ociserver.SecurityAttributeValue{
					Value: "app1",
				},
			},
		}
		got := toCustomerSecurityAttributes(in)
		require.Contains(tt, got, "ns1")
		leaf, ok := got["ns1"]["app"].(map[string]string)
		require.True(tt, ok, "leaf must be map[string]string so the JSON encoder produces a flat object")
		assert.Equal(tt, "app1", leaf["value"])
		mode, hasMode := leaf["mode"]
		require.True(tt, hasMode,
			"mode is required in the OAS schema; the helper unconditionally emits it so the on-wire shape is stable")
		assert.Equal(tt, "", mode,
			"zero-value Mode round-trips as empty string in memory; production traffic always has a valid value because ogen rejects requests without mode")
	})

	t.Run("mode=enforce is preserved as string", func(tt *testing.T) {
		in := ociserver.SecurityAttributes{
			"ns1": ociserver.SecurityAttributesItem{
				"app": ociserver.SecurityAttributeValue{
					Value: "app1",
					Mode:  ociserver.SecurityAttributeValueModeEnforce,
				},
			},
		}
		got := toCustomerSecurityAttributes(in)
		leaf := got["ns1"]["app"].(map[string]string)
		assert.Equal(tt, "app1", leaf["value"])
		assert.Equal(tt, "enforce", leaf["mode"])
	})

	t.Run("mode=audit is preserved as string", func(tt *testing.T) {
		in := ociserver.SecurityAttributes{
			"ns1": ociserver.SecurityAttributesItem{
				"app": ociserver.SecurityAttributeValue{
					Value: "app2",
					Mode:  ociserver.SecurityAttributeValueModeAudit,
				},
			},
		}
		got := toCustomerSecurityAttributes(in)
		leaf := got["ns1"]["app"].(map[string]string)
		assert.Equal(tt, "audit", leaf["mode"])
	})

	t.Run("multiple namespaces and attributes are all preserved", func(tt *testing.T) {
		in := ociserver.SecurityAttributes{
			"ns1": ociserver.SecurityAttributesItem{
				"app":  ociserver.SecurityAttributeValue{Value: "v1"},
				"tier": ociserver.SecurityAttributeValue{Value: "gold"},
			},
			"ns2": ociserver.SecurityAttributesItem{
				"env": ociserver.SecurityAttributeValue{
					Value: "prod",
					Mode:  ociserver.SecurityAttributeValueModeEnforce,
				},
			},
		}
		got := toCustomerSecurityAttributes(in)
		require.Len(tt, got, 2, "both namespaces must be present in the output")
		require.Len(tt, got["ns1"], 2, "ns1 must carry both attributes")
		require.Len(tt, got["ns2"], 1, "ns2 must carry exactly one attribute")

		assert.Equal(tt, "v1", got["ns1"]["app"].(map[string]string)["value"])
		assert.Equal(tt, "gold", got["ns1"]["tier"].(map[string]string)["value"])
		assert.Equal(tt, "prod", got["ns2"]["env"].(map[string]string)["value"])
		assert.Equal(tt, "enforce", got["ns2"]["env"].(map[string]string)["mode"])
	})
}

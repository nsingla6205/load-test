package hyperscaler

import (
	"context"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/google"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/oci"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	errors2 "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

var GetGCPService = _getGCPService
var MaxRetries = env.GetInt("GOOGLE_API_MAX_RETRIES", 6)
var maxOCIRetries = env.GetInt("OCI_API_MAX_RETRIES", 6)
var GetOCIService = _getOCIService

// _getGCPService initializes and returns a GcpServices instance.
func _getGCPService(ctx context.Context) (*google.GcpServices, error) {
	gcpService := NewGcpServices(ctx)

	gcpService.Logger.Debug("gcpService initialized")
	err := gcpService.InitializeClients()
	if err != nil || !gcpService.IsAdminClientInitialized() {
		gcpService.Logger.Debug("Initialisation of service failed")
		return nil, errors.NewVCPError(errors.ErrGCPClientInitializationError, errors2.New("initialisation of Google GCP service failed"))
	}
	return gcpService, nil
}

// NewGcpServices creates a new instance of GcpServices with the provided context.
func NewGcpServices(ctx context.Context) *google.GcpServices {
	return &google.GcpServices{
		Ctx:    ctx,
		Logger: util.GetLogger(ctx),
		Retry:  google.NewExponentialRetryStrategy(time.Second, uint(MaxRetries)),
	}
}

// InitSharedOCIClients eagerly initializes the process-wide OCI clients at
// startup. It must be called once (per OCI process) before any OciServices is
// constructed.
func InitSharedOCIClients(ctx context.Context) error {
	return oci.InitSharedOCIClients(ctx)
}

// _getOCIService initializes and returns an OciServices instance.
func _getOCIService(ctx context.Context) (*oci.OciServices, error) {
	ociService := NewOCIServices(ctx)

	ociService.Logger.Debug("ociService initialized")
	err := ociService.InitializeClients()
	if err != nil || !ociService.IsAdminClientInitialized() {
		ociService.Logger.Debug("Initialisation of OCI service failed")
		return nil, errors.NewVCPError(errors.ErrOCIClientInitializationError, errors2.New("initialisation of OCI service failed"))
	}
	return ociService, nil
}

// NewOCIServices creates a new instance of OciServices with the provided context.
func NewOCIServices(ctx context.Context) *oci.OciServices {
	return &oci.OciServices{
		Ctx:    ctx,
		Logger: util.GetLogger(ctx),
		Retry:  oci.NewExponentialRetryStrategy(time.Second, uint(maxOCIRetries)),
	}
}

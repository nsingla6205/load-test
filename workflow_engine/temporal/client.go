package temporal

import (
	"context"
	"crypto/tls"
	"fmt"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.opentelemetry.io/otel"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/contrib/opentelemetry"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/workflow"
)

var (
	CustomerTaskQueue   = fetchQueueVersionFromEnv(CustomerWorkerType)
	BackgroundTaskQueue = fetchQueueVersionFromEnv(BackgroundWorkerType)

	CustomerWorkerType   = "customer-workflows"
	BackgroundWorkerType = "background-workflows"
)

var (
	createClientOptionsFromEnv = _createClientOptionsFromEnv
	waitTime                   = 5 * time.Second // Time to wait before retrying connection to Temporal server

	// FetchTemporalClient is a function variable that returns a Temporal client.
	// This allows for easier mocking in tests and can be overridden at runtime.
	FetchTemporalClient func() (client.Client, error)
)

type NamespaceClientFactory func(client.Options) (client.NamespaceClient, error)
type ClientDialFunc func(client.Options) (client.Client, error)

type WorkflowEngine struct {
	temporalClient         client.Client
	NamespaceClientFactory NamespaceClientFactory
	ClientDial             ClientDialFunc
	Sleep                  func(time.Duration) // Inject sleep for testability
}

func (t *WorkflowEngine) LoadConfig() workflow_engine.ClientConfig {
	return LoadTemporalConfig()
}

func (t *WorkflowEngine) InitializeClient(cfg workflow_engine.ClientConfig, logger log.Logger) error {
	// Initialize the temporal server client
	clientOptions, err := createClientOptionsFromEnv(cfg, logger)
	if err != nil {
		logger.Error("failed to create temporal client options: %w", "error", err.Error())
		return err
	}

	if cfg.ShouldEnableDataEncryption() && cfg.GetEncryptionID() != "" {
		logger.Infof("Enabling encrypting Data Converter: %s", cfg.GetEncryptionID())
		defaultDataConverter := converter.GetDefaultDataConverter()
		// TODO: Store the Data Encryption Key in a Secret Management Service and add logic to retrieve the secret at runtime
		clientOptions.DataConverter = util.NewEncryptionDataConverter(defaultDataConverter, cfg.GetEncryptionID())
	}

	factory := t.NamespaceClientFactory
	if factory == nil {
		factory = client.NewNamespaceClient
	}
	dial := t.ClientDial
	if dial == nil {
		dial = client.Dial
	}
	sleep := t.Sleep
	if sleep == nil {
		sleep = time.Sleep
	}

	var temporalClient client.Client
	namespaceClient, err := factory(clientOptions)
	if err != nil {
		logger.Error("Failed to create temporal namespace client", "error", err.Error())
		return err
	}
	for {
		temporalClient, err = dial(clientOptions)
		if err != nil {
			sleep(waitTime)
			continue
		}

		name, err := namespaceClient.Describe(context.Background(), cfg.GetNamespace())

		if err == nil {
			t.temporalClient = temporalClient
			logger.Info("Connected to Temporal namespace", "namespace", name.GetNamespaceInfo().GetName())
			return nil
		}
		logger.Error("Failed to connect to Temporal server", "error", err.Error(), "retrying in 5 seconds")
		sleep(waitTime) // Retry after 5 seconds
		// Add a delay between retries to avoid overwhelming the temporal server
	}
}

func (t *WorkflowEngine) CloseClient(client client.Client) {
	if client != nil {
		client.Close()
	}
}

// GetTemporalClient returns the temporal client instance.
func (t *WorkflowEngine) GetTemporalClient() client.Client {
	return t.temporalClient
}

// CreateClientOptionsFromEnv creates a client.Options instance, configures
// it based on environment variables, and returns that instance. It
// supports the following environment variables:
//
//	TEMPORAL_ADDRESS: Host and port (formatted as host:port) of the Temporal Frontend Service
//	TEMPORAL_NAMESPACE: Namespace to be used by the Client
//	TEMPORAL_TLS_CERT: Path to the x509 certificate
//	TEMPORAL_TLS_KEY: Path to the private certificate key
//
// If these environment variables are not set, the client.Options
// instance returned will be based on the SDK's default configuration.
func _createClientOptionsFromEnv(cfg workflow_engine.ClientConfig, logger log.Logger) (client.Options, error) {
	tracingInterceptor, err := opentelemetry.NewTracingInterceptor(opentelemetry.TracerOptions{
		Tracer: otel.GetTracerProvider().Tracer("Temporal-Worker"),
	})
	if err != nil {
		logger.Error("Unable to create interceptor", "error", err)
		tracingInterceptor = nil
	}
	// Interceptor order: tracing first (so spans wrap the submission emission
	// too), then the workflow-submission interceptor. Both are safe to combine
	// because each only overrides the SDK methods it cares about and forwards
	// everything else through its embedded ClientOutboundInterceptorBase.
	interceptors := make([]interceptor.ClientInterceptor, 0, 2)
	if tracingInterceptor != nil {
		interceptors = append(interceptors, tracingInterceptor)
	}
	interceptors = append(interceptors, NewWorkflowSubmissionInterceptor())

	clientOpts := client.Options{
		HostPort:     cfg.GetHostPort(),
		Namespace:    cfg.GetNamespace(),
		Interceptors: interceptors,
		MetricsHandler: opentelemetry.NewMetricsHandler(opentelemetry.MetricsHandlerOptions{
			Meter: otel.GetMeterProvider().Meter("Temporal-Worker"),
		}),
		Logger: logger,
	}

	if cfg.GetTLSCertPath() != "" && cfg.GetTLSKeyPath() != "" {
		cert, err := tls.LoadX509KeyPair(cfg.GetTLSCertPath(), cfg.GetTLSKeyPath())
		if err != nil {
			return clientOpts, fmt.Errorf("failed loading tls key pair for temporal: %w", err)
		}

		clientOpts.ConnectionOptions.TLS = &tls.Config{
			Certificates: []tls.Certificate{cert},
		}
	}

	clientOpts.ContextPropagators = []workflow.ContextPropagator{
		util.NewContextMapPropagator(),
	}

	return clientOpts, nil
}

func fetchQueueVersionFromEnv(workerType string) string {
	version := env.GetString("TASK_QUEUE_VERSION", "")
	if version != "" {
		return workerType + "-" + version
	}
	return workerType
}

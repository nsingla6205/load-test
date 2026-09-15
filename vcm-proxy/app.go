package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	cbackend "github.com/cschleiden/go-workflows/backend"
	"github.com/cschleiden/go-workflows/backend/sqlite"
	"github.com/go-chi/chi/v5"
	"github.com/go-faster/errors"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/vlm"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/factory"
	vcm "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/factory/gcp/vcm"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/vcm/recovery"
	vcm_workflows "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows/vcm-workflows"
	vcmdb "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcm"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/auth"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	utilsmiddleware "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/httphelpers"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	vcmapi "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcm-proxy/api/endpoints"
	vcmserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcm-proxy/api/vcm-servergen"
	vcmconfig "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcm-proxy/config"
	vcmmiddleware "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcm-proxy/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator/gowfbackend"
	"golang.org/x/sync/errgroup"
	vlmclient "netapp.com/vsa/lifecycle-manager/pkg/vlmclient"
	vlmgowf "netapp.com/vsa/vsa-orchestrator/gowfbackend"
)

// vcmCloudProvider is the cloud VCM provisions VSA clusters on. VCM is GCP-only,
// so the VLM workflows and provider factory are wired for GCP.
const vcmCloudProvider = "gcp"

const workflowUIPath = "/workflow-ui"

func main() {
	ctx := context.WithValue(context.Background(), utilsmiddleware.CorrelationContextKey, uuid.NewString())
	ctx, cancel := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	logger := log.NewLogger()
	logger.Info("Starting VCM proxy API")

	// Register the VLM provider factory so the VLM config wrapper types can be
	// built/JSON-decoded while identifying VMs and building the deployment. VCM
	// provisions GCP VSA clusters, so the active provider is GCP.
	vlm.SetActiveProvider(vlm.GCPCloud)
	logger.Info("VLM active provider set", "provider", vlm.GCPCloud)

	// lifecycle-manager's pkg/vlmconfig resolves the cloud provider from the
	// PROVIDER env var (vlmconfig.GetProviderFromEnv) when decoding VLM child-
	// workflow inputs. In the standalone vlm-worker this is injected by its
	// configMap; VCM runs the VLM workflows in-process, so nothing sets it
	// unless we do. Set it from the same GCP-only source of truth so in-process
	// child-workflow input conversion succeeds in every environment (local and
	// deployed) without relying on external env injection.
	if err := os.Setenv("PROVIDER", vcmCloudProvider); err != nil {
		logger.Error("Failed to set PROVIDER env for in-process VLM workflows", "error", err.Error())
		os.Exit(1)
	}
	logger.Info("PROVIDER env set for in-process VLM workflows", "provider", vcmCloudProvider)

	cfg := vcmconfig.LoadServerConfig()
	if err := env.ValidateVCMEnvironmentVariables(); err != nil {
		logger.Error("Failed to validate environment variables", "error", err.Error())
		os.Exit(1)
	}

	shutdown, err := log.SetupOpenTelemetry(ctx)
	if err != nil {
		logger.ErrorContext(ctx, "error setting up OpenTelemetry", "error", err)
		shutdown = func(ctx context.Context) error { return nil }
	}
	defer func() {
		if err := shutdown(ctx); err != nil {
			logger.ErrorContext(ctx, "error shutting down OpenTelemetry", "error", err)
		}
	}()

	// Created before the workflow engine so the job-progress activity can be
	// registered with it.
	configStore := vcmconfig.NewStore(logger, cfg.DBValidateTimeout)

	workflowRunner, err := setupWorkflowEngine(ctx, logger, configStore)
	if err != nil {
		logger.ErrorContext(ctx, "failed to create workflow engine", "error", err)
		os.Exit(1)
	}

	// The config store doubles as the durable job store for the orchestrator, and
	// the runner's workflow starter dispatches the VCM pool workflow.
	orch := factory.GetVCMOrchestrator(configStore, workflowRunner.WorkflowStarter(), logger)

	// Wire the VLM config rebuild dispatcher into the config store.
	configStore.SetVLMConfigRebuildStarter(vcm.NewVLMConfigRebuildDispatcher(workflowRunner.WorkflowStarter()))

	// Wire crash-recovery: on every startup (after the DB is validated) the
	// coordinator resumes durable jobs left NEW or PROCESSING by a previous
	// process. Each job type registers a handler that rebuilds and re-dispatches
	// its workflow; the config store fires the coordinator once the DB is ready.
	recoveryCoordinator := recovery.NewCoordinator(configStore, logger)
	if err := recoveryCoordinator.Register(vcmdb.JobTypePoolCreate,
		vcm.NewCreatePoolRecoveryHandler(workflowRunner.WorkflowStarter(), logger)); err != nil {
		logger.ErrorContext(ctx, "failed to register create-pool recovery handler", "error", err)
		os.Exit(1)
	}
	configStore.SetOngoingJobResumer(recoveryCoordinator)

	serverState := vcmapi.NewServerState()
	handler := &vcmapi.Handler{
		Orchestrator: orch,
		ServerState:  serverState,
		ConfigStore:  configStore,
	}

	vcmServer, err := vcmserver.NewServer(handler)
	if err != nil {
		logger.Error("Failed to create VCM server", "error", err.Error())
		os.Exit(1)
	}

	if err := runServer(ctx, cfg, vcmServer, serverState, configStore, workflowRunner, logger); err != nil {
		logger.Error("Server error", "error", err.Error())
		os.Exit(1)
	}
}

// runServer wires the go-workflows runner, HTTP server, and graceful shutdown.
// It blocks until ctx is cancelled or a goroutine returns an error.
func runServer(ctx context.Context, cfg *vcmconfig.ServerConfig, vcmServer http.Handler, serverState *vcmapi.ServerState, configStore *vcmconfig.Store, workflowRunner *gowfbackend.Runner, logger log.Logger) error {
	workflowUI := workflowUIHandler(workflowRunner, logger, cfg.WorkflowUIEnabled)
	httpServer := setupHTTPServer(cfg, vcmServer, workflowUI, configStore)

	// Start the worker with the errgroup context so an HTTP/server failure cancels polling before handleGracefulShutdown calls Stop().
	eg, ctx := errgroup.WithContext(ctx)
	if err := workflowRunner.Start(ctx); err != nil {
		_ = workflowRunner.Stop()
		return errors.Wrap(err, "failed to start go-workflows runner")
	}

	eg.Go(func() error {
		logger.Info("Starting VCM HTTP server on " + cfg.Host + ":" + cfg.Port)
		httpServer.Addr = cfg.Host + ":" + cfg.Port
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("VCM HTTP server error", "error", err.Error())
			return err
		}
		return nil
	})

	handleGracefulShutdown(ctx, eg, httpServer, serverState, workflowRunner, logger)

	if err := eg.Wait(); err != nil {
		return err
	}
	logger.Info("VCM server stopped gracefully")
	return nil
}

// filteredBackendHandler wraps a slog.Handler to drop cschleiden's noisy
// startup "Applying migrations" line while surfacing everything else, including
// workflow/activity failures and retries.
type filteredBackendHandler struct {
	slog.Handler
}

func (h filteredBackendHandler) Handle(ctx context.Context, r slog.Record) error {
	if strings.Contains(strings.ToLower(r.Message), "applying migrations") {
		return nil
	}
	return h.Handler.Handle(ctx, r)
}

// setupWorkflowEngine creates the in-memory go-workflows engine and returns the
// runner. The caller is responsible for Start/Stop and derives the workflow
// starter from the runner. The shared config store is wired into job-progress
// and VLM config rebuild activities for durable persistence and write-back.
func setupWorkflowEngine(ctx context.Context, logger log.Logger, store *vcmconfig.Store) (*gowfbackend.Runner, error) {
	// Build the in-memory backend with both context propagators installed: the
	// VCP orchestrator's (PoolWorkflow/activities read deadline+correlation under
	// its key) and the VLM/vsa-orchestrator's (VLM workflows/activities read them
	// under a different key). Both must be present so context survives the
	// PoolWorkflow -> VLM child-workflow boundary; the propagators are additive.
	backendOpts := append(gowfbackend.DefaultBackendOptions(), vlmgowf.DefaultBackendOptions()...)
	// Surface the go-workflows engine's own logs (workflow/activity failures and
	// retries) so problems at the VLM child-workflow boundary are visible;
	// cschleiden otherwise logs to slog.Default() which the JSON logger drops.
	// Drop the noisy "Applying migrations" startup line.
	backendLogger := slog.New(filteredBackendHandler{
		Handler: slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}),
	})
	backendOpts = append(backendOpts, cbackend.WithLogger(backendLogger))
	backend := sqlite.NewInMemoryBackend(sqlite.WithBackendOptions(backendOpts...))

	runner, err := gowfbackend.NewRunner(ctx, gowfbackend.WithBackend(backend))
	if err != nil {
		return nil, err
	}

	registerWorkflows(runner, store)
	logger.InfoContext(ctx, "in-memory go-workflows engine created")

	return runner, nil
}

// registerWorkflows registers all workflows and activities on the VCM proxy worker.
func registerWorkflows(runner *gowfbackend.Runner, store *vcmconfig.Store) {
	reg := runner.Registrar()
	reg.RegisterWorkflow(vcm_workflows.PoolWorkflow)
	reg.RegisterWorkflow(vcm_workflows.VcmCreateHostGroupWorkflow)
	reg.RegisterWorkflow(vcm_workflows.VcmDeleteHostGroupWorkflow)
	reg.RegisterWorkflow(vcm_workflows.VcmUpdateHostGroupWorkflow)
	reg.RegisterWorkflow(vcm_workflows.VcmCreateVolumeWorkflow)
	reg.RegisterWorkflow(vcm_workflows.VcmPreBlockVolumeWorkflow)
	reg.RegisterWorkflow(vcm_workflows.VcmPreFileVolumeWorkflow)
	reg.RegisterWorkflow(vcm_workflows.VcmPostBlockVolumeWorkflow)
	reg.RegisterWorkflow(vcm_workflows.VcmPostFileVolumeWorkflow)
	reg.RegisterWorkflow(vcm_workflows.VcmPostFileVolumeWorkflowForSMB)
	reg.RegisterWorkflow(vcm_workflows.VcmDeleteVolumeWorkflow)

	var poolActivity activities.PoolActivity
	var credentialActivity activities.VCMCredentialActivity
	var svmActivity activities.SvmActivity
	var volumeCreateActivity activities.VolumeCreateActivity
	var volumeDeleteActivity activities.VolumeDeleteActivity
	var volumeUpdateActivity activities.VolumeUpdateActivity
	var vcmHostGroupActivity activities.HostGroupActivity
	jobActivity := &activities.JobActivity{Store: store}

	reg.RegisterActivity(poolActivity.IdentifyVMs)
	reg.RegisterActivity(credentialActivity.ResolveOntapAdminPassword)
	reg.RegisterActivity(credentialActivity.ResolveOntapCertificate)
	reg.RegisterActivity(svmActivity.CreateQoSPolicyAndApplyToSVM)
	reg.RegisterActivity(svmActivity.GetSvmByName)
	reg.RegisterActivity(vcmHostGroupActivity.ValidateHostGroupsExistInONTAP)
	reg.RegisterActivity(vcmHostGroupActivity.CreateIgroup)
	reg.RegisterActivity(vcmHostGroupActivity.UpdateIgroup)
	reg.RegisterActivity(vcmHostGroupActivity.DeleteIgroup)
	reg.RegisterActivity(volumeCreateActivity.CreateVolumeInONTAP)
	reg.RegisterActivity(volumeCreateActivity.CreateLun)
	reg.RegisterActivity(volumeCreateActivity.CreateLunMap)
	reg.RegisterActivity(volumeCreateActivity.GetOntapClusterHealth)
	reg.RegisterActivity(volumeCreateActivity.CreateExportPolicyInOntap)
	reg.RegisterActivity(volumeCreateActivity.CreateSnapshotPolicyInONTAP)
	reg.RegisterActivity(volumeUpdateActivity.GetVolumeFromONTAP)
	reg.RegisterActivity(volumeDeleteActivity.DeleteVolumeInONTAP)
	reg.RegisterActivity(volumeDeleteActivity.DeleteExportPolicy)
	reg.RegisterActivity(volumeDeleteActivity.DeleteSnapshotPolicyInONTAP)
	reg.RegisterActivity(jobActivity.GetJob)
	reg.RegisterActivity(jobActivity.UpdateJobProgress)

	// Config rebuild workflow and supporting activities.
	reg.RegisterWorkflow(vcm_workflows.RebuildVLMConfigWorkflow)
	configActivity := &activities.VLMConfigActivity{Store: store}
	reg.RegisterActivity(configActivity.ValidateGetVLMConfigInputs)
	reg.RegisterActivity(configActivity.FinalizeVLMConfig)

	vlmReg := vlmgowf.NewRegistrar(runner.Worker())
	vlmclient.RegisterWorkflows(vlmReg, vcmCloudProvider)
}

func setupHTTPServer(cfg *vcmconfig.ServerConfig, handler, workflowUI http.Handler, configStore *vcmconfig.Store) *http.Server {
	mux := chi.NewRouter()

	// Serve the diagnostic UI outside LoggingHttpHandler so static JS/CSS
	// and diag API responses are not dumped into request/response body logs.
	// LoggingMiddleware is still required so RecoverMiddleware has a logger in context.
	if workflowUI != nil {
		mux.Group(func(r chi.Router) {
			r.Use(log.LoggingMiddleware)
			r.Use(log.RecoverMiddleware)
			r.Mount(workflowUIPath, http.StripPrefix(workflowUIPath, workflowUI))
		})
	}

	api := chi.NewRouter()
	api.Use(httphelpers.LoggingHttpHandler)
	api.Use(log.LoggingMiddleware)
	api.Use(log.RecoverMiddleware)
	api.Use(auth.VCMAuthMiddleware(true))
	// Avoid passing a typed-nil *Store as ConfiguredChecker (nil interface check would fail).
	var configuredChecker vcmmiddleware.ConfiguredChecker
	if configStore != nil {
		configuredChecker = configStore
	}
	api.Use(vcmmiddleware.ConfiguredGate(configuredChecker, vcmmiddleware.DefaultAllowlist))
	api.Mount("/", handler)
	api.Handle("/metrics", promhttp.Handler())
	mux.Mount("/", api)

	return &http.Server{
		Handler:           mux,
		ReadTimeout:       cfg.ReadTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
	}
}

// workflowUIHandler builds the go-workflows diagnostic UI handler when enabled.
func workflowUIHandler(runner *gowfbackend.Runner, logger log.Logger, enabled bool) http.Handler {
	if !enabled {
		logger.Info("go-workflows UI disabled via VCM_WORKFLOW_UI_ENABLED")
		return nil
	}
	workflowUI, err := runner.DiagnosticsHandler()
	if err != nil {
		logger.Warn("go-workflows UI unavailable; continuing without diagnostic UI", "error", err.Error())
		return nil
	}
	logger.Info("Go workflows UI will be available at " + workflowUIPath)
	return workflowUI
}

func handleGracefulShutdown(ctx context.Context, eg *errgroup.Group, httpServer *http.Server, serverState *vcmapi.ServerState, workflowRunner *gowfbackend.Runner, logger log.Logger) {
	eg.Go(func() error {
		<-ctx.Done()
		logger.Info("Received shutdown signal, marking VCM server as not ready")
		serverState.SetShuttingDown()

		logger.Info("Shutting down VCM HTTP server")
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer shutdownCancel()

		var shutdownErr error
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			logger.Error("Failed to shut down VCM server gracefully", "error", err.Error())
			shutdownErr = err
		} else {
			logger.Info("VCM HTTP server shut down successfully")
		}

		if err := workflowRunner.Stop(); err != nil {
			logger.Error("Failed to stop go-workflows runner", "error", err.Error())
			if shutdownErr == nil {
				shutdownErr = err
			}
		}
		return shutdownErr
	})
}

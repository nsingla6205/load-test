package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	cbackend "github.com/cschleiden/go-workflows/backend"
	"github.com/cschleiden/go-workflows/backend/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	vcmapi "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcm-proxy/api/endpoints"
	vcmconfig "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcm-proxy/config"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator/gowfbackend"
	"golang.org/x/sync/errgroup"
)

var staticAssetPathRE = regexp.MustCompile(`/static/[^"'?\s]+`)

func firstStaticAssetPath(html string) (string, bool) {
	match := staticAssetPathRE.FindString(html)
	if match == "" {
		return "", false
	}
	return match, true
}

func TestFilteredBackendHandler_DropsMigrationLine(t *testing.T) {
	var buf bytes.Buffer
	h := filteredBackendHandler{
		Handler: slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}),
	}

	// The noisy cschleiden startup line (any case) must be swallowed.
	require.NoError(t, h.Handle(context.Background(),
		slog.NewRecord(time.Now(), slog.LevelInfo, "Applying migrations...", 0)))
	assert.Empty(t, buf.String(), "migration line must be dropped")

	// Everything else (e.g. workflow/activity failures) must pass through.
	require.NoError(t, h.Handle(context.Background(),
		slog.NewRecord(time.Now(), slog.LevelError, "workflow execution failed", 0)))
	assert.Contains(t, buf.String(), "workflow execution failed")
}

func TestSetupWorkflowEngine_RegistersWorkflows(t *testing.T) {
	// registerWorkflows wires the in-process VLM workflows, whose config decoder
	// resolves the provider from the PROVIDER env (main() sets this).
	t.Setenv("PROVIDER", vcmCloudProvider)

	runner, err := setupWorkflowEngine(context.Background(), log.NewLogger(), nil)
	require.NoError(t, err)
	require.NotNil(t, runner)
	t.Cleanup(func() {
		_ = runner.Stop()
	})

	// The diagnostics handler is available on the in-memory backend, confirming
	// the runner was built with a real (SQLite) backend.
	uiHandler, err := runner.DiagnosticsHandler()
	require.NoError(t, err)
	require.NotNil(t, uiHandler)
}

func TestSetupHTTPServer_VCMAuthMiddleware(t *testing.T) {
	handlerCalled := false
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	})
	server := setupHTTPServer(&vcmconfig.ServerConfig{}, handler, nil, nil)

	t.Run("skips auth for VCM bypass paths", func(t *testing.T) {
		for _, path := range []string{"/health", "/ready"} {
			handlerCalled = false
			rr := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, path, nil)
			server.Handler.ServeHTTP(rr, req)
			assert.Equal(t, http.StatusOK, rr.Code, path)
			assert.True(t, handlerCalled, path)
		}

		// /metrics is registered on the router (promhttp), not the mounted API handler.
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
		server.Handler.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusOK, rr.Code, "/metrics")
	})

	t.Run("requires auth for VCP bypass paths and VCM API paths", func(t *testing.T) {
		for _, tc := range []struct {
			method string
			path   string
		}{
			{method: http.MethodGet, path: "/version"},
			{method: http.MethodGet, path: "/v1/expertMode"},
			{method: http.MethodGet, path: "/v1/host"},
			{method: http.MethodPost, path: "/v1/config"},
			{method: http.MethodGet, path: "/v1/pools"},
		} {
			handlerCalled = false
			rr := httptest.NewRecorder()
			req := httptest.NewRequest(tc.method, tc.path, nil)
			server.Handler.ServeHTTP(rr, req)
			assert.Equal(t, http.StatusUnauthorized, rr.Code, "%s %s", tc.method, tc.path)
			assert.False(t, handlerCalled, "%s %s", tc.method, tc.path)
		}
	})
}

func TestSetupHTTPServerServesWorkflowUI(t *testing.T) {
	runner, err := gowfbackend.NewRunner(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, runner.Stop())
	})

	workflowUI, err := runner.DiagnosticsHandler()
	require.NoError(t, err)

	apiHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	server := setupHTTPServer(&vcmconfig.ServerConfig{}, apiHandler, workflowUI, nil)

	for _, test := range []struct {
		name        string
		path        string
		contentType string
		body        string
	}{
		{
			name:        "web application",
			path:        workflowUIPath + "/",
			contentType: "text/html",
			body:        "<!doctype html>",
		},
		{
			name:        "diagnostics API",
			path:        workflowUIPath + "/api/stats",
			contentType: "application/json",
			body:        "{",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, test.path, nil)
			recorder := httptest.NewRecorder()

			server.Handler.ServeHTTP(recorder, request)

			require.NotEqual(t, http.StatusNotFound, recorder.Code, "UI path should not fall through to API")
			require.Equal(t, http.StatusOK, recorder.Code)
			require.Contains(t, recorder.Header().Get("Content-Type"), test.contentType)
			require.Contains(t, strings.ToLower(recorder.Body.String()), test.body)
		})
	}

	// Resolve a static asset from the HTML rather than hard-coding a content-hashed
	// filename that changes when the upstream go-workflows UI bundle is rebuilt.
	t.Run("static asset path is served by UI mount", func(t *testing.T) {
		htmlRequest := httptest.NewRequest(http.MethodGet, workflowUIPath+"/", nil)
		htmlRecorder := httptest.NewRecorder()
		server.Handler.ServeHTTP(htmlRecorder, htmlRequest)
		require.Equal(t, http.StatusOK, htmlRecorder.Code)

		html := htmlRecorder.Body.String()
		assetPath, ok := firstStaticAssetPath(html)
		require.True(t, ok, "expected HTML to reference a /static/ asset")

		assetRequest := httptest.NewRequest(http.MethodGet, workflowUIPath+assetPath, nil)
		assetRecorder := httptest.NewRecorder()
		server.Handler.ServeHTTP(assetRecorder, assetRequest)

		// Must be handled by the UI mount, not fall through to the API stub (204).
		require.NotEqual(t, http.StatusNoContent, assetRecorder.Code)
		require.NotEqual(t, http.StatusNotFound, assetRecorder.Code)
		require.Equal(t, http.StatusOK, assetRecorder.Code)
	})
}

func TestSetupHTTPServerWithoutWorkflowUI(t *testing.T) {
	apiHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	server := setupHTTPServer(&vcmconfig.ServerConfig{}, apiHandler, nil, nil)

	request := httptest.NewRequest(http.MethodGet, "/health", nil)
	recorder := httptest.NewRecorder()
	server.Handler.ServeHTTP(recorder, request)
	require.Equal(t, http.StatusNoContent, recorder.Code)

	uiRequest := httptest.NewRequest(http.MethodGet, workflowUIPath+"/", nil)
	uiRecorder := httptest.NewRecorder()
	server.Handler.ServeHTTP(uiRecorder, uiRequest)
	// Without a UI handler the path falls through to the API router, which requires auth.
	require.Equal(t, http.StatusUnauthorized, uiRecorder.Code)
}

func TestWorkflowUIHandlerDisabled(t *testing.T) {
	runner, err := gowfbackend.NewRunner(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, runner.Stop())
	})

	require.Nil(t, workflowUIHandler(runner, log.NewLogger(), false))
}

func TestWorkflowUIHandlerEnabled(t *testing.T) {
	runner, err := gowfbackend.NewRunner(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, runner.Stop())
	})

	handler := workflowUIHandler(runner, log.NewLogger(), true)
	require.NotNil(t, handler)
}

func TestWorkflowUIHandlerDiagnosticsUnavailable(t *testing.T) {
	// Wrap a real backend so it satisfies cbackend.Backend but not diag.Backend.
	type noDiagBackend struct{ cbackend.Backend }
	runner, err := gowfbackend.NewRunner(context.Background(), gowfbackend.WithBackend(noDiagBackend{
		Backend: sqlite.NewInMemoryBackend(),
	}))
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, runner.Stop())
	})

	require.Nil(t, workflowUIHandler(runner, log.NewLogger(), true))
}

// errCloseListener makes http.Server.Shutdown return immediately with an error
// (listener Close failure) without waiting for the 30s shutdown timeout.
type errCloseListener struct {
	net.Listener
}

func (e errCloseListener) Close() error {
	_ = e.Listener.Close()
	return errors.New("listener close failed")
}

// failCloseBackend makes gowfbackend.Runner.Stop return an error via Backend.Close.
type failCloseBackend struct {
	cbackend.Backend
}

func (f failCloseBackend) Close() error {
	return errors.New("backend close failed")
}

func startTestHTTPServer(t *testing.T, eg *errgroup.Group, ln net.Listener) *http.Server {
	t.Helper()
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})}
	eg.Go(func() error {
		err := server.Serve(ln)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	})
	waitForHTTPServer(t, ln)
	return server
}

func waitForHTTPServer(t *testing.T, ln net.Listener) {
	t.Helper()
	url := fmt.Sprintf("http://%s/", ln.Addr().String())
	require.Eventually(t, func() bool {
		resp, err := http.Get(url)
		if err != nil {
			return false
		}
		_ = resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, 2*time.Second, 10*time.Millisecond)
}

func TestHandleGracefulShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	eg, ctx := errgroup.WithContext(ctx)

	runner, err := gowfbackend.NewRunner(context.Background())
	require.NoError(t, err)
	require.NoError(t, runner.Start(ctx))

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := startTestHTTPServer(t, eg, ln)

	serverState := vcmapi.NewServerState()
	handleGracefulShutdown(ctx, eg, server, serverState, runner, log.NewLogger())

	cancel()
	require.NoError(t, eg.Wait())
	require.True(t, serverState.IsShuttingDown())
}

func TestHandleGracefulShutdown_HTTPShutdownError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	eg, ctx := errgroup.WithContext(ctx)

	runner, err := gowfbackend.NewRunner(context.Background())
	require.NoError(t, err)
	require.NoError(t, runner.Start(ctx))

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := startTestHTTPServer(t, eg, errCloseListener{Listener: ln})

	serverState := vcmapi.NewServerState()
	handleGracefulShutdown(ctx, eg, server, serverState, runner, log.NewLogger())

	cancel()
	require.EqualError(t, eg.Wait(), "listener close failed")
	require.True(t, serverState.IsShuttingDown())
}

func TestHandleGracefulShutdown_WorkflowStopError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	eg, ctx := errgroup.WithContext(ctx)

	runner, err := gowfbackend.NewRunner(context.Background(), gowfbackend.WithBackend(failCloseBackend{
		Backend: sqlite.NewInMemoryBackend(),
	}))
	require.NoError(t, err)
	require.NoError(t, runner.Start(ctx))

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := startTestHTTPServer(t, eg, ln)

	serverState := vcmapi.NewServerState()
	handleGracefulShutdown(ctx, eg, server, serverState, runner, log.NewLogger())

	cancel()
	require.EqualError(t, eg.Wait(), "backend close failed")
	require.True(t, serverState.IsShuttingDown())
}

func TestHandleGracefulShutdown_PrefersHTTPShutdownError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	eg, ctx := errgroup.WithContext(ctx)

	runner, err := gowfbackend.NewRunner(context.Background(), gowfbackend.WithBackend(failCloseBackend{
		Backend: sqlite.NewInMemoryBackend(),
	}))
	require.NoError(t, err)
	require.NoError(t, runner.Start(ctx))

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := startTestHTTPServer(t, eg, errCloseListener{Listener: ln})

	serverState := vcmapi.NewServerState()
	handleGracefulShutdown(ctx, eg, server, serverState, runner, log.NewLogger())

	cancel()
	// HTTP shutdown error is recorded first and must win over Stop's error.
	require.EqualError(t, eg.Wait(), "listener close failed")
	require.True(t, serverState.IsShuttingDown())
}

func TestRunServer(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := ln.Addr().(*net.TCPAddr).Port
	require.NoError(t, ln.Close())

	ctx, cancel := context.WithCancel(context.Background())
	cfg := &vcmconfig.ServerConfig{
		Host:              "127.0.0.1",
		Port:              strconv.Itoa(port),
		WorkflowUIEnabled: true,
	}
	serverState := vcmapi.NewServerState()
	apiHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	runner, err := gowfbackend.NewRunner(ctx)
	require.NoError(t, err)

	errCh := make(chan error, 1)
	go func() {
		errCh <- runServer(ctx, cfg, apiHandler, serverState, nil, runner, log.NewLogger())
	}()

	url := fmt.Sprintf("http://127.0.0.1:%d/health", port)
	require.Eventually(t, func() bool {
		resp, err := http.Get(url)
		if err != nil {
			return false
		}
		_ = resp.Body.Close()
		return resp.StatusCode == http.StatusNoContent
	}, 5*time.Second, 25*time.Millisecond)

	// Workflow UI should be mounted when enabled.
	uiResp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d%s/", port, workflowUIPath))
	require.NoError(t, err)
	_ = uiResp.Body.Close()
	require.Equal(t, http.StatusOK, uiResp.StatusCode)

	cancel()
	require.NoError(t, <-errCh)
	require.True(t, serverState.IsShuttingDown())
}

func TestRunServer_WorkflowUIDisabled(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := ln.Addr().(*net.TCPAddr).Port
	require.NoError(t, ln.Close())

	ctx, cancel := context.WithCancel(context.Background())
	cfg := &vcmconfig.ServerConfig{
		Host:              "127.0.0.1",
		Port:              strconv.Itoa(port),
		WorkflowUIEnabled: false,
	}
	serverState := vcmapi.NewServerState()
	apiHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	runner, err := gowfbackend.NewRunner(ctx)
	require.NoError(t, err)

	errCh := make(chan error, 1)
	go func() {
		errCh <- runServer(ctx, cfg, apiHandler, serverState, nil, runner, log.NewLogger())
	}()

	url := fmt.Sprintf("http://127.0.0.1:%d/health", port)
	require.Eventually(t, func() bool {
		resp, err := http.Get(url)
		if err != nil {
			return false
		}
		_ = resp.Body.Close()
		return resp.StatusCode == http.StatusNoContent
	}, 5*time.Second, 25*time.Millisecond)

	cancel()
	require.NoError(t, <-errCh)
	require.True(t, serverState.IsShuttingDown())
}

func TestRegisterWorkflows(t *testing.T) {
	t.Setenv("PROVIDER", vcmCloudProvider)

	runner, err := gowfbackend.NewRunner(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, runner.Stop()) })

	registerWorkflows(runner, vcmconfig.NewStore(log.NewLogger(), 0))

	uiHandler, err := runner.DiagnosticsHandler()
	require.NoError(t, err)
	require.NotNil(t, uiHandler)
}

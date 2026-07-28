package gowfbackend

import (
	"context"
	"errors"
	"net/http"
	"sync"

	cbackend "github.com/cschleiden/go-workflows/backend"
	"github.com/cschleiden/go-workflows/backend/sqlite"
	cclient "github.com/cschleiden/go-workflows/client"
	"github.com/cschleiden/go-workflows/diag"
	"github.com/cschleiden/go-workflows/worker"
	orchestrator "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
)

// Runner bundles the three moving pieces a cschleiden deployment needs at
// runtime:
//
//   - Backend: an in-memory SQLite store (no external DB / Redis dependency)
//   - Worker:  polls the backend, dispatches workflows and activities
//   - Client:  the surface used to start workflow instances and signal them
//
// This is purely additive — it does not replace the externally-owned worker
// pattern used by temporalbackend; callers who prefer to construct each
// piece themselves can still use NewRegistrar(w) directly.
type Runner struct {
	backend cbackend.Backend
	worker  *worker.Worker
	client  *cclient.Client

	mu      sync.Mutex
	started bool
}

// RunnerOption customises Runner construction. New options can be added
// without breaking existing callers (variadic-options pattern).
type RunnerOption func(*runnerConfig)

type runnerConfig struct {
	backend       cbackend.Backend
	workerOptions *worker.Options
}

// WithBackend overrides the default in-memory SQLite backend with a
// caller-supplied one. Useful for tests that want to inject a mock or share
// a single backend across multiple Runners.
func WithBackend(b cbackend.Backend) RunnerOption {
	return func(c *runnerConfig) { c.backend = b }
}

// WithWorkerOptions sets cschleiden worker options. Passing nil falls back
// to cschleiden's package-level DefaultOptions.
func WithWorkerOptions(opts *worker.Options) RunnerOption {
	return func(c *runnerConfig) { c.workerOptions = opts }
}

// NewRunner constructs a Runner. The default backend is an in-memory SQLite
// instance (no persistence across process restarts) which is appropriate
// for tests, demos, and library-mode deployments.
func NewRunner(_ context.Context, opts ...RunnerOption) (*Runner, error) {
	cfg := runnerConfig{}
	for _, o := range opts {
		o(&cfg)
	}

	b := cfg.backend
	if b == nil {
		b = sqlite.NewInMemoryBackend(sqlite.WithBackendOptions(DefaultBackendOptions()...))
	}

	w := worker.New(b, cfg.workerOptions)
	c := cclient.New(b)

	return &Runner{
		backend: b,
		worker:  w,
		client:  c,
	}, nil
}

// Registrar returns an orchestrator.WorkflowRegistrar that wraps and
// registers VLM workflows/activities with this Runner's worker. Safe to
// call at any time before Start; callers typically register everything they
// care about, then invoke Start.
func (r *Runner) Registrar() orchestrator.WorkflowRegistrar {
	return NewRegistrar(r.worker)
}

// Worker returns the underlying cschleiden Worker. Exposed so callers that
// need to use cschleiden-specific extension points (e.g. registering
// internal/system workflows) can do so without reaching into Runner internals.
func (r *Runner) Worker() *worker.Worker {
	return r.worker
}

// Client returns the cschleiden Client used to schedule and signal workflow
// instances.
func (r *Runner) Client() *cclient.Client {
	return r.client
}

// Backend returns the underlying cschleiden Backend. Mainly useful for
// tests that want to inspect persisted state.
func (r *Runner) Backend() cbackend.Backend {
	return r.backend
}

// DiagnosticsHandler returns the go-workflows diagnostic UI and API handler.
func (r *Runner) DiagnosticsHandler() (http.Handler, error) {
	diagnosticsBackend, ok := r.backend.(diag.Backend)
	if !ok {
		return nil, errors.New("gowfbackend.Runner: backend does not support diagnostics")
	}
	return diag.NewServeMux(diagnosticsBackend), nil
}

// Start launches the worker. The provided context controls the worker's
// lifetime: cancelling it signals all polling loops to stop. Start is
// idempotent — calling it twice returns an error rather than panicking.
// Concurrent Start/Stop calls are guarded by an internal mutex.
func (r *Runner) Start(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.started {
		return errors.New("gowfbackend.Runner: already started")
	}
	if err := r.worker.Start(ctx); err != nil {
		return err
	}
	r.started = true
	return nil
}

// Stop waits for the worker's in-flight tasks to drain and closes the
// backend if it supports closing. Callers must cancel the context passed to
// Start before calling Stop, otherwise the worker will keep polling forever.
//
// Returns the first non-nil error encountered (worker drain or backend
// close), so the caller can surface either failure.
func (r *Runner) Stop() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	var firstErr error
	if r.started {
		if err := r.worker.WaitForCompletion(); err != nil {
			firstErr = err
		}
	}
	if closer, ok := r.backend.(interface{ Close() error }); ok {
		if err := closer.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

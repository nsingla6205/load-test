package temporalmanager

import (
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
)

// newWorkerFn is the factory used to construct Temporal workers. Tests may replace this to
// capture the options passed to worker.New without connecting to a real Temporal server.
var newWorkerFn = worker.New

type Worker struct {
	client client.Client
	worker worker.Worker
}

// NewWorker creates a new Temporal worker for the given task queue with the provided options.
// All concurrency caps, rate limits, and WorkflowPanicPolicy are taken from opts as-is,
// allowing per-queue tuning via env vars (see utils/env for the CUSTOMER_* / BACKGROUND_* vars).
func NewWorker(c client.Client, taskQueue string, opts worker.Options) *Worker {
	w := newWorkerFn(c, taskQueue, opts)
	return &Worker{
		client: c,
		worker: w,
	}
}

// RegisterWorkflow registers a workflow with the worker
func (w *Worker) RegisterWorkflow(workflow interface{}) {
	w.worker.RegisterWorkflow(workflow)
}

// RegisterActivity registers an activity with the worker
func (w *Worker) RegisterActivity(activity interface{}) {
	w.worker.RegisterActivity(activity)
}

// Runs the worker
func (w *Worker) Run() error {
	return w.worker.Run(worker.InterruptCh())
}

// Stops the worker
func (w *Worker) Stop() {
	w.worker.Stop()
}

// Get current worker
func (w *Worker) GetWorker() worker.Worker {
	return w.worker
}

func (w *Worker) GetClient() client.Client {
	return w.client
}

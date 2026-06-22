package orchestrator

// WorkflowRegistrar abstracts workflow/activity registration so that
// packages under pkg/ never import the Temporal SDK directly.
// The temporalbackend package provides the concrete TemporalRegistrar
// that wraps worker.Worker.
type WorkflowRegistrar interface {
	// RegisterWorkflow registers a workflow function with an auto-derived name
	// (from the Go function's fully-qualified name).
	RegisterWorkflow(fn interface{})

	// RegisterWorkflowWithName registers a workflow function with an explicit name.
	RegisterWorkflowWithName(name string, fn interface{})

	// RegisterActivity registers an activity function.
	RegisterActivity(fn interface{})
}

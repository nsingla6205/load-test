package temporalbackend

import (
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
	"go.temporal.io/sdk/workflow"
)

// temporalFuture delegates to workflow.Future.
type temporalFuture struct {
	inner workflow.Future
}

func (f *temporalFuture) Get(ctx *orchestrator.Context, valuePtr interface{}) error {
	return f.inner.Get(wfCtx(ctx), valuePtr)
}

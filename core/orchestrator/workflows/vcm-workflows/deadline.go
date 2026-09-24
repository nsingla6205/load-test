package vcm_workflows

import (
	"fmt"
	"time"

	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	orchestrator "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
)

// checkDeadline returns ErrOperationDeadlineExceeded if the request deadline has already passed.
func checkDeadline(ctx *orchestrator.Context, deadline time.Time, step string) error {
	if deadline.IsZero() {
		return nil
	}
	now := orchestrator.Now(ctx).UTC()
	if now.Before(deadline.UTC()) {
		return nil
	}
	wfLogger(ctx).Error("workflow: operation deadline exceeded", "step", step, "deadline", deadline.UTC(), "now", now)
	return vsaerrors.NewVCPError(vsaerrors.ErrOperationDeadlineExceeded,
		fmt.Errorf("operation deadline exceeded before %s: deadline=%s now=%s", step, deadline.UTC().Format(time.RFC3339), now.Format(time.RFC3339)))
}

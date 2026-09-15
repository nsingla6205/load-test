package vcm_workflows

import (
	"context"
	"encoding/json"
	"testing"

	cclient "github.com/cschleiden/go-workflows/client"
	"github.com/stretchr/testify/require"
	vcmdb "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcm"
	orchestrator "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator/gowfbackend"
)

func ensureJobStateWorkflow(ctx *orchestrator.Context, expected vcmdb.JobState) error {
	bw := &baseWorkflow{ID: orchestrator.GetInfo(ctx).WorkflowExecution.ID}
	return bw.EnsureJobState(ctx, expected)
}

func TestEnsureJobState_AllowsMatchingJobState(t *testing.T) {
	const jobID = "job-ensure-state-ok"
	job := &stubJobActivity{}

	ctx, cancel := context.WithTimeout(context.Background(), hostGroupWorkflowTestTimeout)
	defer cancel()

	runner, err := gowfbackend.NewRunner(ctx)
	require.NoError(t, err)

	reg := runner.Registrar()
	reg.RegisterWorkflow(ensureJobStateWorkflow)
	reg.RegisterActivity(job.GetJob)

	require.NoError(t, runner.Start(ctx))
	t.Cleanup(func() {
		cancel()
		_ = runner.Stop()
	})

	wfi, err := runner.Client().CreateWorkflowInstance(ctx, cclient.WorkflowInstanceOptions{
		InstanceID: jobID,
	}, gowfbackend.FuncName(ensureJobStateWorkflow), vcmdb.JobStateProcessing)
	require.NoError(t, err)

	_, err = cclient.GetWorkflowResult[json.RawMessage](ctx, runner.Client(), wfi, hostGroupWorkflowTestTimeout)
	require.NoError(t, err)
}

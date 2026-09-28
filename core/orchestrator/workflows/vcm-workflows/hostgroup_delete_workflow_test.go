package vcm_workflows

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	cclient "github.com/cschleiden/go-workflows/client"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	vcmdb "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcm"
	orchestrator "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator/gowfbackend"
)

type stubDeleteIgroupActivity struct {
	mu          sync.Mutex
	deleteCalls int
	igroupNames []string
	svmNames    []string
	err         error
}

func (s *stubDeleteIgroupActivity) DeleteIgroup(_ context.Context, igroupName string, svmName string, _ *models.Node) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deleteCalls++
	s.igroupNames = append(s.igroupNames, igroupName)
	s.svmNames = append(s.svmNames, svmName)
	return s.err
}

func (s *stubDeleteIgroupActivity) snapshot() (int, []string, []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	igroupNames := make([]string, len(s.igroupNames))
	copy(igroupNames, s.igroupNames)
	svmNames := make([]string, len(s.svmNames))
	copy(svmNames, s.svmNames)
	return s.deleteCalls, igroupNames, svmNames
}

func runVcmDeleteHostGroupWorkflow(
	t *testing.T,
	instanceID string,
	register func(orchestrator.WorkflowRegistrar),
	node *models.Node,
	igroupName string,
	expectedSvmName string,
) error {
	return runVcmDeleteHostGroupWorkflowWithDeadline(
		t, instanceID, register, node, igroupName, expectedSvmName, time.Now().UTC().Add(time.Hour),
	)
}

func runVcmDeleteHostGroupWorkflowWithDeadline(
	t *testing.T,
	instanceID string,
	register func(orchestrator.WorkflowRegistrar),
	node *models.Node,
	igroupName string,
	expectedSvmName string,
	deadline time.Time,
) error {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), hostGroupWorkflowTestTimeout)
	defer cancel()

	runner, err := gowfbackend.NewRunner(ctx)
	require.NoError(t, err)

	reg := runner.Registrar()
	reg.RegisterWorkflow(VcmDeleteHostGroupWorkflow)
	register(reg)

	require.NoError(t, runner.Start(ctx))
	t.Cleanup(func() {
		cancel()
		_ = runner.Stop()
	})

	if instanceID == "" {
		instanceID = uuid.NewString()
	}
	wfi, err := runner.Client().CreateWorkflowInstance(ctx, cclient.WorkflowInstanceOptions{
		InstanceID: instanceID,
	}, gowfbackend.FuncName(VcmDeleteHostGroupWorkflow), common.WorkflowInputs{
		Node:            node,
		ExpectedSvmName: expectedSvmName,
		Deadline:        deadline,
	}, igroupName)
	require.NoError(t, err)

	_, err = cclient.GetWorkflowResult[json.RawMessage](ctx, runner.Client(), wfi, hostGroupWorkflowTestTimeout)
	return err
}

func TestVcmDeleteHostGroupWorkflow_Success(t *testing.T) {
	const operationID = "op-delete-success"
	job := &stubJobActivity{}
	deleter := &stubDeleteIgroupActivity{}

	err := runVcmDeleteHostGroupWorkflow(t, operationID, func(reg orchestrator.WorkflowRegistrar) {
		reg.RegisterActivity(job.UpdateJobProgress)
		reg.RegisterActivity((&stubSvmActivity{}).GetSvmByName)
		reg.RegisterActivity(deleter.DeleteIgroup)
	}, testNode(), "hg-1", testExpectedSvmName)

	require.NoError(t, err)
	calls := job.snapshot()
	require.Len(t, calls, 2)
	assert.Equal(t, operationID, calls[0].jobID)
	assert.Equal(t, vcmdb.JobStateProcessing, calls[0].state)
	assert.Equal(t, vcmdb.JobStateDone, calls[1].state)
	deleteCalls, igroupNames, svmNames := deleter.snapshot()
	assert.Equal(t, 1, deleteCalls)
	assert.Equal(t, []string{"hg-1"}, igroupNames)
	assert.Equal(t, []string{testExpectedSvmName}, svmNames)
}

func TestVcmDeleteHostGroupWorkflow_DeleteIgroupFails(t *testing.T) {
	const operationID = "op-delete-igroup-fails"
	job := &stubJobActivity{}
	deleter := &stubDeleteIgroupActivity{err: nonRetryableTestErr("ontap delete failed")}

	err := runVcmDeleteHostGroupWorkflow(t, operationID, func(reg orchestrator.WorkflowRegistrar) {
		reg.RegisterActivity(job.UpdateJobProgress)
		reg.RegisterActivity((&stubSvmActivity{}).GetSvmByName)
		reg.RegisterActivity(deleter.DeleteIgroup)
	}, testNode(), "hg-1", testExpectedSvmName)

	require.Error(t, err)
	calls := job.snapshot()
	require.GreaterOrEqual(t, len(calls), 2)
	assert.Equal(t, vcmdb.JobStateError, calls[len(calls)-1].state)
	assert.Contains(t, calls[len(calls)-1].errorMessage, "ontap delete failed")
	deleteCalls, _, _ := deleter.snapshot()
	assert.Equal(t, 1, deleteCalls)
}

func TestVcmDeleteHostGroupWorkflow_ProcessingUpdateFails(t *testing.T) {
	const operationID = "op-delete-processing-fails"
	job := &stubJobActivity{
		failOnState: vcmdb.JobStateProcessing,
		failErr:     errors.New("spanner unavailable"),
	}
	deleter := &stubDeleteIgroupActivity{}

	err := runVcmDeleteHostGroupWorkflow(t, operationID, func(reg orchestrator.WorkflowRegistrar) {
		reg.RegisterActivity(job.UpdateJobProgress)
		reg.RegisterActivity((&stubSvmActivity{}).GetSvmByName)
		reg.RegisterActivity(deleter.DeleteIgroup)
	}, testNode(), "hg-1", testExpectedSvmName)

	require.Error(t, err)
	deleteCalls, _, _ := deleter.snapshot()
	assert.Equal(t, 0, deleteCalls)
}

func TestVcmDeleteHostGroupWorkflow_FinishJobUpdateFails(t *testing.T) {
	const operationID = "op-delete-finish-fails"
	job := &stubJobActivity{
		failOnState: vcmdb.JobStateDone,
		failErr:     errors.New("final job update failed"),
	}
	deleter := &stubDeleteIgroupActivity{}

	err := runVcmDeleteHostGroupWorkflow(t, operationID, func(reg orchestrator.WorkflowRegistrar) {
		reg.RegisterActivity(job.UpdateJobProgress)
		reg.RegisterActivity((&stubSvmActivity{}).GetSvmByName)
		reg.RegisterActivity(deleter.DeleteIgroup)
	}, testNode(), "hg-1", testExpectedSvmName)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "final job update failed")
	deleteCalls, _, _ := deleter.snapshot()
	assert.Equal(t, 1, deleteCalls)
}

func TestVcmDeleteHostGroupWorkflow_GetSvmByNameFails(t *testing.T) {
	job := &stubJobActivity{}
	svm := &stubSvmActivity{err: nonRetryableTestErr("SVM lookup failed")}

	err := runVcmDeleteHostGroupWorkflow(t, "op-delete-get-svm-fails", func(reg orchestrator.WorkflowRegistrar) {
		reg.RegisterActivity(job.UpdateJobProgress)
		reg.RegisterActivity(svm.GetSvmByName)
	}, testNode(), "hg-1", testExpectedSvmName)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "SVM lookup failed")
}

func TestVcmDeleteHostGroupWorkflow_DeadlineChecks(t *testing.T) {
	tests := []struct {
		name           string
		jobDelay       time.Duration
		svmDelay       time.Duration
		deadlineOffset time.Duration
	}{
		{name: "before mark processing", deadlineOffset: -time.Second},
		{name: "before get SVM", jobDelay: 3 * time.Second, deadlineOffset: 2 * time.Second},
		{name: "before delete igroup", svmDelay: 3 * time.Second, deadlineOffset: 2 * time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			job := &stubJobActivity{processingDelay: tt.jobDelay}
			svm := &stubSvmActivity{delay: tt.svmDelay}
			deleter := &stubDeleteIgroupActivity{}

			err := runVcmDeleteHostGroupWorkflowWithDeadline(t, uuid.NewString(), func(reg orchestrator.WorkflowRegistrar) {
				reg.RegisterActivity(job.UpdateJobProgress)
				reg.RegisterActivity(svm.GetSvmByName)
				reg.RegisterActivity(deleter.DeleteIgroup)
			}, testNode(), "hg-1", testExpectedSvmName, time.Now().UTC().Add(tt.deadlineOffset))

			require.Error(t, err)
			assert.Contains(t, err.Error(), "operation timed out")
		})
	}
}

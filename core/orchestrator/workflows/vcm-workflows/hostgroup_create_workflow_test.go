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

const hostGroupWorkflowTestTimeout = 30 * time.Second

type stubHostGroupActivity struct {
	mu          sync.Mutex
	createCalls int
	svmNames    []string
	err         error
}

func (s *stubHostGroupActivity) CreateIgroup(_ context.Context, svmName string, _ *common.HostParams, _ *models.Node) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.createCalls++
	s.svmNames = append(s.svmNames, svmName)
	return s.err
}

func (s *stubHostGroupActivity) snapshot() (int, []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	names := make([]string, len(s.svmNames))
	copy(names, s.svmNames)
	return s.createCalls, names
}

func runVcmCreateHostGroupWorkflow(
	t *testing.T,
	instanceID string,
	register func(orchestrator.WorkflowRegistrar),
	node *models.Node,
	host *common.HostParams,
	expectedSvmName string,
) error {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), hostGroupWorkflowTestTimeout)
	defer cancel()

	runner, err := gowfbackend.NewRunner(ctx)
	require.NoError(t, err)

	reg := runner.Registrar()
	reg.RegisterWorkflow(VcmCreateHostGroupWorkflow)
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
	}, gowfbackend.FuncName(VcmCreateHostGroupWorkflow), node, host, expectedSvmName)
	require.NoError(t, err)

	_, err = cclient.GetWorkflowResult[json.RawMessage](ctx, runner.Client(), wfi, hostGroupWorkflowTestTimeout)
	return err
}

func testHostParams() *common.HostParams {
	return &common.HostParams{
		HostName: "hg-1",
		HostIQNs: []string{"iqn.1998-01.com.vmware:db1"},
		OsType:   "LINUX",
	}
}

func TestVcmCreateHostGroupWorkflow_Success(t *testing.T) {
	const operationID = "op-get-svm-success"
	job := &stubJobActivity{}
	svm := &stubSvmActivity{}
	volume := &stubHostGroupActivity{}

	err := runVcmCreateHostGroupWorkflow(t, operationID, func(reg orchestrator.WorkflowRegistrar) {
		reg.RegisterActivity(job.UpdateJobProgress)
		reg.RegisterActivity(svm.GetSvmByName)
		reg.RegisterActivity(volume.CreateIgroup)
	}, testNode(), testHostParams(), testExpectedSvmName)

	require.NoError(t, err)
	assert.Equal(t, 1, svm.callCount())

	createCalls, svmNames := volume.snapshot()
	assert.Equal(t, 1, createCalls)
	assert.Equal(t, []string{testExpectedSvmName}, svmNames)

	calls := job.snapshot()
	require.Len(t, calls, 2)
	assert.Equal(t, operationID, calls[0].jobID)
	assert.Equal(t, vcmdb.JobStateProcessing, calls[0].state)
	assert.Equal(t, vcmdb.JobStateDone, calls[1].state)
	assert.JSONEq(t, `{"igroups":{"name":"hg-1","hosts":["iqn.1998-01.com.vmware:db1"],"osType":"LINUX"}}`, calls[1].responsePayload)
}

func TestVcmCreateHostGroupWorkflow_GetSvmByNameFails(t *testing.T) {
	const operationID = "op-get-svm-fail"
	job := &stubJobActivity{}
	svm := &stubSvmActivity{err: nonRetryableTestErr("ONTAP returned no SVM records")}
	volume := &stubHostGroupActivity{}

	err := runVcmCreateHostGroupWorkflow(t, operationID, func(reg orchestrator.WorkflowRegistrar) {
		reg.RegisterActivity(job.UpdateJobProgress)
		reg.RegisterActivity(svm.GetSvmByName)
		reg.RegisterActivity(volume.CreateIgroup)
	}, testNode(), testHostParams(), testExpectedSvmName)

	require.Error(t, err)
	createCalls, _ := volume.snapshot()
	assert.Equal(t, 0, createCalls)

	calls := job.snapshot()
	require.Len(t, calls, 2)
	assert.Equal(t, vcmdb.JobStateProcessing, calls[0].state)
	assert.Equal(t, vcmdb.JobStateError, calls[1].state)
	assert.Contains(t, calls[1].errorMessage, "ONTAP returned no SVM records")
}

func TestVcmCreateHostGroupWorkflow_CreateIgroupFails(t *testing.T) {
	const operationID = "op-create-igroup-fail"
	job := &stubJobActivity{}
	svm := &stubSvmActivity{}
	volume := &stubHostGroupActivity{err: nonRetryableTestErr("igroup create failed")}

	err := runVcmCreateHostGroupWorkflow(t, operationID, func(reg orchestrator.WorkflowRegistrar) {
		reg.RegisterActivity(job.UpdateJobProgress)
		reg.RegisterActivity(svm.GetSvmByName)
		reg.RegisterActivity(volume.CreateIgroup)
	}, testNode(), testHostParams(), testExpectedSvmName)

	require.Error(t, err)
	createCalls, svmNames := volume.snapshot()
	assert.Equal(t, 1, createCalls)
	assert.Equal(t, []string{testExpectedSvmName}, svmNames)

	calls := job.snapshot()
	require.Len(t, calls, 2)
	assert.Equal(t, vcmdb.JobStateProcessing, calls[0].state)
	assert.Equal(t, vcmdb.JobStateError, calls[1].state)
	assert.Contains(t, calls[1].errorMessage, "igroup create failed")
}

func TestVcmCreateHostGroupWorkflow_IgroupAlreadyExists(t *testing.T) {
	const operationID = "op-create-igroup-conflict"
	job := &stubJobActivity{}
	svm := &stubSvmActivity{}
	volume := &stubHostGroupActivity{err: nonRetryableTestErr(`igroup "hg-1" already exists`)}

	err := runVcmCreateHostGroupWorkflow(t, operationID, func(reg orchestrator.WorkflowRegistrar) {
		reg.RegisterActivity(job.UpdateJobProgress)
		reg.RegisterActivity(svm.GetSvmByName)
		reg.RegisterActivity(volume.CreateIgroup)
	}, testNode(), testHostParams(), testExpectedSvmName)

	require.Error(t, err)
	createCalls, svmNames := volume.snapshot()
	assert.Equal(t, 1, createCalls)
	assert.Equal(t, []string{testExpectedSvmName}, svmNames)

	calls := job.snapshot()
	require.Len(t, calls, 2)
	assert.Equal(t, vcmdb.JobStateProcessing, calls[0].state)
	assert.Equal(t, vcmdb.JobStateError, calls[1].state)
	assert.Contains(t, calls[1].errorMessage, `igroup "hg-1" already exists`)
}

func TestVcmCreateHostGroupWorkflow_MarkProcessingFails(t *testing.T) {
	const operationID = "op-processing-fail"
	job := &stubJobActivity{
		failOnState: vcmdb.JobStateProcessing,
		failErr:     errors.New("store unavailable"),
	}
	svm := &stubSvmActivity{}
	volume := &stubHostGroupActivity{}

	err := runVcmCreateHostGroupWorkflow(t, operationID, func(reg orchestrator.WorkflowRegistrar) {
		reg.RegisterActivity(job.UpdateJobProgress)
		reg.RegisterActivity(svm.GetSvmByName)
		reg.RegisterActivity(volume.CreateIgroup)
	}, testNode(), testHostParams(), testExpectedSvmName)

	require.Error(t, err)
	createCalls, _ := volume.snapshot()
	assert.Equal(t, 0, createCalls)

	calls := job.snapshot()
	require.Len(t, calls, 1)
	assert.Equal(t, vcmdb.JobStateProcessing, calls[0].state)
}

func TestVcmCreateHostGroupWorkflow_MarkDoneFails(t *testing.T) {
	const operationID = "op-done-fail"
	job := &stubJobActivity{
		failOnState: vcmdb.JobStateDone,
		failErr:     errors.New("store unavailable on done"),
	}
	svm := &stubSvmActivity{}
	volume := &stubHostGroupActivity{}

	err := runVcmCreateHostGroupWorkflow(t, operationID, func(reg orchestrator.WorkflowRegistrar) {
		reg.RegisterActivity(job.UpdateJobProgress)
		reg.RegisterActivity(svm.GetSvmByName)
		reg.RegisterActivity(volume.CreateIgroup)
	}, testNode(), testHostParams(), testExpectedSvmName)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "store unavailable on done")
	createCalls, _ := volume.snapshot()
	assert.Equal(t, 1, createCalls)

	calls := job.snapshot()
	require.Len(t, calls, 2)
	assert.Equal(t, vcmdb.JobStateProcessing, calls[0].state)
	assert.Equal(t, vcmdb.JobStateDone, calls[1].state)
}

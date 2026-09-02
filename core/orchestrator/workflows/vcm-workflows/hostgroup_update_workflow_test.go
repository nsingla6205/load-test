package vcm_workflows

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	cclient "github.com/cschleiden/go-workflows/client"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	vcmdb "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcm"
	orchestrator "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator/gowfbackend"
)

type stubUpdateIgroupActivity struct {
	mu          sync.Mutex
	updateCalls int
	igroupNames []string
	hostsArgs   [][]string
	svmNames    []string
	osType      string
	err         error
}

func (s *stubUpdateIgroupActivity) UpdateIgroup(_ context.Context, igroupName string, hosts []string, svmName string, _ *models.Node) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.updateCalls++
	s.igroupNames = append(s.igroupNames, igroupName)
	copied := make([]string, len(hosts))
	copy(copied, hosts)
	s.hostsArgs = append(s.hostsArgs, copied)
	s.svmNames = append(s.svmNames, svmName)
	if s.err != nil {
		return "", s.err
	}
	return s.osType, nil
}

func (s *stubUpdateIgroupActivity) snapshot() (int, []string, [][]string, []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	igroups := append([]string(nil), s.igroupNames...)
	svms := append([]string(nil), s.svmNames...)
	hosts := make([][]string, len(s.hostsArgs))
	for i, h := range s.hostsArgs {
		hosts[i] = append([]string(nil), h...)
	}
	return s.updateCalls, igroups, hosts, svms
}

func runVcmUpdateHostGroupWorkflow(
	t *testing.T,
	instanceID string,
	register func(orchestrator.WorkflowRegistrar),
	node *models.Node,
	igroupName string,
	hosts []string,
	expectedSvmName string,
) error {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), hostGroupWorkflowTestTimeout)
	defer cancel()

	runner, err := gowfbackend.NewRunner(ctx)
	require.NoError(t, err)

	reg := runner.Registrar()
	reg.RegisterWorkflow(VcmUpdateHostGroupWorkflow)
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
	}, gowfbackend.FuncName(VcmUpdateHostGroupWorkflow), node, igroupName, hosts, expectedSvmName)
	require.NoError(t, err)

	_, err = cclient.GetWorkflowResult[json.RawMessage](ctx, runner.Client(), wfi, hostGroupWorkflowTestTimeout)
	return err
}

func TestVcmUpdateHostGroupWorkflow_Success(t *testing.T) {
	const operationID = "op-update-success"
	job := &stubJobActivity{}
	svm := &stubSvmActivity{}
	updater := &stubUpdateIgroupActivity{osType: "LINUX"}
	hosts := []string{"iqn.1998-01.com.vmware:db1", "iqn.1998-01.com.vmware:db2"}

	err := runVcmUpdateHostGroupWorkflow(t, operationID, func(reg orchestrator.WorkflowRegistrar) {
		reg.RegisterActivity(job.UpdateJobProgress)
		reg.RegisterActivity(svm.GetSvmByName)
		reg.RegisterActivity(updater.UpdateIgroup)
	}, testNode(), "hg-1", hosts, testExpectedSvmName)

	require.NoError(t, err)
	assert.Equal(t, 1, svm.callCount())

	calls := job.snapshot()
	require.Len(t, calls, 2)
	assert.Equal(t, operationID, calls[0].jobID)
	assert.Equal(t, vcmdb.JobStateProcessing, calls[0].state)
	assert.Equal(t, vcmdb.JobStateDone, calls[1].state)
	assert.JSONEq(t, `{"igroups":{"name":"hg-1","hosts":["iqn.1998-01.com.vmware:db1","iqn.1998-01.com.vmware:db2"],"osType":"LINUX"}}`, calls[1].responsePayload)

	updateCalls, igroups, hostsArgs, svms := updater.snapshot()
	assert.Equal(t, 1, updateCalls)
	assert.Equal(t, []string{"hg-1"}, igroups)
	require.Len(t, hostsArgs, 1)
	assert.Equal(t, hosts, hostsArgs[0])
	assert.Equal(t, []string{testExpectedSvmName}, svms)
}

func TestVcmUpdateHostGroupWorkflow_UpdateIgroupFails(t *testing.T) {
	const operationID = "op-update-igroup-fails"
	job := &stubJobActivity{}
	svm := &stubSvmActivity{}
	updater := &stubUpdateIgroupActivity{err: nonRetryableTestErr("ontap update failed")}

	err := runVcmUpdateHostGroupWorkflow(t, operationID, func(reg orchestrator.WorkflowRegistrar) {
		reg.RegisterActivity(job.UpdateJobProgress)
		reg.RegisterActivity(svm.GetSvmByName)
		reg.RegisterActivity(updater.UpdateIgroup)
	}, testNode(), "hg-1", []string{"iqn.1998-01.com.vmware:db1"}, testExpectedSvmName)

	require.Error(t, err)
	calls := job.snapshot()
	require.GreaterOrEqual(t, len(calls), 2)
	assert.Equal(t, vcmdb.JobStateError, calls[len(calls)-1].state)
	assert.Contains(t, calls[len(calls)-1].errorMessage, "ontap update failed")
}

func TestVcmUpdateHostGroupWorkflow_GetSvmByNameFails(t *testing.T) {
	const operationID = "op-update-get-svm-fail"
	job := &stubJobActivity{}
	svm := &stubSvmActivity{err: nonRetryableTestErr("ONTAP returned no SVM records")}
	updater := &stubUpdateIgroupActivity{}

	err := runVcmUpdateHostGroupWorkflow(t, operationID, func(reg orchestrator.WorkflowRegistrar) {
		reg.RegisterActivity(job.UpdateJobProgress)
		reg.RegisterActivity(svm.GetSvmByName)
		reg.RegisterActivity(updater.UpdateIgroup)
	}, testNode(), "hg-1", []string{"iqn.1998-01.com.vmware:db1"}, testExpectedSvmName)

	require.Error(t, err)
	updateCalls, _, _, _ := updater.snapshot()
	assert.Equal(t, 0, updateCalls)

	calls := job.snapshot()
	require.Len(t, calls, 2)
	assert.Equal(t, vcmdb.JobStateProcessing, calls[0].state)
	assert.Equal(t, vcmdb.JobStateError, calls[1].state)
	assert.Contains(t, calls[1].errorMessage, "ONTAP returned no SVM records")
}

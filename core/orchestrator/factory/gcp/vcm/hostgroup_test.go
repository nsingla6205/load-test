package vcm

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	cbackend "github.com/cschleiden/go-workflows/backend"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	ontapRest "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/ontap-rest"
	ontapModels "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/models"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	vcm_workflows "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows/vcm-workflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vcmdb "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcm"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator/gowfbackend"
)

func createHostGroupParams() *commonparams.CreateHostGroupParams {
	return &commonparams.CreateHostGroupParams{
		Name:        "hg-1",
		Hosts:       []string{"iqn.1998-01.com.vmware:db1"},
		OSType:      "LINUX",
		OperationID: "request-1",
		Deadline:    time.Now().UTC().Add(time.Hour),
	}
}

func updateHostGroupParams() *commonparams.UpdateHostGroupParams {
	hosts := []string{"iqn.1998-01.com.vmware:db1", "iqn.1998-01.com.vmware:db2"}
	return &commonparams.UpdateHostGroupParams{
		Name:        "hg-1",
		Hosts:       &hosts,
		OperationID: "request-update-1",
		Deadline:    time.Now().UTC().Add(time.Hour),
	}
}

func deleteHostGroupParams() *commonparams.DeleteHostGroupParams {
	return &commonparams.DeleteHostGroupParams{
		Name:        "hg-1",
		OperationID: "request-delete-1",
		Deadline:    time.Now().UTC().Add(time.Hour),
	}
}

func TestCreateHostGroup_PersistsJobAndDispatchesWorkflow(t *testing.T) {
	store := &stubStore{input: validNodeProviderInput()}
	starter := &stubWorkflowStarter{}
	orch := NewVCMOrchestrator(store, starter, log.NewLogger())
	params := createHostGroupParams()

	hostGroup, err := orch.CreateHostGroup(context.Background(), params)

	require.NoError(t, err)
	require.NotNil(t, hostGroup)
	assert.Equal(t, params.OperationID, hostGroup.UUID)
	assert.Equal(t, params.Name, hostGroup.Name)
	require.NotNil(t, store.createdJob)
	assert.Equal(t, params.OperationID, store.createdJob.ID)
	assert.Equal(t, params.Name, store.createdJob.ResourceID)
	assert.Equal(t, string(datamodel.JobTypeCreateHostGroup), store.createdJob.Type)
	assert.Equal(t, vcmdb.JobStateNew, store.createdJob.State)
	assert.True(t, store.createdJob.DeadlineAt.Equal(params.Deadline))
	assert.Equal(t, params.OperationID, starter.instanceID)
	assert.Equal(t, gowfbackend.FuncName(vcm_workflows.VcmCreateHostGroupWorkflow), gowfbackend.FuncName(starter.workflowFn))
	require.Len(t, starter.args, 3)
	nodeArg, ok := starter.args[0].(*models.Node)
	require.True(t, ok)
	require.NotNil(t, nodeArg)
	assert.Equal(t, "gcnv-test", nodeArg.DeploymentName)
	assert.Equal(t, env.USERNAME_PWD_SEC_MGR, nodeArg.AuthType)
	assert.Equal(t, "secret-1", nodeArg.SecretID)
	assert.Equal(t, "198.18.0.5", nodeArg.EndpointAddressesToHostNameMap["198.18.0.5"])
	hostParamsArg, ok := starter.args[1].(*commonparams.HostParams)
	require.True(t, ok)
	require.NotNil(t, hostParamsArg)
	assert.Equal(t, params.Name, hostParamsArg.HostName)
	assert.Equal(t, params.Hosts, hostParamsArg.HostIQNs)
	assert.Equal(t, params.OSType, hostParamsArg.OsType)
	expectedSvmName, ok := starter.args[2].(string)
	require.True(t, ok)
	assert.Equal(t, "gcnv-test-svm-01", expectedSvmName)
}

func TestCreateHostGroup_EmptyDeploymentIdReturnsErrorWithoutCreatingJob(t *testing.T) {
	input := validNodeProviderInput()
	input.DeploymentName = ""
	store := &stubStore{input: input}
	starter := &stubWorkflowStarter{}
	orch := NewVCMOrchestrator(store, starter, log.NewLogger())

	_, err := orch.CreateHostGroup(context.Background(), createHostGroupParams())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "derive SVM name")
	assert.Nil(t, store.createdJob)
	assert.Empty(t, store.updatedJobID)
	assert.Empty(t, starter.instanceID)
}

func TestCreateHostGroup_InvalidConfigReturnsErrorWithoutCreatingJob(t *testing.T) {
	store := &stubStore{inputErr: datamodel.ErrInvalidConfig}
	starter := &stubWorkflowStarter{}
	orch := NewVCMOrchestrator(store, starter, log.NewLogger())

	_, err := orch.CreateHostGroup(context.Background(), createHostGroupParams())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "build ONTAP node")
	assert.Nil(t, store.createdJob)
	assert.Empty(t, store.updatedJobID)
	assert.Empty(t, starter.instanceID)
}

func TestCreateHostGroup_CreateJobFailureDoesNotDispatchWorkflow(t *testing.T) {
	store := &stubStore{input: validNodeProviderInput(), createErr: errors.New("database unavailable")}
	starter := &stubWorkflowStarter{}
	orch := NewVCMOrchestrator(store, starter, log.NewLogger())

	_, err := orch.CreateHostGroup(context.Background(), createHostGroupParams())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "persist job")
	assert.Empty(t, starter.instanceID)
}

func TestCreateHostGroup_DispatchFailureMarksJobError(t *testing.T) {
	store := &stubStore{input: validNodeProviderInput()}
	starter := &stubWorkflowStarter{err: errors.New("worker unavailable")}
	orch := NewVCMOrchestrator(store, starter, log.NewLogger())

	_, err := orch.CreateHostGroup(context.Background(), createHostGroupParams())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "start workflow")
	assert.Equal(t, "request-1", store.updatedJobID)
	assert.Equal(t, vcmdb.JobStateError, store.updatedState)
	assert.Equal(t, jobStepFailedToStart, store.updatedStep)
	assert.Equal(t, "worker unavailable", store.updatedError)
	assert.True(t, store.updatedRetriable)
}

func TestCreateHostGroup_DispatchFailureUpdateJobAlsoFails(t *testing.T) {
	store := &stubStore{input: validNodeProviderInput(), updateErr: errors.New("update failed")}
	starter := &stubWorkflowStarter{err: errors.New("worker unavailable")}
	orch := NewVCMOrchestrator(store, starter, log.NewLogger())

	_, err := orch.CreateHostGroup(context.Background(), createHostGroupParams())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "start workflow")
}

func TestCreateHostGroup_InstanceAlreadyExistsIsIdempotent(t *testing.T) {
	store := &stubStore{input: validNodeProviderInput()}
	starter := &stubWorkflowStarter{err: cbackend.ErrInstanceAlreadyExists}
	orch := NewVCMOrchestrator(store, starter, log.NewLogger())

	hostGroup, err := orch.CreateHostGroup(context.Background(), createHostGroupParams())

	require.NoError(t, err)
	require.NotNil(t, hostGroup)
	assert.Equal(t, "request-1", hostGroup.UUID)
	assert.Empty(t, store.updatedJobID)
}

func TestCreateHostGroup_DuplicateJobIsIdempotent(t *testing.T) {
	store := &stubStore{
		input:     validNodeProviderInput(),
		createErr: fmt.Errorf("%w: request-1", vcmdb.ErrJobAlreadyExists),
		existingJob: &vcmdb.Job{
			ID:    "request-1",
			State: vcmdb.JobStateNew,
		},
	}
	starter := &stubWorkflowStarter{}
	orch := NewVCMOrchestrator(store, starter, log.NewLogger())

	hostGroup, err := orch.CreateHostGroup(context.Background(), createHostGroupParams())

	require.NoError(t, err)
	require.NotNil(t, hostGroup)
	assert.Equal(t, "request-1", hostGroup.UUID)
	assert.NotNil(t, store.createdJob)
	assert.Empty(t, starter.instanceID)
}

func TestCreateHostGroup_DuplicateJobGetJobFailure(t *testing.T) {
	store := &stubStore{
		input:     validNodeProviderInput(),
		createErr: fmt.Errorf("%w: request-1", vcmdb.ErrJobAlreadyExists),
		getJobErr: errors.New("database unavailable"),
	}
	orch := NewVCMOrchestrator(store, &stubWorkflowStarter{}, log.NewLogger())

	_, err := orch.CreateHostGroup(context.Background(), createHostGroupParams())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "get existing job")
}

func TestUpdateHostGroup_PersistsJobAndDispatchesWorkflow(t *testing.T) {
	store := &stubStore{input: validNodeProviderInput()}
	starter := &stubWorkflowStarter{}
	orch := NewVCMOrchestrator(store, starter, log.NewLogger())
	params := updateHostGroupParams()

	hostGroup, operationID, err := orch.UpdateHostGroup(context.Background(), params)

	require.NoError(t, err)
	assert.Equal(t, params.OperationID, operationID)
	require.NotNil(t, hostGroup)
	assert.Equal(t, "hg-1", hostGroup.Name)
	assert.Equal(t, operationID, hostGroup.UUID)
	require.NotNil(t, store.createdJob)
	assert.Equal(t, operationID, store.createdJob.ID)
	assert.Equal(t, params.Name, store.createdJob.ResourceID)
	assert.Equal(t, string(datamodel.JobTypeUpdateHostGroup), store.createdJob.Type)
	assert.Equal(t, vcmdb.JobStateNew, store.createdJob.State)
	assert.True(t, store.createdJob.DeadlineAt.Equal(params.Deadline.UTC()))
	assert.Equal(t, operationID, starter.instanceID)
	assert.Equal(t, gowfbackend.FuncName(vcm_workflows.VcmUpdateHostGroupWorkflow), gowfbackend.FuncName(starter.workflowFn))
	require.Len(t, starter.args, 4)
	nodeArg, ok := starter.args[0].(*models.Node)
	require.True(t, ok)
	require.NotNil(t, nodeArg)
	assert.Equal(t, "gcnv-test", nodeArg.DeploymentName)
	igroupName, ok := starter.args[1].(string)
	require.True(t, ok)
	assert.Equal(t, params.Name, igroupName)
	hostsArg, ok := starter.args[2].([]string)
	require.True(t, ok)
	assert.Equal(t, *params.Hosts, hostsArg)
	expectedSvmName, ok := starter.args[3].(string)
	require.True(t, ok)
	assert.Equal(t, "gcnv-test-svm-01", expectedSvmName)
}

func TestUpdateHostGroup_InvalidConfigReturnsErrorWithoutCreatingJob(t *testing.T) {
	store := &stubStore{inputErr: datamodel.ErrInvalidConfig}
	starter := &stubWorkflowStarter{}
	orch := NewVCMOrchestrator(store, starter, log.NewLogger())

	_, _, err := orch.UpdateHostGroup(context.Background(), updateHostGroupParams())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "build ONTAP node")
	assert.Nil(t, store.createdJob)
	assert.Empty(t, store.updatedJobID)
	assert.Empty(t, starter.instanceID)
}

func TestUpdateHostGroup_EmptyDeploymentIdReturnsErrorWithoutCreatingJob(t *testing.T) {
	input := validNodeProviderInput()
	input.DeploymentName = ""
	store := &stubStore{input: input}
	starter := &stubWorkflowStarter{}
	orch := NewVCMOrchestrator(store, starter, log.NewLogger())

	_, _, err := orch.UpdateHostGroup(context.Background(), updateHostGroupParams())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "derive SVM name")
	assert.Nil(t, store.createdJob)
	assert.Empty(t, store.updatedJobID)
	assert.Empty(t, starter.instanceID)
}

func TestUpdateHostGroup_CreateJobFailureDoesNotDispatchWorkflow(t *testing.T) {
	store := &stubStore{input: validNodeProviderInput(), createErr: errors.New("database unavailable")}
	starter := &stubWorkflowStarter{}
	orch := NewVCMOrchestrator(store, starter, log.NewLogger())

	_, _, err := orch.UpdateHostGroup(context.Background(), updateHostGroupParams())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "persist job")
	assert.Empty(t, starter.instanceID)
}

func TestUpdateHostGroup_InstanceAlreadyExistsIsIdempotent(t *testing.T) {
	store := &stubStore{input: validNodeProviderInput()}
	starter := &stubWorkflowStarter{err: cbackend.ErrInstanceAlreadyExists}
	orch := NewVCMOrchestrator(store, starter, log.NewLogger())
	params := updateHostGroupParams()

	hostGroup, operationID, err := orch.UpdateHostGroup(context.Background(), params)

	require.NoError(t, err)
	require.NotNil(t, hostGroup)
	assert.Equal(t, params.Name, hostGroup.Name)
	assert.NotEmpty(t, operationID)
	assert.Equal(t, operationID, hostGroup.UUID)
}

func TestUpdateHostGroup_DispatchFailureMarksJobError(t *testing.T) {
	store := &stubStore{input: validNodeProviderInput()}
	starter := &stubWorkflowStarter{err: errors.New("worker unavailable")}
	orch := NewVCMOrchestrator(store, starter, log.NewLogger())

	_, operationID, err := orch.UpdateHostGroup(context.Background(), updateHostGroupParams())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "start workflow")
	assert.Empty(t, operationID)
	assert.Equal(t, store.createdJob.ID, store.updatedJobID)
	assert.Equal(t, vcmdb.JobStateError, store.updatedState)
	assert.Equal(t, jobStepFailedToStart, store.updatedStep)
	assert.Equal(t, "worker unavailable", store.updatedError)
	assert.True(t, store.updatedRetriable)
}

func TestUpdateHostGroup_DispatchFailureUpdateJobAlsoFails(t *testing.T) {
	store := &stubStore{input: validNodeProviderInput(), updateErr: errors.New("update failed")}
	starter := &stubWorkflowStarter{err: errors.New("worker unavailable")}
	orch := NewVCMOrchestrator(store, starter, log.NewLogger())

	_, _, err := orch.UpdateHostGroup(context.Background(), updateHostGroupParams())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "start workflow")
}

func TestDeleteHostGroup_PersistsJobAndDispatchesWorkflow(t *testing.T) {
	store := &stubStore{input: validNodeProviderInput()}
	starter := &stubWorkflowStarter{}
	orch := NewVCMOrchestrator(store, starter, log.NewLogger())
	params := deleteHostGroupParams()

	hostGroup, err := orch.DeleteHostGroup(context.Background(), params)

	require.NoError(t, err)
	require.NotNil(t, hostGroup)
	assert.Equal(t, params.Name, hostGroup.Name)
	assert.Equal(t, params.OperationID, hostGroup.UUID)
	require.NotNil(t, store.createdJob)
	assert.Equal(t, params.OperationID, store.createdJob.ID)
	assert.Equal(t, params.Name, store.createdJob.ResourceID)
	assert.Equal(t, string(datamodel.JobTypeDeleteHostGroup), store.createdJob.Type)
	assert.Equal(t, vcmdb.JobStateNew, store.createdJob.State)
	assert.True(t, store.createdJob.DeadlineAt.Equal(params.Deadline.UTC()))
	assert.Equal(t, params.OperationID, starter.instanceID)
	assert.Equal(t, gowfbackend.FuncName(vcm_workflows.VcmDeleteHostGroupWorkflow), gowfbackend.FuncName(starter.workflowFn))
	require.Len(t, starter.args, 3)
	nodeArg, ok := starter.args[0].(*models.Node)
	require.True(t, ok)
	require.NotNil(t, nodeArg)
	assert.Equal(t, "gcnv-test", nodeArg.DeploymentName)
	igroupName, ok := starter.args[1].(string)
	require.True(t, ok)
	assert.Equal(t, "hg-1", igroupName)
	expectedSvmName, ok := starter.args[2].(string)
	require.True(t, ok)
	assert.Equal(t, "gcnv-test-svm-01", expectedSvmName)
}

func TestDeleteHostGroup_EmptyDeploymentIdReturnsErrorWithoutCreatingJob(t *testing.T) {
	input := validNodeProviderInput()
	input.DeploymentName = ""
	store := &stubStore{input: input}
	starter := &stubWorkflowStarter{}
	orch := NewVCMOrchestrator(store, starter, log.NewLogger())

	_, err := orch.DeleteHostGroup(context.Background(), deleteHostGroupParams())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "derive SVM name")
	assert.Nil(t, store.createdJob)
	assert.Empty(t, store.updatedJobID)
	assert.Empty(t, starter.instanceID)
}

func TestDeleteHostGroup_InvalidConfigReturnsErrorWithoutCreatingJob(t *testing.T) {
	store := &stubStore{inputErr: datamodel.ErrInvalidConfig}
	starter := &stubWorkflowStarter{}
	orch := NewVCMOrchestrator(store, starter, log.NewLogger())

	_, err := orch.DeleteHostGroup(context.Background(), deleteHostGroupParams())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "build ONTAP node")
	assert.Nil(t, store.createdJob)
	assert.Empty(t, store.updatedJobID)
	assert.Empty(t, starter.instanceID)
}

func TestDeleteHostGroup_CreateJobFailureDoesNotDispatchWorkflow(t *testing.T) {
	store := &stubStore{input: validNodeProviderInput(), createErr: errors.New("database unavailable")}
	starter := &stubWorkflowStarter{}
	orch := NewVCMOrchestrator(store, starter, log.NewLogger())

	_, err := orch.DeleteHostGroup(context.Background(), deleteHostGroupParams())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "persist job")
	assert.Empty(t, starter.instanceID)
}

func TestDeleteHostGroup_DispatchFailureMarksJobError(t *testing.T) {
	store := &stubStore{input: validNodeProviderInput()}
	starter := &stubWorkflowStarter{err: errors.New("worker unavailable")}
	orch := NewVCMOrchestrator(store, starter, log.NewLogger())

	_, err := orch.DeleteHostGroup(context.Background(), deleteHostGroupParams())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "start workflow")
	assert.Equal(t, store.createdJob.ID, store.updatedJobID)
	assert.Equal(t, vcmdb.JobStateError, store.updatedState)
	assert.Equal(t, "worker unavailable", store.updatedError)
}

func TestDeleteHostGroup_InstanceAlreadyExistsIsIdempotent(t *testing.T) {
	store := &stubStore{input: validNodeProviderInput()}
	starter := &stubWorkflowStarter{err: cbackend.ErrInstanceAlreadyExists}
	orch := NewVCMOrchestrator(store, starter, log.NewLogger())

	hostGroup, err := orch.DeleteHostGroup(context.Background(), deleteHostGroupParams())

	require.NoError(t, err)
	require.NotNil(t, hostGroup)
	assert.Equal(t, "hg-1", hostGroup.Name)
	assert.Equal(t, hostGroup.UUID, store.createdJob.ID)
	assert.Empty(t, store.updatedJobID)
}

func TestCreateHostGroup_DuplicateJobReturnsExistingRegardlessOfState(t *testing.T) {
	store := &stubStore{
		input:     validNodeProviderInput(),
		createErr: fmt.Errorf("%w: request-1", vcmdb.ErrJobAlreadyExists),
		existingJob: &vcmdb.Job{
			ID:    "request-1",
			State: vcmdb.JobStateProcessing,
		},
	}
	starter := &stubWorkflowStarter{}
	orch := NewVCMOrchestrator(store, starter, log.NewLogger())

	hostGroup, err := orch.CreateHostGroup(context.Background(), createHostGroupParams())

	require.NoError(t, err)
	require.NotNil(t, hostGroup)
	assert.Equal(t, "request-1", hostGroup.UUID)
	assert.Empty(t, starter.instanceID)
}

func TestGetHostGroup_InvalidConfig(t *testing.T) {
	orch := NewVCMOrchestrator(&stubStore{inputErr: datamodel.ErrInvalidConfig}, &stubWorkflowStarter{}, log.NewLogger())
	_, err := orch.GetHostGroup(context.Background(), "hg-1", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "build ONTAP node")
}

func TestGetHostGroup_EmptyDeploymentIdReturnsError(t *testing.T) {
	input := validNodeProviderInput()
	input.DeploymentName = ""
	store := &stubStore{input: input}
	orch := NewVCMOrchestrator(store, &stubWorkflowStarter{}, log.NewLogger())

	_, err := orch.GetHostGroup(context.Background(), "hg-1", "")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "derive SVM name")
}

func TestGetHostGroup_SVMNotFoundDoesNotSurfaceAsHostGroupNotFound(t *testing.T) {
	svmName := "gcnv-test-svm-01"
	mockProvider := vsa.NewMockProvider(t)
	mockProvider.EXPECT().
		GetSVM(vsa.GetSvmParams{Name: svmName}).
		Return(nil, vsaerrors.NewVCPError(vsaerrors.ErrSVMNotFound, customerrors.NewNotFoundErr("svm", &svmName)))

	orig := getProviderByNode
	getProviderByNode = func(context.Context, *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}
	t.Cleanup(func() { getProviderByNode = orig })

	orch := NewVCMOrchestrator(&stubStore{input: validNodeProviderInput()}, &stubWorkflowStarter{}, log.NewLogger())
	_, err := orch.GetHostGroup(context.Background(), "hg-1", "")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "resolve SVM")
	assert.False(t, customerrors.IsNotFoundErrForObjectTypeInChain(err, "HostGroup"))
}

func TestGetHostGroup_ReturnsOntapIgroup(t *testing.T) {
	iqn1 := "iqn.1998-01.com.vmware:testhost1"
	iqn2 := "iqn.1998-01.com.vmware:testhost2"
	osType := "linux"
	igroupName := "hg-test-linux"
	svmName := "gcnv-test-svm-01"

	mockProvider := vsa.NewMockProvider(t)
	mockProvider.EXPECT().
		GetSVM(vsa.GetSvmParams{Name: svmName}).
		Return(&ontapRest.Svm{Svm: ontapModels.Svm{Name: &svmName}}, nil)
	mockProvider.EXPECT().
		IgroupGet(&igroupName, &svmName).
		Return(&ontapRest.Igroup{
			Igroup: ontapModels.Igroup{
				Name:   &igroupName,
				OsType: &osType,
				IgroupInlineInitiators: []*ontapModels.IgroupInlineInitiatorsInlineArrayItem{
					{Name: &iqn1},
					{Name: &iqn2},
				},
			},
		}, nil)

	orig := getProviderByNode
	getProviderByNode = func(context.Context, *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}
	t.Cleanup(func() { getProviderByNode = orig })

	orch := NewVCMOrchestrator(&stubStore{input: validNodeProviderInput()}, &stubWorkflowStarter{}, log.NewLogger())
	hg, err := orch.GetHostGroup(context.Background(), igroupName, "")
	require.NoError(t, err)
	require.NotNil(t, hg)
	assert.Equal(t, igroupName, hg.Name)
	assert.Equal(t, osType, hg.OSType)
	assert.Equal(t, []string{iqn1, iqn2}, hg.Hosts)
}

func TestGetHostGroup_NotFound(t *testing.T) {
	svmName := "gcnv-test-svm-01"
	missingHG := "missing-hg"
	mockProvider := vsa.NewMockProvider(t)
	mockProvider.EXPECT().
		GetSVM(vsa.GetSvmParams{Name: svmName}).
		Return(&ontapRest.Svm{Svm: ontapModels.Svm{Name: &svmName}}, nil)
	mockProvider.EXPECT().
		IgroupGet(&missingHG, &svmName).
		Return(nil, customerrors.NewNotFoundErr("igroup", nil))

	orig := getProviderByNode
	getProviderByNode = func(context.Context, *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}
	t.Cleanup(func() { getProviderByNode = orig })

	orch := NewVCMOrchestrator(&stubStore{input: validNodeProviderInput()}, &stubWorkflowStarter{}, log.NewLogger())
	_, err := orch.GetHostGroup(context.Background(), "missing-hg", "")
	require.Error(t, err)
	assert.True(t, customerrors.IsNotFoundErr(err))
}

func TestGetHostGroup_NilIgroupResult(t *testing.T) {
	svmName := "gcnv-test-svm-01"
	name := "missing-hg"
	mockProvider := vsa.NewMockProvider(t)
	mockProvider.EXPECT().
		GetSVM(vsa.GetSvmParams{Name: svmName}).
		Return(&ontapRest.Svm{Svm: ontapModels.Svm{Name: &svmName}}, nil)
	mockProvider.EXPECT().
		IgroupGet(&name, &svmName).
		Return(nil, nil)

	orig := getProviderByNode
	getProviderByNode = func(context.Context, *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}
	t.Cleanup(func() { getProviderByNode = orig })

	orch := NewVCMOrchestrator(&stubStore{input: validNodeProviderInput()}, &stubWorkflowStarter{}, log.NewLogger())
	_, err := orch.GetHostGroup(context.Background(), name, "")
	require.Error(t, err)
	assert.True(t, customerrors.IsNotFoundErr(err))
}

func TestGetMultipleHostGroups_ListsOntapIgroups(t *testing.T) {
	svmName := "gcnv-test-svm-01"
	name1 := "hg-linux"
	name2 := "hg-win"
	osLinux := "linux"
	osWin := "windows"
	iqn1 := "iqn.1998-01.com.vmware:testhost1"
	iqn2 := "iqn.1998-01.com.vmware:win1"

	mockProvider := vsa.NewMockProvider(t)
	mockProvider.EXPECT().
		GetSVM(vsa.GetSvmParams{Name: svmName}).
		Return(&ontapRest.Svm{Svm: ontapModels.Svm{Name: &svmName}}, nil)
	mockProvider.EXPECT().
		IgroupList(&svmName).
		Return([]*ontapRest.Igroup{
			{
				Igroup: ontapModels.Igroup{
					Name:   &name1,
					OsType: &osLinux,
					IgroupInlineInitiators: []*ontapModels.IgroupInlineInitiatorsInlineArrayItem{
						{Name: &iqn1},
					},
				},
			},
			{
				Igroup: ontapModels.Igroup{
					Name:   &name2,
					OsType: &osWin,
					IgroupInlineInitiators: []*ontapModels.IgroupInlineInitiatorsInlineArrayItem{
						{Name: &iqn2},
					},
				},
			},
		}, nil)

	orig := getProviderByNode
	getProviderByNode = func(context.Context, *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}
	t.Cleanup(func() { getProviderByNode = orig })

	orch := NewVCMOrchestrator(&stubStore{input: validNodeProviderInput()}, &stubWorkflowStarter{}, log.NewLogger())
	hgs, err := orch.GetMultipleHostGroups(context.Background(), "", nil)
	require.NoError(t, err)
	require.Len(t, hgs, 2)
	assert.Equal(t, "hg-linux", hgs[0].Name)
	assert.Equal(t, osLinux, hgs[0].OSType)
	assert.Equal(t, []string{iqn1}, hgs[0].Hosts)
	assert.Equal(t, "hg-win", hgs[1].Name)
	assert.Equal(t, osWin, hgs[1].OSType)
}

func TestGetMultipleHostGroups_InvalidConfig(t *testing.T) {
	orch := NewVCMOrchestrator(&stubStore{inputErr: datamodel.ErrInvalidConfig}, &stubWorkflowStarter{}, log.NewLogger())
	_, err := orch.GetMultipleHostGroups(context.Background(), "", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "build ONTAP node")
}

func TestGetMultipleHostGroups_EmptyDeploymentIdReturnsError(t *testing.T) {
	input := validNodeProviderInput()
	input.DeploymentName = ""
	orch := NewVCMOrchestrator(&stubStore{input: input}, &stubWorkflowStarter{}, log.NewLogger())

	_, err := orch.GetMultipleHostGroups(context.Background(), "", nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "derive SVM name")
}

func TestGetMultipleHostGroups_GetProviderByNodeFailureReturnsError(t *testing.T) {
	orig := getProviderByNode
	getProviderByNode = func(context.Context, *models.Node) (vsa.Provider, error) {
		return nil, errors.New("provider unavailable")
	}
	t.Cleanup(func() { getProviderByNode = orig })

	orch := NewVCMOrchestrator(&stubStore{input: validNodeProviderInput()}, &stubWorkflowStarter{}, log.NewLogger())
	_, err := orch.GetMultipleHostGroups(context.Background(), "", nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "create ONTAP provider")
}

func TestGetMultipleHostGroups_GetSVMFailureReturnsError(t *testing.T) {
	svmName := "gcnv-test-svm-01"
	mockProvider := vsa.NewMockProvider(t)
	mockProvider.EXPECT().
		GetSVM(vsa.GetSvmParams{Name: svmName}).
		Return(nil, errors.New("svm not found"))

	orig := getProviderByNode
	getProviderByNode = func(context.Context, *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}
	t.Cleanup(func() { getProviderByNode = orig })

	orch := NewVCMOrchestrator(&stubStore{input: validNodeProviderInput()}, &stubWorkflowStarter{}, log.NewLogger())
	_, err := orch.GetMultipleHostGroups(context.Background(), "", nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "resolve SVM")
}

func TestGetMultipleHostGroups_IgroupListFailureReturnsError(t *testing.T) {
	svmName := "gcnv-test-svm-01"
	mockProvider := vsa.NewMockProvider(t)
	mockProvider.EXPECT().
		GetSVM(vsa.GetSvmParams{Name: svmName}).
		Return(&ontapRest.Svm{Svm: ontapModels.Svm{Name: &svmName}}, nil)
	mockProvider.EXPECT().
		IgroupList(&svmName).
		Return(nil, errors.New("ontap list failed"))

	orig := getProviderByNode
	getProviderByNode = func(context.Context, *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}
	t.Cleanup(func() { getProviderByNode = orig })

	orch := NewVCMOrchestrator(&stubStore{input: validNodeProviderInput()}, &stubWorkflowStarter{}, log.NewLogger())
	_, err := orch.GetMultipleHostGroups(context.Background(), "", nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "list igroups")
}

func TestGetMultipleHostGroups_EmptyList(t *testing.T) {
	svmName := "gcnv-test-svm-01"
	mockProvider := vsa.NewMockProvider(t)
	mockProvider.EXPECT().
		GetSVM(vsa.GetSvmParams{Name: svmName}).
		Return(&ontapRest.Svm{Svm: ontapModels.Svm{Name: &svmName}}, nil)
	mockProvider.EXPECT().
		IgroupList(&svmName).
		Return([]*ontapRest.Igroup{}, nil)

	orig := getProviderByNode
	getProviderByNode = func(context.Context, *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}
	t.Cleanup(func() { getProviderByNode = orig })

	orch := NewVCMOrchestrator(&stubStore{input: validNodeProviderInput()}, &stubWorkflowStarter{}, log.NewLogger())
	hgs, err := orch.GetMultipleHostGroups(context.Background(), "", nil)
	require.NoError(t, err)
	assert.Empty(t, hgs)
}

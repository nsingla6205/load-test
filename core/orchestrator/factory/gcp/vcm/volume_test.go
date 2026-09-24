package vcm

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	cbackend "github.com/cschleiden/go-workflows/backend"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	vcmworkflows "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows/vcm-workflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vcmdb "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcm"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	utilErrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator/gowfbackend"
)

func createVCMVolumeParams() *commonparams.CreateVolumeParams {
	return &commonparams.CreateVolumeParams{
		OperationID:  "request-volume-1",
		Deadline:     time.Now().UTC().Add(time.Hour),
		Name:         "vol-1",
		QuotaInBytes: 107374182400,
		Protocols:    []string{"ISCSI"},
		BlockDevices: &[]commonparams.BlockDevice{
			{
				Name:           "vol-1",
				OSType:         "LINUX",
				HostGroupNames: []string{"hg-db-linux"},
			},
		},
	}
}

func TestCreateVCMVolume_PersistsJobAndDispatchesWorkflow(t *testing.T) {
	store := &stubStore{input: validNodeProviderInput()}
	starter := &stubWorkflowStarter{}
	orch := NewVCMOrchestrator(store, starter, log.NewLogger())
	params := createVCMVolumeParams()

	_, _, err := orch.CreateVolume(context.Background(), params)

	require.NoError(t, err)
	require.NotNil(t, store.createdJob)
	assert.Equal(t, params.OperationID, store.createdJob.ID)
	assert.Equal(t, params.Name, store.createdJob.ResourceID)
	assert.Equal(t, string(datamodel.JobTypeCreateVolume), store.createdJob.Type)
	assert.Equal(t, vcmdb.JobStateNew, store.createdJob.State)
	assert.True(t, store.createdJob.DeadlineAt.Equal(params.Deadline))
	assert.Equal(t, params.OperationID, starter.instanceID)
	assert.Equal(t, gowfbackend.FuncName(vcmworkflows.VcmCreateVolumeWorkflow), gowfbackend.FuncName(starter.workflowFn))
	require.Len(t, starter.args, 3)
	volume, ok := starter.args[0].(*datamodel.Volume)
	require.True(t, ok)
	inputs, ok := starter.args[1].(commonparams.WorkflowInputs)
	require.True(t, ok)
	startedParams, ok := starter.args[2].(*commonparams.CreateVolumeParams)
	require.True(t, ok)
	require.NotNil(t, inputs.Node)
	assert.Equal(t, "gcnv-test-svm-01", inputs.ExpectedSvmName)
	assert.True(t, inputs.Deadline.Equal(params.Deadline.UTC()))
	assert.Equal(t, params, startedParams)
	assert.Equal(t, "gcnv-test-svm-01", volume.Svm.Name)
	assert.Equal(t, params.Name, volume.Name)
	require.NotNil(t, startedParams.BlockDevices)
	require.Len(t, *startedParams.BlockDevices, 1)
	assert.Equal(t, "hg-db-linux", (*startedParams.BlockDevices)[0].HostGroupNames[0])
	require.NotNil(t, volume.VolumeAttributes.BlockDevices)
	require.Len(t, *volume.VolumeAttributes.BlockDevices, 1)
	assert.Equal(t, "vol-1", (*volume.VolumeAttributes.BlockDevices)[0].Name)
	assert.Equal(t, "LINUX", (*volume.VolumeAttributes.BlockDevices)[0].OSType)
	require.Len(t, (*volume.VolumeAttributes.BlockDevices)[0].HostGroupDetails, 1)
	assert.Equal(t, "hg-db-linux", (*volume.VolumeAttributes.BlockDevices)[0].HostGroupDetails[0].HostGroupName)
	assert.Nil(t, volume.VolumeAttributes.BlockProperties)
}

func TestCreateVCMVolume_MapsMultipleHostGroupNamesToBlockDevice(t *testing.T) {
	store := &stubStore{input: validNodeProviderInput()}
	starter := &stubWorkflowStarter{}
	orch := NewVCMOrchestrator(store, starter, log.NewLogger())
	params := createVCMVolumeParams()
	(*params.BlockDevices)[0].HostGroupNames = []string{"hg1", "hg2", "hg3"}

	_, _, err := orch.CreateVolume(context.Background(), params)
	require.NoError(t, err)

	volume, ok := starter.args[0].(*datamodel.Volume)
	require.True(t, ok)
	require.NotNil(t, volume.VolumeAttributes.BlockDevices)
	hostGroupDetails := (*volume.VolumeAttributes.BlockDevices)[0].HostGroupDetails
	require.Len(t, hostGroupDetails, 3)
	assert.Equal(t, "hg1", hostGroupDetails[0].HostGroupName)
	assert.Equal(t, "hg2", hostGroupDetails[1].HostGroupName)
	assert.Equal(t, "hg3", hostGroupDetails[2].HostGroupName)
}

func TestCreateVCMVolume_InstanceAlreadyExistsIsIdempotent(t *testing.T) {
	store := &stubStore{input: validNodeProviderInput()}
	starter := &stubWorkflowStarter{err: cbackend.ErrInstanceAlreadyExists}
	orch := NewVCMOrchestrator(store, starter, log.NewLogger())

	_, _, err := orch.CreateVolume(context.Background(), createVCMVolumeParams())
	require.NoError(t, err)
}

func TestCreateVCMVolume_DispatchFailureMarksJobError(t *testing.T) {
	store := &stubStore{input: validNodeProviderInput()}
	starter := &stubWorkflowStarter{err: errors.New("worker unavailable")}
	orch := NewVCMOrchestrator(store, starter, log.NewLogger())

	_, _, err := orch.CreateVolume(context.Background(), createVCMVolumeParams())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "start workflow")
	assert.Equal(t, "request-volume-1", store.updatedJobID)
	assert.Equal(t, vcmdb.JobStateError, store.updatedState)
	assert.Equal(t, jobStepFailedToStart, store.updatedStep)
	assert.Equal(t, "worker unavailable", store.updatedError)
	assert.True(t, store.updatedRetriable)
}

func TestCreateVCMVolume_FileVolumeDispatchesWorkflow(t *testing.T) {
	store := &stubStore{input: validNodeProviderInput()}
	starter := &stubWorkflowStarter{}
	orch := NewVCMOrchestrator(store, starter, log.NewLogger())
	params := &commonparams.CreateVolumeParams{
		OperationID:   "request-volume-nfs",
		Deadline:      time.Now().UTC().Add(time.Hour),
		Name:          "nfs-vol",
		CreationToken: "nfs-vol",
		QuotaInBytes:  107374182400,
		Protocols:     []string{utils.ProtocolNFSv3},
		FileProperties: &models.FileProperties{
			JunctionPath: "/nfs-vol",
		},
	}

	_, _, err := orch.CreateVolume(context.Background(), params)
	require.NoError(t, err)

	volume, ok := starter.args[0].(*datamodel.Volume)
	require.True(t, ok)
	require.NotNil(t, volume.VolumeAttributes.FileProperties)
	assert.Equal(t, "/nfs-vol", volume.VolumeAttributes.FileProperties.JunctionPath)
	assert.Nil(t, starter.args[2].(*commonparams.CreateVolumeParams).BlockDevices)
}

func TestCreateVCMVolume_DualProtocolDispatchesWorkflow(t *testing.T) {
	store := &stubStore{input: validNodeProviderInput()}
	starter := &stubWorkflowStarter{}
	orch := NewVCMOrchestrator(store, starter, log.NewLogger())
	params := &commonparams.CreateVolumeParams{
		OperationID:   "request-volume-dual",
		Deadline:      time.Now().UTC().Add(time.Hour),
		Name:          "dual-vol",
		CreationToken: "dual-vol",
		QuotaInBytes:  107374182400,
		Protocols:     []string{utils.ProtocolNFSv3, utils.ProtocolSMB},
		FileProperties: &models.FileProperties{
			SecurityStyle: "ntfs",
			ExportPolicy: &models.ExportPolicy{
				ExportPolicyName: "dual-vol",
				ExportRules: []*models.ExportRule{
					{
						AllowedClients: "0.0.0.0/0",
						AccessType:     models.ReadWrite,
						NFSv3:          true,
						CIFS:           true,
						Index:          1,
						UnixReadWrite:  true,
					},
				},
			},
		},
		SnapshotPolicy: &models.SnapshotPolicy{
			IsEnabled: true,
			Schedules: []*models.SnapshotPolicySchedule{
				{Count: 6, SnapmirrorLabel: "daily", Schedule: &models.Schedule{Hours: []int{1}}},
			},
		},
	}

	_, _, err := orch.CreateVolume(context.Background(), params)
	require.NoError(t, err)

	volume, ok := starter.args[0].(*datamodel.Volume)
	require.True(t, ok)
	require.NotNil(t, volume.SnapshotPolicy)
	assert.Equal(t, "dual-vol", volume.SnapshotPolicy.Name)
	require.NotNil(t, volume.VolumeAttributes.FileProperties)
	assert.True(t, volume.VolumeAttributes.FileProperties.ExportPolicy.ExportRules[0].CIFS)
}

func TestCreateVCMVolume_IncludesSnapReserveAndSnapshotDirectory(t *testing.T) {
	store := &stubStore{input: validNodeProviderInput()}
	starter := &stubWorkflowStarter{}
	params := createVCMVolumeParams()
	params.SnapReserve = 20
	params.SnapshotDirectory = false

	orch := NewVCMOrchestrator(store, starter, log.NewLogger())
	_, _, err := orch.CreateVolume(context.Background(), params)
	require.NoError(t, err)

	volume, ok := starter.args[0].(*datamodel.Volume)
	require.True(t, ok)
	require.NotNil(t, volume.VolumeAttributes)
	assert.Equal(t, int64(20), volume.VolumeAttributes.SnapReserve)
	assert.False(t, volume.VolumeAttributes.SnapshotDirectory)
}

func TestCreateVCMVolume_CreateJobFailureDoesNotDispatchWorkflow(t *testing.T) {
	store := &stubStore{input: validNodeProviderInput(), createErr: errors.New("database unavailable")}
	starter := &stubWorkflowStarter{}
	orch := NewVCMOrchestrator(store, starter, log.NewLogger())

	_, _, err := orch.CreateVolume(context.Background(), createVCMVolumeParams())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "persist job")
	assert.Empty(t, starter.instanceID)
}

func TestCreateVCMVolume_DuplicateJobIsIdempotent(t *testing.T) {
	existingJob := &vcmdb.Job{
		ID:         "request-volume-1",
		ResourceID: "vol-1",
		Type:       string(datamodel.JobTypeCreateVolume),
		State:      vcmdb.JobStateDone,
	}
	store := &stubStore{
		input:       validNodeProviderInput(),
		existingJob: existingJob,
		createErr:   fmt.Errorf("%w: request-volume-1", vcmdb.ErrJobAlreadyExists),
	}
	starter := &stubWorkflowStarter{}
	orch := NewVCMOrchestrator(store, starter, log.NewLogger())

	_, _, err := orch.CreateVolume(context.Background(), createVCMVolumeParams())
	require.NoError(t, err)
	assert.Empty(t, starter.instanceID)
}

func TestCreateVCMVolume_InvalidConfigReturnsErrorWithoutCreatingJob(t *testing.T) {
	store := &stubStore{inputErr: datamodel.ErrInvalidConfig}
	starter := &stubWorkflowStarter{}
	orch := NewVCMOrchestrator(store, starter, log.NewLogger())

	_, _, err := orch.CreateVolume(context.Background(), createVCMVolumeParams())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "build ONTAP node")
	assert.Nil(t, store.createdJob)
	assert.Empty(t, starter.instanceID)
}

func TestCreateVCMVolume_EmptyDeploymentIdReturnsErrorWithoutCreatingJob(t *testing.T) {
	input := validNodeProviderInput()
	input.DeploymentName = ""
	store := &stubStore{input: input}
	starter := &stubWorkflowStarter{}
	orch := NewVCMOrchestrator(store, starter, log.NewLogger())

	_, _, err := orch.CreateVolume(context.Background(), createVCMVolumeParams())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "derive SVM name")
	assert.Nil(t, store.createdJob)
	assert.Empty(t, starter.instanceID)
}

func TestCreateVCMVolume_DispatchFailureUpdateJobAlsoFails(t *testing.T) {
	store := &stubStore{input: validNodeProviderInput(), updateErr: errors.New("update failed")}
	starter := &stubWorkflowStarter{err: errors.New("worker unavailable")}
	orch := NewVCMOrchestrator(store, starter, log.NewLogger())

	_, _, err := orch.CreateVolume(context.Background(), createVCMVolumeParams())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "start workflow")
}

const testVolumeUUID = "9760acf5-4638-11e7-9bdb-020073ca0001"

func deleteVCMVolumeParams(volumeUUID string) *commonparams.DeleteVolumeParams {
	return &commonparams.DeleteVolumeParams{
		VolumeUUID:  volumeUUID,
		OperationID: "delete-op-1",
		Deadline:    time.Now().UTC().Add(time.Hour),
	}
}

func TestDeleteVolume_PersistsJobAndDispatchesWorkflow(t *testing.T) {
	store := &stubStore{input: validNodeProviderInput()}
	starter := &stubWorkflowStarter{}
	orch := NewVCMOrchestrator(store, starter, log.NewLogger())
	params := deleteVCMVolumeParams(testVolumeUUID)

	_, operationID, err := orch.DeleteVolume(context.Background(), params)

	require.NoError(t, err)
	assert.Equal(t, params.OperationID, operationID)
	assert.Equal(t, params.OperationID, store.createdJob.ID)
	assert.Equal(t, testVolumeUUID, store.createdJob.ResourceID)
	assert.Equal(t, string(datamodel.JobTypeDeleteVolume), store.createdJob.Type)
	assert.Equal(t, vcmdb.JobStateNew, store.createdJob.State)
	assert.True(t, store.createdJob.DeadlineAt.Equal(params.Deadline))
	assert.Equal(t, params.OperationID, starter.instanceID)
	assert.Equal(t, gowfbackend.FuncName(vcmworkflows.VcmDeleteVolumeWorkflow), gowfbackend.FuncName(starter.workflowFn))
	require.Len(t, starter.args, 2)
	volume, ok := starter.args[0].(*datamodel.Volume)
	require.True(t, ok)
	require.NotNil(t, volume)
	assert.Empty(t, volume.Name)
	assert.Equal(t, testVolumeUUID, volume.VolumeAttributes.ExternalUUID)
	assert.Equal(t, "gcnv-test-svm-01", volume.Svm.Name)
	require.NotNil(t, volume.VolumeAttributes)
	require.NotNil(t, volume.VolumeAttributes.FileProperties)
	require.NotNil(t, volume.VolumeAttributes.FileProperties.ExportPolicy)
	assert.Empty(t, volume.VolumeAttributes.RestrictedActions)
	inputs, ok := starter.args[1].(commonparams.WorkflowInputs)
	require.True(t, ok)
	require.NotNil(t, inputs.Node)
	assert.Equal(t, "gcnv-test-svm-01", inputs.ExpectedSvmName)
	assert.True(t, inputs.Deadline.Equal(params.Deadline.UTC()))
}

func TestDeleteVolume_InstanceAlreadyExistsIsIdempotent(t *testing.T) {
	store := &stubStore{input: validNodeProviderInput()}
	starter := &stubWorkflowStarter{err: cbackend.ErrInstanceAlreadyExists}
	orch := NewVCMOrchestrator(store, starter, log.NewLogger())

	_, operationID, err := orch.DeleteVolume(context.Background(), deleteVCMVolumeParams(testVolumeUUID))
	require.NoError(t, err)
	assert.Equal(t, "delete-op-1", operationID)
}

func TestDeleteVolume_DispatchFailureMarksJobError(t *testing.T) {
	store := &stubStore{input: validNodeProviderInput()}
	starter := &stubWorkflowStarter{err: errors.New("worker unavailable")}
	orch := NewVCMOrchestrator(store, starter, log.NewLogger())

	_, _, err := orch.DeleteVolume(context.Background(), deleteVCMVolumeParams(testVolumeUUID))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "start workflow")
	assert.Equal(t, vcmdb.JobStateError, store.updatedState)
	assert.Equal(t, jobStepFailedToStart, store.updatedStep)
	assert.True(t, store.updatedRetriable)
}

func TestDeleteVolume_DuplicateJobIsIdempotent(t *testing.T) {
	store := &stubStore{
		input:       validNodeProviderInput(),
		createErr:   fmt.Errorf("%w: delete-op-1", vcmdb.ErrJobAlreadyExists),
		getJobState: vcmdb.JobStateNew,
	}
	starter := &stubWorkflowStarter{err: cbackend.ErrInstanceAlreadyExists}
	orch := NewVCMOrchestrator(store, starter, log.NewLogger())

	_, operationID, err := orch.DeleteVolume(context.Background(), deleteVCMVolumeParams(testVolumeUUID))
	require.NoError(t, err)
	assert.Equal(t, "delete-op-1", operationID)
	assert.Empty(t, starter.instanceID)
}

func TestDeleteVolume_DuplicateJobNotNewSkipsWorkflow(t *testing.T) {
	store := &stubStore{
		input:       validNodeProviderInput(),
		createErr:   fmt.Errorf("%w: delete-op-1", vcmdb.ErrJobAlreadyExists),
		getJobState: vcmdb.JobStateProcessing,
	}
	starter := &stubWorkflowStarter{}
	orch := NewVCMOrchestrator(store, starter, log.NewLogger())

	_, operationID, err := orch.DeleteVolume(context.Background(), deleteVCMVolumeParams(testVolumeUUID))
	require.NoError(t, err)
	assert.Equal(t, "delete-op-1", operationID)
	assert.Empty(t, starter.instanceID)
}

func updateVCMVolumeParams(volumeUUID string) *commonparams.UpdateVolumeParams {
	return &commonparams.UpdateVolumeParams{
		OperationID:  "update-op-1",
		Deadline:     time.Now().UTC().Add(time.Hour),
		VolumeId:     volumeUUID,
		QuotaInBytes: 214748364800,
	}
}

func TestGetVolume_FileVolumeMapsONTAPFields(t *testing.T) {
	provider := vsa.NewMockProvider(t)
	originalGetProvider := getProviderByNode
	t.Cleanup(func() { getProviderByNode = originalGetProvider })
	getProviderByNode = func(context.Context, *models.Node) (vsa.Provider, error) {
		return provider, nil
	}

	provider.EXPECT().GetSVM(vsa.GetSvmParams{Name: "gcnv-test-svm-01"}).Return(nil, nil)
	provider.EXPECT().GetVolume(mock.MatchedBy(func(params vsa.GetVolumeParams) bool {
		return params.UUID == testVolumeUUID && params.SvmName == "gcnv-test-svm-01"
	})).Return(&vsa.VolumeResponse{
		ProviderResponse:               vsa.ProviderResponse{Name: "file-volume", ExternalUUID: testVolumeUUID},
		Size:                           100,
		UsedBytes:                      25,
		SnapReserve:                    5,
		SnapshotDirectoryAccessEnabled: true,
		JunctionPath:                   "/file-volume",
		SecurityStyle:                  "unix",
		UnixPermissions:                "0755",
		Created:                        time.Date(2016, 11, 30, 20, 32, 50, 459000000, time.UTC),
	}, nil)
	provider.EXPECT().LunList(vsa.LunGetParams{
		SvmName:    "gcnv-test-svm-01",
		VolumeName: "file-volume",
	}).Return(nil, vsaerrors.NewVCPError(vsaerrors.ErrOntapRestAPIError, utilErrors.NewNotFoundErr("lun", nil)))

	store := &stubStore{input: validNodeProviderInput()}
	orch := NewVCMOrchestrator(store, &stubWorkflowStarter{}, log.NewLogger())

	volume, err := orch.GetVolume(context.Background(), testVolumeUUID, false)

	require.NoError(t, err)
	assert.Equal(t, testVolumeUUID, volume.UUID)
	assert.Equal(t, "file-volume", volume.DisplayName)
	assert.Equal(t, uint64(100), volume.QuotaInBytes)
	assert.Equal(t, uint64(25), volume.UsedBytes)
	assert.Equal(t, int64(5), volume.SnapReserve)
	assert.True(t, volume.SnapshotDirectory)
	require.NotNil(t, volume.FileProperties)
	assert.Equal(t, "/file-volume", volume.FileProperties.JunctionPath)
	assert.Equal(t, "unix", volume.FileProperties.SecurityStyle)
	assert.Equal(t, "0755", volume.FileProperties.UnixPermissions)
	require.NotNil(t, volume.FileProperties.ExportPolicy)
	assert.Equal(t, models.DefaultExportPolicyName, volume.FileProperties.ExportPolicy.ExportPolicyName)
	assert.Empty(t, volume.FileProperties.ExportPolicy.ExportRules)
	assert.Equal(t, time.Date(2016, 11, 30, 20, 32, 50, 459000000, time.UTC), volume.CreatedAt)
}

func TestGetVolume_CustomExportPolicyEnrichesRules(t *testing.T) {
	provider := vsa.NewMockProvider(t)
	originalGetProvider := getProviderByNode
	t.Cleanup(func() { getProviderByNode = originalGetProvider })
	getProviderByNode = func(context.Context, *models.Node) (vsa.Provider, error) {
		return provider, nil
	}

	provider.EXPECT().GetSVM(vsa.GetSvmParams{Name: "gcnv-test-svm-01"}).Return(nil, nil)
	provider.EXPECT().GetVolume(mock.MatchedBy(func(params vsa.GetVolumeParams) bool {
		return params.UUID == testVolumeUUID && params.SvmName == "gcnv-test-svm-01"
	})).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{Name: "nfs-volume", ExternalUUID: testVolumeUUID},
		JunctionPath:     "/nfs-volume",
		ExportPolicyName: "nfs-volume",
	}, nil)
	provider.EXPECT().GetExportPolicy("nfs-volume", "gcnv-test-svm-01").Return(&vsa.ExportPolicy{
		ExportPolicyName: "nfs-volume",
		ExportRules: []*vsa.ExportRule{
			{
				AllowedClients: "10.0.0.0/8",
				AccessType:     models.ReadWrite,
				NFSv3:          true,
			},
		},
	}, nil)
	provider.EXPECT().LunList(vsa.LunGetParams{
		SvmName:    "gcnv-test-svm-01",
		VolumeName: "nfs-volume",
	}).Return(nil, vsaerrors.NewVCPError(vsaerrors.ErrOntapRestAPIError, utilErrors.NewNotFoundErr("lun", nil)))

	store := &stubStore{input: validNodeProviderInput()}
	orch := NewVCMOrchestrator(store, &stubWorkflowStarter{}, log.NewLogger())

	volume, err := orch.GetVolume(context.Background(), testVolumeUUID, false)

	require.NoError(t, err)
	require.NotNil(t, volume.FileProperties)
	require.NotNil(t, volume.FileProperties.ExportPolicy)
	assert.Equal(t, "nfs-volume", volume.FileProperties.ExportPolicy.ExportPolicyName)
	require.Len(t, volume.FileProperties.ExportPolicy.ExportRules, 1)
	assert.Equal(t, "10.0.0.0/8", volume.FileProperties.ExportPolicy.ExportRules[0].AllowedClients)
}

func TestGetVolume_DefaultExportPolicySkipsEnrichment(t *testing.T) {
	provider := vsa.NewMockProvider(t)
	originalGetProvider := getProviderByNode
	t.Cleanup(func() { getProviderByNode = originalGetProvider })
	getProviderByNode = func(context.Context, *models.Node) (vsa.Provider, error) {
		return provider, nil
	}

	provider.EXPECT().GetSVM(vsa.GetSvmParams{Name: "gcnv-test-svm-01"}).Return(nil, nil)
	provider.EXPECT().GetVolume(mock.MatchedBy(func(params vsa.GetVolumeParams) bool {
		return params.UUID == testVolumeUUID
	})).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{Name: "nfs-volume", ExternalUUID: testVolumeUUID},
		JunctionPath:     "/nfs-volume",
		ExportPolicyName: models.DefaultExportPolicyName,
	}, nil)
	provider.EXPECT().LunList(vsa.LunGetParams{
		SvmName:    "gcnv-test-svm-01",
		VolumeName: "nfs-volume",
	}).Return(nil, vsaerrors.NewVCPError(vsaerrors.ErrOntapRestAPIError, utilErrors.NewNotFoundErr("lun", nil)))

	store := &stubStore{input: validNodeProviderInput()}
	orch := NewVCMOrchestrator(store, &stubWorkflowStarter{}, log.NewLogger())

	volume, err := orch.GetVolume(context.Background(), testVolumeUUID, false)

	require.NoError(t, err)
	require.NotNil(t, volume.FileProperties)
	require.NotNil(t, volume.FileProperties.ExportPolicy)
	assert.Equal(t, models.DefaultExportPolicyName, volume.FileProperties.ExportPolicy.ExportPolicyName)
	assert.Empty(t, volume.FileProperties.ExportPolicy.ExportRules)
}

func TestGetVolume_BlockVolumeMapsONTAPFields(t *testing.T) {
	provider := vsa.NewMockProvider(t)
	originalGetProvider := getProviderByNode
	t.Cleanup(func() { getProviderByNode = originalGetProvider })
	getProviderByNode = func(context.Context, *models.Node) (vsa.Provider, error) {
		return provider, nil
	}
	provider.EXPECT().GetSVM(vsa.GetSvmParams{Name: "gcnv-test-svm-01"}).Return(nil, nil)
	provider.EXPECT().GetVolume(mock.MatchedBy(func(params vsa.GetVolumeParams) bool {
		return params.UUID == testVolumeUUID
	})).Return(&vsa.VolumeResponse{
		ProviderResponse:               vsa.ProviderResponse{Name: "block-volume", ExternalUUID: testVolumeUUID},
		Size:                           100,
		UsedBytes:                      10,
		SnapshotDirectoryAccessEnabled: false,
	}, nil)
	provider.EXPECT().LunList(vsa.LunGetParams{
		SvmName:    "gcnv-test-svm-01",
		VolumeName: "block-volume",
	}).Return([]*vsa.LunResponse{
		{
			ProviderResponse: vsa.ProviderResponse{Name: "lun_block-volume", ExternalUUID: "lun-uuid"},
			OSType:           "linux",
			IgroupNames:      []string{"hg-db-linux"},
		},
	}, nil)
	provider.EXPECT().IgroupNamesForLunUUID("gcnv-test-svm-01", "lun-uuid").Return([]string{"hg-db-linux", "hg-db-windows"}, nil)

	store := &stubStore{input: validNodeProviderInput()}
	orch := NewVCMOrchestrator(store, &stubWorkflowStarter{}, log.NewLogger())

	volume, err := orch.GetVolume(context.Background(), testVolumeUUID, false)

	require.NoError(t, err)
	assert.Equal(t, "block-volume", volume.DisplayName)
	assert.Nil(t, volume.FileProperties)
	require.NotNil(t, volume.BlockDevices)
	require.Len(t, *volume.BlockDevices, 1)
	assert.Equal(t, "LINUX", (*volume.BlockDevices)[0].OSType)
	require.Len(t, (*volume.BlockDevices)[0].HostGroupDetail, 2)
	assert.Equal(t, "hg-db-linux", (*volume.BlockDevices)[0].HostGroupDetail[0].HostGroupID)
	assert.Equal(t, "hg-db-windows", (*volume.BlockDevices)[0].HostGroupDetail[1].HostGroupID)
}

func TestListVolumes_MapsOntapVolumes(t *testing.T) {
	provider := vsa.NewMockProvider(t)
	originalGetProvider := getProviderByNode
	t.Cleanup(func() { getProviderByNode = originalGetProvider })
	getProviderByNode = func(context.Context, *models.Node) (vsa.Provider, error) {
		return provider, nil
	}

	provider.EXPECT().GetSVM(vsa.GetSvmParams{Name: "gcnv-test-svm-01"}).Return(nil, nil)
	provider.EXPECT().ListVolumesForSVM("gcnv-test-svm-01").Return([]*vsa.VolumeResponse{
		{
			ProviderResponse:               vsa.ProviderResponse{Name: "file-volume", ExternalUUID: testVolumeUUID},
			Size:                           100,
			UsedBytes:                      25,
			SnapReserve:                    5,
			SnapshotDirectoryAccessEnabled: true,
			JunctionPath:                   "/file-volume",
			SecurityStyle:                  "unix",
			UnixPermissions:                "0755",
		},
		{
			ProviderResponse:               vsa.ProviderResponse{Name: "block-volume", ExternalUUID: "8760acf5-4638-11e7-9bdb-020073ca0002"},
			Size:                           100,
			UsedBytes:                      10,
			SnapshotDirectoryAccessEnabled: false,
		},
	}, nil)

	store := &stubStore{input: validNodeProviderInput()}
	orch := NewVCMOrchestrator(store, &stubWorkflowStarter{}, log.NewLogger())

	volumes, err := orch.ListVolumes(context.Background(), "")

	require.NoError(t, err)
	require.Len(t, volumes, 2)
	assert.Equal(t, "file-volume", volumes[0].DisplayName)
	require.NotNil(t, volumes[0].FileProperties)
	assert.Equal(t, "block-volume", volumes[1].DisplayName)
	assert.Nil(t, volumes[1].FileProperties)
}

func TestListVolumes_SkipsNilOntapVolumes(t *testing.T) {
	provider := vsa.NewMockProvider(t)
	originalGetProvider := getProviderByNode
	t.Cleanup(func() { getProviderByNode = originalGetProvider })
	getProviderByNode = func(context.Context, *models.Node) (vsa.Provider, error) {
		return provider, nil
	}

	provider.EXPECT().GetSVM(vsa.GetSvmParams{Name: "gcnv-test-svm-01"}).Return(nil, nil)
	provider.EXPECT().ListVolumesForSVM("gcnv-test-svm-01").Return([]*vsa.VolumeResponse{
		nil,
		{
			ProviderResponse:               vsa.ProviderResponse{Name: "block-volume", ExternalUUID: testVolumeUUID},
			Size:                           100,
			UsedBytes:                      10,
			SnapshotDirectoryAccessEnabled: false,
		},
	}, nil)

	store := &stubStore{input: validNodeProviderInput()}
	orch := NewVCMOrchestrator(store, &stubWorkflowStarter{}, log.NewLogger())

	volumes, err := orch.ListVolumes(context.Background(), "")

	require.NoError(t, err)
	require.Len(t, volumes, 1)
	assert.Equal(t, "block-volume", volumes[0].DisplayName)
}

func TestListVolumes_InvalidConfig(t *testing.T) {
	orch := NewVCMOrchestrator(&stubStore{inputErr: datamodel.ErrInvalidConfig}, &stubWorkflowStarter{}, log.NewLogger())
	_, err := orch.ListVolumes(context.Background(), "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ListVolumes")
}

func TestUpdateVolumeV2_PersistsJobAndDispatchesWorkflow(t *testing.T) {
	store := &stubStore{input: validNodeProviderInput()}
	starter := &stubWorkflowStarter{}
	orch := NewVCMOrchestrator(store, starter, log.NewLogger())
	params := updateVCMVolumeParams(testVolumeUUID)

	_, operationID, err := orch.UpdateVolumeV2(context.Background(), params)

	require.NoError(t, err)
	require.NotNil(t, store.createdJob)
	assert.Equal(t, params.OperationID, store.createdJob.ID)
	assert.Equal(t, params.VolumeId, store.createdJob.ResourceID)
	assert.Equal(t, string(datamodel.JobTypeUpdateVolume), store.createdJob.Type)
	assert.Equal(t, gowfbackend.FuncName(vcmworkflows.VcmUpdateVolumeWorkflow), gowfbackend.FuncName(starter.workflowFn))
	assert.Equal(t, operationID, params.OperationID)
}

func TestUpdateVolumeV2_ReturnsWhenJobAlreadyExists(t *testing.T) {
	store := &stubStore{
		input:       validNodeProviderInput(),
		createErr:   fmt.Errorf("%w: update-op-1", vcmdb.ErrJobAlreadyExists),
		existingJob: &vcmdb.Job{ID: "update-op-1", State: vcmdb.JobStateProcessing},
	}
	starter := &stubWorkflowStarter{}
	orch := NewVCMOrchestrator(store, starter, log.NewLogger())

	_, operationID, err := orch.UpdateVolumeV2(context.Background(), updateVCMVolumeParams(testVolumeUUID))
	require.NoError(t, err)
	assert.Equal(t, "update-op-1", operationID)
	assert.Empty(t, starter.instanceID)
}

func TestGetVolume_ReturnsNilWhenOntapVolumeMissing(t *testing.T) {
	provider := vsa.NewMockProvider(t)
	originalGetProvider := getProviderByNode
	t.Cleanup(func() { getProviderByNode = originalGetProvider })
	getProviderByNode = func(context.Context, *models.Node) (vsa.Provider, error) {
		return provider, nil
	}

	provider.EXPECT().GetSVM(vsa.GetSvmParams{Name: "gcnv-test-svm-01"}).Return(nil, nil)
	provider.EXPECT().GetVolume(mock.Anything).Return(nil, nil)

	store := &stubStore{input: validNodeProviderInput()}
	orch := NewVCMOrchestrator(store, &stubWorkflowStarter{}, log.NewLogger())

	volume, err := orch.GetVolume(context.Background(), testVolumeUUID, false)
	require.NoError(t, err)
	assert.Nil(t, volume)
}

func TestListVolumes_ProviderError(t *testing.T) {
	provider := vsa.NewMockProvider(t)
	originalGetProvider := getProviderByNode
	t.Cleanup(func() { getProviderByNode = originalGetProvider })
	getProviderByNode = func(context.Context, *models.Node) (vsa.Provider, error) {
		return provider, nil
	}

	provider.EXPECT().GetSVM(vsa.GetSvmParams{Name: "gcnv-test-svm-01"}).Return(nil, nil)
	provider.EXPECT().ListVolumesForSVM("gcnv-test-svm-01").Return(nil, errors.New("list failed"))

	store := &stubStore{input: validNodeProviderInput()}
	orch := NewVCMOrchestrator(store, &stubWorkflowStarter{}, log.NewLogger())

	_, err := orch.ListVolumes(context.Background(), "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ListVolumes")
}

func TestGetVolume_EnrichExportPolicyError(t *testing.T) {
	provider := vsa.NewMockProvider(t)
	originalGetProvider := getProviderByNode
	t.Cleanup(func() { getProviderByNode = originalGetProvider })
	getProviderByNode = func(context.Context, *models.Node) (vsa.Provider, error) {
		return provider, nil
	}

	provider.EXPECT().GetSVM(vsa.GetSvmParams{Name: "gcnv-test-svm-01"}).Return(nil, nil)
	provider.EXPECT().GetVolume(mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{Name: "nfs-volume", ExternalUUID: testVolumeUUID},
		ExportPolicyName: "nfs-volume",
	}, nil)
	provider.EXPECT().GetExportPolicy("nfs-volume", "gcnv-test-svm-01").Return(nil, errors.New("export policy unavailable"))

	store := &stubStore{input: validNodeProviderInput()}
	orch := NewVCMOrchestrator(store, &stubWorkflowStarter{}, log.NewLogger())

	_, err := orch.GetVolume(context.Background(), testVolumeUUID, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "enrich ONTAP volume")
}

func TestGetVolume_EnrichesBlockDevicesFromLuns(t *testing.T) {
	provider := vsa.NewMockProvider(t)
	originalGetProvider := getProviderByNode
	t.Cleanup(func() { getProviderByNode = originalGetProvider })
	getProviderByNode = func(context.Context, *models.Node) (vsa.Provider, error) {
		return provider, nil
	}

	provider.EXPECT().GetSVM(vsa.GetSvmParams{Name: "gcnv-test-svm-01"}).Return(nil, nil)
	provider.EXPECT().GetVolume(mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{Name: "block-volume", ExternalUUID: testVolumeUUID},
	}, nil)
	provider.EXPECT().LunList(vsa.LunGetParams{
		SvmName:    "gcnv-test-svm-01",
		VolumeName: "block-volume",
	}).Return([]*vsa.LunResponse{
		{
			ProviderResponse: vsa.ProviderResponse{Name: "lun_block-volume"},
			SerialNumber:     "serial-1",
			Size:             100,
			OSType:           "linux",
			IgroupNames:      []string{"hg-db-linux"},
		},
		nil,
	}, nil)

	store := &stubStore{input: validNodeProviderInput()}
	orch := NewVCMOrchestrator(store, &stubWorkflowStarter{}, log.NewLogger())

	volume, err := orch.GetVolume(context.Background(), testVolumeUUID, false)
	require.NoError(t, err)
	require.NotNil(t, volume.BlockDevices)
	require.Len(t, *volume.BlockDevices, 1)
	assert.Equal(t, "LINUX", (*volume.BlockDevices)[0].OSType)
	require.Len(t, (*volume.BlockDevices)[0].HostGroupDetail, 1)
}

func TestUpdateVolumeV2_WorkflowStartFailureMarksJobError(t *testing.T) {
	store := &stubStore{input: validNodeProviderInput()}
	starter := &stubWorkflowStarter{err: errors.New("workflow start failed")}
	orch := NewVCMOrchestrator(store, starter, log.NewLogger())

	_, _, err := orch.UpdateVolumeV2(context.Background(), updateVCMVolumeParams(testVolumeUUID))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "start workflow")
	assert.Equal(t, vcmdb.JobStateError, store.updatedState)
}

func TestGetVolume_EnrichesSnapshotPolicy(t *testing.T) {
	provider := vsa.NewMockProvider(t)
	originalGetProvider := getProviderByNode
	t.Cleanup(func() { getProviderByNode = originalGetProvider })
	getProviderByNode = func(context.Context, *models.Node) (vsa.Provider, error) {
		return provider, nil
	}

	provider.EXPECT().GetSVM(vsa.GetSvmParams{Name: "gcnv-test-svm-01"}).Return(nil, nil)
	provider.EXPECT().GetVolume(mock.Anything).Return(&vsa.VolumeResponse{
		ProviderResponse:   vsa.ProviderResponse{Name: "nfs-volume", ExternalUUID: testVolumeUUID},
		JunctionPath:       "/nfs-volume",
		SnapshotPolicyName: "nfs-volume",
	}, nil)
	provider.EXPECT().GetSnapshotPolicy("nfs-volume").Return(&vsa.SnapshotPolicy{
		Name:      "nfs-volume",
		IsEnabled: true,
	}, nil)
	provider.EXPECT().LunList(mock.Anything).Return(nil, vsaerrors.NewVCPError(vsaerrors.ErrOntapRestAPIError, utilErrors.NewNotFoundErr("lun", nil)))

	store := &stubStore{input: validNodeProviderInput()}
	orch := NewVCMOrchestrator(store, &stubWorkflowStarter{}, log.NewLogger())

	volume, err := orch.GetVolume(context.Background(), testVolumeUUID, false)
	require.NoError(t, err)
	require.NotNil(t, volume.SnapshotPolicy)
	assert.Equal(t, "nfs-volume", volume.SnapshotPolicy.Name)
}
